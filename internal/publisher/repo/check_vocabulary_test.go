package repo

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// goodCurrentModel is goodModel in the current vocabulary of the JSON form (records, fields, record), the copy that `modelspec rewrite --write` makes of it. A
// repository that holds it is not in the comparison with the reference checker, which reads the earlier vocabulary only: see model_vocabulary_test.go.
const goodCurrentModel = `{"modelspec": "1.0-draft-2", "module": {"name": "chinook"}, "records": {"Album": {"fields": {"Id": {"type": "int"}}}, "Artist": {"fields": {"Id": {"type": "int"}}}}}`

// currentModelWith is goodCurrentModel with one more record type, written as the argument says (a member of records, by its text).
func currentModelWith(record string) string {
	return `{"modelspec": "1.0-draft-2", "module": {"name": "chinook"}, "records": {"Album": {"fields": {"Id": {"type": "int"}}}, "Artist": {"fields": {"Id": {"type": "int"}}}, ` + record + `}}`
}

func repositoryWithModel(model string) *Memory {
	m := goodRepository()
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(model)}
	return m
}

// A repository whose model file is in the current vocabulary is accepted as the same repository in the earlier one is, under both profiles and with the
// --repository option.
func TestAModelInTheCurrentVocabularyIsAccepted(t *testing.T) {
	same := ownRepo
	for _, opts := range []Options{publisher(), {Profile: manifest.Directory}, {Profile: manifest.Publisher, Repository: &same}} {
		earlier := Check(goodRepository(), opts)
		current := Check(repositoryWithModel(goodCurrentModel), opts)
		if !current.OK() || !earlier.OK() || !slices.Equal(rulesOf(current), rulesOf(earlier)) {
			t.Errorf("earlier %v, current %v", earlier.Findings, current.Findings)
		}
	}
	// A model in the earlier vocabulary under the identifier of the current one has no records and has the stray keys: two findings, in that order.
	r := Check(repositoryWithModel(strings.Replace(goodModel, `"1.0-draft"`, `"1.0-draft-2"`, 1)), publisher())
	if !slices.Equal(rulesOf(r), []string{RuleModelEntities, RuleModelVocabulary}) || !strings.Contains(r.Findings[1].Message, `it has "entities", which`) {
		t.Errorf("findings %v", r.Findings)
	}
	// A model that mixes them is refused for it, once, whichever of the two the identifier names.
	mixed := strings.Replace(goodCurrentModel, `"records"`, `"entities": {}, "records"`, 1)
	only(t, Check(repositoryWithModel(mixed), publisher()), RuleModelVocabulary, modelPath, 0, `"modelspec" is "1.0-draft-2", so the model must use the keys records, fields and record; it has "entities", which belong to the other version of the format`)
	mixed = strings.Replace(goodModel, `"entities"`, `"records": {}, "entities"`, 1)
	only(t, Check(repositoryWithModel(mixed), publisher()), RuleModelVocabulary, modelPath, 0, `"modelspec" is "1.0-draft", so the model must use the keys entities, properties and entity; it has "records", which belong to the other version of the format`)
}

func TestTheModelFileInTheCurrentVocabularyMustBeAModelSpec(t *testing.T) {
	for name, c := range map[string]struct {
		model string
		rule  string
		text  string
	}{
		"too many records":                              {manyRecords(MaxEntities + 1), RuleEntitiesLimit, "has a records object of more than 10000 record types, which is more than this check reads"},
		"no records":                                    {`{"modelspec": "1.0-draft-2", "module": {"name": "chinook"}}`, RuleModelEntities, "has no records (an object of ModelSpec record types)"},
		"records a list":                                {`{"modelspec": "1.0-draft-2", "module": {"name": "chinook"}, "records": []}`, RuleModelEntities, "has no records"},
		"a record with no fields":                       {currentModelWith(`"Artist": {"fields": {}}`), RuleModelEntity, "record Artist has no fields (an object with at least one field)"},
		"fields that are a list":                        {currentModelWith(`"Artist": {"fields": []}`), RuleModelEntity, "record Artist has no fields"},
		"a field name that is not an identifier":        {currentModelWith(`"Artist": {"fields": {"Na-me": {"type": "int"}}}`), RuleModelProperty, `field name "Artist.Na-me" must be an identifier`},
		"a type that is not a type name":                {currentModelWith(`"Artist": {"fields": {"Name": {"type": "not a type"}}}`), RuleModelProperty, `Artist.Name has type "not a type", which is not a type name`},
		"a field with no type and no record":            {currentModelWith(`"Artist": {"fields": {"Name": {}}}`), RuleModelProperty, "Artist.Name has neither a type nor a record"},
		"a reference to a record the model lacks":       {currentModelWith(`"Artist": {"fields": {"Name": {"record": "Nope"}}}`), RuleModelProperty, "Artist.Name references record Nope, which the model does not have"},
		"a reference to an entity, a stray word":        {currentModelWith(`"Artist": {"fields": {"Name": {"type": "int"}}, "properties": {}}`), RuleModelVocabulary, `it has "properties", which`},
		"a removed construct":                           {strings.Replace(goodCurrentModel, `"records"`, `"collections": {}, "records"`, 1), RuleModelRemoved, `has "collections", which ModelSpec removed`},
		"a reserved word":                               {strings.Replace(goodCurrentModel, `"records"`, `"migrations": {}, "records"`, 1), RuleModelRemoved, `has "migrations", a word that ModelSpec reserves and has given no content`},
		"a removed construct in the earlier vocabulary": {strings.Replace(goodModel, `"entities"`, `"recordsets": {}, "entities"`, 1), RuleModelRemoved, `has "recordsets", which ModelSpec removed: the shape of a query result or a view is an entity with no key`},
		"a reserved word in the earlier vocabulary":     {strings.Replace(goodModel, `"entities"`, `"projections": [], "entities"`, 1), RuleModelRemoved, `has "projections", a word that ModelSpec reserves`},
	} {
		f := only(t, Check(repositoryWithModel(c.model), publisher()), c.rule, modelPath, 0, c.text)
		if f.Severity != manifest.SeverityError {
			t.Errorf("%s: severity %v", name, f.Severity)
		}
	}
}

