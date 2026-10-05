package representation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Malformed documents and broken referenced assets exercise fail-closed boundaries
// without weakening the closed schema or skipping the exact publisher gate.
func TestAdditionalRejectedDocuments(t *testing.T) {
	for _, name := range []string{"target.modelspec.json", "snapshot.json", "target.meaning.json", "bridge.json", "decision.md", "keys.json"} {
		t.Run("unreadable/"+name, func(t *testing.T) {
			data, ctx := fixture(t)
			old := ctx.Resolve
			ctx.Resolve = func(r Reference) ([]byte, error) {
				if r.Path == name {
					return nil, errors.New("unreadable immutable object")
				}
				return old(r)
			}
			if _, err := Check(data, ctx); err == nil {
				t.Fatal("unreadable object accepted")
			}
		})
	}
	for _, name := range []string{"source.modelspec.json", "snapshot.json", "target.meaning.json", "core.meaning.json", "bridge.json"} {
		t.Run("malformed/"+name, func(t *testing.T) {
			data, ctx := fixture(t)
			doc, _ := Parse(data)
			c := &doc.Contracts[0]
			raw := []byte("[")
			for _, r := range []*Reference{&c.Source.Schema, &c.Target.Snapshot, &c.Target.Binding.Document, &c.Target.Binding.Meaning.Document, &c.Bridge.Artifact} {
				if r.Path == name {
					r.SHA256 = Hash(raw)
				}
			}
			old := ctx.Resolve
			var snap []byte
			if name == "bridge.json" {
				snap, _ = old(c.Target.Snapshot)
				var v map[string]any
				_ = json.Unmarshal(snap, &v)
				for _, item := range v["artifacts"].([]any) {
					m := item.(map[string]any)
					if m["path"] == name {
						m["sha256"] = Hash(raw)
					}
				}
				snap, _ = json.Marshal(v)
				c.Target.Snapshot.SHA256 = Hash(snap)
			}
			ctx.Resolve = func(r Reference) ([]byte, error) {
				if r.Path == name {
					return raw, nil
				}
				if snap != nil && r.Path == "snapshot.json" {
					return snap, nil
				}
				return old(r)
			}
			data, _ = json.Marshal(doc)
			if _, err := Check(data, ctx); err == nil {
				t.Fatal("malformed asset accepted")
			}
		})
	}
	data, ctx := fixture(t)
	doc, _ := Parse(data)
	doc.Contracts[0].Source.Schema.Repository = ""
	doc.Contracts[0].Source.Schema.Revision = ""
	data, _ = json.Marshal(doc)
	if _, err := Check(data, ctx); err == nil {
		t.Fatal("unqualified source accepted")
	}
	for _, field := range []string{"target", "serving"} {
		data, ctx := fixture(t)
		doc, _ := Parse(data)
		if field == "target" {
			doc.Contracts[0].Bridge.TargetKeyColumn = "missing"
		} else {
			doc.Contracts[0].Bridge.ServingIdentityColumn = "missing"
		}
		data, _ = json.Marshal(doc)
		if _, err := Check(data, ctx); err == nil {
			t.Fatal("missing physical column accepted")
		}
	}
	if _, err := read(Reference{Path: "../escape"}, ctx); err == nil {
		t.Fatal("unsafe read accepted")
	}
	for _, input := range []string{strings.Repeat("[", 34) + strings.Repeat("]", 34), `{"x"`, `{"x":`, `{"x":1,`, `[[`} {
		var v any
		if err := strictJSON([]byte(input), MaxDocumentBytes, &v); err == nil {
			t.Fatal("malformed/deep document accepted")
		}
	}
	var b BridgeArtifact
	if err := exactClosed([]byte("["), &b, []string{"table", "rows"}, true); err == nil {
		t.Fatal("malformed bridge accepted")
	}
	if repository("https://elsewhere.example/a/b") || path("https://example.org/keys.json") {
		t.Fatal("unsafe location accepted")
	}
	for _, raw := range []string{`{`, `{"generator":{"repository":"https://github.com/example/provider","revision":"` + strings.Repeat("b", 40) + `"},"artifacts":[{"path":"../escape","sha256":"` + strings.Repeat("a", 64) + `"}]}`} {
		if err := checkSnapshot([]byte(raw), doc.Contracts[0]); err == nil {
			t.Fatal("bad snapshot accepted")
		}
	}
}
func TestSchemaDefinitionIntegrity(t *testing.T) {
	if _, err := compileSchema("schema.json", []byte("[")); err == nil {
		t.Fatal("invalid embedded schema bytes accepted")
	}
	if _, err := compileSchema("https://json-schema.org/draft/2020-12/schema", []byte("{}")); err == nil {
		t.Fatal("reserved schema resource accepted")
	}
	if _, err := compileSchema("schema.json", []byte(`{"type":"invalid"}`)); err == nil {
		t.Fatal("invalid schema definition accepted")
	}
	if _, err := decodeDocument([]byte(`{"contracts": 7}`)); err == nil {
		t.Fatal("incompatible document type accepted")
	}
	// Corrupt schema bytes must fail closed rather than produce a validation pass.
	original := schemaJSON
	schemaJSON = "["
	t.Cleanup(func() { schemaJSON = original })
	if _, err := Parse([]byte(`{}`)); err == nil {
		t.Fatal("corrupt schema accepted")
	}
}

func TestSchemaReturnsIndependentBytes(t *testing.T) {
	data := Schema()
	data[0] = '!'
	if Schema()[0] == '!' {
		t.Fatal("caller altered canonical schema")
	}
}
