package repo

import (
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The JSON form of a model has two vocabularies, and its "modelspec" identifier says which: "1.0-draft" has entities, properties and entity, and "1.0-draft-2"
// has records, fields and record. These tests are about the second and about the two together. They are kept out of the comparison with the reference checker
// (golden_test.go and testdata/reference), which reads the first vocabulary only and so has no verdict on a document in the second: they call readModel and Check
// on documents of their own.
const (
	earlierDoc = `{"modelspec":"1.0-draft","module":{"name":"m"},"entities":{"A":{"key":["id"],"properties":{"id":{"type":"int"},"b":{"entity":"B"}}},"B":{"properties":{"id":{"type":"int"}}}}}`
	currentDoc = `{"modelspec":"1.0-draft-2","module":{"name":"m"},"records":{"A":{"key":["id"],"fields":{"id":{"type":"int"},"b":{"record":"B"}}},"B":{"fields":{"id":{"type":"int"}}}}}`
)

// issueTexts are the issues of a reading, each as "rule: text".
func issueTexts(spec modelSpec) []string {
	var out []string
	for _, issue := range spec.issues {
		out = append(out, issue.rule+": "+issue.text)
	}
	return out
}

// wantIssues holds the issues of a reading to the expected ones: the same number, in the same order, each of the rule and containing the text that is given as
// "rule|text".
func wantIssues(t *testing.T, name string, spec modelSpec, want []string) {
	t.Helper()
	got := issueTexts(spec)
	if len(got) != len(want) {
		t.Errorf("%s: issues %q, want %q", name, got, want)
		return
	}
	for i, w := range want {
		rule, text, _ := strings.Cut(w, "|")
		if !strings.HasPrefix(got[i], rule+": ") || !strings.Contains(got[i], text) {
			t.Errorf("%s: issue %d is %q, want rule %s and %q", name, i, got[i], rule, text)
		}
	}
}

func TestReadModelVocabularies(t *testing.T) {
	missingVersion := RuleModelVersion + `|has no "modelspec" version`
	for name, c := range map[string]struct {
		in     string
		group  string   // the key of the group that was read: entities or records
		names  []string // the record types in it
		object bool     // the group is an object
		issues []string
	}{
		"the earlier vocabulary":                         {in: earlierDoc, group: "entities", names: []string{"A", "B"}, object: true},
		"the current vocabulary":                         {in: currentDoc, group: "records", names: []string{"A", "B"}, object: true},
		"the current, empty":                             {in: `{"modelspec":"1.0-draft-2","records":{}}`, group: "records", object: true},
		"the current, not an object":                     {in: `{"modelspec":"1.0-draft-2","records":[]}`, group: "records"},
		"the current, null":                              {in: `{"modelspec":"1.0-draft-2","records":null}`, group: "records"},
		"the current, repeated":                          {in: `{"modelspec":"1.0-draft-2","records":{"X":{}},"records":{"Y":{}}}`, group: "records", names: []string{"Y"}, object: true, issues: []string{RuleModelEntity + "|record Y has no fields (an object with at least one field)"}},
		"the current, repeated and then a list":          {in: `{"modelspec":"1.0-draft-2","records":{"X":{}},"records":[]}`, group: "records"},
		"the current, a name that is not an identifier":  {in: `{"modelspec":"1.0-draft-2","records":{"a-b":{"fields":{"x":{"type":"int"}}}}}`, group: "records", names: []string{"a-b"}, object: true, issues: []string{RuleModelEntity + `|record name "a-b" must be an identifier`}},
		"the current, a field that is not an identifier": {in: `{"modelspec":"1.0-draft-2","records":{"A":{"fields":{"a-b":{"type":"int"}}}}}`, group: "records", names: []string{"A"}, object: true, issues: []string{RuleModelProperty + `|field name "A.a-b" must be an identifier`}},
		"the current, a type that is not a type name":    {in: `{"modelspec":"1.0-draft-2","records":{"A":{"fields":{"a":{"type":"1x"}}}}}`, group: "records", names: []string{"A"}, object: true, issues: []string{RuleModelProperty + `|A.a has type "1x"`}},
		"the current, a field with neither":              {in: `{"modelspec":"1.0-draft-2","records":{"A":{"fields":{"a":{}}}}}`, group: "records", names: []string{"A"}, object: true, issues: []string{RuleModelProperty + "|A.a has neither a type nor a record"}},
		"the current, a record the model lacks":          {in: `{"modelspec":"1.0-draft-2","records":{"A":{"fields":{"a":{"record":"Z"}}}}}`, group: "records", names: []string{"A"}, object: true, issues: []string{RuleModelProperty + "|A.a references record Z, which the model does not have"}},
		"the earlier, an entity the model lacks":         {in: `{"modelspec":"1.0-draft","entities":{"A":{"properties":{"a":{"entity":"Z"}}}}}`, group: "entities", names: []string{"A"}, object: true, issues: []string{RuleModelProperty + "|A.a references entity Z, which the model does not have"}},

		"the current beside entities":          {in: strings.Replace(currentDoc, `"records"`, `"entities":{},"records"`, 1), group: "records", names: []string{"A", "B"}, object: true, issues: []string{RuleModelVocabulary + `|it has "entities", which`}},
		"the current, entities that is a list": {in: strings.Replace(currentDoc, `"records"`, `"entities":[],"records"`, 1), group: "records", names: []string{"A", "B"}, object: true, issues: []string{RuleModelVocabulary + `|it has "entities", which`}},
		"the current with properties": {in: strings.Replace(currentDoc, `"fields":{"id":{"type":"int"},"b"`, `"properties":{"id":{"type":"int"},"b"`, 1), group: "records", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "properties", which`, RuleModelEntity + "|record A has no fields"}},
		"the current with entity": {in: strings.Replace(currentDoc, `"record":"B"`, `"entity":"B"`, 1), group: "records", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "entity", which`, RuleModelProperty + "|A.b has neither a type nor a record"}},
		"the earlier beside records": {in: strings.Replace(earlierDoc, `"entities"`, `"records":{},"entities"`, 1), group: "entities", names: []string{"A", "B"}, object: true, issues: []string{RuleModelVocabulary + `|it has "records", which`}},
		"the earlier with fields": {in: strings.Replace(earlierDoc, `"properties":{"id":{"type":"int"},"b"`, `"fields":{"id":{"type":"int"},"b"`, 1), group: "entities", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "fields", which`, RuleModelEntity + "|entity A has no properties"}},
		"the earlier with record": {in: strings.Replace(earlierDoc, `"entity":"B"`, `"record":"B"`, 1), group: "entities", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`, RuleModelProperty + "|A.b has neither a type nor an entity"}},
		"the earlier with all three": {in: `{"modelspec":"1.0-draft","entities":{"A":{"fields":{},"properties":{"b":{"record":"B"}}}},"records":{}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "records", "fields", "record", which`, RuleModelProperty + "|A.b has neither a type nor an entity"}},

		"the earlier, a component with an entity": {in: `{"modelspec":"1.0-draft","components":{"C":{"fields":{"x":{"entity":"A"}}}},"entities":{}}`, group: "entities", object: true},
		"the earlier, a component with a record": {in: `{"modelspec":"1.0-draft","components":{"C":{"fields":{"x":{"record":"A"}}}},"entities":{}}`, group: "entities", object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},
		"the current, a component with a record": {in: `{"modelspec":"1.0-draft-2","components":{"C":{"fields":{"x":{"record":"A"}}}},"records":{}}`, group: "records", object: true},
		"the current, a component with an entity": {in: `{"modelspec":"1.0-draft-2","components":{"C":{"fields":{"x":{"entity":"A"}}}},"records":{}}`, group: "records", object: true,
			issues: []string{RuleModelVocabulary + `|it has "entity", which`}},
		"a component of every shape": {in: `{"modelspec":"1.0-draft","components":{"C":{"use":["D"],"fields":{"x":{"type":"int"},"y":5,"z":{"record":"R"}}},"D":{"fields":[]},"E":7},"entities":{}}`, group: "entities", object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},
		"a component of every shape, in order": {in: `{"modelspec":"1.0-draft-2","components":{"C":{"use":["D"],"fields":{"x":{"type":"int"},"y":5,"z":{"record":"R"}}},"D":{"fields":[]},"E":7},"records":{}}`, group: "records", object: true},
		"components that are not an object":    {in: `{"modelspec":"1.0-draft-2","components":[1,2],"records":{}}`, group: "records", object: true},

		// A member that carries both words of the reference is refused, whichever it is the member of, and whichever vocabulary names the document.
		"the earlier, a member with both reference words": {in: strings.Replace(earlierDoc, `"entity":"B"`, `"entity":"B","record":"B"`, 1), group: "entities", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},
		"the earlier, both reference words the other way round": {in: strings.Replace(earlierDoc, `"entity":"B"`, `"record":"B","entity":"B"`, 1), group: "entities", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},
		"the current, a member with both reference words": {in: strings.Replace(currentDoc, `"record":"B"`, `"record":"B","entity":"B"`, 1), group: "records", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "entity", which`}},
		"the current, both reference words the other way round": {in: strings.Replace(currentDoc, `"record":"B"`, `"entity":"B","record":"B"`, 1), group: "records", names: []string{"A", "B"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "entity", which`}},
		"the earlier, a component member with both reference words": {in: `{"modelspec":"1.0-draft","components":{"C":{"fields":{"x":{"entity":"A","record":"A"}}}},"entities":{}}`, group: "entities", object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},
		"the current, a component member with both reference words": {in: `{"modelspec":"1.0-draft-2","components":{"C":{"fields":{"x":{"entity":"A","record":"A"}}}},"records":{}}`, group: "records", object: true,
			issues: []string{RuleModelVocabulary + `|it has "entity", which`}},

		// A key is noticed in every occurrence of a repeated key, also in one that a later occurrence overwrites: the verdict is the safer one, and the reading is the last's.
		"the earlier, a stray members key in a group that is repeated": {in: `{"modelspec":"1.0-draft","entities":{"A":{"fields":{}}},"entities":{"A":{"properties":{"id":{"type":"int"}}}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "fields", which`}},
		"the earlier, a stray reference key in a group that is repeated": {in: `{"modelspec":"1.0-draft","entities":{"A":{"properties":{"x":{"record":"A"}}}},"entities":{"A":{"properties":{"id":{"type":"int"}}}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},
		"the earlier, a stray members key in a record type that repeats a name": {in: `{"modelspec":"1.0-draft","entities":{"A":{"fields":{}},"A":{"properties":{"id":{"type":"int"}}}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "fields", which`}},
		"the current, a stray members key in a group that is repeated": {in: `{"modelspec":"1.0-draft-2","records":{"A":{"properties":{}}},"records":{"A":{"fields":{"id":{"type":"int"}}}}}`, group: "records", names: []string{"A"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "properties", which`}},
		"the current, a stray group that is overwritten by a list": {in: `{"modelspec":"1.0-draft-2","entities":{},"entities":[],"records":{"A":{"fields":{"id":{"type":"int"}}}}}`, group: "records", names: []string{"A"}, object: true,
			issues: []string{RuleModelVocabulary + `|it has "entities", which`}},
		"components repeated, the stray reference in the first": {in: `{"modelspec":"1.0-draft","components":{"C":{"fields":{"x":{"record":"A"}}}},"components":{},"entities":{}}`, group: "entities", object: true,
			issues: []string{RuleModelVocabulary + `|it has "record", which`}},

		// An identifier that is neither is read in the earlier vocabulary, unless the document has a key of the current one: then it is refused for its identifier.
		"an unknown identifier, records only": {in: `{"modelspec":"1.0-draft2","module":{"name":"m"},"records":{"A":{"fields":{"id":{"type":"int"}}}}}`, group: "entities",
			issues: []string{RuleModelVersion + `|"modelspec" is "1.0-draft2", which this check does not know: it reads "1.0-draft" (entities, properties, entity) and "1.0-draft-2" (records, fields, record), and the model has "records", which belong to "1.0-draft-2"`}},
		"an unknown identifier, entities and records": {in: `{"modelspec":"1.0-Draft-2","entities":{"A":{"properties":{"id":{"type":"int"}}}},"records":{"B":{}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVersion + `|"modelspec" is "1.0-Draft-2", which this check does not know`}},
		"an unknown identifier, fields in an entity": {in: `{"modelspec":"1.0-draft ","entities":{"A":{"fields":{}}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVersion + `|and the model has "fields", which belong to`, RuleModelEntity + "|entity A has no properties"}},
		"an unknown identifier, a record reference": {in: `{"modelspec":"2","entities":{"A":{"properties":{"x":{"record":"A"}}}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVersion + `|and the model has "record", which belong to`, RuleModelProperty + "|A.x has neither a type nor an entity"}},
		"an unknown identifier, a record reference in a component": {in: `{"modelspec":"2","components":{"C":{"fields":{"x":{"record":"A"}}}},"entities":{}}`, group: "entities", object: true,
			issues: []string{RuleModelVersion + `|and the model has "record", which belong to`}},
		"an unknown identifier, all three": {in: `{"modelspec":"2","entities":{"A":{"fields":{},"properties":{"x":{"record":"A"}}}},"records":{}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVersion + `|and the model has "records", "fields", "record", which belong to`, RuleModelProperty + "|A.x has neither a type nor an entity"}},
		"an unknown identifier with the keys of the earlier vocabulary only": {in: `{"modelspec":"2","components":{"C":{"fields":{"x":{"entity":"A"}}}},"entities":{"A":{"properties":{"b":{"entity":"A"}}}}}`, group: "entities", names: []string{"A"}, object: true},
		"another identifier reads the earlier vocabulary": {in: `{"modelspec":"1","entities":{"A":{"properties":{"b":{"entity":"A"}}}},"records":{"B":{}},"components":{"C":{"fields":{"x":{"record":"A"}}}}}`, group: "entities", names: []string{"A"}, object: true,
			issues: []string{RuleModelVersion + `|and the model has "records", "record", which belong to`}},
		"a later identifier with records only": {in: `{"modelspec":"1.0-draft-3","records":{"B":{}}}`, group: "entities",
			issues: []string{RuleModelVersion + `|"modelspec" is "1.0-draft-3", which this check does not know`}},
		"no identifier, records":            {in: `{"module":{"name":"m"},"records":{"A":{}}}`, group: "entities", issues: []string{missingVersion}},
		"no identifier, entities":           {in: `{"entities":{"A":{"fields":{}}}}`, group: "entities", names: []string{"A"}, object: true, issues: []string{missingVersion, RuleModelEntity + "|entity A has no properties"}},
		"an identifier that is a number":    {in: `{"modelspec":2,"entities":{}}`, group: "entities", object: true, issues: []string{missingVersion}},
		"the identifier last":               {in: `{"records":{"A":{"fields":{"x":{"type":"int"}}}},"modelspec":"1.0-draft-2"}`, group: "records", names: []string{"A"}, object: true},
		"the identifier repeated, the last": {in: `{"modelspec":"1.0-draft","modelspec":"1.0-draft-2","records":{"A":{"fields":{"x":{"type":"int"}}}}}`, group: "records", names: []string{"A"}, object: true},
	} {
		spec, err := readModel([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if spec.spelling.group != c.group || spec.hasEntities != c.object || !slices.Equal(spec.entities, c.names) {
			t.Errorf("%s: read %s, object %v, names %v; want %s, %v, %v", name, spec.spelling.group, spec.hasEntities, spec.entities, c.group, c.object, c.names)
		}
		wantIssues(t, name, spec, c.issues)
	}
}

// A removed construct and a reserved word is refused under either identifier and under none, and each is named once.
func TestReadModelRefusesRemovedAndReservedKeys(t *testing.T) {
	for _, word := range []string{"collections", "recordsets", "projections", "migrations"} {
		for name, head := range map[string]string{"earlier": `"modelspec":"1.0-draft",`, "current": `"modelspec":"1.0-draft-2",`, "none": ``, "other": `"modelspec":"7",`} {
			for _, value := range []string{`{}`, `[]`, `null`, `3`} {
				spec, err := readModel([]byte(`{` + head + `"module":{"name":"m"},"` + word + `":` + value + `}`))
				if err != nil {
					t.Fatal(err)
				}
				var want []string
				if name == "none" {
					want = append(want, RuleModelVersion+"|")
				}
				text := "has " + `"` + word + `", which ModelSpec removed`
				if word == "projections" || word == "migrations" {
					text = "has " + `"` + word + `", a word that ModelSpec reserves`
				}
				wantIssues(t, word+" "+name+" "+value, spec, append(want, RuleModelRemoved+"|"+text))
			}
		}
	}
	// What the hint says of a result's shape uses the words of the vocabulary.
	for head, want := range map[string]string{`"modelspec":"1.0-draft",`: "is an entity with no key", `"modelspec":"1.0-draft-2",`: "is a record with no key"} {
		spec, _ := readModel([]byte(`{` + head + `"recordsets":{}}`))
		wantIssues(t, head, spec, []string{RuleModelRemoved + "|" + want})
	}
	spec, _ := readModel([]byte(`{"modelspec":"1.0-draft-2","collections":{}}`))
	wantIssues(t, "collections", spec, []string{RuleModelRemoved + "|a stored set of rows is described with the database that holds it"})

	// Each is named once, whatever its value or how often it is written, and in the order of their names.
	spec, _ = readModel([]byte(`{"modelspec":"1.0-draft","recordsets":1,"collections":{},"collections":2,"projections":[],"records":{}}`))
	wantIssues(t, "several", spec, []string{RuleModelVocabulary + "|", RuleModelRemoved + `|has "collections"`, RuleModelRemoved + `|has "projections"`, RuleModelRemoved + `|has "recordsets"`})
	// The names of other things are not these words, in either place: the key must be a top-level key, written exactly.
	for _, in := range []string{
		`{"modelspec":"1.0-draft","records2":1,"Collections":{},"recordset":{},"entities":{"collections":{"properties":{"recordsets":{"type":"int"}}}},"module":{"collections":1}}`,
		`{"modelspec":"1.0-draft-2","records":{"collections":{"fields":{"projections":{"type":"int"},"migrations":{"type":"int"}}}},"module":{"recordsets":1}}`,
	} {
		spec, _ = readModel([]byte(in))
		wantIssues(t, in, spec, nil)
	}
}

// A document in the current vocabulary has the same reading as the same document in the earlier one, apart from the words of its findings.
func TestTheCurrentVocabularyIsReadAsTheEarlierOne(t *testing.T) {
	earlierSpec, err := readModel([]byte(earlierDoc))
	if err != nil {
		t.Fatal(err)
	}
	currentSpec, err := readModel([]byte(currentDoc))
	if err != nil {
		t.Fatal(err)
	}
	if !sameReading(earlierSpec, currentSpec) || earlierSpec.spelling.group == currentSpec.spelling.group {
		t.Errorf("earlier %+v, current %+v", earlierSpec, currentSpec)
	}
	if !slices.Equal(currentSpec.entities, []string{"A", "B"}) || len(currentSpec.properties["A"]) != 2 {
		t.Errorf("current %+v", currentSpec)
	}

	// The same for the registered model of this repository and the copy of it that `modelspec rewrite --write` makes (modelspec 0.2.0): its testdata copy.
	registered, err := os.ReadFile("../../../publisher/source/model/ecb-daily.modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile("testdata/modelspec/ecb-daily.rewritten.modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(registered), `"1.0-draft"`) || !strings.Contains(string(rewritten), `"1.0-draft-2"`) {
		t.Fatal("the registered model must be in the earlier vocabulary and its copy in the current one")
	}
	a, err := readModel(registered)
	if err != nil {
		t.Fatal(err)
	}
	b, err := readModel(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if !sameReading(a, b) || a.module != "ecb" || !slices.Equal(a.entities, []string{"FxReferenceQuote"}) || len(a.issues) > 0 || a.spelling.group != "entities" || b.spelling.group != "records" {
		t.Errorf("registered %+v, rewritten %+v", a, b)
	}
}

// sameReading says whether two readings are the same in everything but the vocabulary they were read in.
func sameReading(a, b modelSpec) bool {
	a.spelling, b.spelling = spelling{}, spelling{}
	return reflect.DeepEqual(a, b)
}

// manyRecords is a model in the current vocabulary with n records called e0, e1 ... in base 36.
func manyRecords(n int) string {
	var b strings.Builder
	b.WriteString(`{"modelspec":"1.0-draft-2","records":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"e` + strconv.FormatInt(int64(i), 36) + `":{}`)
	}
	b.WriteString("}}")
	return b.String()
}

func TestReadModelBoundsInTheCurrentVocabulary(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	for name, c := range map[string]struct {
		in        string
		err       error
		truncated bool
		have      int // the number of record types read, when there is no error
	}{
		"MaxEntities records":                            {in: manyRecords(MaxEntities), have: MaxEntities},
		"one more than that":                             {in: manyRecords(MaxEntities + 1), err: errRecords},
		"entities and then too many records":             {in: strings.Replace(manyRecords(MaxEntities+1), `"records"`, `"entities":{"A":{}},"records"`, 1), err: errRecords},
		"too many entities still":                        {in: manyEntities(MaxEntities + 1), err: errEntities},
		"95 levels below a field of a component":         {in: `{"components":{"C":{"fields":{"x":{"y":` + deep(95) + `}}}}}`},
		"96 levels below a field of a component":         {in: `{"components":{"C":{"fields":{"x":{"y":` + deep(96) + `}}}}}`, err: errDepth},
		"a field of a component that is a list too deep": {in: `{"components":{"C":{"fields":{"x":` + deep(97) + `}}}}`, err: errDepth},
		"fields of a component that are a list too deep": {in: `{"components":{"C":{"fields":` + deep(98) + `}}}`, err: errDepth},
		"a component attribute too deep":                 {in: `{"components":{"C":{"use":` + deep(98) + `}}}`, err: errDepth},
		"a component that is a list too deep":            {in: `{"components":{"C":` + deep(99) + `}}`, err: errDepth},
		"components that are a list too deep":            {in: `{"components":` + deep(100) + `}`, err: errDepth},
		"a record too deep":                              {in: `{"records":{"E":{"x":` + deep(98) + `}}}`, err: errDepth},
		"a field of a record too deep":                   {in: `{"records":{"E":{"fields":{"p":` + deep(97) + `}}}}`, err: errDepth},
		"a removed word too deep":                        {in: `{"collections":` + deep(100) + `}`, err: errDepth},
		"a component, cut":                               {in: `{"components":{"C":{"fields":{"x":{"entity":`, truncated: true},
	} {
		spec, err := readModel([]byte(c.in))
		switch {
		case c.truncated:
			if err == nil {
				t.Errorf("%s: accepted a truncated value", name)
			}
		case c.err != nil:
			if err != c.err {
				t.Errorf("%s: err = %v, want %v", name, err, c.err)
			}
		case err != nil:
			t.Errorf("%s: err = %v", name, err)
		case len(spec.entities) != c.have:
			t.Errorf("%s: %d record types read, want %d", name, len(spec.entities), c.have)
		}
	}
}
