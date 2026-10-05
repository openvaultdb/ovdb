package representation

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// MarshalJSON omits bridge-only fields for the explicitly native format. The
// schema still rejects an input containing those fields; there is no fallback.
func (c Contract) MarshalJSON() ([]byte, error) {
	type plain Contract
	if c.Execution != NativeIdentifier {
		return json.Marshal(plain(c))
	}
	type nativeTarget struct {
		Target
		Keys *Reference `json:"keys,omitempty"`
	}
	return json.Marshal(struct {
		Execution string       `json:"execution"`
		Source    Source       `json:"source"`
		Target    nativeTarget `json:"target"`
		Native    *Native      `json:"native"`
		Policy    Policy       `json:"policy"`
		Decision  Decision     `json:"decision"`
	}{c.Execution, c.Source, nativeTarget{Target: c.Target}, c.Native, c.Policy, c.Decision})
}

func checkNative(c Contract, model []byte, ctx Context) error {
	if c.Native == nil || c.Source.Namespace != c.Target.Namespace {
		return fmt.Errorf("native identifier requires equal exact namespaces")
	}
	for _, ref := range []Reference{c.Native.Dataset, c.Native.Provenance} {
		if ref.Repository != "" || ref.Revision != "" || !path(ref.Path) || !hex(ref.SHA256, 64) {
			return fmt.Errorf("native dataset/provenance must be provider-local immutable artifact references")
		}
	}
	// Dataset is intentionally never passed to Resolve: it can be a large SQLite
	// snapshot. Its checksum was bound through the small generation snapshot.
	provenance, err := read(c.Native.Provenance, ctx)
	if err != nil {
		return err
	}
	if err = checkNativeProvenance(provenance, c); err != nil {
		return err
	}
	var spec struct {
		Entities map[string]struct {
			Key        []string `json:"key"`
			Properties map[string]struct {
				Required bool `json:"required"`
			} `json:"properties"`
		} `json:"entities"`
	}
	if err = exactModel(model, &spec, true); err != nil {
		return err
	}
	entity := spec.Entities[c.Target.Entity]
	if len(entity.Key) != 1 || entity.Key[0] != c.Target.Property || !entity.Properties[c.Target.Property].Required {
		return fmt.Errorf("native target must declare this required single-property key")
	}
	if c.Native.ServingIdentityColumn != "" {
		if c.Native.ServingIdentityColumn == c.Target.Property {
			return fmt.Errorf("native key differs from serving identity")
		}
		if err = property(model, c.Target.Module, c.Target.Entity, c.Native.ServingIdentityColumn, ""); err != nil {
			return err
		}
	}
	return nil
}

// NativeLookupRequest is the exact immutable target scope for an explicit keyed
// data query. Limit two retains ambiguity without downloading a native keyset.
type NativeLookupRequest struct {
	Repository, Revision                          string
	Model, Snapshot, Dataset                      Reference
	Module, Entity, Property, Datatype, Namespace string
	Raw                                           string
	Limit                                         int
}

