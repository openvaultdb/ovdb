// Package source validates immutable declarations of original HTTP resources.
// Validation never fetches a resource, authorizes execution or retains its data.
package source

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const Format = "ovdb-http-source/1"

// Evidence verifies declaration bytes only. It is never a SourceRight, a read
// receipt, proof of upstream authenticity, or an execution/copy authorization.
type Evidence struct {
	Origin            string      `json:"origin"`
	InputVerification string      `json:"inputVerification"`
	Manifest          license.Pin `json:"manifest"`
}

//go:embed schema.json
var schemaBytes []byte

// Schema returns a private copy of the closed preparatory definition schema.
func Schema() []byte { return bytes.Clone(schemaBytes) }

type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}
type Resource struct {
	URL           string `json:"url"`
	Method        string `json:"method"`
	ContentType   string `json:"contentType"`
	TimeoutMs     int    `json:"timeoutMs"`
	MaxBytes      int    `json:"maxBytes"`
	Mode          string `json:"mode"`
	Retention     string `json:"retention"`
	Cache         string `json:"cache"`
	Redirect      string `json:"redirect"`
	BrowserAccess string `json:"browserAccess"`
}
type Recordset struct {
	ProviderSourceID string            `json:"providerSourceId"`
	Resource         string            `json:"resource"`
	Decoder          string            `json:"decoder"`
	Entity           string            `json:"entity"`
	KeyFields        []string          `json:"keyFields"`
	FieldMapping     map[string]string `json:"fieldMapping"`
	Context          map[string]string `json:"context"`
}
type Rights struct {
	Terms           []license.Notice `json:"terms"`
	Attribution     license.Notice   `json:"attribution"`
	FreeSource      license.Notice   `json:"freeSource"`
	Transformations []string         `json:"transformations"`
}
type ReadEvidence struct {
	Format            string `json:"format"`
	Classification    string `json:"classification"`
	InputVerification string `json:"inputVerification"`
}
type Gates struct {
	Runtime  string `json:"runtime"`
	Semantic string `json:"semantic"`
	Rights   string `json:"rights"`
	Paid     string `json:"paid"`
}
type Admission struct {
	Status           string `json:"status"`
	ExecutionEnabled bool   `json:"executionEnabled"`
	Gates            Gates  `json:"gates"`
}
type Definition struct {
	Format       string               `json:"format"`
	Provider     Provider             `json:"provider"`
	Resources    map[string]Resource  `json:"resources"`
	Recordsets   map[string]Recordset `json:"recordsets"`
	Rights       Rights               `json:"rights"`
	ReadEvidence ReadEvidence         `json:"readEvidence"`
	Admission    Admission            `json:"admission"`
}

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	var value any
	if err := json.Unmarshal(schemaBytes, &value); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const id = "https://openvaultdb.com/schemas/http-source-1.json"
	if err := c.AddResource(id, value); err != nil {
		return nil, err
	}
	return c.Compile(id)
})

// Parse refuses missing/null fields, unknown/case-folded/duplicate keys,
// retention, execution admission and secret-bearing resource URLs. The schema
// deliberately has no pass/enable state: runtime and consumer enforcement plus
// semantic, source-rights and paid applicability reviews require a later contract.
func Parse(data []byte) (*Definition, error) {
	var d Definition
	if err := datarights.Decode(data, &d); err != nil {
		return nil, err
	}
	s, err := compiledSchema()
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	if err := s.Validate(value); err != nil {
		return nil, fmt.Errorf("HTTP source schema: %w", err)
	}
	if err := d.validateLinks(); err != nil {
		return nil, err
	}
	return &d, nil
}

func (d Definition) validateLinks() error {
	if _, err := rules.ParsePublicHTTPSURL(d.Provider.URL); err != nil {
		return fmt.Errorf("provider URL: %w", err)
	}
	used := map[string]bool{}
	for name, r := range d.Recordsets {
		if r.ProviderSourceID != "provider:"+d.Provider.ID+"/"+name {
			return fmt.Errorf("recordset %s provider identity mismatch", name)
		}
		if _, ok := d.Resources[r.Resource]; !ok {
			return fmt.Errorf("recordset %s names an undeclared resource", name)
		}
		used[r.Resource] = true
		for _, key := range r.KeyFields {
			if _, ok := r.FieldMapping[key]; !ok {
				return fmt.Errorf("recordset %s key has no field mapping", name)
			}
		}
	}
	for name, r := range d.Resources {
		if !used[name] {
			return fmt.Errorf("resource %s has no modeled recordset", name)
		}
		if _, err := rules.ParsePublicHTTPSURL(r.URL); err != nil {
			return fmt.Errorf("resource URL: %w", err)
		}
	}
	for _, n := range append(append([]license.Notice{}, d.Rights.Terms...), d.Rights.Attribution, d.Rights.FreeSource) {
		if err := license.ValidateURL(n.URL); err != nil {
			return err
		}
	}
	free := false
	for _, r := range d.Resources {
		free = free || r.URL == d.Rights.FreeSource.URL
	}
	if !free {
		return fmt.Errorf("freeSource must link to an original resource")
	}
	return nil
}

// Matches binds this definition to the existing manifest's native recordsets
// and ModelSpec entities. Field meanings and decoder correctness remain gates.
func (d Definition) Matches(names []string, entities map[string]string) error {
	if len(names) != len(d.Recordsets) {
		return fmt.Errorf("HTTP source recordsets differ from manifest")
	}
	for name, r := range d.Recordsets {
		entity := name
		if mapped, ok := entities[name]; ok {
			entity = mapped
		}
		if !slices.Contains(names, name) || entity != r.Entity {
			return fmt.Errorf("HTTP source recordset/model mapping differs from manifest")
		}
	}
	return nil
}

// MatchesTerms prevents an unrelated legacy/SPDX licence from replacing this
// linked upstream declaration. It makes no judgement about permitted reuse.
func (d Definition) MatchesTerms(declaration license.Declaration) error {
	if declaration.SPDX != "" {
		return fmt.Errorf("HTTP source requires linked source terms without inferred SPDX")
	}
	for _, n := range d.Rights.Terms {
		if declaration.URL == n.URL {
			return nil
		}
	}
	return fmt.Errorf("manifest data terms must link to the source definition terms")
}

// RequireExecution is a hard refusal in this preparatory contract, even if a
// caller mutates the decoded struct. A valid definition is not runtime admission.
func (d Definition) RequireExecution() error {
	return fmt.Errorf("HTTP source execution blocked: runtime, semantic, rights and paid gates require admission")
}
