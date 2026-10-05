// Package representation validates scoped execution attachments. It does not
// establish semantic acceptance: independent review and canonical publication
// admission remain prerequisites for product eligibility.
package representation

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The embedded schema is private so callers cannot change validation rules.
//
//go:embed schema.json
var schemaJSON string

//go:embed schema2.json
var schema2JSON string

//go:embed schema3.json
var schema3JSON string

// Schema returns the closed version-one interchange schema as an independent copy.
func Schema() []byte { return []byte(schemaJSON) }

const Format = "ovdb-representation-contract/1"
const Format2 = "ovdb-representation-contract/2"
const Format3 = "ovdb-representation-contract/3"
const LabelBridge = "label-bridge"
const NativeIdentifier = "native-identifier"

// Schema2 returns the closed discriminated execution schema.
func Schema2() []byte { return []byte(schema2JSON) }

// Schema3 returns the closed native exact-artifact interchange schema.
func Schema3() []byte { return []byte(schema3JSON) }

const MaxDocumentBytes = 2 << 20
const MaxArtifactBytes = 4 << 20

// Reference names a committed regular file. An own-provider reference omits
// Repository and Revision; its revision is the outer Directory provider pin.
type Reference struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision,omitempty"`
}
type Source struct {
	Schema    Reference  `json:"schema"`
	Data      *Reference `json:"data,omitempty"`
	Module    string     `json:"module"`
	Entity    string     `json:"entity"`
	Property  string     `json:"property"`
	Datatype  string     `json:"datatype"`
	Namespace string     `json:"namespace"`
}
type Meaning struct {
	Document Reference `json:"document"`
	Concept  string    `json:"concept"`
}
type Binding struct {
	Document Reference `json:"document"`
	Concept  string    `json:"concept"`
	Role     string    `json:"role"`
	Meaning  Meaning   `json:"meaning"`
}
type Target struct {
	Keys      Reference `json:"keys"`
	Snapshot  Reference `json:"snapshot"`
	Model     Reference `json:"model"`
	Module    string    `json:"module"`
	Entity    string    `json:"entity"`
	Property  string    `json:"property"`
	Datatype  string    `json:"datatype"`
	Namespace string    `json:"namespace"`
	Binding   Binding   `json:"binding"`
}
type Bridge struct {
	Artifact              Reference `json:"artifact"`
	Table                 string    `json:"table"`
	RawLabelColumn        string    `json:"raw_label_column"`
	TargetKeyColumn       string    `json:"target_key_column"`
	ServingIdentityColumn string    `json:"serving_identity_column,omitempty"`
}
type Policy struct {
	Transform   string `json:"transform"`
	Equality    string `json:"equality"`
	Cardinality string `json:"cardinality"`
	Unmatched   string `json:"unmatched"`
	Collision   string `json:"collision"`
}
type Decision struct {
	Document Reference `json:"document"`
	Scope    string    `json:"scope"`
}
type Native struct {
	Dataset               Reference `json:"dataset"`
	Provenance            Reference `json:"provenance"`
	ServingIdentityColumn string    `json:"serving_identity_column,omitempty"`
}
type Contract struct {
	Execution string   `json:"execution,omitempty"`
	Native    *Native  `json:"native,omitempty"`
	Source    Source   `json:"source"`
	Target    Target   `json:"target"`
	Bridge    Bridge   `json:"bridge"`
	Policy    Policy   `json:"policy"`
	Decision  Decision `json:"decision"`
}
type Document struct {
	Format    string     `json:"format"`
	Contracts []Contract `json:"contracts"`
}

// Context supplies checked immutable files. Resolve must check repository identity,
// exact revision and regular-file status, never follow redirects/symlinks or
// implicitly fetch a mutable branch. Check validates paths, limits and hashes.
// Independent semantic review and trusted canonical publication are outside this
// structural validator; a successful Check alone does not authorize eligibility.
type Context struct {
	Repository string
	Revision   string
	Resolve    func(Reference) ([]byte, error)
}

type Row struct {
	RawLabel  string `json:"raw_label"`
	TargetKey string `json:"target_key"`
}
type BridgeArtifact struct {
	Table string `json:"table"`
	Rows  []Row  `json:"rows"`
}

