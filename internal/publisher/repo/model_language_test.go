package repo

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

func sourceLanguageFixture(t *testing.T, suffix string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/source-language/modelspec-source-reader" + suffix + ".modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSourceLanguageDeclarationsCompositionAndClosure(t *testing.T) {
	for _, suffix := range []string{"", "-earlier"} {
		data := sourceLanguageFixture(t, suffix)
		original := append([]byte(nil), data...)
		s, err := readModel(data)
		if err != nil || len(s.issues) != 0 {
			t.Fatalf("Go-approved %s refused: %v %v", suffix, err, s.issues)
		}
		if !bytes.Equal(s.source, original) || !bytes.Equal(data, original) || !slices.Equal(s.entities, []string{"Empty", "Order-Line", "Target", "\ufeff"}) {
			t.Fatal("source names/bytes lost")
		}
		fields := sortedPropertiesForTest(s.properties["Order-Line"])
		if !slices.Equal(fields, []string{"a.b", "field-name", "nested", "shared", "target"}) {
			t.Fatalf("effective fields: %q", fields)
		}
		own, _ := s.fieldOrigin("Order-Line", "shared")
		inherited, _ := s.fieldOrigin("Order-Line", "field-name")
		if own.Kind != "record" || own.Declaration != "Order-Line" || inherited.Kind != "component" || inherited.Declaration != "Left" {
			t.Fatalf("source origins: %+v %+v", own, inherited)
		}
		if len(s.incomplete["Order-Line"]) != 0 || !slices.Equal(s.records["Order-Line"].use, []string{"Left", "chinook.Left", "Right"}) {
			t.Fatal("overrides or repeated source uses lost")
		}
		if s.components["Node"].props["self"].text != "chinook.Node" {
			t.Fatal("recursive authored ref rewritten")
		}
		targets, unavailable := s.recordReferenceClosure("Order-Line")
		if !slices.Equal(targets, []string{"Target"}) || len(unavailable) != 0 {
			t.Fatalf("cycle-safe closure: %v / %v", targets, unavailable)
		}
	}
}

func sortedPropertiesForTest(fields map[string]struct{}) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestSourceNamesAndIndependentInvalidDeclarations(t *testing.T) {
	for _, doc := range []string{
		`{"modelspec":"1.0-draft-2","module":{"name":" spaced module "},"records":{" 9-名 ":{"fields":{" a/b % ":{"type":"string"},"\ufeff":{"type":"int"}}}}}`,
		`{"modelspec":"1.0-draft-2","module":{"name":"m"},"records":{"A":{"fields":{"status":{"type":"string","enum":["open","closed"]}}}}}`,
	} {
		if s, err := readModel([]byte(doc)); err != nil || len(s.issues) != 0 {
			t.Fatalf("source rejected: %v %v", err, s.issues)
		}
	}
	for _, edit := range []string{
		`"components":{"Unused":{"fields":{"x":{"component":"chinook.Missing"}}}}`,
		`"components":{"Unused":{"fields":{"x":{"component":"Missing"}}}}`,
		`"components":{"Unused":{"fields":{"x":{"record":"chinook.Missing"}}}}`,
		`"components":{"Unused":{"fields":{"x":{"type":"string","enum":"Missing"}}}}`,
		`"components":{"Unused":{"fields":{"x":{"type":"string","enum":""}}}}`,
		`"components":{"Unused":{"fields":{"x":{"component":"other..Thing"}}}}`,
		`"components":{"Unused":{"fields":{"x":{"component":" .Thing"}}}}`,
		`"components":{"Unused":{"fields":{"x":{"component":"other.\u0085"}}}}`,
		`"components":{"Unused":{"properties":{"x":{"type":"int"}}}}`,
		`"components":{"Album":{"fields":{}}}`,
		`"enums":{"Album":{"values":[]}}`,
		`"components":[]`,
		`"enums":[]`,
		`"components":{"a.b":{"fields":{}}}`,
		`"components":{"records":{"fields":{}}}`,
	} {
		doc := strings.TrimSuffix(goodModel, "}") + "," + edit + "}"
		if s, err := readModel([]byte(doc)); err != nil || len(s.issues) == 0 {
			t.Fatalf("unused invalid source bypassed: %s: %v %v", edit, err, s.issues)
		}
	}
	for _, field := range []string{"", "\u0085", "   "} {
		doc := `{"modelspec":"1.0-draft-2","records":{"A":{"fields":{"` + field + `":{"type":"int"}}}}}`
		if s, err := readModel([]byte(doc)); err != nil || len(s.issues) == 0 {
			t.Fatalf("Go blank name accepted: %q %v %v", field, err, s.issues)
		}
	}
}

func TestLogicalMappingNamesAndInheritedColumnsInBothManifestFormats(t *testing.T) {
	for _, suffix := range []string{"", "-earlier"} {
		for _, format := range []string{manifest.ManifestFormat, manifest.ManifestFormatDraft2} {
			r := goodRepository()
			doc := modelOnlyManifest(ownManifest)
			lines := "  - orders\n  - empty\n  - target\n  - bom\n"
			mapping := "recordset_entities:\n  orders: Order-Line\n  empty: Empty\n  target: Target\n  bom: \"\\uFEFF\"\n"
			if format == manifest.ManifestFormatDraft2 {
				mapping = ""
				lines = "  - name: orders\n    record_type: Order-Line\n    columns:\n      inherited-name:\n        field: field-name\n  - name: empty\n    record_type: Empty\n  - name: target\n    record_type: Target\n  - name: bom\n    record_type: \"\\uFEFF\"\n"
			}
			doc = strings.Replace(doc, manifest.ManifestFormat, format, 1)
			doc = strings.Replace(doc, "  - Album\n  - Artist\n", lines, 1) + mapping
			r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
			r.Nodes[modelPath] = Node{Kind: File, Content: sourceLanguageFixture(t, suffix)}
			for _, profile := range []manifest.Profile{manifest.Directory, manifest.Publisher} {
				if got := Check(r, Options{Profile: profile}); !got.OK() {
					t.Fatalf("source logical mapping refused %s/%s: %v", suffix, format, got.Findings)
				}
			}
		}
	}
	// Astra's actual Chinook use fixture proved by the Go checker.
	data, err := os.ReadFile("testdata/source-language/inherited-chinook.modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	r := currentChinook(t)
	doc := modelOnlyManifest(string(r.Nodes["ovdb.yaml"].Content))
	doc = strings.Replace(doc, manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
	doc = strings.Replace(doc, "  - Artist\n", "  - name: Artist\n    columns:\n      inherited-value:\n        field: Inherited\n", 1)
	r.Nodes[modelPath] = Node{Kind: File, Content: data}
	r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
	if got := Check(r, publisher()); !got.OK() {
		t.Fatalf("Go-approved inherited field refused: %v", got.Findings)
	}
}

func TestUnavailableFieldProofsDoNotInventAbsence(t *testing.T) {
	for _, use := range []string{`["foreign.C"]`, `["Left","Right"]`} {
		model := `{"modelspec":"1.0-draft-2","module":{"name":"chinook"},"records":{"Album":{"use":` + use + `,"fields":{"Id":{"type":"int"}}},"Artist":{}},"components":{"Left":{"fields":{"Inherited":{"type":"string"},"UniqueLocal":{"type":"int"}}},"Right":{"fields":{"Inherited":{"type":"int"}}}}}`
		r := goodRepository()
		r.Nodes[modelPath] = Node{Kind: File, Content: []byte(model)}
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(modelOnlyManifest(ownManifest))}
		if got := Check(r, publisher()); !got.OK() {
			t.Fatalf("record-list-only check invented field requirement: %v", got.Findings)
		}
		columns := modelOnlyManifest(ownManifest)
		columns = strings.Replace(columns, manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
		columns = strings.Replace(columns, "  - Album\n", "  - name: Album\n    columns:\n      source-column:\n        field: Inherited\n", 1)
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(columns)}
		got := Check(r, publisher())
		if !slices.Equal(rulesOf(got), []string{RuleModelCapability}) {
			t.Fatalf("field proof absence fabricated: %v", got.Findings)
		}
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(ownManifest)}
		r.Nodes[meaningPth] = Node{Kind: File, Content: []byte(strings.Replace(goodMeaning, "concepts: []", "concepts:\n  - id: inherited\n    bindings:\n      - model: modelspec:///chinook.Album\n        role: identifier\n        property: Inherited", 1))}
		got = Check(r, publisher())
		if !slices.Equal(rulesOf(got), []string{"meaning-capability"}) {
			t.Fatalf("semantic proof absence fabricated: %v", got.Findings)
		}
		r.Nodes[meaningPth] = Node{Kind: File, Content: []byte(strings.Replace(string(r.Nodes[meaningPth].Content), "property: Inherited", "property: Id", 1))}
		if got := Check(r, publisher()); !got.OK() {
			t.Fatalf("certain own field lost: %v", got.Findings)
		}
		if use == `["Left","Right"]` {
			positive := r.Nodes[meaningPth]
			positive.Content = []byte(strings.Replace(string(positive.Content), "property: Id", "property: UniqueLocal", 1))
			r.Nodes[meaningPth] = positive
			if got := Check(r, publisher()); !got.OK() {
				t.Fatalf("unique inspected inherited field lost across unrelated ambiguity: %v", got.Findings)
			}
			positive.Content = []byte(strings.Replace(string(positive.Content), "property: UniqueLocal", "property: Id", 1))
			r.Nodes[meaningPth] = positive
		}
		r.Nodes[meaningPth] = Node{Kind: File, Content: []byte(strings.Replace(string(r.Nodes[meaningPth].Content), "role: identifier\n        property: Id", "role: entity", 1))}
		if got := Check(r, publisher()); !got.OK() {
			t.Fatalf("known record existence conflated with fields: %v", got.Findings)
		}
		s, err := readModel([]byte(model))
		if err != nil || len(s.issues) != 0 {
			t.Fatal(err, s.issues)
		}
		_, unavailable := s.recordReferenceClosure("Album")
		if use == `["foreign.C"]` {
			if !slices.Equal(unavailable, []string{"foreign.C"}) {
				t.Fatal(unavailable)
			}
		} else if len(unavailable) != 1 || !strings.HasPrefix(unavailable[0], "ambiguous-inherited-field:") {
			t.Fatalf("ambiguous closure claimed complete: %v", unavailable)
		}
	}
}

func TestSuppliedSemanticFieldsUseCompleteSourceEvidence(t *testing.T) {
	for _, field := range []string{"Inherited", "a.b"} {
		model := `{"modelspec":"1.0-draft-2","module":{"name":"chinook"},"records":{"Album":{"use":["C","chinook.C"],"fields":{"a.b":{"type":"int"}}},"Artist":{}},"components":{"C":{"fields":{"Inherited":{"type":"string"}}}}}`
		r := goodRepository()
		r.Nodes[modelPath] = Node{Kind: File, Content: []byte(model)}
		meaning := strings.Replace(goodMeaning, "concepts: []", "concepts:\n  - id: valid\n    bindings:\n      - model: modelspec:///chinook.Album\n        role: identifier\n        property: "+field, 1)
		r.Nodes[meaningPth] = Node{Kind: File, Content: []byte(meaning)}
		if got := Check(r, publisher()); !got.OK() {
			t.Fatalf("complete semantic field proof refused: %v", got.Findings)
		}
	}
}

func TestComposedFieldWorkIsBoundedWithoutRejectingRecordLists(t *testing.T) {
	var model strings.Builder
	model.WriteString(`{"modelspec":"1.0-draft-2","module":{"name":"chinook"},"components":{"C":{"fields":{`)
	for field := 0; field < 2000; field++ {
		if field > 0 {
			model.WriteByte(',')
		}
		fmt.Fprintf(&model, `"p%d":{"type":"int"}`, field)
	}
	model.WriteString(`}}},"records":{`)
	var lines strings.Builder
	for record := 0; record < 100; record++ {
		if record > 0 {
			model.WriteByte(',')
		}
		fmt.Fprintf(&model, `"R%02d":{"use":["C"]}`, record)
		fmt.Fprintf(&lines, "  - R%02d\n", record)
	}
	model.WriteString(`}}`)
	s, err := readModel([]byte(model.String()))
	if err != nil || len(s.issues) != 0 {
		t.Fatal(err, s.issues)
	}
	_, unavailable := s.recordReferenceClosure("R99")
	if len(unavailable) == 0 || !strings.HasPrefix(unavailable[0], "field-view-limit:") {
		t.Fatalf("bounded closure claimed complete: %v", unavailable)
	}
	r := goodRepository()
	doc := modelOnlyManifest(ownManifest)
	doc = strings.Replace(doc, "  - Album\n  - Artist\n", lines.String(), 1)
	r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
	r.Nodes[modelPath] = Node{Kind: File, Content: []byte(model.String())}
	if got := Check(r, publisher()); !got.OK() {
		t.Fatalf("field-work bound rejected record list: %v", got.Findings)
	}
	doc = strings.Replace(doc, manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
	doc = strings.Replace(doc, "  - R99\n", "  - name: R99\n    columns:\n      value:\n        field: p0\n", 1)
	r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
	if got := Check(r, publisher()); !slices.Equal(rulesOf(got), []string{RuleModelCapability}) {
		t.Fatalf("bounded field proof not refused explicitly: %v", got.Findings)
	}
}

func TestSourceLanguagePairedArtifactsRetainProvenance(t *testing.T) {
	root := "testdata/source-language/"
	raw, err := os.ReadFile(root + "provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Fixtures         map[string]map[string]string
		InheritedChinook map[string]string
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Fixtures) != 2 || len(p.InheritedChinook) != 2 {
		t.Fatal("paired fixture provenance incomplete")
	}
	groups := []map[string]string{p.InheritedChinook}
	for _, pair := range p.Fixtures {
		if len(pair) != 2 {
			t.Fatal("missing paired HCL/JSON")
		}
		groups = append(groups, pair)
	}
	for _, files := range groups {
		for name, digest := range files {
			data, err := os.ReadFile(root + name)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
				t.Fatalf("paired source changed: %s", name)
			}
		}
	}
}

func TestLiteralLogicalColumnSegmentsPreserveSourceSpelling(t *testing.T) {
	for _, field := range []string{"field-name", "a/b", "é", "1a", " spaced ", "\ufeff", "a%2Fb"} {
		model := map[string]any{"modelspec": "1.0-draft-2", "module": map[string]any{"name": "chinook"}, "records": map[string]any{"Album": map[string]any{"fields": map[string]any{field: map[string]any{"type": "int"}}}, "Artist": map[string]any{}}}
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		value := []byte(strconv.QuoteToASCII(field))
		doc := strings.Replace(modelOnlyManifest(ownManifest), manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
		doc = strings.Replace(doc, "  - Album\n", "  - name: Album\n    columns:\n      literal-column:\n        field: "+string(value)+"\n", 1)
		r := goodRepository()
		r.Nodes[modelPath] = Node{Kind: File, Content: raw}
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
		got := Check(r, publisher())
		if !got.OK() {
			t.Fatalf("literal source field %q refused: %v", field, got.Findings)
		}
		if got.Manifest.Mapping.Value[0].Columns[0].Field != field {
			t.Fatalf("logical source field %q normalized", field)
		}
		// An unknown spelling is not decoded or trimmed to the declared field.
		wrong := []byte(strconv.QuoteToASCII(field + "-absent"))
		r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(doc, "field: "+string(value), "field: "+string(wrong), 1))}
		if got := Check(r, publisher()); !slices.Equal(rulesOf(got), []string{RuleColumns}) {
			t.Fatalf("unknown literal spelling accepted: %v", got.Findings)
		}
	}
	for _, field := range []string{"", " ", "\u0085", "a..b", ".a", "a."} {
		value := []byte(strconv.QuoteToASCII(field))
		doc := strings.Replace(modelOnlyManifest(ownManifest), manifest.ManifestFormat, manifest.ManifestFormatDraft2, 1)
		doc = strings.Replace(doc, "  - Album\n", "  - name: Album\n    columns:\n      literal-column:\n        field: "+string(value)+"\n", 1)
		_, findings := manifest.CheckManifest([]byte(doc), "ovdb.yaml", manifest.Publisher)
		if len(findings) != 1 || findings[0].Rule != manifest.RuleColumns {
			t.Fatalf("invalid logical path %q accepted: %v", field, findings)
		}
	}
}
