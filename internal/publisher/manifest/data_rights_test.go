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
