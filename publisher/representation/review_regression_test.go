package representation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewParserAgreement(t *testing.T) {
	for _, tc := range []struct{ name, asset, text string }{
		{"bridge_case_alias", "bridge.json", `{"table":"CustomerCountries","rows":[{"raw_label":"USA","RAW_LABEL":"Canada","target_key":"US"}]}`},
		{"bridge_surrogate", "bridge.json", `{"table":"CustomerCountries","rows":[{"raw_label":"\ud800","target_key":"US"}]}`},
		{"source_case_alias", "source.modelspec.json", `{"modelspec":"1.0-draft","module":{"name":"wrong","NAME":"sample"},"entities":{"Customer":{"properties":{"Country":{"type":"integer","TYPE":"string"}}}}}`},
		{"binding_trailing_document", "target.meaning.json", "format: meaning/draft-1\nconcepts:\n- id: native-country\n  extends: meaning://github.com/example/source/country?ref=" + strings.Repeat("b", 40) + "\n  bindings:\n  - model: modelspec:///geo.Countries\n    property: iso\n    role: identifier\n---\nformat: broken\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, ctx := fixture(t)
			d, _ := Parse(data)
			c := &d.Contracts[0]
			old := ctx.Resolve
			raw := []byte(tc.text)
			for _, r := range []*Reference{&c.Source.Schema, &c.Bridge.Artifact, &c.Target.Binding.Document} {
				if r.Path == tc.asset {
					r.SHA256 = Hash(raw)
				}
			}
			snap, _ := old(c.Target.Snapshot)
			var s map[string]any
			_ = json.Unmarshal(snap, &s)
			for _, v := range s["artifacts"].([]any) {
				a := v.(map[string]any)
				if a["path"] == tc.asset {
					a["sha256"] = Hash(raw)
				}
			}
			snap, _ = json.Marshal(s)
			c.Target.Snapshot.SHA256 = Hash(snap)
			ctx.Resolve = func(r Reference) ([]byte, error) {
				if r.Path == tc.asset {
					return raw, nil
				}
				if r.Path == "snapshot.json" {
					return snap, nil
				}
				return old(r)
			}
			data, _ = json.Marshal(d)
			if _, err := Check(data, ctx); err == nil {
				t.Errorf("invalid/ambiguous %s admitted", tc.name)
			}
		})
	}
}