// Hash returns the lower-case SHA256 of the exact bytes.
func Hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// Parse checks a bounded closed JSON document, including duplicate keys and UTF8.
func Parse(data []byte) (*Document, error) {
	var value any
	if err := strictJSON(data, MaxDocumentBytes, &value); err != nil {
		return nil, err
	}
	schemaData := schemaJSON
	if object, ok := value.(map[string]any); ok {
		switch object["format"] {
		case Format2:
			schemaData = schema2JSON
		case Format3:
			schemaData = schema3JSON
		}
	}
	schema, err := compileSchema("schema.json", []byte(schemaData))
	if err != nil {
		return nil, err
	}
	if err := schema.Validate(value); err != nil {
		return nil, fmt.Errorf("representation schema: %w", err)
	}
	return decodeDocument(data)
}

// sameSource compares exact data coordinates as values, including a missing
// descriptor. Pointer identity cannot grant source equivalence.
func sameSource(a, b Source) bool {
	ad, bd := a.Data, b.Data
	a.Data, b.Data = nil, nil
	if a != b || (ad == nil) != (bd == nil) {
		return false
	}
	return ad == nil || *ad == *bd
}

// externalReference validates descriptor syntax without resolving data bytes.
func externalReference(ref Reference, ctx Context) bool {
	return path(ref.Path) && hex(ref.SHA256, 64) && repository(ref.Repository) && hex(ref.Revision, 40) && ref.Repository != ctx.Repository
}

// Check verifies reference closure, property/binding identity, bridge cardinality
// and decision provenance. A successful Check is structural verification only.
func Check(data []byte, ctx Context) (*Document, error) {
	doc, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if !repository(ctx.Repository) || !hex(ctx.Revision, 40) {
		return nil, fmt.Errorf("outer provider repository/revision must be immutable")
	}
	var seen []Source
	for i, c := range doc.Contracts {
		for _, prior := range seen {
			if sameSource(prior, c.Source) {
				return nil, fmt.Errorf("duplicate source scope is ineligible")
			}
		}
		seen = append(seen, c.Source)
		if err := checkContract(c, doc.Format, ctx); err != nil {
			return nil, fmt.Errorf("contracts[%d]: %w", i, err)
		}
	}
	return doc, nil
}

func read(ref Reference, ctx Context) ([]byte, error) {
	if !path(ref.Path) || !hex(ref.SHA256, 64) {
		return nil, fmt.Errorf("unsafe reference path or checksum")
	}
	if ref.Repository != "" || ref.Revision != "" {
		if !repository(ref.Repository) || !hex(ref.Revision, 40) || ref.Repository == ctx.Repository {
			return nil, fmt.Errorf("external references require another repository and immutable revision; own references stay relative")
		}
	}
	if ctx.Resolve == nil {
		return nil, fmt.Errorf("reference resolver unavailable")
	}
	data, err := ctx.Resolve(ref)
	if err != nil {
		return nil, fmt.Errorf("unresolved %s: %w", ref.Path, err)
	}
	if len(data) > MaxArtifactBytes || Hash(data) != ref.SHA256 {
		return nil, fmt.Errorf("reference %s byte limit/checksum mismatch", ref.Path)
	}
	return data, nil
}

