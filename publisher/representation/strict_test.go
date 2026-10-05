package representation

import (
	"encoding/json"
	"strings"
	"testing"
)

// rewrittenFixture changes immutable bytes and all affected hashes together, so
// parser regressions cannot pass merely because a stale checksum was rejected.
func rewrittenFixture(t *testing.T, assets map[string][]byte) ([]byte, Context) {
	t.Helper()
	data, ctx := fixture(t)
	doc, _ := Parse(data)
	c := &doc.Contracts[0]
	old := ctx.Resolve
	snapshot, _ := old(c.Target.Snapshot)
	var receipt map[string]any
	_ = json.Unmarshal(snapshot, &receipt)
	for _, raw := range receipt["artifacts"].([]any) {
		artifact := raw.(map[string]any)
		if bytes, ok := assets[artifact["path"].(string)]; ok {
			artifact["sha256"] = Hash(bytes)
		}
	}
	snapshot, _ = json.Marshal(receipt)
	if _, provided := assets["snapshot.json"]; !provided {
		assets["snapshot.json"] = snapshot
	}
	for _, r := range []*Reference{&c.Source.Schema, &c.Target.Model, &c.Target.Snapshot, &c.Target.Keys, &c.Target.Binding.Document, &c.Target.Binding.Meaning.Document, &c.Bridge.Artifact, &c.Decision.Document} {
		if bytes, ok := assets[r.Path]; ok {
			r.SHA256 = Hash(bytes)
		}
	}
	ctx.Resolve = func(r Reference) ([]byte, error) {
		if bytes, ok := assets[r.Path]; ok {
			return bytes, nil
		}
		return old(r)
	}
	data, _ = json.Marshal(doc)
	return data, ctx
}
func TestExactConsumedJSONKeys(t *testing.T) {
	for _, tc := range []struct{ asset, text string }{
		{"bridge.json", `{"TABLE":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"US"}]}`},
		{"bridge.json", `{"table":"CustomerCountries","ROWS":[{"raw_label":"USA","target_key":"US"}]}`},
		{"bridge.json", `{"table":"CustomerCountries","rows":[{"raw_label":"USA","TARGET_KEY":"US"}]}`},
		{"bridge.json", `{"table":"CustomerCountries","rows":[{"raw_label":"USA","target_key":"US","unknown":0}]}`},
		{"keys.json", `{"Namespace":"iso-3166-1-alpha-2","keys":["US"]}`},
		{"source.modelspec.json", `{"MODELSPEC":"1.0-draft","module":{"name":"sample"},"entities":{}}`},
		{"source.modelspec.json", `{"modelspec":"1.0-draft","MODULE":{"name":"sample"},"entities":{}}`},
		{"source.modelspec.json", `{"modelspec":"1.0-draft","module":{"name":"sample"},"ENTITIES":{}}`},
		{"source.modelspec.json", `{"modelspec":"1.0-draft","module":{"Name":"sample"},"entities":{}}`},
		{"source.modelspec.json", `{"modelspec":"1.0-draft","module":{"name":"sample"},"entities":{"Customer":{"PROPERTIES":{"Country":{"type":"string"}}}}}`},
		{"source.modelspec.json", `{"modelspec":"1.0-draft","module":{"name":"sample"},"entities":{"Customer":{"properties":{"Country":{"TYPE":"string"}}}}}`},
		{"snapshot.json", `{"GENERATOR":{"repository":"https://github.com/example/provider","revision":"` + strings.Repeat("b", 40) + `"},"artifacts":[]}`},
		{"snapshot.json", `{"generator":{"Repository":"https://github.com/example/provider","revision":"` + strings.Repeat("b", 40) + `"},"artifacts":[]}`},
		{"snapshot.json", `{"generator":{"repository":"https://github.com/example/provider","revision":"` + strings.Repeat("b", 40) + `"},"ARTIFACTS":[]}`},
		{"snapshot.json", `{"generator":{"repository":"https://github.com/example/provider","revision":"` + strings.Repeat("b", 40) + `"},"artifacts":[{"PATH":"bridge.json","sha256":"` + strings.Repeat("a", 64) + `"}]}`},
	} {
		data, ctx := rewrittenFixture(t, map[string][]byte{tc.asset: []byte(tc.text)})
		if _, err := Check(data, ctx); err == nil {
			t.Fatalf("case alias admitted: %s", tc.text)
		}
	}
	// Arbitrary case-distinct domain names and unrelated canonical fields survive.
	source := `{"modelspec":"1.0-draft","description":"unrelated","module":{"name":"sample","version":"1"},"entities":{"Customer":{"key":["Country"],"properties":{"Country":{"type":"string","required":true},"COUNTRY":{"type":"integer"}}},"CUSTOMER":{"properties":{"Country":{"type":"integer"}}}}}`
	data, ctx := rewrittenFixture(t, map[string][]byte{"source.modelspec.json": []byte(source)})
	if _, err := Check(data, ctx); err != nil {
		t.Fatal(err)
	}
}
func TestUnicodeScalarIdentity(t *testing.T) {
	for _, raw := range []string{`\ud800`, `\udfff`, `\uD800\u0041`, `\uD800\uD801`, `\uD800\u`, `\uD800\uxxxx`} {
		bridge := []byte(`{"table":"CustomerCountries","rows":[{"raw_label":"` + raw + `","target_key":"US"}]}`)
		data, ctx := rewrittenFixture(t, map[string][]byte{"bridge.json": bridge})
		if _, err := Check(data, ctx); err == nil {
			t.Fatalf("invalid scalar admitted: %s", raw)
		}
	}
	for _, tc := range []struct{ encoded, decoded string }{{`\ud83d\ude00`, "😀"}, {`\uD83D\uDE00`, "😀"}, {`\uFFFD`, "�"}, {"�", "�"}, {`\\ud800`, `\ud800`}, {`\"label\"`, `"label"`}} {
		bridge := []byte(`{"table":"CustomerCountries","rows":[{"raw_label":"` + tc.encoded + `","target_key":"US"}]}`)
		data, ctx := rewrittenFixture(t, map[string][]byte{"bridge.json": bridge})
		doc, err := Check(data, ctx)
		if err != nil {
			t.Fatal(err)
		}
		var artifact BridgeArtifact
		if err := exactClosed(bridge, &artifact, []string{"table", "rows"}, true); err != nil {
			t.Fatal(err)
		}
		found, err := Lookup(doc.Contracts[0], doc.Contracts[0].Source, artifact.Rows, tc.decoded)
		if err != nil || found == nil || *found != "US" {
			t.Fatalf("scalar identity changed: %s %v %v", tc.encoded, found, err)
		}
	}
	for _, raw := range []string{`{"x":"\`, `{"x":"\u12`, `{"x":"\u12g4"}`} {
		var out any
		if err := strictJSON([]byte(raw), MaxArtifactBytes, &out); err == nil {
			t.Fatal("invalid escape admitted")
		}
	}
}
func TestOneCompleteYAMLDocument(t *testing.T) {
	for _, asset := range []string{"target.meaning.json", "core.meaning.json"} {
		data, ctx := fixture(t)
		doc, _ := Parse(data)
		var r Reference
		if asset == "target.meaning.json" {
			r = doc.Contracts[0].Target.Binding.Document
		} else {
			r = doc.Contracts[0].Target.Binding.Meaning.Document
		}
		original, _ := ctx.Resolve(r)
		for _, extra := range []string{"\n---\nformat: broken\n", "\n---\n", "\n---\n["} {
			bytes := append(append([]byte{}, original...), []byte(extra)...)
			data, ctx := rewrittenFixture(t, map[string][]byte{asset: bytes})
			if _, err := Check(data, ctx); err == nil {
				t.Fatal("extra YAML document admitted")
			}
		}
		bytes := append(append([]byte{}, original...), []byte("\n...\n# trailing comment\n")...)
		data, ctx = rewrittenFixture(t, map[string][]byte{asset: bytes})
		if _, err := Check(data, ctx); err != nil {
			t.Fatal(err)
		}
	}
	manifest := "representation_contract: {path: contracts.json, sha256: " + strings.Repeat("a", 64) + "}\n"
	for _, extra := range []string{"---\n", "---\nformat: broken\n", "---\n["} {
		if _, err := ParseAttachment([]byte(manifest + extra)); err == nil {
			t.Fatal("extra manifest YAML admitted")
		}
	}
	if _, err := ParseAttachment([]byte(manifest + "...\n# comment\n")); err != nil {
		t.Fatal(err)
	}
	if err := singleYAML([]byte(strings.Repeat(" ", MaxArtifactBytes+1)), &map[string]any{}); err == nil {
		t.Fatal("oversized YAML admitted")
	}
}