// The recordsets of the manifest are the record types of the model, in both directions, and the findings say record types.
func TestTheRecordsetsAreTheRecordTypesOfAModelInTheCurrentVocabulary(t *testing.T) {
	m, text := withManifest("  - Artist\n", "")
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(goodCurrentModel)}
	only(t, Check(m, publisher()), RuleRecordsets, "ovdb.yaml", lineOf(text, "  - Album"), "recordsets lacks the ModelSpec record types of \"model/chinook.modelspec.json\": \"Artist\"")
	m, text = withManifest("  - Artist\n", "  - Artist\n  - Zed\n")
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(goodCurrentModel)}
	only(t, Check(m, publisher()), RuleRecordsets, "ovdb.yaml", lineOf(text, "  - Album"), "recordsets names things that are not ModelSpec record types of \"model/chinook.modelspec.json\": \"Zed\"; if they are the database's own names, map each to its record under recordset_entities (name: Entity)")
	// A native name that recordset_entities maps to a record type is that record type.
	m, _ = withManifest("  - Artist\n", "  - artists\nrecordset_entities:\n  artists: Artist\n")
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(goodCurrentModel)}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("mapped: %v", r.Findings)
	}
}

// A binding of a concept names a record type and a field of a model in the current vocabulary, as it names an entity and a property of one in the earlier.
func TestBindingsAreJudgedAgainstAModelInTheCurrentVocabulary(t *testing.T) {
	meaning := func(property string) []byte {
		return []byte("id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts:\n  - id: a\n    bindings:\n      - model: modelspec:///chinook.Album\n        property: " + property + "\n        role: identifier\n")
	}
	m := repositoryWithModel(goodCurrentModel)
	m.Nodes[meaningPth] = Node{Kind: File, Content: meaning("Id")}
	if r := Check(m, publisher()); !r.OK() {
		t.Errorf("a field the model has: %v", r.Findings)
	}
	m.Nodes[meaningPth] = Node{Kind: File, Content: meaning("Nope")}
	only(t, Check(m, publisher()), "meaning-binding", meaningPth, 9, `names property "Nope", which Album does not have in the ModelSpec`)
	// With a field that is refused there are no facts to hold the binding to, as with a property that is refused.
	m.Nodes[modelPath] = Node{Kind: File, Content: []byte(strings.Replace(goodCurrentModel, `"Album": {"fields": {"Id": {"type": "int"}}}`, `"Album": {}`, 1))}
	only(t, Check(m, publisher()), RuleModelEntity, modelPath, 0, "record Album has no fields")
}

// The registered model of this repository, and the copy of it that `modelspec rewrite --write` makes, are accepted alike in a repository that registers them.
func TestTheRegisteredModelAndItsRewrittenCopyAreAcceptedAlike(t *testing.T) {
	registered, err := os.ReadFile("../../../publisher/source/model/ecb-daily.modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile("testdata/modelspec/ecb-daily.rewritten.modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string][]byte{"registered": registered, "rewritten": rewritten} {
		m := repositoryWithModel(strings.Replace(string(model), `"name": "ecb"`, `"name": "chinook"`, 1))
		m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(ownManifest, "  - Album\n  - Artist\n", "  - FxReferenceQuote\n", 1))}
		if r := Check(m, publisher()); !r.OK() {
			t.Errorf("%s: %v", name, r.Findings)
		}
	}
}

// The README has a row for every rule with no verdict of the reference checker, with the text that is here.
var noReferenceVerdict = map[string]string{
	RuleModelVocabulary: "The `\"modelspec\"` identifier of the model file names one vocabulary, `1.0-draft` (entities, properties, entity) or `1.0-draft-2` (records, fields, record), and the document has a key of the other (at the top level, in a record type or entity, or in a member of one or of a component): it is refused, as ModelSpec's own reader refuses it. The reference checker reads the earlier vocabulary only.",
	RuleModelRemoved:    "The model file has a top-level key of a construct that ModelSpec removed (`collections`, `recordsets`) or reserved with no content (`projections`, `migrations`), in either vocabulary: it is refused, as ModelSpec's own reader refuses it. The reference checker never reads those keys.",
}

func TestReadmeDescribesTheRulesWithNoReferenceVerdict(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	section := string(readme)
	_, section, ok := strings.Cut(section, "### Rules with no verdict of the reference\n")
	if !ok {
		t.Fatal("the README has no section \"Rules with no verdict of the reference\"")
	}
	section, _, _ = strings.Cut(section, "\n#")
	rows := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		if cells := strings.Split(line, "|"); len(cells) == 4 && strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			rows[strings.Trim(strings.TrimSpace(cells[1]), "`")] = strings.TrimSpace(cells[2])
		}
	}
	for rule, text := range noReferenceVerdict {
		if rows[rule] != text {
			t.Errorf("README row of %q is %q, want %q", rule, rows[rule], text)
		}
	}
	if len(rows) != len(noReferenceVerdict) {
		t.Errorf("README has rows %v", rows)
	}
	// None of them is among the kinds in which Go is stricter than the reference: no case of the comparison shows them.
	for rule := range noReferenceVerdict {
		if _, ok := stricterKinds[rule]; ok {
			t.Errorf("%s is in stricterKinds", rule)
		}
	}
}
