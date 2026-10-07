package datarights

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
)

func encoded(v any) []byte { b, _ := json.Marshal(v); return b }
func ref(path string, data []byte) Reference {
	sum := sha256.Sum256(data)
	return Reference{Path: path, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}
}
func declaration(t *testing.T, s string) license.Declaration {
	t.Helper()
	d, e := license.ParseJSON([]byte(s), license.Directory)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func fixture(t *testing.T) (Profile, license.Declaration, Context, map[string][]byte) {
	t.Helper()
	d := declaration(t, `{"name":"Source terms","url":"https://example.org/terms#reuse"}`)
	server := encoded(map[string]any{"format": "ovdb-server/draft-1", "id": "https://example.org/ovdb", "licences": map[string]any{"data": d}, "data_rights": map[string]any{"format": Format}})
	input, terms := []byte("original input"), []byte("dated terms")
	provenance := encoded(Provenance{Format: ProvenanceFormat, Attribution: license.Notice{Text: "Source credit"}, FreeSource: &license.Notice{Text: "Available free", URL: "https://example.org/source.xml"}, Transformations: []string{"XML restructured into rows"}, Inputs: []Reference{ref("input.xml", input)}, Terms: []Reference{ref("terms.txt", terms)}, CapturedAt: "2026-10-05"})
	s := ref("server.json", server)
	p := Profile{Format: Format, Server: &s, Recordsets: map[string]license.Declaration{"rates": declaration(t, `{"text":"Complete override\nsecond line"}`)}, Provenance: ref("provenance.json", provenance)}
	files := map[string][]byte{"server.json": server, "input.xml": input, "terms.txt": terms, "provenance.json": provenance}
	ctx := Context{Repository: "https://github.com/owner/source", Revision: strings.Repeat("a", 40), ManifestPath: "ovdb.yaml", ManifestBytes: []byte("manifest bytes"), Source: license.Identity{ServerID: "https://example.org/ovdb", DatabaseID: "https://example.org/ovdb/dbs/data"}, Profile: license.Publisher, Read: func(r Reference) ([]byte, error) { return files[r.Path], nil }}
	return p, d, ctx, files
}
func TestRawScopeAndWholeDeclarationMaterialization(t *testing.T) {
	p, d, ctx, _ := fixture(t)
	rights, err := Verify(p, d, []string{"rates", "inherited"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rights) != 3 {
		t.Fatal(rights)
	}
	for _, r := range rights {
		if r.EvidenceOrigin != "publisher-verified" || (len(r.Pins) != 5 && r.Source.Recordset != "rates" || len(r.Pins) != 6 && r.Source.Recordset == "rates") || r.Attribution.Text != "Source credit" || r.FreeSource.URL == "" || len(r.Transformations) != 1 {
			t.Fatalf("missing evidence: %+v", r)
		}
		if r.Source.Recordset == "rates" {
			if r.DeclarationScope != license.RecordsetScope || r.Declaration.URL != "" || r.Declaration.Text == "" {
				t.Fatalf("merged parent fields: %+v", r)
			}
		} else {
			if r.DeclarationScope != license.ServerScope || r.DeclaredAt.DatabaseID != "" {
				t.Fatalf("copied effective terms lost raw scope: %+v", r)
			}
		}
		for _, pin := range r.Pins {
			if pin.Revision != ctx.Revision || pin.Repository != ctx.Repository {
				t.Fatal("local reference did not inherit immutable provider revision")
			}
		}
	}
	override := declaration(t, `{"text":"database override"}`)
	p.Database = &override
	if _, err := Verify(p, d, []string{"rates", "inherited"}, ctx); err == nil {
		t.Fatal("stale materialized database terms accepted")
	}
	rights, err = Verify(p, override, []string{"rates", "inherited"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rights {
		if r.Source.Recordset != "rates" && r.DeclarationScope != license.DatabaseScope {
			t.Fatal("database scope lost")
		}
	}
}
func TestPinsAndRequiredEffectiveDatabase(t *testing.T) {
	for _, mutate := range []func(*Profile, *Context, map[string][]byte){
		func(_ *Profile, _ *Context, f map[string][]byte) { f["server.json"] = []byte("corrupt") },
		func(_ *Profile, _ *Context, f map[string][]byte) { f["terms.txt"] = []byte("stale") },
		func(p *Profile, _ *Context, _ map[string][]byte) { p.Server = nil },
		func(p *Profile, _ *Context, _ map[string][]byte) {
			p.Recordsets["unknown"] = license.Declaration{Text: "terms"}
		},
		func(_ *Profile, c *Context, _ map[string][]byte) { c.Revision = "main" },
		func(p *Profile, _ *Context, _ map[string][]byte) { p.Provenance.Bytes++ },
	} {
		p, d, c, f := fixture(t)
		mutate(&p, &c, f)
		if _, err := Verify(p, d, []string{"rates"}, c); err == nil {
			t.Fatal("invalid pin/profile accepted")
		}
	}
}
func TestClosedProfileAndReferenceShape(t *testing.T) {
	p, _, _, _ := fixture(t)
	good := encoded(p)
	if _, err := ParseProfile(good, license.Publisher, []string{"rates"}); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(string(good), `"format":`, `"extra":true,"format":`, 1),
		strings.Replace(string(good), `"format":`, `"format":"wrong","format":`, 1),
		strings.Replace(string(good), `"recordsets":`, `"database":null,"recordsets":`, 1),
		strings.Replace(string(good), `"recordsets":`, `"database":{"name":"only"},"recordsets":`, 1),
		strings.Replace(string(good), `"bytes":`, `"unknown":0,"bytes":`, 1),
		strings.Replace(string(good), `"bytes":`, `"revision":"main","repository":"https://github.com/owner/repo","bytes":`, 1),
	} {
		if _, err := ParseProfile([]byte(data), license.Publisher, []string{"rates"}); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	for _, data := range []string{`{"path":"file","sha256":"` + strings.Repeat("a", 64) + `"}`, `{"path":"file","sha256":"` + strings.Repeat("a", 64) + `","bytes":null}`, `{"path":"file","sha256":"` + strings.Repeat("a", 64) + `","bytes":0,"repository":null}`} {
		var r Reference
		if Decode([]byte(data), &r) == nil {
			t.Fatal("missing/null reference field accepted")
		}
	}
	// The Directory atom profile remains wider than the Publisher whitelist.
	data := strings.Replace(string(good), `"recordsets":`, `"database":{"spdx":"CC-BY-3.0"},"recordsets":`, 1)
	if _, err := ParseProfile([]byte(data), license.Directory, []string{"rates"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseProfile([]byte(data), license.Publisher, []string{"rates"}); err == nil {
		t.Fatal("Publisher accepted unknown SPDX")
	}
}
func TestProvenanceClosedAndBounded(t *testing.T) {
	_, _, _, files := fixture(t)
	good := string(files["provenance.json"])
	for _, data := range []string{
		strings.Replace(good, `"format":`, `"unexpected":true,"format":`, 1),
		strings.Replace(good, `"Source credit"`, `""`, 1),
		strings.Replace(good, `"2026-10-05"`, `"2026-02-30"`, 1),
		strings.Replace(good, `"https://example.org/source.xml"`, `"javascript:alert(1)"`, 1),
		strings.Replace(good, `"Source credit"`, `"bad\u0000text"`, 1),
		strings.Replace(good, `"transformations":["XML restructured into rows"]`, `"transformations":[null]`, 1),
	} {
		if _, err := ParseProvenance([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := ParseProvenance(files["provenance.json"]); err != nil {
		t.Fatal(err)
	}
}

func TestServerAuthorshipClosedAndNoCyclePins(t *testing.T) {
	for _, extra := range []map[string]any{
		{"unknown": true},
		{"licences": nil},
		{"databases": []any{map[string]any{"path": "db.json", "sha256": strings.Repeat("a", 64), "bytes": 1}}},
		{"data_rights": map[string]any{"format": Format, "database": "MIT"}},
	} {
		p, d, c, f := fixture(t)
		var doc map[string]any
		_ = json.Unmarshal(f["server.json"], &doc)
		for key, v := range extra {
			doc[key] = v
		}
		f["server.json"] = encoded(doc)
		r := ref("server.json", f["server.json"])
		p.Server = &r
		if _, err := Verify(p, d, []string{"rates"}, c); err == nil {
			t.Fatal("invalid server author artifact accepted")
		}
	}
	p, d, c, f := fixture(t)
	var doc map[string]any
	_ = json.Unmarshal(f["server.json"], &doc)
	doc["databases"] = []any{map[string]any{"id": c.Source.DatabaseID, "localId": "data", "serverDbBaseUrl": "https://example.org/ovdb/dbs/data", "manifestUrl": "https://example.org/db.json", "apiUrl": "https://example.org/api"}}
	f["server.json"] = encoded(doc)
	r := ref("server.json", f["server.json"])
	p.Server = &r
	if _, err := Verify(p, d, []string{"rates"}, c); err != nil {
		t.Fatal("unpinned child URLs create no immutable cycle:", err)
	}
}