// LookupNative executes one bounded keyed read via query. This is a data reader,
// not an acceptance oracle. Callers must use a structurally checked, independently
// admitted contract and execute against the request's reviewed immutable snapshot.
// It verifies returned native keys only; status/details remain caller data and no
// live response proves full-source uniqueness or semantic truth.
func LookupNative(c Contract, source Source, ctx Context, raw string, query func(NativeLookupRequest) ([]string, error)) (*string, error) {
	if c.Execution != NativeIdentifier || c.Native == nil {
		return nil, fmt.Errorf("native execution required")
	}
	if c.Source != source {
		return nil, fmt.Errorf("source scope/revision/namespace mismatch")
	}
	if !repository(ctx.Repository) || !hex(ctx.Revision, 40) {
		return nil, fmt.Errorf("immutable outer target required")
	}
	if !utf8.ValidString(raw) || len(raw) > 1024 {
		return nil, fmt.Errorf("native lookup key byte bounds/UTF8")
	}
	if query == nil {
		return nil, fmt.Errorf("bounded native query unavailable")
	}
	req := NativeLookupRequest{Repository: ctx.Repository, Revision: ctx.Revision, Model: c.Target.Model, Snapshot: c.Target.Snapshot, Dataset: c.Native.Dataset, Module: c.Target.Module, Entity: c.Target.Entity, Property: c.Target.Property, Datatype: c.Target.Datatype, Namespace: c.Target.Namespace, Raw: raw, Limit: 2}
	keys, err := query(req)
	if err != nil {
		return nil, err
	}
	if len(keys) > 1 {
		return nil, fmt.Errorf("ambiguous native target or exceeded keyed row bound")
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if keys[0] == "" || keys[0] != raw {
		return nil, fmt.Errorf("unexpected returned native key")
	}
	return &keys[0], nil
}

// Generation claims are checked for exact association only. Independent review
// must prove them against the native source; this is not an acceptance registry.
func checkNativeProvenance(data []byte, c Contract) error {
	var root map[string]any
	if err := strictJSON(data, MaxDocumentBytes, &root); err != nil {
		return err
	}
	if err := exactKeys(root, []string{"native_key", "snapshot"}, false); err != nil {
		return err
	}
	native, _ := root["native_key"].(map[string]any)
	for _, field := range []string{"module", "entity", "property", "namespace", "model", "binding", "dataset", "records", "duplicates"} {
		if _, ok := native[field]; !ok {
			return fmt.Errorf("missing native generation receipt field %s", field)
		}
	}
	for _, field := range []string{"records", "duplicates"} {
		if _, ok := native[field].(float64); !ok {
			return fmt.Errorf("native generation counts must be JSON numbers")
		}
	}
	if err := exactKeys(native, []string{"module", "entity", "property", "namespace", "model", "binding", "dataset", "records", "duplicates"}, true); err != nil {
		return err
	}
	for _, field := range []string{"model", "binding", "dataset"} {
		ref, _ := native[field].(map[string]any)
		if err := exactKeys(ref, []string{"path", "sha256"}, true); err != nil {
			return err
		}
	}
	snapshot, _ := root["snapshot"].(map[string]any)
	if err := exactKeys(snapshot, []string{"outputs", "counts"}, false); err != nil {
		return err
	}
	counts, _ := snapshot["counts"].(map[string]any)
	if _, ok := counts[c.Target.Entity].(float64); !ok {
		return fmt.Errorf("original native snapshot count must exist as a JSON number")
	}
	outputs, _ := snapshot["outputs"].(map[string]any)
	for _, raw := range outputs {
		output, _ := raw.(map[string]any)
		if err := exactKeys(output, []string{"sha256"}, false); err != nil {
			return err
		}
	}
	var receipt struct {
		NativeKey struct {
			Module     string    `json:"module"`
			Entity     string    `json:"entity"`
			Property   string    `json:"property"`
			Namespace  string    `json:"namespace"`
			Model      Reference `json:"model"`
			Binding    Reference `json:"binding"`
			Dataset    Reference `json:"dataset"`
			Records    int64     `json:"records"`
			Duplicates int64     `json:"duplicates"`
		} `json:"native_key"`
		Snapshot struct {
			Outputs map[string]struct {
				SHA256 string `json:"sha256"`
			} `json:"outputs"`
			Counts map[string]int64 `json:"counts"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	n := receipt.NativeKey
	if n.Module != c.Target.Module || n.Entity != c.Target.Entity || n.Property != c.Target.Property || n.Namespace != c.Target.Namespace || n.Model != c.Target.Model || n.Binding != c.Target.Binding.Document || n.Dataset != c.Native.Dataset || n.Records < 0 || n.Duplicates != 0 {
		return fmt.Errorf("native generation receipt scope/model/binding/data/uniqueness association mismatch")
	}
	if receipt.Snapshot.Outputs[n.Dataset.Path].SHA256 != n.Dataset.SHA256 || receipt.Snapshot.Counts[n.Entity] != n.Records {
		return fmt.Errorf("native generation receipt does not bind original snapshot data/counts")
	}
	return nil
}
