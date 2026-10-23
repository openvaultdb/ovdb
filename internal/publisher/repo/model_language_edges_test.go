package repo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

func TestSourceDeclarationFailuresInBothVocabularies(t *testing.T) {
	for _, words := range []spelling{earlier, current} {
		version := identifierEarlier
		if words.group == current.group {
			version = identifierCurrent
		}
		for _, tc := range []struct{ name, body, text string }{
			{"use shape", `"A":{"use":{}}`, "use must be a list"},
			{"mixed use", `"A":{"use":["C",null,{}]}`, "use must be a list"},
			{"unknown use", `"A":{"use":["Missing"]}`, "references component Missing"},
			{"invalid use", `"A":{"use":["a..b"]}`, "must be a bare name"},
			{"field shape", `"A":{"MEMBERS":{"x":{"type":"int","component":"C"}}}`, "must have exactly one"},
			{"invalid enum", `"A":{"MEMBERS":{"x":{"type":"int","enum":1}}}`, "enum must be a string"},
			{"missing enum", `"A":{"MEMBERS":{"x":{"type":"int","enum":"Missing"}}}`, "references enum Missing"},
		} {
			t.Run(words.group+"/"+tc.name, func(t *testing.T) {
				body := strings.ReplaceAll(tc.body, "MEMBERS", words.members)
				doc := fmt.Sprintf(`{"modelspec":%q,"module":{"name":"m"},%q:{%s},"components":{"C":{"fields":{}}}}`, version, words.group, body)
				spec, err := readModel([]byte(doc))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, issue := range spec.issues {
					found = found || strings.Contains(issue.text, tc.text)
				}
				if !found {
					t.Fatalf("missing %q: %+v", tc.text, spec.issues)
				}
			})
		}
	}
}

func TestEnumDeclarationLimitsAndMalformedShapes(t *testing.T) {
	for _, body := range []string{`"Bad":null`, `"Bad":[]`, `"Bad":1`} {
		spec, err := readModel([]byte(`{"modelspec":"1.0-draft-2","module":{"name":"m"},"records":{},"enums":{` + body + `}}`))
		if err != nil || len(spec.issues) != 1 || !strings.Contains(spec.issues[0].text, "must be an object") {
			t.Fatal(err, spec.issues)
		}
	}
	var declarations strings.Builder
	for i := 0; i <= MaxEntities; i++ {
		if i > 0 {
			declarations.WriteByte(',')
		}
		fmt.Fprintf(&declarations, `"E%d":{}`, i)
	}
	doc := `{"modelspec":"1.0-draft-2","module":{"name":"chinook"},"records":{},"enums":{` + declarations.String() + `}}`
	if _, err := readModel([]byte(doc)); !errors.Is(err, errDeclarations) {
		t.Fatalf("enum declarations were not bounded: %v", err)
	}
	r := goodRepository()
	r.Nodes[modelPath] = Node{Kind: File, Content: []byte(doc)}
	got := Check(r, publisher())
	if !slices.Equal(rulesOf(got), []string{RuleEntitiesLimit}) || !strings.Contains(got.Findings[0].Message, "component or enum declarations") {
		t.Fatalf("declaration failure was not actionable: %+v", got.Findings)
	}
}

func TestMalformedUseAndFieldValuesReturnDecoderErrors(t *testing.T) {
	for _, doc := range []string{
		`{"modelspec":"1.0-draft-2","records":{"A":{"use":["\q"]}}}`,
		`{"modelspec":"1.0-draft-2","records":{"A":{"use":[[`,
		`{"modelspec":"1.0-draft-2","records":{"A":{"fields":{"x":[[`,
	} {
		if _, err := readModel([]byte(doc)); err == nil {
			t.Fatalf("malformed source accepted: %s", doc)
		}
	}
}

