package repo

import (
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// A manifest in ovdb-manifest/draft-2 is held to its model by the record types its recordsets have, and its columns to the fields of those record types
// (decision 0012 of openvaultdb/openvaultdb). The shape of the mapping is judged by package manifest; these tests are about the file stage.

// fieldModel is a model with two record types of two fields each, in the earlier vocabulary and in the current one.
const (
	fieldModelEarlier = `{"modelspec": "1.0-draft", "module": {"name": "chinook"}, "entities": {"Album": {"properties": {"Id": {"type": "int"}, "Title": {"type": "string"}}}, "Artist": {"properties": {"Id": {"type": "int"}, "Name": {"type": "string"}}}}}`
	fieldModelCurrent = `{"modelspec": "1.0-draft-2", "module": {"name": "chinook"}, "records": {"Album": {"fields": {"Id": {"type": "int"}, "Title": {"type": "string"}}}, "Artist": {"fields": {"Id": {"type": "int"}, "Name": {"type": "string"}}}}}`
)

// draft2Repository is the good repository with a manifest in the second format, the recordsets the lines give, and the model.
func draft2Repository(lines, model string) *Memory {
	r := goodRepository()
	doc := strings.Replace(ownManifest, "format: ovdb-manifest/draft-1", "format: ovdb-manifest/draft-2", 1)
	doc = strings.Replace(doc, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n"+lines, 1)
	r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(doc)}
	r.Nodes[modelPath] = Node{Kind: File, Content: []byte(model)}
	return r
}

func findingTexts(r manifest.Result) string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule+": "+f.Message)
	}
	return strings.Join(out, "\n")
}

func TestADraft2ManifestIsHeldToTheRecordTypesOfTheModel(t *testing.T) {
	for _, model := range []string{fieldModelEarlier, fieldModelCurrent} {
		for _, lines := range []string{
			"  - Album\n  - Artist\n",
			"  - name: albums\n    record_type: Album\n  - name: artists\n    record_type: Artist\n",
			"  - name: Album\n    columns:\n      title:\n        field: Title\n  - name: Artist\n    columns:\n      Name:\n        field: Name\n      who:\n        field: Id\n",
			"  - name: Album\n    columns:\n      Title:\n        field: Id\n      Id:\n        field: Title\n  - Artist\n",
			"  - name: Album\n    columns: {}\n  - Artist\n",
		} {
			for _, profile := range []manifest.Profile{manifest.Publisher, manifest.Directory} {
				if r := Check(draft2Repository(lines, model), Options{Profile: profile}); !r.OK() || len(r.Notices) != 0 {
					t.Errorf("profile %v, accepted by the reference, refused: %s\n%s", profile, findingTexts(r), lines)
				}
			}
		}
	}
	for _, c := range []struct {
		name, lines, text string
		rules             []string
	}{
		{"a record type of the model that has no recordset", "  - Album\n", `recordsets lacks the ModelSpec record types of "model/chinook.modelspec.json": "Artist"`, []string{RuleRecordsets}},
		{"a recordset whose record type the model lacks", "  - Album\n  - Artist\n  - name: Tracks\n    record_type: Track\n", `recordsets names record types that are not in the model file "model/chinook.modelspec.json": "Tracks" (record type "Track"); give each a record_type: that the model has`, []string{RuleRecordsets}},
		{"a name alone that is not a record type", "  - Album\n  - Artist\n  - Tracks\n", `"Tracks" (record type "Tracks")`, []string{RuleRecordsets}},
		{"both", "  - Album\n  - name: Tracks\n    record_type: Track\n", `recordsets lacks the ModelSpec record types`, []string{RuleRecordsets, RuleRecordsets}},
		{"a recordset renamed away from its record type", "  - name: Albums\n    record_type: Album\n", `recordsets lacks the ModelSpec record types of "model/chinook.modelspec.json": "Artist"`, []string{RuleRecordsets}},
		{"more than five", "  - Album\n  - Artist\n  - A1\n  - A2\n  - A3\n  - A4\n  - A5\n  - A6\n", `"A1" (record type "A1"), "A2" (record type "A2"), "A3" (record type "A3"), "A4" (record type "A4"), "A5" (record type "A5") and 1 more`, []string{RuleRecordsets}},
	} {
		for _, model := range []string{fieldModelEarlier, fieldModelCurrent} {
			r := Check(draft2Repository(c.lines, model), publisher())
			if !slices.Equal(rulesOf(r), c.rules) || !strings.Contains(findingTexts(r), c.text) {
				t.Errorf("%s: findings\n%s\nwant %v with %q", c.name, findingTexts(r), c.rules, c.text)
			}
			assertBounded(t, r.Findings)
		}
	}
}