func checkContract(c Contract, format string, ctx Context) error {
	if c.Source.Schema.Repository == "" {
		return fmt.Errorf("source schema must name immutable external source repository/revision")
	}
	if format == Format3 {
		if c.Execution != NativeIdentifier || c.Source.Data == nil || !externalReference(*c.Source.Data, ctx) {
			return fmt.Errorf("exact-artifact native source.data must be an immutable external reference")
		}
	}
	if c.Target.Keys.Repository != "" || c.Target.Model.Repository != "" || c.Target.Snapshot.Repository != "" || c.Bridge.Artifact.Repository != "" || c.Target.Binding.Document.Repository != "" {
		return fmt.Errorf("target model/snapshot/binding/bridge must be provider-local")
	}
	source, err := read(c.Source.Schema, ctx)
	if err != nil {
		return err
	}
	if err = property(source, c.Source.Module, c.Source.Entity, c.Source.Property, c.Source.Datatype); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	model, err := read(c.Target.Model, ctx)
	if err != nil {
		return err
	}
	if err = property(model, c.Target.Module, c.Target.Entity, c.Target.Property, c.Target.Datatype); err != nil {
		return fmt.Errorf("target: %w", err)
	}
	snapshot, err := read(c.Target.Snapshot, ctx)
	if err != nil {
		return err
	}
	if err = checkSnapshot(snapshot, c); err != nil {
		return err
	}
	binding, err := read(c.Target.Binding.Document, ctx)
	if err != nil {
		return err
	}
	meaning, err := read(c.Target.Binding.Meaning.Document, ctx)
	if err != nil {
		return err
	}
	if err = checkBinding(binding, meaning, c.Target); err != nil {
		return err
	}
	if c.Decision.Document.Repository == "" {
		return fmt.Errorf("decision provenance must be externally pinned")
	}
	if _, err = read(c.Decision.Document, ctx); err != nil {
		return err
	}
	if c.Execution == NativeIdentifier {
		return checkNative(c, model, ctx, snapshot)
	}
	if c.Bridge.RawLabelColumn == c.Bridge.TargetKeyColumn || c.Bridge.ServingIdentityColumn != "" && (c.Bridge.ServingIdentityColumn == c.Bridge.RawLabelColumn || c.Bridge.ServingIdentityColumn == c.Bridge.TargetKeyColumn) {
		return fmt.Errorf("native/raw columns must differ from serving identity")
	}
	if c.Bridge.ServingIdentityColumn != "" {
		if err = property(model, c.Target.Module, c.Bridge.Table, c.Bridge.ServingIdentityColumn, ""); err != nil {
			return fmt.Errorf("serving identity column: %w", err)
		}
	}
	if err = property(model, c.Target.Module, c.Bridge.Table, c.Bridge.RawLabelColumn, "string"); err != nil {
		return fmt.Errorf("bridge raw column: %w", err)
	}
	if err = property(model, c.Target.Module, c.Bridge.Table, c.Bridge.TargetKeyColumn, "string"); err != nil {
		return fmt.Errorf("bridge target column: %w", err)
	}
	artifact, err := read(c.Bridge.Artifact, ctx)
	if err != nil {
		return err
	}
	var bridge BridgeArtifact
	if err = exactClosed(artifact, &bridge, []string{"table", "rows"}, true); err != nil {
		return err
	}
	if bridge.Table != c.Bridge.Table || len(bridge.Rows) == 0 || len(bridge.Rows) > 10000 {
		return fmt.Errorf("bridge table/row bounds mismatch")
	}
	labels := map[string]bool{}
	for _, row := range bridge.Rows {
		if row.RawLabel == "" || row.TargetKey == "" || labels[row.RawLabel] {
			return fmt.Errorf("empty or duplicate raw-label collision is ineligible")
		}
		labels[row.RawLabel] = true
	}
	keyData, err := read(c.Target.Keys, ctx)
	if err != nil {
		return err
	}
	var index struct {
		Namespace string   `json:"namespace"`
		Keys      []string `json:"keys"`
	}
	if err = exactClosed(keyData, &index, []string{"namespace", "keys"}, false); err != nil {
		return err
	}
	if index.Namespace != c.Target.Namespace || len(index.Keys) == 0 || len(index.Keys) > 10000 {
		return fmt.Errorf("target key index namespace/bounds mismatch")
	}
	keys := map[string]bool{}
	for _, key := range index.Keys {
		if key == "" || keys[key] {
			return fmt.Errorf("empty or duplicate native target key")
		}
		keys[key] = true
	}
	for _, row := range bridge.Rows {
		if !keys[row.TargetKey] {
			return fmt.Errorf("bridge target key does not exist in native target index")
		}
	}

	return nil
}

func property(data []byte, module, entity, key, datatype string) error {
	var spec struct {
		Format string `json:"modelspec"`
		Module struct {
			Name string `json:"name"`
		} `json:"module"`
		Entities map[string]struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		} `json:"entities"`
	}
	if err := exactModel(data, &spec, false); err != nil {
		return err
	}
	p, ok := spec.Entities[entity].Properties[key]
	if spec.Format != "1.0-draft" || spec.Module.Name != module || !ok || datatype != "" && p.Type != datatype {
		return fmt.Errorf("ModelSpec module/entity/property/datatype does not resolve exactly: %s.%s.%s", module, entity, key)
	}
	return nil
}

