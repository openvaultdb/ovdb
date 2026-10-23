package manifest

import (
	"strings"
	"testing"
)

func TestMeaningBindingsDistinguishKnownFieldsAndUnavailableProof(t *testing.T) {
	for _, format := range []string{meaningDraft1, meaningDraft2} {
		member := "property"
		if format == meaningDraft2 {
			member = "field"
		}
		for _, tc := range []struct{ name, binding, rule string }{
			{"known own field", "        role: identifier\n        " + member + ": Own\n", ""},
			{"unknown field", "        role: identifier\n        " + member + ": Unknown\n", "meaning-capability"},
			{"record identity", "        role: instances\n", ""},
			{"nonstring member", "        role: identifier\n        " + member + ": 42\n", "meaning-binding"},
			{"missing member", "        role: identifier\n", "meaning-binding"},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				facts := &ModelFacts{Entities: map[string]map[string]struct{}{"Artist": {"Own": {}}}, IncompleteFields: map[string]string{"Artist": "field-set-unavailable: \"other.Fields\""}}
				wants := MeaningWants{File: "m.yaml", Module: "chinook", ModelHCL: "chinook.modelspec.hcl", Model: facts}
				judge, _ := NewJudge(Publisher)
				doc := "format: " + format + "\n" + meaningHead + artistModel + tc.binding
				got := judge.Meaning([]byte(doc), wants)
				if tc.rule == "" {
					if len(got) != 0 {
						t.Fatal(got)
					}
					return
				}
				if len(got) != 1 || got[0].Rule != tc.rule {
					t.Fatalf("want %s: %v", tc.rule, got)
				}
				if tc.rule == "meaning-capability" && !strings.Contains(got[0].Message, "other.Fields") {
					t.Fatalf("missing source evidence: %v", got)
				}
			})
		}
	}
}