func TestTheColumnsOfADraft2ManifestAreHeldToTheFieldsOfTheRecordType(t *testing.T) {
	for _, c := range []struct {
		name, lines, text string
	}{
		{"a field the record type lacks", "  - name: Album\n    columns:\n      created:\n        field: CreatedAt\n  - Artist\n", `recordsets "Album": column "created" holds "CreatedAt", but Album has no field "CreatedAt"`},
		{"a field of another record type", "  - name: Album\n    columns:\n      who:\n        field: Name\n  - Artist\n", `column "who" holds "Name", but Album has no field "Name"`},
		{"the first name of a path the record type lacks", "  - name: Album\n    columns:\n      year:\n        field: Created.Year\n  - Artist\n", `column "year" holds "Created.Year", but Album has no field "Created"`},
		{"a path into a component", "  - name: Album\n    columns:\n      year:\n        field: Title.Year\n  - Artist\n", `column "year" holds "Title.Year": no reader of the model reads a component yet, so "Year" cannot be read in Title`},
		{"a path of three names", "  - name: Album\n    columns:\n      year:\n        field: Title.Year.Month\n  - Artist\n", `so "Year" cannot be read in Title`},
		{"a column named like a field with no column of its own", "  - name: Album\n    columns:\n      Title:\n        field: Id\n  - Artist\n", `column "Title" is also the name of the field Title of Album, which has no column of its own listed, so two fields would claim the column Title`},
	} {
		for _, model := range []string{fieldModelEarlier, fieldModelCurrent} {
			for _, profile := range []manifest.Profile{manifest.Publisher, manifest.Directory} {
				r := Check(draft2Repository(c.lines, model), Options{Profile: profile})
				if !slices.Equal(rulesOf(r), []string{RuleColumns}) || !strings.Contains(findingTexts(r), c.text) || r.Findings[0].Line == 0 || r.Findings[0].Document != "ovdb.yaml" {
					t.Errorf("%s, profile %v: findings\n%s\nwant %s with %q", c.name, profile, findingTexts(r), RuleColumns, c.text)
				}
				assertBounded(t, r.Findings)
			}
		}
	}
	// The columns of a recordset whose record type the model lacks are not judged: the recordsets are.
	r := Check(draft2Repository("  - Album\n  - Artist\n  - name: Tracks\n    record_type: Track\n    columns:\n      a:\n        field: b\n", fieldModelEarlier), publisher())
	if !slices.Equal(rulesOf(r), []string{RuleRecordsets}) {
		t.Errorf("findings\n%s", findingTexts(r))
	}
	// Nor are the columns of a manifest whose model is refused.
	broken := strings.Replace(fieldModelEarlier, `"Title": {"type": "string"}`, `"Title": {"type": 12}`, 1)
	r = Check(draft2Repository("  - name: Album\n    columns:\n      a:\n        field: b\n  - Artist\n", broken), publisher())
	if slices.Contains(rulesOf(r), RuleColumns) || r.OK() {
		t.Errorf("findings\n%s", findingTexts(r))
	}
	// A manifest of the first format has no columns, and the same model is read as it always was.
	if r := Check(goodRepository(), publisher()); !r.OK() || len(r.Notices) != 0 {
		t.Errorf("findings\n%s", findingTexts(r))
	}
}

