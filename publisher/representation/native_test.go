package representation

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func realFixture(t *testing.T, name string) ([]byte, Context, map[Reference][]byte) {
	t.Helper()
	dir := "testdata/" + name + "/"
	data, err := os.ReadFile(dir + "contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var refs []struct {
		Reference Reference `json:"reference"`
		File      string    `json:"file"`
	}
	index, err := os.ReadFile(dir + "references.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(index, &refs); err != nil {
		t.Fatal(err)
	}
	assets := map[Reference][]byte{}
	for _, ref := range refs {
		b, err := os.ReadFile(dir + ref.File)
		if err != nil {
			t.Fatal(err)
		}
		if Hash(b) != ref.Reference.SHA256 {
			t.Fatal("captured fixture changed")
		}
		assets[ref.Reference] = b
	}
	repo := "https://github.com/ingitdb/ror-ingitdb"
	rev := "bbbec903248680caea04e68f94b9a957b6efc55b"
	name = strings.TrimSuffix(name, "-current") // the fixture in the current vocabulary is the same provider's
	if name == "real-geonames" {
		repo = "https://github.com/ingitdb/geo-ingitdb"
		rev = "873538b63de475f8a04f5accdd9a14e51e59e90a"
	}
	if name == "native-geonames" {
		repo = "https://github.com/ingitdb/geo-ingitdb"
		rev = "7c0223df8e59b80df8ddf71e775197a153cf3e30"
	}
	ctx := Context{Repository: repo, Revision: rev, Resolve: func(r Reference) ([]byte, error) {
		if b, ok := assets[r]; ok {
			return b, nil
		}
		return nil, errors.New("unresolved immutable fixture; dataset must never be resolved")
	}}
	return data, ctx, assets
}
func TestRealExecutionFixtures(t *testing.T) {
	for _, name := range []string{"real-ror", "real-geonames"} {
		t.Run(name, func(t *testing.T) {
			data, ctx, _ := realFixture(t, name)
			doc, err := Check(data, ctx)
			if err != nil {
				t.Fatal(err)
			}
			again, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Check(again, ctx); err != nil {
				t.Fatal(err)
			}
			if name == "real-geonames" {
				var rows BridgeArtifact
				b, _ := ctx.Resolve(doc.Contracts[0].Bridge.Artifact)
				if err = json.Unmarshal(b, &rows); err != nil {
					t.Fatal(err)
				}
				v, err := Lookup(doc.Contracts[0], doc.Contracts[0].Source, rows.Rows, "USA")
				if err != nil || v == nil || *v != "US" {
					t.Fatal(v, err)
				}
				return
			}
			c := doc.Contracts[0]
			called := false
			result, err := LookupNative(c, c.Source, ctx, "https://ror.org/000025p04", func(req NativeLookupRequest) ([]string, error) {
				called = true
				if req.Limit != 2 || req.Repository != ctx.Repository || req.Revision != ctx.Revision || req.Dataset != c.Native.Dataset || req.Model != c.Target.Model || req.Snapshot != c.Target.Snapshot || req.Entity != "organizations" || req.Property != "id" || req.Namespace != "ROR:URL" {
					t.Fatalf("wrong keyed request %+v", req)
				}
				return []string{req.Raw}, nil
			})
			if err != nil || result == nil || *result != "https://ror.org/000025p04" || !called {
				t.Fatal(result, err)
			}
		})
	}
	// Existing reviewed bytes remain unchanged and /1 is still accepted.
	data, ctx := fixture(t)
	if _, err := Check(data, ctx); err != nil {
		t.Fatal(err)
	}
	// /2 bridge is explicit; no unspecified execution fallback exists.
	d, _ := Parse(data)
	d.Format = Format2
	d.Contracts[0].Execution = LabelBridge
	data, _ = json.Marshal(d)
	if _, err := Check(data, ctx); err != nil {
		t.Fatal(err)
	}
}
func nativeFixture(t *testing.T) (*Document, Context, map[Reference][]byte) {
	t.Helper()
	return nativeFixtureOf(t, "real-ror")
}
func nativeFixtureOf(t *testing.T, name string) (*Document, Context, map[Reference][]byte) {
	t.Helper()
	b, ctx, a := realFixture(t, name)
	doc, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return doc, ctx, a
}
func TestNativeScopeNegatives(t *testing.T) {
	tests := map[string]func(*Document, *Context){
		"wrong source property": func(d *Document, _ *Context) { d.Contracts[0].Source.Property = "missing" },
		"wrong namespace":       func(d *Document, _ *Context) { d.Contracts[0].Source.Namespace = "other" },
		"wrong pin":             func(d *Document, _ *Context) { d.Contracts[0].Source.Schema.Revision = strings.Repeat("b", 40) },
		"mutable pin":           func(d *Document, _ *Context) { d.Contracts[0].Decision.Document.Revision = "main" },
		"wrong key property":    func(d *Document, _ *Context) { d.Contracts[0].Target.Property = "status" },
		"wrong hash":            func(d *Document, _ *Context) { d.Contracts[0].Native.Provenance.SHA256 = strings.Repeat("a", 64) },
		"path":                  func(d *Document, _ *Context) { d.Contracts[0].Native.Dataset.Path = "../ror.sqlite" },
		"URL":                   func(d *Document, _ *Context) { d.Contracts[0].Native.Dataset.Path = "https://example.com/ror.sqlite" },
		"external data": func(d *Document, _ *Context) {
			d.Contracts[0].Native.Dataset.Repository = "https://github.com/example/data"
			d.Contracts[0].Native.Dataset.Revision = strings.Repeat("c", 40)
		},
		"external receipt": func(d *Document, _ *Context) {
			d.Contracts[0].Native.Provenance.Repository = "https://github.com/example/data"
			d.Contracts[0].Native.Provenance.Revision = strings.Repeat("c", 40)
		},
		"serving conflation":     func(d *Document, _ *Context) { d.Contracts[0].Native.ServingIdentityColumn = "id" },
		"missing serving column": func(d *Document, _ *Context) { d.Contracts[0].Native.ServingIdentityColumn = "serving_id" },
		"missing resolver":       func(_ *Document, c *Context) { c.Resolve = nil },
		"unknown format":         func(d *Document, _ *Context) { d.Format = "ovdb-representation-contract/3" },
		"missing discriminator":  func(d *Document, _ *Context) { d.Contracts[0].Execution = "" },
		"wrong discriminator":    func(d *Document, _ *Context) { d.Contracts[0].Execution = LabelBridge },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			d, c, _ := nativeFixture(t)
			mutate(d, &c)
			b, _ := json.Marshal(d)
			if _, err := Check(b, c); err == nil {
				t.Fatal("negative admitted")
			}
		})
	}
}
func TestNativeClosedDiscrimination(t *testing.T) {
	original, ctx, _ := realFixture(t, "real-ror")
	for _, mutate := range []func(map[string]any){
		func(c map[string]any) { c["bridge"] = map[string]any{} },
		func(c map[string]any) { c["target"].(map[string]any)["keys"] = map[string]any{} },
		func(c map[string]any) { delete(c, "native") },
		func(c map[string]any) { c["execution"] = "Native-Identifier" },
		func(c map[string]any) { c["native"].(map[string]any)["provenance"] = map[string]any{} },
	} {
		var raw map[string]any
		_ = json.Unmarshal(original, &raw)
		mutate(raw["contracts"].([]any)[0].(map[string]any))
		b, _ := json.Marshal(raw)
		if _, err := Check(b, ctx); err == nil {
			t.Fatal("ambiguous closed shape admitted")
		}
	}
	schema := Schema2()
	schema[0] = 'x'
	if _, err := Parse(original); err != nil {
		t.Fatal("schema copy mutated parser", err)
	}
}
func TestNativeBoundedLookup(t *testing.T) {
	d, ctx, _ := nativeFixture(t)
	c := d.Contracts[0]
	for _, keys := range [][]string{nil, {""}, {"other"}, {"https://ROR.org/000025p04"}, {" https://ror.org/000025p04"}, {"https://ror.org/000025p04", "https://ror.org/000025p04"}, {"a", "b", "c"}} {
		v, err := LookupNative(c, c.Source, ctx, "https://ror.org/000025p04", func(NativeLookupRequest) ([]string, error) { return keys, nil })
		if len(keys) == 0 {
			if err != nil || v != nil {
				t.Fatal(v, err)
			}
		} else if err == nil {
			t.Fatal("bad result admitted", keys)
		}
	}
	for _, mutate := range []func(*Contract, *Source, *Context){func(c *Contract, _ *Source, _ *Context) { c.Execution = LabelBridge }, func(c *Contract, _ *Source, _ *Context) { c.Native = nil }, func(_ *Contract, s *Source, _ *Context) { s.Property = "other" }, func(_ *Contract, s *Source, _ *Context) { s.Namespace = "other" }, func(_ *Contract, s *Source, _ *Context) { s.Schema.Revision = strings.Repeat("c", 40) }, func(_ *Contract, _ *Source, c *Context) { c.Revision = "main" }} {
		next, src, target := c, c.Source, ctx
		mutate(&next, &src, &target)
		if _, err := LookupNative(next, src, target, "id", nil); err == nil {
			t.Fatal("scope mismatch admitted")
		}
	}
	if _, err := LookupNative(c, c.Source, ctx, "id", nil); err == nil {
		t.Fatal("missing keyed query")
	}
	for _, raw := range []string{strings.Repeat("x", 1025), string([]byte{255})} {
		if _, err := LookupNative(c, c.Source, ctx, raw, nil); err == nil {
			t.Fatal("invalid key bytes")
		}
	}
	if _, err := LookupNative(c, c.Source, ctx, "id", func(NativeLookupRequest) ([]string, error) { return nil, errors.New("timeout") }); err == nil {
		t.Fatal("query failure lost")
	}
	if _, err := Lookup(c, c.Source, nil, "id"); err == nil {
		t.Fatal("native used label bridge")
	}
}

