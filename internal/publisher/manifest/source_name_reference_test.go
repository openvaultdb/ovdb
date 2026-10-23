package manifest

import (
	"reflect"
	"strings"
	"testing"
)

// These are complete source-derived documents, independent of Go's findings.
// Original frozen verdicts remain unchanged. The three pinned conformance inputs
// move from identifier checks to file proofs; 24 text edits and six exact Unicode
// suffixes exercise the broader logical-name contract. Other edits cannot match.
type sourceNode struct {
	Kind   int
	Text   string
	Fields map[string]*sourceNode
	Items  []*sourceNode
}

func sourceTree(doc []byte) *sourceNode {
	n, err := parseYAML(doc)
	if err != nil {
		return nil
	}
	return sourceNodeOf(n)
}
func sourceNodeOf(n *Node) *sourceNode {
	if n == nil {
		return nil
	}
	out := &sourceNode{Kind: int(n.Kind), Text: n.Text}
	if n.Fields != nil {
		out.Fields = map[string]*sourceNode{}
		for key, value := range n.Fields {
			out.Fields[key] = sourceNodeOf(value)
		}
	}
	for _, item := range n.Items {
		out.Items = append(out.Items, sourceNodeOf(item))
	}
	return out
}

var sourceConformanceMappings = []string{
	`{"format":"ovdb-manifest/draft-1","recordsets":["Customer","Order Lines"],"recordset_entities":{"Order Lines":"Order-Line"}}`,
	`{"format":"ovdb-manifest/draft-2","recordsets":["Customer",{"name":"Order Lines","record_type":"Order-Line"}]}`,
	`{"format":"ovdb-manifest/draft-2","recordsets":[{"name":"Customer","columns":{"created":{"field":"Created At"}}},"OrderLine"]}`,
}

const sourceGoodMapping = `["Album",{"name":"Order Lines","record_type":"OrderLine","columns":{"created":{"field":"CreatedAt"},"Unit Price":{"field":"UnitPrice"}}},{"name":"Artist","columns":{}}]`

func sourceNameReferenceCase(c referenceCase, bases map[string]string) bool {
	got := sourceTree(c.Document)
	if got == nil {
		return false
	}
	if c.Family == "mapping: conformance" && (c.Base == "own-json" || c.Base == "shared-json") || c.Family == "mapping: conformance (block)" && c.Base == "chinook-yaml" {
		for _, mapping := range sourceConformanceMappings {
			want := sourceTree([]byte(bases[c.Base]))
			if want == nil {
				return false
			}
			delete(want.Fields, "format")
			delete(want.Fields, "recordsets")
			delete(want.Fields, "recordset_entities")
			for key, value := range sourceTree([]byte(mapping)).Fields {
				want.Fields[key] = value
			}
			if reflect.DeepEqual(got, want) {
				return true
			}
		}
	}
	if c.Family == "mapping: text" && (c.Base == "own-json" || c.Base == "shared-json") {
		for _, text := range []string{"a b", "a/b", "1a", "é"} {
			for _, path := range []string{"record_type", "created", "Unit Price"} {
				want := sourceTree([]byte(bases[c.Base]))
				want.Fields["format"] = sourceTree([]byte(`"ovdb-manifest/draft-2"`))
				want.Fields["recordsets"] = sourceTree([]byte(sourceGoodMapping))
				delete(want.Fields, "recordset_entities")
				entry := want.Fields["recordsets"].Items[1]
				target := entry.Fields["record_type"]
				if path != "record_type" {
					target = entry.Fields["columns"].Fields[path].Fields["field"]
				}
				target.Text = text
				if reflect.DeepEqual(got, want) {
					return true
				}
			}
		}
	}
	if c.Family == "unicode" && (c.Base == "mapping-own-yaml" || c.Base == "mapping-shared-yaml") {
		for _, text := range []string{"record_type: OrderLine", "field: CreatedAt", "field: UnitPrice"} {
			want := strings.Replace(bases[c.Base], text+"\n", text+"é\n", 1)
			if reflect.DeepEqual(got, sourceTree([]byte(want))) {
				return true
			}
		}
	}
	return false
}

func sourceNameReferenceProbe(doc []byte, bases map[string]string) bool {
	want := sourceTree([]byte(bases["own-json"]))
	if want == nil {
		return false
	}
	want.Fields["recordset_entities"] = sourceTree([]byte(`{"Album":"not an entity"}`))
	return reflect.DeepEqual(sourceTree(doc), want)
}

func TestFrozenSourceNameDifferencesAreExact(t *testing.T) {
	corpus, _, cases, _ := loadReference(t)
	for _, profile := range []Profile{Directory, Publisher} {
		count := 0
		for _, c := range cases {
			if !sourceNameReferenceCase(c, corpus.Bases) {
				continue
			}
			count++
			if c.Verdict || profile == Publisher && c.Publisher {
				t.Fatalf("source-name case no longer differs from pinned validator")
			}
			_, findings := CheckManifest(c.Document, "ovdb.yaml", profile)
			if len(findings) != 0 {
				t.Fatalf("complete expected source-name transformation refused: %v", findings)
			}
			changed := c
			changed.Document = append([]byte(nil), c.Document...)
			// A whole-document unrelated edit or malformed supplied meaning is never
			// excused by one supported logical name.
			for _, extra := range []string{"\nunrelated: changed\n", "\nmeaning: null\n"} {
				changed.Document = append(append([]byte(nil), c.Document...), []byte(extra)...)
				if sourceNameReferenceCase(changed, corpus.Bases) {
					t.Fatal("source matcher widened to another edit")
				}
			}
		}
		if count != 39 {
			t.Fatalf("profile %d: %d source-name differences, want 39", profile, count)
		}
	}
}
