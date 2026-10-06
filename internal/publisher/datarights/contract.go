// Package datarights validates the opt-in publication contract. No URL is fetched.
package datarights

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

const Format = "ovdb-data-rights/1"
const ProvenanceFormat = "ovdb-source-rights/1"
const MaxDocumentBytes = 256 << 10

// Reference is provider-local, or explicitly pinned to another repository.
// A local reference inherits the selected provider revision; it cannot self-pin.
type Reference struct {
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision,omitempty"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
}

type Profile struct {
	Format     string                         `json:"format"`
	Server     *Reference                     `json:"server,omitempty"`
	Database   *license.Declaration           `json:"database,omitempty"`
	Recordsets map[string]license.Declaration `json:"recordsets"`
	Provenance Reference                      `json:"provenance"`
}

// Provenance describes source metadata; validation does not certify its truth.
type Provenance struct {
	Format          string          `json:"format"`
	Attribution     license.Notice  `json:"attribution"`
	FreeSource      *license.Notice `json:"freeSource,omitempty"`
	Transformations []string        `json:"transformations"`
	Inputs          []Reference     `json:"inputs"`
	Terms           []Reference     `json:"terms"`
	CapturedAt      string          `json:"capturedAt,omitempty"`
}

var fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (r Reference) Validate() error {
	if !rules.IsRepositoryPath(r.Path) {
		return fmt.Errorf("invalid immutable reference path")
	}
	hash, err := hex.DecodeString(r.SHA256)
	if err != nil || len(hash) != 32 || r.SHA256 != strings.ToLower(r.SHA256) {
		return fmt.Errorf("reference requires a lowercase SHA256")
	}
	if r.Bytes < 0 || r.Bytes > 16<<20 {
		return fmt.Errorf("reference bytes must be between 0 and 16 MiB")
	}
	if r.Repository != "" || r.Revision != "" {
		if _, ok := rules.RepositoryKey(r.Repository); !ok || !fullRevision.MatchString(r.Revision) {
			return fmt.Errorf("external reference requires a repository URL and full revision")
		}
	}
	return nil
}

// Decode refuses duplicate keys anywhere, excessive nesting, and trailing data.
func Decode(data []byte, target any) error {
	if len(data) > MaxDocumentBytes {
		return fmt.Errorf("rights document exceeds 256 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := walk(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("rights document must contain one JSON value")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func walk(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("rights nesting exceeds 64")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter == '{' {
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			text, ok := key.(string)
			if !ok || seen[text] {
				return fmt.Errorf("duplicate rights key")
			}
			seen[text] = true
			if err := walk(d, depth+1); err != nil {
				return err
			}
		}
	} else if delimiter == '[' {
		for d.More() {
			if err := walk(d, depth+1); err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("invalid rights JSON")
	}
	_, err = d.Token()
	return err
}

func ParseProfile(data []byte, profile license.Profile, names []string) (*Profile, error) {
	// Raw fields preserve explicit null, which a pointer alone would mistake for inheritance.
	var raw struct {
		Format     string                     `json:"format"`
		Server     json.RawMessage            `json:"server"`
		Database   json.RawMessage            `json:"database"`
		Recordsets map[string]json.RawMessage `json:"recordsets"`
		Provenance json.RawMessage            `json:"provenance"`
	}
	if err := Decode(data, &raw); err != nil {
		return nil, err
	}
	if raw.Format != Format || raw.Recordsets == nil || len(raw.Provenance) == 0 {
		return nil, fmt.Errorf("data_rights requires format, recordsets and provenance")
	}
	out := &Profile{Format: Format, Recordsets: map[string]license.Declaration{}}
	if len(raw.Server) > 0 {
		out.Server = new(Reference)
		if err := Decode(raw.Server, out.Server); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(raw.Server), []byte("null")) {
			return nil, fmt.Errorf("server reference cannot be null")
		}
		if err := out.Server.Validate(); err != nil {
			return nil, err
		}
	}
	if len(raw.Database) > 0 {
		d, err := license.ParseJSON(raw.Database, profile)
		if err != nil {
			return nil, err
		}
		out.Database = &d
	}
	if err := Decode(raw.Provenance, &out.Provenance); err != nil {
		return nil, err
	}
	if err := out.Provenance.Validate(); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, name := range names {
		if known[name] {
			return nil, fmt.Errorf("duplicate native recordset name")
		}
		known[name] = true
	}
	for name, value := range raw.Recordsets {
		if !known[name] {
			return nil, fmt.Errorf("override names an unlisted native recordset")
		}
		d, err := license.ParseJSON(value, profile)
		if err != nil {
			return nil, err
		}
		out.Recordsets[name] = d
	}
	return out, nil
}