// A proposed receipt extension is modified together with its metadata snapshot
// hash so each rejection reaches receipt validation rather than checksum failure.
func mutatedNativeReceipt(t *testing.T, mutate func(map[string]any)) ([]byte, Context) {
	t.Helper()
	d, ctx, assets := nativeFixture(t)
	c := &d.Contracts[0]
	old := c.Native.Provenance
	var receipt map[string]any
	_ = json.Unmarshal(assets[old], &receipt)
	mutate(receipt)
	b, _ := json.Marshal(receipt)
	c.Native.Provenance.SHA256 = Hash(b)
	assets[c.Native.Provenance] = b
	snapold := c.Target.Snapshot
	snap := []byte(strings.ReplaceAll(string(assets[snapold]), old.SHA256, c.Native.Provenance.SHA256))
	c.Target.Snapshot.SHA256 = Hash(snap)
	assets[c.Target.Snapshot] = snap
	out, _ := json.Marshal(d)
	return out, ctx
}
func TestNativeReceiptAssociation(t *testing.T) {
	tests := map[string]func(map[string]any){
		"missing scope":      func(r map[string]any) { delete(r, "native_key") },
		"wrong scope":        func(r map[string]any) { r["native_key"].(map[string]any)["entity"] = "other" },
		"duplicates":         func(r map[string]any) { r["native_key"].(map[string]any)["duplicates"] = 1 },
		"missing duplicates": func(r map[string]any) { delete(r["native_key"].(map[string]any), "duplicates") },
		"negative records":   func(r map[string]any) { r["native_key"].(map[string]any)["records"] = -1 },
		"model mismatch": func(r map[string]any) {
			r["native_key"].(map[string]any)["model"].(map[string]any)["sha256"] = strings.Repeat("c", 64)
		},
		"dataset mismatch": func(r map[string]any) {
			r["snapshot"].(map[string]any)["outputs"].(map[string]any)["ror.sqlite"].(map[string]any)["sha256"] = strings.Repeat("c", 64)
		},
		"count mismatch":      func(r map[string]any) { r["snapshot"].(map[string]any)["counts"].(map[string]any)["organizations"] = 1 },
		"alias":               func(r map[string]any) { r["NATIVE_KEY"] = r["native_key"] },
		"scope alias":         func(r map[string]any) { r["native_key"].(map[string]any)["PROPERTY"] = "id" },
		"unknown scope field": func(r map[string]any) { r["native_key"].(map[string]any)["eligible"] = true },
		"ref alias":           func(r map[string]any) { r["native_key"].(map[string]any)["model"].(map[string]any)["SHA256"] = "x" },
		"snapshot alias": func(r map[string]any) {
			r["snapshot"].(map[string]any)["OUTPUTS"] = r["snapshot"].(map[string]any)["outputs"]
		},
		"output alias": func(r map[string]any) {
			r["snapshot"].(map[string]any)["outputs"].(map[string]any)["ror.sqlite"].(map[string]any)["SHA256"] = "x"
		},
		"fractional records": func(r map[string]any) { r["native_key"].(map[string]any)["records"] = 1.5 },
		"null records":       func(r map[string]any) { r["native_key"].(map[string]any)["records"] = nil },
		"null duplicates":    func(r map[string]any) { r["native_key"].(map[string]any)["duplicates"] = nil },
		"null count": func(r map[string]any) {
			r["snapshot"].(map[string]any)["counts"].(map[string]any)["organizations"] = nil
		},
		"missing count": func(r map[string]any) {
			delete(r["snapshot"].(map[string]any)["counts"].(map[string]any), "organizations")
		},
		"wrong scalar type": func(r map[string]any) { r["native_key"].(map[string]any)["records"] = "141528" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			b, c := mutatedNativeReceipt(t, mutate)
			if _, err := Check(b, c); err == nil {
				t.Fatal("unassociated receipt admitted")
			}
		})
	}
	b, c := mutatedNativeReceipt(t, func(r map[string]any) {
		r["native_key"].(map[string]any)["records"] = 0
		r["snapshot"].(map[string]any)["counts"].(map[string]any)["organizations"] = 0
	})
	if _, err := Check(b, c); err != nil {
		t.Fatal("empty native target must be structurally expressible", err)
	}
}

