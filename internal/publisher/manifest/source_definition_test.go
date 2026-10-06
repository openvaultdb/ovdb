package manifest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/publisher/source"
	"gopkg.in/yaml.v3"
)

func httpManifest(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../publisher/source/ecb-daily.example.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	r := d.Recordsets["FxReferenceQuote"]
	d.Recordsets = map[string]source.Recordset{}
	for _, name := range []string{"Album", "Artist"} {
		x := r
		x.Entity = name
		x.ProviderSourceID = "provider:ecb/" + name
		d.Recordsets[name] = x
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(ownManifest), &doc); err != nil {
		t.Fatal(err)
	}
	doc["source_definition"] = d
	doc["licences"].(map[string]any)["data"] = map[string]any{"url": d.Rights.Terms[0].URL}
	b, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHTTPManifestDefinitionAndConflicts(t *testing.T) {
	for _, profile := range []Profile{Directory, Publisher} {
		m, f := CheckManifest(httpManifest(t), "ovdb.yaml", profile)
		if len(f) != 0 || !m.SourceDefinition.Usable() {
			t.Fatal(f)
		}
		if m.SourceDefinitionEvidence != nil {
			t.Fatal("manifest-only parse claimed immutable evidence")
		}
	}
	for _, mutate := range []func(map[string]any){
		func(d map[string]any) { d["source_definition"] = nil },
		func(d map[string]any) { d["data_rights"] = nil },
		func(d map[string]any) { d["representation_contract"] = nil },
		func(d map[string]any) {
			d["source_definition"].(map[string]any)["recordsets"].(map[string]any)["Album"].(map[string]any)["entity"] = "Other"
		},
		func(d map[string]any) { d["licences"].(map[string]any)["data"] = "MIT" },
	} {
		var d map[string]any
		_ = json.Unmarshal(httpManifest(t), &d)
		mutate(d)
		b, _ := json.Marshal(d)
		if _, f := CheckManifest(b, "ovdb.yaml", Publisher); len(f) == 0 {
			t.Fatal("invalid HTTP manifest accepted")
		}
	}
	if !HasSourceDefinition(httpManifest(t)) || !HasSourceDefinition([]byte(ownManifest+"source_definition: null\n")) {
		t.Fatal("authored dynamic profile not detected")
	}
	for _, d := range []string{ownManifest, "[a]", "bad: [", "null"} {
		if HasSourceDefinition([]byte(d)) {
			t.Fatal("invalid/legacy document acquired profile")
		}
	}
}

func TestHTTPDescriptorRequiresExactDefinition(t *testing.T) {
	paired, f := CheckManifest(httpManifest(t), "ovdb.yaml", Publisher)
	if len(f) != 0 {
		t.Fatal(f)
	}
	base := map[string]any{"source_definition": paired.SourceDefinition.Value}
	j, _ := NewJudge(Publisher)
	check := func(doc map[string]any, paired Manifest) (Descriptor, []Finding) {
		c := newCollector("db.json", newBudget())
		out := Descriptor{}
		j.descriptorSourceDefinition(doc, paired, &out, c)
		return out, c.findings
	}
	if out, f := check(base, paired); len(f) != 0 || out.SourceDefinition == nil {
		t.Fatal(f)
	}
	if _, f := check(map[string]any{}, Manifest{}); len(f) != 0 {
		t.Fatal("legacy changed")
	}
	for _, tc := range []struct {
		doc    map[string]any
		paired Manifest
	}{
		{map[string]any{}, paired}, {base, Manifest{}},
		{map[string]any{"source_definition": nil}, paired},
	} {
		if _, f := check(tc.doc, tc.paired); len(f) == 0 {
			t.Fatal("descriptor definition bypass")
		}
	}
	b, _ := json.Marshal(base)
	var changed map[string]any
	_ = json.Unmarshal(b, &changed)
	changed["source_definition"].(map[string]any)["provider"].(map[string]any)["name"] = "Gateway"
	if _, f := check(changed, paired); len(f) == 0 {
		t.Fatal("descriptor identity drift accepted")
	}
	// The normal descriptor path checks duplicate original JSON before any
	// last-key-wins object could provide a successful dynamic admission.
	full := map[string]any{"format": DescriptorFormat, "id": paired.URL.Value, "localId": paired.ID.Value, "serverId": "https://chinookdb.com/ovdb", "serverDbBaseUrl": "https://chinookdb.com/ovdb/dbs/chinook", "apiUrl": "https://chinookdb.com/ovdb/api", "deployment": map[string]any{"discovery": paired.Discovery.Value}, "source_definition": paired.SourceDefinition.Value}
	b, _ = json.Marshal(full)
	if _, f := j.Descriptor(b, "db.json", paired, "ovdb.yaml"); len(f) != 0 {
		t.Fatal(f)
	}
	duplicate := []byte(strings.Replace(string(b), `"source_definition":`, `"source_definition":null,"source_definition":`, 1))
	if _, f := j.Descriptor(duplicate, "db.json", paired, "ovdb.yaml"); len(f) == 0 {
		t.Fatal("duplicate descriptor definition accepted")
	}
}
