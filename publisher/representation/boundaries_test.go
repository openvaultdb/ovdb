package representation

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCanonicalYAMLAndArtifactBounds(t *testing.T) {
	data, ctx := fixture(t)
	doc, _ := Parse(data)
	core := []byte("format: meaning/draft-1\nconcepts:\n  - id: country\n    kind: entity\n")
	doc.Contracts[0].Target.Binding.Meaning.Document.SHA256 = Hash(core)
	old := ctx.Resolve
	ctx.Resolve = func(r Reference) ([]byte, error) {
		if r.Path == "core.meaning.json" {
			return core, nil
		}
		return old(r)
	}
	data, _ = json.Marshal(doc)
	if _, err := Check(data, ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty keys", "duplicate keys", "unknown keys member", "bad snapshot generator", "snapshot omission", "source schema missing", "source type", "target model", "binding format", "binding missing", "meaning format", "local canonical meaning", "local decision", "duplicate scope", "raw column", "same columns", "oversized artifact"} {
		t.Run(name, func(t *testing.T) {
			data, ctx := fixture(t)
			doc, _ := Parse(data)
			c := &doc.Contracts[0]
			old := ctx.Resolve
			var asset string
			var raw []byte
			switch name {
			case "empty keys":
				asset = "keys.json"
				raw = []byte(`{"namespace":"iso-3166-1-alpha-2","keys":[]}`)
			case "duplicate keys":
				asset = "keys.json"
				raw = []byte(`{"namespace":"iso-3166-1-alpha-2","keys":["US","US"]}`)
			case "unknown keys member":
				asset = "keys.json"
				raw = []byte(`{"namespace":"iso-3166-1-alpha-2","keys":["US"],"unknown":true}`)
			case "bad snapshot generator":
				asset = "snapshot.json"
				raw = []byte(`{"generator":{"repository":"https://github.com/example/provider","revision":"main"},"artifacts":[]}`)
			case "snapshot omission":
				asset = "snapshot.json"
				raw = []byte(`{"generator":{"repository":"https://github.com/example/provider","revision":"` + strings.Repeat("b", 40) + `"},"artifacts":[]}`)
			case "source schema missing":
				ctx.Resolve = func(Reference) ([]byte, error) { return nil, errors.New("missing schema") }
			case "source type":
				c.Source.Datatype = "integer"
			case "target model":
				c.Target.Property = "missing"
			case "binding format":
				asset = "target.meaning.json"
				raw = []byte(`{"format":"meaning/unknown","concepts":[]}`)
			case "binding missing":
				c.Target.Binding.Concept = "missing"
			case "meaning format":
				asset = "core.meaning.json"
				raw = []byte(`{"format":"meaning/unknown","concepts":[]}`)
			case "local canonical meaning":
				c.Target.Binding.Meaning.Document.Repository = ""
				c.Target.Binding.Meaning.Document.Revision = ""
			case "local decision":
				c.Decision.Document.Repository = ""
				c.Decision.Document.Revision = ""
			case "duplicate scope":
				doc.Contracts = append(doc.Contracts, *c)
			case "raw column":
				c.Bridge.RawLabelColumn = "missing"
			case "same columns":
				c.Bridge.TargetKeyColumn = c.Bridge.RawLabelColumn
			case "oversized artifact":
				asset = "source.modelspec.json"
				raw = []byte(strings.Repeat(" ", MaxArtifactBytes+1))
			}
			if asset != "" {
				for _, r := range []*Reference{&c.Source.Schema, &c.Target.Model, &c.Target.Snapshot, &c.Target.Keys, &c.Target.Binding.Document, &c.Target.Binding.Meaning.Document, &c.Decision.Document} {
					if r.Path == asset {
						r.SHA256 = Hash(raw)
					}
				}
				if asset == "keys.json" {
					snap, _ := old(c.Target.Snapshot)
					previous, _ := os.ReadFile("testdata/keys.json")
					snap = []byte(strings.ReplaceAll(string(snap), Hash(previous), Hash(raw)))
					c.Target.Snapshot.SHA256 = Hash(snap)
					ctx.Resolve = func(r Reference) ([]byte, error) {
						if r.Path == "snapshot.json" {
							return snap, nil
						}
						if r.Path == asset {
							return raw, nil
						}
						return old(r)
					}
				} else {
					ctx.Resolve = func(r Reference) ([]byte, error) {
						if r.Path == asset {
							return raw, nil
						}
						return old(r)
					}
				}
			}
			data, _ = json.Marshal(doc)
			if _, err := Check(data, ctx); err == nil {
				t.Fatal("boundary admitted")
			}
		})
	}
}
