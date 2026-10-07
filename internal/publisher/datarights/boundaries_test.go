package datarights

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
)

func TestRightsJSONSyntaxAndResourceLimits(t *testing.T) {
	for _, s := range []string{"", `{"a":`, `{"a":1,`, `[1,`, `{"a":1`, `{} {}`, strings.Repeat("[", 66) + strings.Repeat("]", 66), strings.Repeat(" ", MaxDocumentBytes+1)} {
		var v any
		if err := Decode([]byte(s), &v); err == nil {
			t.Fatalf("invalid JSON/resource bound accepted: %.80s", s)
		}
	}
	var object struct {
		A string `json:"a"`
	}
	if err := Decode([]byte(`{"a":2}`), &object); err == nil {
		t.Fatal("wrong target type accepted")
	}
}
func TestReferenceValueBoundaries(t *testing.T) {
	good := ref("file", []byte("x"))
	for _, change := range []func(*Reference){func(r *Reference) { r.Path = "../escape" }, func(r *Reference) { r.SHA256 = "AA" + r.SHA256[2:] }, func(r *Reference) { r.SHA256 = "bad" }, func(r *Reference) { r.Bytes = -1 }, func(r *Reference) { r.Bytes = 16<<20 + 1 }, func(r *Reference) { r.Repository = "https://other.example/repo"; r.Revision = strings.Repeat("a", 40) }} {
		r := good
		change(&r)
		if err := r.Validate(); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	var r Reference
	if err := r.UnmarshalJSON(append(encoded(good), []byte(" trailing")...)); err == nil {
		t.Fatal("trailing reference data accepted")
	}
	if err := r.UnmarshalJSON([]byte(`{"bytes":"wrong"}`)); err == nil {
		t.Fatal("wrong typed reference accepted")
	}
}
func TestProfilePresenceAndOverrideFailures(t *testing.T) {
	p, _, _, _ := fixture(t)
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["format"] = "wrong" }, func(m map[string]any) { delete(m, "recordsets") }, func(m map[string]any) { m["provenance"] = true }, func(m map[string]any) { m["recordsets"] = map[string]any{"rates": nil} },
	} {
		var m map[string]any
		_ = json.Unmarshal(encoded(p), &m)
		change(m)
		if _, err := ParseProfile(encoded(m), license.Publisher, []string{"rates"}); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	if _, err := ParseProfile(encoded(p), license.Publisher, []string{"rates", "rates"}); err == nil {
		t.Fatal("duplicate native names accepted")
	}
	if _, err := ParseProfile(encoded(p), license.Publisher, nil); err == nil {
		t.Fatal("unknown recordset override accepted")
	}
}
func TestProvenanceNoticeAndPresenceFailures(t *testing.T) {
	_, _, _, f := fixture(t)
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["format"] = "unknown" }, func(m map[string]any) { delete(m, "inputs") }, func(m map[string]any) { m["attribution"] = true }, func(m map[string]any) { m["attribution"] = map[string]any{"text": "credit", "url": true} }, func(m map[string]any) { m["freeSource"] = map[string]any{"text": "available free"} }, func(m map[string]any) { m["capturedAt"] = true }, func(m map[string]any) {
			m["inputs"] = []any{map[string]any{"path": "../escape", "sha256": strings.Repeat("a", 64), "bytes": 0}}
		},
	} {
		var m map[string]any
		_ = json.Unmarshal(f["provenance.json"], &m)
		change(m)
		if _, err := ParseProvenance(encoded(m)); err == nil {
			t.Fatal("invalid provenance accepted")
		}
	}
}
func TestVerificationContextAndDeclarationFailures(t *testing.T) {
	for _, change := range []func(*Profile, *license.Declaration, *Context){
		func(_ *Profile, _ *license.Declaration, c *Context) { c.Profile = 99 },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.ManifestPath = "../escape" },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.ManifestBytes = nil },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.Source.DatabaseID = "not a URL" },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.Repository = "bad" },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.Source.ServerID = "http://example.org" },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.Source.Recordset = "already selected" },
		func(_ *Profile, _ *license.Declaration, c *Context) { c.Read = nil },
		func(p *Profile, _ *license.Declaration, _ *Context) { p.Format = "unknown" },
		func(p *Profile, _ *license.Declaration, _ *Context) { p.Provenance.Path = "../escape" },
		func(p *Profile, _ *license.Declaration, _ *Context) { p.Server.Path = "ovdb.yaml" },
		func(p *Profile, _ *license.Declaration, _ *Context) { p.Server.Path = "../escape" },
		func(_ *Profile, d *license.Declaration, _ *Context) { *d = license.Declaration{} },
		func(p *Profile, _ *license.Declaration, _ *Context) { p.Database = &license.Declaration{} },
		func(p *Profile, _ *license.Declaration, _ *Context) { p.Recordsets["rates"] = license.Declaration{} },
		func(_ *Profile, _ *license.Declaration, c *Context) {
			c.Read = func(Reference) ([]byte, error) { return nil, errors.New("immutable read refused") }
		},
	} {
		p, d, c, _ := fixture(t)
		change(&p, &d, &c)
		if _, err := Verify(p, d, []string{"rates"}, c); err == nil {
			t.Fatal("invalid verification context/declaration accepted")
		}
	}
	p, d, c, _ := fixture(t)
	if _, err := Verify(p, d, []string{"rates", "rates"}, c); err == nil {
		t.Fatal("duplicate native names accepted")
	}
	// This evidence must fail before it can be returned/persisted as an inventory.
	long := license.Declaration{Text: strings.Repeat("x", 65536)}
	p.Database = &long
	if _, err := Verify(p, long, []string{"rates", "a", "b", "c", "d"}, c); err == nil {
		t.Fatal("oversized inventory accepted")
	}
}
func TestPinnedDocumentsValidateBeforeEvidence(t *testing.T) {
	for _, key := range []string{"server.json", "provenance.json"} {
		p, d, c, f := fixture(t)
		f[key] = []byte("{")
		r := ref(key, f[key])
		if key == "server.json" {
			p.Server = &r
		} else {
			p.Provenance = r
		}
		if _, err := Verify(p, d, []string{"rates"}, c); err == nil {
			t.Fatal("pinned malformed document accepted")
		}
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["format"] = "wrong" }, func(m map[string]any) { m["licences"] = true }, func(m map[string]any) { m["licences"] = map[string]any{"unknown": "MIT"} }, func(m map[string]any) { m["licences"] = map[string]any{"data": nil} },
	} {
		p, d, c, f := fixture(t)
		var m map[string]any
		_ = json.Unmarshal(f["server.json"], &m)
		change(m)
		f["server.json"] = encoded(m)
		r := ref("server.json", f["server.json"])
		p.Server = &r
		if _, err := Verify(p, d, []string{"rates"}, c); err == nil {
			t.Fatal("invalid pinned server accepted")
		}
	}
}
func TestImmutableIdentityHelpers(t *testing.T) {
	_, _, c, f := fixture(t)
	good := f["server.json"]
	r := ref("server.json", good)
	if id, err := ServerIdentity(good, r); err != nil || id != c.Source.ServerID {
		t.Fatal(id, err)
	}
	invalid := r
	invalid.Path = "../escape"
	if _, err := ServerIdentity(good, invalid); err == nil {
		t.Fatal("invalid ref accepted")
	}
	if _, err := ServerIdentity([]byte("stale"), r); err == nil {
		t.Fatal("stale identity accepted")
	}
	for _, data := range [][]byte{[]byte("{"), []byte(`{"id":false}`)} {
		raw := []byte(data)
		if _, err := ServerIdentity(raw, ref("server.json", raw)); err == nil {
			t.Fatal("invalid identity document accepted")
		}
	}
	if id := DescriptorServerID([]byte("{")); id != "" {
		t.Fatal("malformed descriptor returned identity")
	}
	if id := DescriptorServerID([]byte(`{"serverId":"https://example.org"}`)); id != "https://example.org" {
		t.Fatal("descriptor identity lost")
	}
}
func TestServerShapeValueFailures(t *testing.T) {
	good := map[string]json.RawMessage{"title": encoded("title"), "description": encoded("description"), "homepage": encoded("https://example.org/"), "apiUrl": encoded("https://example.org/api"), "schemaUrl": encoded("https://example.org/schema")}
	if err := validateServerShape(good); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(map[string]json.RawMessage){
		func(m map[string]json.RawMessage) { m["title"] = encoded(3) }, func(m map[string]json.RawMessage) { m["homepage"] = encoded(false) }, func(m map[string]json.RawMessage) { m["homepage"] = encoded("http://example.org") }, func(m map[string]json.RawMessage) { m["databases"] = encoded(nil) },
		func(m map[string]json.RawMessage) {
			m["databases"] = encoded([]any{map[string]any{"id": false, "localId": "data", "serverDbBaseUrl": "https://example.org/base", "manifestUrl": "https://example.org/db", "apiUrl": "https://example.org/api"}})
		},
		func(m map[string]json.RawMessage) {
			m["databases"] = encoded([]any{map[string]any{"id": "https://example.org/data", "localId": "bad space", "serverDbBaseUrl": "https://example.org/base", "manifestUrl": "https://example.org/db", "apiUrl": "https://example.org/api"}})
		},
		func(m map[string]json.RawMessage) {
			m["databases"] = encoded([]any{map[string]any{"id": "http://example.org/data", "localId": "data", "serverDbBaseUrl": "https://example.org/base", "manifestUrl": "https://example.org/db", "apiUrl": "https://example.org/api"}})
		},
	} {
		m := map[string]json.RawMessage{}
		for k, v := range good {
			m[k] = v
		}
		change(m)
		if err := validateServerShape(m); err == nil {
			t.Fatal("invalid server shape accepted")
		}
	}
}
