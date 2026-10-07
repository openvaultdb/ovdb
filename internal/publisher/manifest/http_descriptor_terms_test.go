package manifest

import (
	"encoding/json"
	"testing"
)

func TestHTTPDescriptorRightsCannotConflictOrSpoofEvidence(t *testing.T) {
	paired, f := CheckManifest(httpManifest(t), "ovdb.yaml", Publisher)
	if len(f) > 0 {
		t.Fatal(f)
	}
	base := map[string]any{"source_definition": paired.SourceDefinition.Value, "licences": map[string]any{"data": paired.DataDeclaration.Value}, "recordsets": []any{map[string]any{"name": "Album", "licences": map[string]any{"data": paired.DataDeclaration.Value}}, map[string]any{"name": "Artist", "licences": map[string]any{"data": paired.DataDeclaration.Value}}}}
	for name, mutate := range map[string]func(map[string]any){
		"missing database licences":      func(d map[string]any) { delete(d, "licences") },
		"null database data":             func(d map[string]any) { d["licences"] = map[string]any{"data": nil} },
		"different database declaration": func(d map[string]any) { d["licences"] = map[string]any{"data": "MIT"} },
		"replacement text": func(d map[string]any) {
			d["licences"].(map[string]any)["data"].(map[string]any)["text"] = "Contradictory replacement terms"
		},
		"missing recordsets":    func(d map[string]any) { delete(d, "recordsets") },
		"extra recordset":       func(d map[string]any) { d["recordsets"] = []any{} },
		"scalar recordset":      func(d map[string]any) { d["recordsets"].([]any)[0] = "Album" },
		"missing native name":   func(d map[string]any) { delete(d["recordsets"].([]any)[0].(map[string]any), "name") },
		"duplicate native name": func(d map[string]any) { d["recordsets"].([]any)[1].(map[string]any)["name"] = "Album" },
		"unknown native name":   func(d map[string]any) { d["recordsets"].([]any)[0].(map[string]any)["name"] = "alias" },
		"recordset override": func(d map[string]any) {
			d["recordsets"].([]any)[0].(map[string]any)["licences"] = map[string]any{"data": "MIT"}
		},
		"database evidence spoof":    func(d map[string]any) { d["sourceRights"] = []any{} },
		"recordset evidence spoof":   func(d map[string]any) { d["recordsets"].([]any)[0].(map[string]any)["providerReads"] = nil },
		"case folded evidence spoof": func(d map[string]any) { d["SourceDefinitionEvidence"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := json.Marshal(base)
			var d map[string]any
			_ = json.Unmarshal(b, &d)
			mutate(d)
			j, _ := NewJudge(Publisher)
			c := newCollector("db.json", newBudget())
			out := Descriptor{}
			j.descriptorSourceDefinition(d, paired, &out, c)
			if len(c.findings) == 0 || out.SourceDefinition != nil {
				t.Fatal("conflicting descriptor admitted")
			}
		})
	}
}
