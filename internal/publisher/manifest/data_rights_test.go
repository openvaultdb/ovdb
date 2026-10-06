package manifest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
)

func rightsManifest(t *testing.T, terms, extra string, profile Profile) Manifest {
	t.Helper()
	data := strings.Replace(ownManifest, "data: MIT", "data: "+terms, 1) + extra
	m, findings := CheckManifest([]byte(data), "ovdb.yaml", profile)
	if len(findings) != 0 {
		t.Fatalf("manifest: %v", findings)
	}
	return m
}
func TestStructuredDataDeclarationsPreserveProfiles(t *testing.T) {
	for _, terms := range []string{`{url: 'https://example.org/terms#reuse'}`, `{text: 'Custom source terms'}`, `{spdx: MIT, url: 'https://example.org/terms'}`} {
		for _, profile := range []Profile{Publisher, Directory} {
			m := rightsManifest(t, terms, "", profile)
			if !m.DataDeclaration.Usable() {
				t.Fatal("missing structured declaration")
			}
		}
	}
	for _, terms := range []string{`null`, `{}`, `{name: 'not terms'}`, `{url: 'http://example.org/terms'}`, `{text: 'terms', secret: 'hidden'}`, `{url: ''}`, `{spdx: null}`} {
		for _, profile := range []Profile{Publisher, Directory} {
			_, findings := CheckManifest([]byte(strings.Replace(ownManifest, "data: MIT", "data: "+terms, 1)), "ovdb.yaml", profile)
			if len(findings) == 0 {
				t.Fatalf("accepted %s", terms)
			}
		}
	}
	rightsManifest(t, `{spdx: CC-BY-3.0}`, "", Directory)
	if _, f := CheckManifest([]byte(strings.Replace(ownManifest, "data: MIT", "data: {spdx: CC-BY-3.0}", 1)), "ovdb.yaml", Publisher); len(f) == 0 {
		t.Fatal("Publisher lost whitelist")
	}
}
func TestDescriptorRawProfileAndMaterializedTerms(t *testing.T) {
	profile := `data_rights: {format: ovdb-data-rights/1, recordsets: {Album: {text: 'Override'}}, provenance: {path: metadata/rights.json, sha256: '` + strings.Repeat("a", 64) + `', bytes: 123}}` + "\n"
	paired := rightsManifest(t, `{url: 'https://example.org/terms'}`, profile, Publisher)
	object := map[string]any{"format": DescriptorFormat, "id": paired.URL.Value, "localId": paired.ID.Value, "serverId": "https://chinookdb.com/ovdb", "serverDbBaseUrl": "https://chinookdb.com/ovdb/dbs/chinook", "apiUrl": "https://chinookdb.com/ovdb/api", "deployment": map[string]any{"discovery": paired.Discovery.Value}}
	object["data_rights"] = paired.DataRights.Value
	object["licences"] = map[string]any{"data": paired.DataDeclaration.Value}
	records := []any{}
	for _, name := range paired.Recordsets.Value {
		d := paired.DataDeclaration.Value
		if name == "Album" {
			d = license.Declaration{Text: "Override"}
		}
		records = append(records, map[string]any{"name": name, "licences": map[string]any{"data": d}})
	}
	object["recordsets"] = records
	encoded := func() []byte { b, _ := json.Marshal(object); return b }
	j, _ := NewJudge(Publisher)
	if _, f := j.Descriptor(encoded(), "db.json", paired, "ovdb.yaml"); len(f) != 0 {
		t.Fatal(f)
	}
	for _, mutate := range []func(){
		func() { object["licences"] = map[string]any{"data": "MIT"} },
		func() { records[0].(map[string]any)["licences"] = map[string]any{"data": paired.DataDeclaration.Value} },
		func() { records[0].(map[string]any)["name"] = "alias" },
		func() { delete(object, "data_rights") },
	} {
		original := encoded()
		mutate()
		j, _ := NewJudge(Publisher)
		if _, f := j.Descriptor(encoded(), "db.json", paired, "ovdb.yaml"); len(f) == 0 {
			t.Fatal("descriptor mismatch accepted")
		}
		_ = json.Unmarshal(original, &object)
		records = object["recordsets"].([]any)
	}
}

func TestRightsShapeAndDetectionRefusals(t *testing.T) {
	if nodeValue(nil) != nil {
		t.Fatal("nil node gained a value")
	}
	for _, doc := range []string{ownManifest, "[a]", "bad: [", "null"} {
		if HasDataRights([]byte(doc)) {
			t.Fatal("legacy document gained rights")
		}
	}
	if !HasDataRights([]byte(ownManifest + "data_rights: null\n")) {
		t.Fatal("invalid authored profile must still trigger original-object reading")
	}
	if _, f := CheckManifest([]byte(ownManifest+"data_rights: null\n"), "ovdb.yaml", Publisher); len(f) == 0 {
		t.Fatal("null profile accepted")
	}
}
func TestDescriptorRightsShapeRefusals(t *testing.T) {
	paired := rightsManifest(t, `{text: 'Database terms'}`, `data_rights: {format: ovdb-data-rights/1, recordsets: {}, database: {text: 'Database terms'}, provenance: {path: metadata/rights.json, sha256: '`+strings.Repeat("a", 64)+`', bytes: 1}}`+"\n", Publisher)
	base := map[string]any{"data_rights": paired.DataRights.Value, "licences": map[string]any{"data": paired.DataDeclaration.Value}, "recordsets": []any{map[string]any{"name": paired.Recordsets.Value[0], "licences": map[string]any{"data": paired.DataDeclaration.Value}}}}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["data_rights"] = nil },
		func(m map[string]any) { delete(m, "licences") },
		func(m map[string]any) { m["recordsets"] = false },
		func(m map[string]any) { m["recordsets"] = []any{false} },
		func(m map[string]any) {
			m["recordsets"] = []any{map[string]any{"name": paired.Recordsets.Value[0]}, map[string]any{"name": paired.Recordsets.Value[0]}}
		},
		func(m map[string]any) {
			m["data_rights"] = map[string]any{"format": "ovdb-data-rights/1", "recordsets": map[string]any{}, "database": "MIT", "provenance": paired.DataRights.Value.Provenance}
		},
	} {
		encoded, _ := json.Marshal(base)
		var object map[string]any
		_ = json.Unmarshal(encoded, &object)
		change(object)
		j, _ := NewJudge(Publisher)
		c := newCollector("db.json", j.b)
		j.descriptorRights(object, paired, &Descriptor{}, c)
		if len(c.findings) == 0 {
			t.Fatal("invalid descriptor rights shape accepted")
		}
	}
	// A descriptor cannot enable a profile absent from its author manifest.
	j, _ := NewJudge(Publisher)
	c := newCollector("db.json", j.b)
	j.descriptorRights(base, Manifest{Recordsets: paired.Recordsets}, &Descriptor{}, c)
	if len(c.findings) == 0 {
		t.Fatal("descriptor silently opted in an unprofiled manifest")
	}
	// JSON duplicates are invalid in the opted-in descriptor even though the
	// legacy descriptor reader intentionally retains JSON.parse compatibility.
	raw := `{"format":"ovdb-database/draft-1","data_rights":null,"data_rights":null}`
	j, _ = NewJudge(Publisher)
	if _, f := j.Descriptor([]byte(raw), "db.json", paired, "ovdb.yaml"); len(f) == 0 {
		t.Fatal("duplicate new profile accepted")
	}
}