func ParseProvenance(data []byte) (*Provenance, error) {
	var p Provenance
	// Notice fields get supplied-field validation via closed raw object checks.
	var raw struct {
		Format          string          `json:"format"`
		Attribution     json.RawMessage `json:"attribution"`
		FreeSource      json.RawMessage `json:"freeSource"`
		Transformations []string        `json:"transformations"`
		Inputs          []Reference     `json:"inputs"`
		Terms           []Reference     `json:"terms"`
		CapturedAt      json.RawMessage `json:"capturedAt"`
	}
	if err := Decode(data, &raw); err != nil {
		return nil, err
	}
	p.Format = raw.Format
	p.Transformations = raw.Transformations
	p.Inputs = raw.Inputs
	p.Terms = raw.Terms
	if p.Format != ProvenanceFormat || p.Transformations == nil || p.Inputs == nil || p.Terms == nil {
		return nil, fmt.Errorf("source provenance requires format, transformations, inputs and terms")
	}
	notice := func(value []byte, requiredURL bool) (*license.Notice, error) {
		var n license.Notice
		if err := Decode(value, &n); err != nil {
			return nil, err
		}
		if _, err := license.ParseJSON([]byte(fmt.Sprintf(`{"text":%s}`, quote(n.Text))), license.Directory); err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(value, &fields)
		if url, ok := fields["url"]; ok {
			var text string
			if err := json.Unmarshal(url, &text); err != nil {
				return nil, err
			}
			if err := license.ValidateURL(text); err != nil {
				return nil, err
			}
		} else if requiredURL {
			return nil, fmt.Errorf("freeSource requires URL")
		}
		return &n, nil
	}
	n, err := notice(raw.Attribution, false)
	if err != nil {
		return nil, err
	}
	p.Attribution = *n
	if len(raw.FreeSource) > 0 {
		p.FreeSource, err = notice(raw.FreeSource, true)
		if err != nil {
			return nil, err
		}
	}
	for _, text := range p.Transformations {
		if _, err := license.ParseJSON([]byte(fmt.Sprintf(`{"text":%s}`, quote(text))), license.Directory); err != nil {
			return nil, err
		}
	}
	for _, refs := range [][]Reference{p.Inputs, p.Terms} {
		for _, ref := range refs {
			if err := ref.Validate(); err != nil {
				return nil, err
			}
		}
	}
	if len(raw.CapturedAt) > 0 {
		if err := json.Unmarshal(raw.CapturedAt, &p.CapturedAt); err != nil {
			return nil, err
		}
		if _, err := time.Parse("2006-01-02", p.CapturedAt); err != nil {
			return nil, fmt.Errorf("capturedAt must be a calendar date")
		}
	}
	return &p, nil
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// UnmarshalJSON preserves presence and rejects null/missing reference fields.
func (r *Reference) UnmarshalJSON(data []byte) error {
	type plain Reference
	var out plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"path", "sha256", "bytes"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("reference requires nonnull %s", key)
		}
	}
	for _, key := range []string{"repository", "revision"} {
		if value, ok := fields[key]; ok {
			var s string
			if err := json.Unmarshal(value, &s); err != nil || s == "" {
				return fmt.Errorf("explicit reference %s must be nonblank", key)
			}
		}
	}
	*r = Reference(out)
	return r.Validate()
}