func TestIssueCapStopsReferenceDiagnosticAssembly(t *testing.T) {
	var fields strings.Builder
	for i := 0; i < maxModelIssues+2; i++ {
		if i > 0 {
			fields.WriteByte(',')
		}
		fmt.Fprintf(&fields, `"p%04d":{"record":"Missing","enum":"Missing"}`, i)
	}
	doc := `{"modelspec":"1.0-draft-2","module":{"name":"m"},"records":{"A":{"fields":{"a":{"type":"bad-name"},` + fields.String() + `}}}}`
	spec, err := readModel([]byte(doc))
	if err != nil || len(spec.issues) != maxModelIssues {
		t.Fatal(err, len(spec.issues))
	}
	for _, issue := range spec.issues {
		if issue.rule != RuleModelProperty || (!strings.Contains(issue.text, "which the model does not have") && !strings.Contains(issue.text, "which is not a type name")) {
			t.Fatalf("unexpected capped finding: %+v", issue)
		}
	}
}

func TestReferenceClosurePreservesForeignAndInvalidSourceEvidence(t *testing.T) {
	doc := `{"modelspec":"1.0-draft-2","module":{"name":"m"},"records":{"A":{"fields":{"foreign":{"record":"other.Target"},"bad":{"component":"a..b"},"missing":{"component":"Missing"},"nested":{"component":"C"}}}},"components":{"C":{"fields":{"value":{"type":"int","enum":["one"]}}}}}`
	spec, err := readModel([]byte(doc))
	if err != nil || len(spec.issues) != 2 {
		t.Fatal(err, spec.issues)
	}
	targets, unavailable := spec.recordReferenceClosure("A")
	if len(targets) != 0 || !slices.Equal(unavailable, []string{"other.Target"}) {
		t.Fatalf("closure invented a target: %v / %v", targets, unavailable)
	}
	if origin, ok := spec.fieldOrigin("A", "absent"); ok || origin != (modelOrigin{}) {
		t.Fatalf("missing source field invented an origin: %+v", origin)
	}
}

func TestValidEnumReferencesKeepExactSourceBytes(t *testing.T) {
	doc := []byte(`{"modelspec":"1.0-draft-2","module":{"id":"test/enum-source","name":"m","version":"1"},"enums":{"State":{"values":["open","closed"]}},"records":{"A":{"fields":{"state":{"type":"string","enum":"m.State"}}}}}`)
	before := bytes.Clone(doc)
	spec, err := readModel(doc)
	if err != nil || len(spec.issues) != 0 || !bytes.Equal(before, spec.source) {
		t.Fatal(err, spec.issues)
	}
	if p := spec.records["A"].props["state"]; p.enum != "m.State" || !p.enumReference {
		t.Fatalf("enum source lost: %+v", p)
	}
}

func TestUnknownPropertyOnIncompleteModelIsCapabilityInBothMeaningFormats(t *testing.T) {
	for _, format := range []string{"meaning/draft-1", "meaning/draft-2"} {
		r := goodRepository()
		r.Nodes[modelPath] = Node{Kind: File, Content: []byte(`{"modelspec":"1.0-draft-2","module":{"name":"chinook"},"records":{"Album":{"use":["other.Fields"]},"Artist":{}}}`)}
		member := "property"
		if format == "meaning/draft-2" {
			member = "field"
		}
		doc := "format: " + format + "\n" + goodMeaning
		doc = strings.Replace(doc, "concepts: []", "concepts:\n  - id: unknown\n    bindings:\n      - model: modelspec:///chinook.Album\n        role: identifier\n        "+member+": Absent", 1)
		r.Nodes[meaningPth] = Node{Kind: File, Content: []byte(doc)}
		got := Check(r, Options{Profile: manifest.Publisher})
		if !slices.Equal(rulesOf(got), []string{"meaning-capability"}) || !strings.Contains(got.Findings[0].Message, "other.Fields") {
			t.Fatalf("%s: %+v", format, got.Findings)
		}
	}
}

// A bad token at a use-list member must be reported even when preceding values
// were valid. This exercises the streaming reader independently of root framing.
func TestUseReaderRejectsInvalidTokenAfterValidReference(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`["C",?]`))
	first, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	info := &entityInfo{}
	if err := readUses(dec, first, info); err == nil || !slices.Equal(info.use, []string{"C"}) {
		t.Fatalf("use token failure lost: %v %+v", err, info)
	}
}