func checkBinding(binding, meaning []byte, t Target) error {
	var graph struct {
		Format   string `yaml:"format"`
		Concepts []struct {
			ID       string `yaml:"id"`
			Extends  string `yaml:"extends"`
			Bindings []struct {
				Model    string `yaml:"model"`
				Property string `yaml:"property"`
				Role     string `yaml:"role"`
			} `yaml:"bindings"`
		} `yaml:"concepts"`
	}
	var core struct {
		Format   string `yaml:"format"`
		Concepts []struct {
			ID string `yaml:"id"`
		} `yaml:"concepts"`
	}
	if err := singleYAML(binding, &graph); err != nil {
		return err
	}
	if err := singleYAML(meaning, &core); err != nil {
		return err
	}
	if graph.Format != "meaning/draft-1" || core.Format != "meaning/draft-1" {
		return fmt.Errorf("unsupported meaning format")
	}
	mr := t.Binding.Meaning.Document
	if mr.Repository == "" {
		return fmt.Errorf("canonical meaning must be externally pinned")
	}
	found := false
	for _, concept := range core.Concepts {
		if concept.ID == t.Binding.Meaning.Concept {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("canonical meaning concept does not resolve")
	}
	address := "meaning://" + strings.TrimPrefix(mr.Repository, "https://") + "/" + t.Binding.Meaning.Concept + "?ref=" + mr.Revision
	for _, concept := range graph.Concepts {
		if concept.ID != t.Binding.Concept || concept.Extends != address {
			continue
		}
		for _, b := range concept.Bindings {
			if b.Model == "modelspec:///"+t.Module+"."+t.Entity && b.Property == t.Property && b.Role == t.Binding.Role {
				return nil
			}
		}
	}
	return fmt.Errorf("canonical property/binding/meaning pin does not resolve exactly")
}

func strictJSON(data []byte, limit int, out any) error {
	if len(data) > limit || !utf8.Valid(data) {
		return fmt.Errorf("JSON byte limit/UTF8 violation")
	}
	if err := scalarEscapes(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := tokens(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("JSON trailing data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// The contract/schema is closed separately. Referenced canonical documents
	// retain their full existing ModelSpec/MeaningGraph surface.
	return decoder.Decode(out)
}
func tokens(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("JSON too deeply nested")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delimiter, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if delimiter == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = true
		}
		if err := tokens(dec, depth+1); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}
func hex(s string, n int) bool { return len(s) == n && strings.Trim(s, "0123456789abcdef") == "" }
func repository(s string) bool {
	parts := strings.Split(strings.TrimPrefix(s, "https://github.com/"), "/")
	if !strings.HasPrefix(s, "https://github.com/") || len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.Trim(p, "abcdefghijklmnopqrstuvwxyz0123456789_.-") != "" {
			return false
		}
	}
	return !strings.HasSuffix(s, ".git")
}
func path(s string) bool {
	if len(s) == 0 || len(s) > 1024 || strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_./$-") != "" {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." || strings.EqualFold(p, ".git") || strings.Contains(p, "$") && p != "$records" {
			return false
		}
	}
	return true
}

// Lookup selects one exact scope and compares raw input without case folding or
// trimming. nil means an unmatched exception, never a guessed target. Callers
// must first verify/admit the immutable Document through canonical metadata.
func Lookup(c Contract, source Source, rows []Row, raw string) (*string, error) {
	if c.Execution == NativeIdentifier {
		return nil, fmt.Errorf("native mode requires bounded keyed lookup")
	}
	if c.Source != source {
		return nil, fmt.Errorf("source scope/revision/namespace mismatch")
	}
	var found *string
	for _, row := range rows {
		if row.RawLabel == raw {
			if found != nil {
				return nil, fmt.Errorf("raw-label collision")
			}
			value := row.TargetKey
			found = &value
		}
	}
	return found, nil
}

func checkSnapshot(data []byte, c Contract) error {
	var snapshot struct {
		Generator struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
		} `json:"generator"`
		Artifacts []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err := exactSnapshot(data, &snapshot); err != nil {
		return err
	}
	if !repository(snapshot.Generator.Repository) || !hex(snapshot.Generator.Revision, 40) || len(snapshot.Artifacts) > 10000 {
		return fmt.Errorf("snapshot generator provenance or artifact bounds invalid")
	}
	found := map[Reference]bool{}
	seen := map[string]bool{}
	for _, artifact := range snapshot.Artifacts {
		if !path(artifact.Path) || !hex(artifact.SHA256, 64) || seen[artifact.Path] {
			return fmt.Errorf("invalid or duplicate snapshot artifact")
		}
		seen[artifact.Path] = true
		found[Reference{Path: artifact.Path, SHA256: artifact.SHA256}] = true
	}
	if c.Execution == NativeIdentifier {
		for _, ref := range []Reference{c.Native.Dataset, c.Native.Provenance, c.Target.Model, c.Target.Binding.Document} {
			if !found[ref] {
				return fmt.Errorf("native data/model/binding/provenance must match snapshot artifact checksums")
			}
		}
	} else if !found[c.Bridge.Artifact] || !found[c.Target.Keys] {
		return fmt.Errorf("bridge and native key index must match snapshot artifact checksums")
	}
	return nil
}

func compileSchema(id string, data []byte) (*jsonschema.Schema, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, value); err != nil {
		return nil, err
	}
	return compiler.Compile(id)
}
func decodeDocument(data []byte) (*Document, error) {
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
