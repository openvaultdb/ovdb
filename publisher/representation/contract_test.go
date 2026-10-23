package representation

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) ([]byte, Context) {
	t.Helper()
	data, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx := Context{Repository: "https://github.com/example/provider", Revision: strings.Repeat("a", 40)}
	ctx.Resolve = func(ref Reference) ([]byte, error) {
		if ref.Repository != "" && (ref.Repository != "https://github.com/example/source" || ref.Revision != strings.Repeat("b", 40)) {
			return nil, errors.New("immutable dependency not available")
		}
		return os.ReadFile("testdata/" + ref.Path)
	}
	return data, ctx
}
func TestReferenceFixture(t *testing.T) {
	data, ctx := fixture(t)
	doc, err := Check(data, ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := doc.Contracts[0]
	rows := []Row{{RawLabel: "USA", TargetKey: "US"}}
	for _, tc := range []struct {
		raw  string
		want bool
	}{{"USA", true}, {"usa", false}, {" USA", false}, {"USA ", false}, {"", false}} {
		found, err := Lookup(c, c.Source, rows, tc.raw)
		if err != nil || (found != nil) != tc.want {
			t.Fatalf("%q: %v %v", tc.raw, found, err)
		}
	}
	for _, mutate := range []func(*Source){func(s *Source) { s.Property = "BillingCountry" }, func(s *Source) { s.Schema.Revision = strings.Repeat("c", 40) }, func(s *Source) { s.Namespace = "other" }} {
		s := c.Source
		mutate(&s)
		if _, err := Lookup(c, s, rows, "USA"); err == nil {
			t.Fatal("wrong source scope accepted")
		}
	}
	if _, err := Lookup(c, c.Source, append(rows, rows...), "USA"); err == nil {
		t.Fatal("collision accepted")
	}
}
func TestContractNegatives(t *testing.T) {
	tests := map[string]func(*Document, *Context){
		"unsupported version": func(d *Document, _ *Context) { d.Format = "ovdb-representation-contract/2" },
		"wrong property":      func(d *Document, _ *Context) { d.Contracts[0].Source.Property = "Missing" },
		"wrong module":        func(d *Document, _ *Context) { d.Contracts[0].Source.Module = "wrong" },
		"wrong revision":      func(d *Document, _ *Context) { d.Contracts[0].Source.Schema.Revision = strings.Repeat("c", 40) },
		"mutable revision":    func(d *Document, _ *Context) { d.Contracts[0].Source.Schema.Revision = "main" },
		"wrong namespace":     func(d *Document, _ *Context) { d.Contracts[0].Target.Namespace = "other" },
		"wrong hash":          func(d *Document, _ *Context) { d.Contracts[0].Bridge.Artifact.SHA256 = strings.Repeat("c", 64) },
		"path escape":         func(d *Document, _ *Context) { d.Contracts[0].Bridge.Artifact.Path = "../bridge.json" },
		"URL instead of path": func(d *Document, _ *Context) { d.Contracts[0].Bridge.Artifact.Path = "https://example.com/bridge.json" },
		"unsafe repository": func(d *Document, _ *Context) {
			d.Contracts[0].Source.Schema.Repository = "https://github.com/../source"
		},
		"self pin":               func(d *Document, c *Context) { d.Contracts[0].Source.Schema.Repository = c.Repository },
		"wrong binding property": func(d *Document, _ *Context) { d.Contracts[0].Target.Property = "raw_label" },
		"wrong meaning":          func(d *Document, _ *Context) { d.Contracts[0].Target.Binding.Meaning.Concept = "missing" },
		"wrong binding pin": func(d *Document, _ *Context) {
			d.Contracts[0].Target.Binding.Meaning.Document.Revision = strings.Repeat("c", 40)
		},
		"external bridge": func(d *Document, _ *Context) {
			d.Contracts[0].Bridge.Artifact.Repository = "https://github.com/example/source"
			d.Contracts[0].Bridge.Artifact.Revision = strings.Repeat("b", 40)
		},
		"unresolved dependency": func(_ *Document, c *Context) { c.Resolve = nil },
		"bad outer pin":         func(_ *Document, c *Context) { c.Revision = "main" },
		"normalization":         func(d *Document, _ *Context) { d.Contracts[0].Policy.Equality = "case-insensitive" },
		"transform":             func(d *Document, _ *Context) { d.Contracts[0].Policy.Transform = "uppercase" },
		"serving conflation":    func(d *Document, _ *Context) { d.Contracts[0].Bridge.ServingIdentityColumn = "target_key" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			data, ctx := fixture(t)
			doc, _ := Parse(data)
			mutate(doc, &ctx)
			data, _ = json.Marshal(doc)
			if _, err := Check(data, ctx); err == nil {
				t.Fatal("negative accepted")
			}
		})
	}
}
func TestArtifactNegatives(t *testing.T) {
	for _, raw := range []string{`{"table":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"US"},{"raw_label":"USA","target_key":"GB"}]}`, `{"table":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"ZZ"}]}`, `{"table":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"us"}]}`, `{"table":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"US "}]}`, `{"table":"other","rows":[]}`, `{"table":"CustomerCountries","rows":[{"raw_label":"","target_key":"US"}]}`, `{"table":"CustomerCountries","rows":[],"secret":"no"}`} {
		data, ctx := fixture(t)
		doc, _ := Parse(data)
		doc.Contracts[0].Bridge.Artifact.SHA256 = Hash([]byte(raw))
		old := ctx.Resolve
		snapshot, _ := old(doc.Contracts[0].Target.Snapshot)
		snapshot = []byte(strings.ReplaceAll(string(snapshot), doc.Contracts[0].Bridge.Artifact.SHA256, Hash([]byte(raw))))
		// Update the snapshot receipt too so this test reaches row/collision validation.
		original, _ := os.ReadFile("testdata/bridge.json")
		snapshot = []byte(strings.ReplaceAll(string(snapshot), Hash(original), Hash([]byte(raw))))
		doc.Contracts[0].Target.Snapshot.SHA256 = Hash(snapshot)
		ctx.Resolve = func(r Reference) ([]byte, error) {
			if r.Path == "snapshot.json" {
				return snapshot, nil
			}
			if r.Path == "bridge.json" {
				return []byte(raw), nil
			}
			return old(r)
		}
		data, _ = json.Marshal(doc)
		if _, err := Check(data, ctx); err == nil {
			t.Fatalf("artifact accepted: %s", raw)
		}
	}
}
func TestClosedJSON(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{"format":"ovdb-representation-contract/1","format":"ovdb-representation-contract/1","contracts":[]}`), []byte(`{"format":"ovdb-representation-contract/1","contracts":[],"secret":"x"}`), []byte(`{} {}`), []byte{0xff}, []byte(strings.Repeat(" ", MaxDocumentBytes+1)), []byte("[")} {
		if _, err := Parse(data); err == nil {
			t.Fatal("bad JSON accepted")
		}
	}
}