// A notice is told with the result of the check and counts for nothing: the repository is accepted.
func TestTheNoticeOfTheEarlierKeyReachesTheResult(t *testing.T) {
	r := goodRepository()
	r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(ownManifest + "recordset_entities: {}\n")}
	got := Check(r, publisher())
	if !got.OK() || len(got.Notices) != 1 || got.Notices[0].Rule != manifest.RuleDeprecated || got.Notices[0].Severity != manifest.SeverityNotice || got.Notices[0].Document != "ovdb.yaml" {
		t.Fatalf("findings %v, notices %v", got.Findings, got.Notices)
	}
	// One for each manifest that writes the key.
	two := goodRepository()
	two.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [./ovdb.yaml, ./other.yaml]\n---\n")}
	two.Nodes["ovdb.yaml"] = r.Nodes["ovdb.yaml"]
	two.Nodes["other.yaml"] = r.Nodes["ovdb.yaml"]
	if got := Check(two, Options{Profile: manifest.Directory}); len(got.Notices) != 2 || got.Notices[0].Document != "ovdb.yaml" || got.Notices[1].Document != "other.yaml" {
		t.Errorf("notices %v, findings %v", got.Notices, got.Findings)
	}
}

// A representation contract reads the columns of its target and of its bridge table by the model's names, so a recordset that it names lists none.
func TestAContractReadsTheColumnsOfItsRecordsetsByTheModelsNames(t *testing.T) {
	target := "  - name: Countries\n    columns:\n      c:\n        field: Id\n  - CustomerCountries\n"
	bridge := "  - Countries\n  - name: CustomerCountries\n    columns:\n      c:\n        field: Id\n"
	for _, c := range []struct {
		name, lines string
		columns     []string // the recordsets that the findings name
	}{
		{"names", "  - Countries\n  - CustomerCountries\n", nil},
		{"the names of a mapping", "  - name: Lands\n    record_type: Countries\n  - CustomerCountries\n", nil},
		{"no column", "  - name: Countries\n    columns: {}\n  - name: CustomerCountries\n    columns: {}\n", nil},
		{"a column of the target", target, []string{"Countries"}},
		{"a column of the target under another name", "  - name: Lands\n    record_type: Countries\n    columns:\n      c:\n        field: Id\n  - CustomerCountries\n", []string{"Lands"}},
		{"a column of the bridge table", bridge, []string{"CustomerCountries"}},
		{"a column of both", "  - name: Countries\n    columns:\n      c:\n        field: Id\n  - name: CustomerCountries\n    columns:\n      c:\n        field: Id\n", []string{"Countries", "CustomerCountries"}},
	} {
		p, _, deps := contractFixture(t)
		n := p.Nodes["ovdb.yaml"]
		text := strings.Replace(string(n.Content), "format: ovdb-manifest/draft-1", "format: ovdb-manifest/draft-2", 1)
		text = strings.Replace(text, "  - Countries\n  - CustomerCountries\n", c.lines, 1)
		n.Content = []byte(text)
		p.Nodes["ovdb.yaml"] = n
		var named []string
		for _, f := range checkFixtureRepresentation(p, deps) {
			if f.Rule != RuleColumns || !strings.Contains(f.Message, "lists columns, but a representation contract reads its columns by the model's names") {
				t.Errorf("%s: unexpected finding %+v", c.name, f)
			}
			named = append(named, strings.Fields(strings.SplitN(f.Message, `"`, 3)[1])[0])
		}
		if !slices.Equal(named, c.columns) {
			t.Errorf("%s: the findings name %v, want %v", c.name, named, c.columns)
		}
	}
	// A contract that is read by the native identifier has a target and no bridge table: the target alone is a recordset it names.
	r, m, att, deps := nativeFixture(t)
	columns := []manifest.Column{{Name: "c", Field: "id"}}
	m.Mapping = manifest.Fact[[]manifest.Recordset]{Present: true, Valid: true, Value: []manifest.Recordset{{Name: "organizations", RecordType: "organizations"}}}
	if findings := CheckRepresentation(r, m, att, deps); len(findings) != 0 {
		t.Fatalf("findings %v", findings)
	}
	m.Mapping.Value[0].Columns = columns
	if findings := CheckRepresentation(r, m, att, deps); len(findings) != 1 || findings[0].Rule != RuleColumns || !strings.Contains(findings[0].Message, `recordset "organizations" lists columns`) {
		t.Fatalf("findings %v", findings)
	}
}
