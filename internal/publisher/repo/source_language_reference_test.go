package repo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// These three unchanged frozen inputs now require model-file proof. None is
// accepted as a dataset: Order-Line and Created At are absent from their models.
func sourceConformanceFileStage(c conformanceCase) bool {
	expected := map[string]string{
		"A7":   `{"format":"ovdb-manifest/draft-1","recordsets":["Customer","Order Lines"],"recordset_entities":{"Order Lines":"Order-Line"}}`,
		"B14a": `{"format":"ovdb-manifest/draft-2","recordsets":["Customer",{"name":"Order Lines","record_type":"Order-Line"}]}`,
		"B23a": `{"format":"ovdb-manifest/draft-2","recordsets":[{"name":"Customer","columns":{"created":{"field":"Created At"}}},"OrderLine"]}`,
	}
	text, known := expected[c.ID]
	if !known || c.Verdict != "refuse" || c.Stage != "manifest" {
		return false
	}
	var want, got any
	raw, _ := json.Marshal(c.Manifest)
	return json.Unmarshal(raw, &got) == nil && json.Unmarshal([]byte(text), &want) == nil && reflect.DeepEqual(got, want)
}

func sourceStageExpectation(t testing.TB, g stageGolden, c stageCase) (outcome, first string, matched bool) {
	// Compare complete operations to independent transformations of the frozen
	// baseline. An unrelated file, malformed source or different edit cannot match.
	switch c.ID {
	case "modelspec-module-name", "modelspec-module-underscore", "modelspec-entity-name", "modelspec-entity-no-properties", "modelspec-property-name":
	default:
		return "", "", false
	}
	var model map[string]any
	if json.Unmarshal([]byte(g.Base[modelPath]), &model) != nil {
		t.Fatal("invalid frozen source")
	}
	changed := map[string]string{}
	switch c.ID {
	case "modelspec-module-name":
		model["module"].(map[string]any)["name"] = "bad-name"
	case "modelspec-module-underscore":
		model["module"].(map[string]any)["name"] = "_chinook"
		head, _, _ := strings.Cut(g.Base[meaningPth], "concepts:\n")
		changed[meaningPth] = strings.Replace(head+"concepts:\n  - id: artist\n", "  chinook: ", "  _chinook: ", 1)
	case "modelspec-entity-name":
		model = renameSourceLiteral(model, "Genre", "Gen-re").(map[string]any)
		changed["ovdb.yaml"] = strings.Replace(g.Base["ovdb.yaml"], "  - Genre\n", "  - Gen-re\n", 1)
	case "modelspec-entity-no-properties":
		model["entities"].(map[string]any)["Genre"].(map[string]any)["properties"] = map[string]any{}
	case "modelspec-property-name":
		fields := model["entities"].(map[string]any)["Genre"].(map[string]any)["properties"].(map[string]any)
		fields["Na-me"] = fields["Name"]
		delete(fields, "Name")
	}
	if len(c.Ops) != len(changed)+1 {
		return "", "", false
	}
	seen := map[string]bool{}
	for _, op := range c.Ops {
		if len(op) != 3 {
			return "", "", false
		}
		var kind, path, text string
		if json.Unmarshal(op[0], &kind) != nil || json.Unmarshal(op[1], &path) != nil || json.Unmarshal(op[2], &text) != nil || kind != "file" || seen[path] {
			return "", "", false
		}
		seen[path] = true
		if path == modelPath {
			var got any
			if json.Unmarshal([]byte(text), &got) != nil || !reflect.DeepEqual(got, model) {
				return "", "", false
			}
		} else if want, exists := changed[path]; !exists || text != want {
			return "", "", false
		}
	}
	if !seen[modelPath] {
		return "", "", false
	}
	if c.ID == "modelspec-module-underscore" {
		return "agree", "", true
	}
	// Broader source is valid, but unchanged supplied bindings still name the
	// earlier module, record or field and must fail as actual binding proofs.
	return "agree", "meaning-binding", true
}

func renameSourceLiteral(value any, old, replacement string) any {
	switch v := value.(type) {
	case string:
		if v == old {
			return replacement
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = renameSourceLiteral(item, old, replacement)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if key == old {
				key = replacement
			}
			out[key] = renameSourceLiteral(item, old, replacement)
		}
		return out
	default:
		return value
	}
}

func TestSourceStageReconciliationIsNarrow(t *testing.T) {
	g := readStageGolden(t)
	count := 0
	for _, c := range g.Cases {
		_, _, matched := sourceStageExpectation(t, g, c)
		if !matched {
			continue
		}
		count++
		changed := c
		changed.Ops = append(append([][]json.RawMessage(nil), c.Ops...), []json.RawMessage{json.RawMessage(`"file"`), json.RawMessage(`"unrelated"`), json.RawMessage(`"changed"`)})
		if _, _, matches := sourceStageExpectation(t, g, changed); matches {
			t.Fatal("source-stage matcher ignored unrelated change")
		}
	}
	if count != 5 {
		t.Fatalf("%d exact source-stage migrations, want 5", count)
	}
}