func mutateNativeAsset(t *testing.T, which string, raw []byte) ([]byte, Context) {
	t.Helper()
	return mutateNativeAssetOf(t, "real-ror", which, raw)
}
func mutateNativeAssetOf(t *testing.T, name, which string, raw []byte) ([]byte, Context) {
	t.Helper()
	d, ctx, assets := nativeFixtureOf(t, name)
	c := &d.Contracts[0]
	oldModel, oldProof, oldSnapshot := c.Target.Model, c.Native.Provenance, c.Target.Snapshot
	proof := assets[oldProof]
	if which == "model" {
		c.Target.Model.SHA256 = Hash(raw)
		assets[c.Target.Model] = raw
		proof = []byte(strings.ReplaceAll(string(proof), oldModel.SHA256, c.Target.Model.SHA256))
	} else {
		proof = raw
	}
	c.Native.Provenance.SHA256 = Hash(proof)
	assets[c.Native.Provenance] = proof
	snap := []byte(strings.ReplaceAll(string(assets[oldSnapshot]), oldModel.SHA256, c.Target.Model.SHA256))
	snap = []byte(strings.ReplaceAll(string(snap), oldProof.SHA256, c.Native.Provenance.SHA256))
	c.Target.Snapshot.SHA256 = Hash(snap)
	assets[c.Target.Snapshot] = snap
	b, _ := json.Marshal(d)
	return b, ctx
}
func TestNativeRequiredKeyAndProof(t *testing.T) {
	_, _, assets := nativeFixture(t)
	d, _, _ := nativeFixture(t)
	original := string(assets[d.Contracts[0].Target.Model])
	for _, raw := range []string{strings.Replace(original, `"key": [`, `"key": "bad", "ignored": [`, 1), strings.Replace(original, `"required": true`, `"required": false`, 1), strings.Replace(original, `"required": true`, `"required": "bad"`, 1), strings.Replace(original, `"key": [`, `"key": [], "ignored": [`, 1), strings.Replace(original, `"key": [`, `"KEY": [`, 1), strings.Replace(original, `"required": true`, `"REQUIRED": true`, 1)} {
		b, ctx := mutateNativeAsset(t, "model", []byte(raw))
		if _, err := Check(b, ctx); err == nil {
			t.Fatal("undeclared/invalid key admitted")
		}
	}
	for _, raw := range []string{`[`, strings.Repeat(" ", MaxDocumentBytes+1), `null`, `{}`, `{"native_key":null}`} {
		b, ctx := mutateNativeAsset(t, "proof", []byte(raw))
		if _, err := Check(b, ctx); err == nil {
			t.Fatal("invalid proof admitted")
		}
	}
	c := d.Contracts[0]
	if err := checkNative(c, assets[c.Target.Model], Context{}, nil); err == nil {
		t.Fatal("unresolved receipt")
	}
	c.Native = &Native{Dataset: Reference{Path: "$shell/data", SHA256: strings.Repeat("a", 64)}}
	if err := checkNative(c, nil, Context{}, nil); err == nil {
		t.Fatal("invalid native ref")
	}
}
func TestCanonicalRecordsPath(t *testing.T) {
	for _, p := range []string{"$records/item.json", "assertions/$records/item.json"} {
		if !path(p) {
			t.Fatal("canonical records path rejected")
		}
	}
	for _, p := range []string{"../$records/x", "/$records/x", "https://host/$records/x", "assertions/%24records/x", "$HOME/x", "$(echo)/x", "${HOME}/x", "$recordsx/x", "assertions/$records/../x", "assertions/$records//x"} {
		if path(p) {
			t.Fatal("unsafe path admitted", p)
		}
	}
	// /1 keeps its schema behavior, while /2 resolves the unchanged real path.
	b, _, _ := realFixture(t, "real-geonames")
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	doc["format"] = Format
	c := doc["contracts"].([]any)[0].(map[string]any)
	delete(c, "execution")
	b, _ = json.Marshal(doc)
	if _, err := Parse(b); err == nil {
		t.Fatal("legacy path schema changed")
	}
}
