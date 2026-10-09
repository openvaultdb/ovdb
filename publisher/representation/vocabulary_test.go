package representation

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// currentSpelling writes an earlier-vocabulary model of the fixtures in the current vocabulary, key by key, as modelspec rewrite does (the fixture models have
// no member that references a record type, so there is no entity key to move).
func currentSpelling(text string) string {
	return strings.NewReplacer(`"1.0-draft"`, `"1.0-draft-2"`, `"entities"`, `"records"`, `"properties"`, `"fields"`).Replace(text)
}

func fixtureModel(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Either vocabulary of a referenced model is read, the contract's own words unchanged: its entity names a record type and its property names a field.
func TestAContractReadsAModelAndASourceSchemaInEitherVocabulary(t *testing.T) {
	source, target := fixtureModel(t, "source.modelspec.json"), fixtureModel(t, "target.modelspec.json")
	for name, tc := range map[string]struct{ source, target string }{
		"both earlier":                   {source, target},
		"both current":                   {currentSpelling(source), currentSpelling(target)},
		"source current, target earlier": {currentSpelling(source), target},
		"source earlier, target current": {source, currentSpelling(target)},
	} {
		t.Run(name, func(t *testing.T) {
			data, ctx := rewrittenFixture(t, map[string][]byte{"source.modelspec.json": []byte(tc.source), "target.modelspec.json": []byte(tc.target)})
			doc, err := Check(data, ctx)
			if err != nil {
				t.Fatal(err)
			}
			if c := doc.Contracts[0]; c.Source.Entity != "Customer" || c.Source.Property != "Country" || c.Target.Entity != "Countries" || c.Target.Property != "iso" {
				t.Fatalf("the contract's own fields changed: %+v", c)
			}
		})
	}
	// The contract document is the same bytes in both: only the hashes of the models it pins differ.
	earlier, _ := rewrittenFixture(t, map[string][]byte{})
	current, _ := rewrittenFixture(t, map[string][]byte{"source.modelspec.json": []byte(currentSpelling(source)), "target.modelspec.json": []byte(currentSpelling(target))})
	var a, b Document
	if err := json.Unmarshal(earlier, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(current, &b); err != nil {
		t.Fatal(err)
	}
	a.Contracts[0].Source.Schema.SHA256, b.Contracts[0].Source.Schema.SHA256 = "", ""
	a.Contracts[0].Target.Model.SHA256, b.Contracts[0].Target.Model.SHA256 = "", ""
	a.Contracts[0].Target.Snapshot.SHA256, b.Contracts[0].Target.Snapshot.SHA256 = "", ""
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("the contract differs beyond the hashes of what it points at:\n%+v\n%+v", a, b)
	}
}

// What is exact in a model stays exact in the current vocabulary, and the identifier decides the vocabulary: a document whose keys disagree with it is refused.
// Each edit is made to the fixture's source schema and to its target model, and each refusal says why, so that none is a refusal for another reason.
func TestAModelWhoseKeysDisagreeWithItsIdentifierIsRefused(t *testing.T) {
	withTop := func(text, member string) string { return strings.Replace(text, "{\n", "{\n  "+member+",\n", 1) }
	once := func(old, new string) func(string) string {
		return func(text string) string { return strings.Replace(text, old, new, 1) }
	}
	const disagree, removed, exact, resolve = "ModelSpec document says", "removed or reserved", "non-exact JSON field", "does not resolve exactly"
	type edit struct {
		earlier bool // the edit is made to the model in the earlier vocabulary
		change  func(string) string
		want    string
	}
	cases := map[string]edit{
		"current keys under the earlier identifier":          {false, once(`"1.0-draft-2"`, `"1.0-draft"`), disagree},
		"earlier keys under the current identifier":          {true, once(`"1.0-draft"`, `"1.0-draft-2"`), disagree},
		"current keys under an identifier that is not known": {false, once(`"1.0-draft-2"`, `"1.0-draft-3"`), resolve},
		"current keys under no identifier":                   {false, once(`  "modelspec": "1.0-draft-2",`+"\n", ""), resolve},
		"entities beside records":                            {false, func(text string) string { return withTop(text, `"entities": {}`) }, disagree},
		"records beside entities":                            {true, func(text string) string { return withTop(text, `"records": {}`) }, disagree},
		"properties in a record type under the current":      {false, once(`"fields": {`, `"properties": {}, "fields": {`), disagree},
		"fields in an entity under the earlier":              {true, once(`"properties": {`, `"fields": {}, "properties": {`), disagree},
		"entity on a member under the current":               {false, once(`"type": "string"`, `"entity": "Customer", "type": "string"`), disagree},
		"record on a member under the earlier":               {true, once(`"type": "string"`, `"record": "Customer", "type": "string"`), disagree},
		"collections under the current":                      {false, func(text string) string { return withTop(text, `"collections": {}`) }, removed},
		"recordsets under the current":                       {false, func(text string) string { return withTop(text, `"recordsets": {}`) }, removed},
		"projections under the earlier":                      {true, func(text string) string { return withTop(text, `"projections": {}`) }, removed},
		"migrations under the earlier":                       {true, func(text string) string { return withTop(text, `"migrations": {}`) }, removed},
		"records spelled in capitals":                        {false, once(`"records"`, `"RECORDS"`), exact},
		"the fields of a record type spelled in capitals":    {false, once(`"fields"`, `"FIELDS"`), exact},
		"the type of a field spelled in capitals":            {false, once(`"type"`, `"TYPE"`), exact},
		"the module name spelled in capitals":                {false, once(`"name"`, `"NAME"`), exact},
		"the module is another":                              {false, once(`"name": "`, `"name": "other`), resolve},
		"the datatype is another":                            {false, once(`"type": "string"`, `"type": "int"`), resolve},
		"the records are not an object": {false, func(string) string {
			return `{"modelspec": "1.0-draft-2", "module": {"name": "sample"}, "records": []}`
		}, "cannot unmarshal"},
		"no records": {false, func(string) string { return `{"modelspec": "1.0-draft-2", "module": {"name": "sample"}}` }, resolve},
	}
	for name, tc := range cases {
		for _, position := range []string{"source.modelspec.json", "target.modelspec.json"} {
			t.Run(name+"/"+position, func(t *testing.T) {
				base := fixtureModel(t, position)
				if !tc.earlier {
					base = currentSpelling(base)
				}
				asset := tc.change(base)
				if asset == base {
					t.Fatal("the edit changed nothing")
				}
				data, ctx := rewrittenFixture(t, map[string][]byte{position: []byte(asset)})
				if _, err := Check(data, ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want a refusal that says %q, got %v for %s", tc.want, err, asset)
				}
			})
		}
	}
	// The same edits that change nothing are accepted, so each refusal above is the edit's. Unrelated keys survive in both vocabularies, as they always did.
	for _, position := range []string{"source.modelspec.json", "target.modelspec.json"} {
		earlier := fixtureModel(t, position)
		for _, text := range []string{earlier, currentSpelling(earlier), withTop(earlier, `"description": "unrelated"`), withTop(currentSpelling(earlier), `"description": "unrelated"`), withTop(currentSpelling(earlier), `"components": {}`)} {
			data, ctx := rewrittenFixture(t, map[string][]byte{position: []byte(text)})
			if _, err := Check(data, ctx); err != nil {
				t.Fatalf("%s: %v", text, err)
			}
		}
	}
}

// Keys are matched by their exact bytes, as the Directory's reader does: a key that differs from a word of the other vocabulary only by case (or by the long s)
// is an unrelated key, which is ignored, in either vocabulary. encoding/json matches a key without regard to case and would read it as the word.
func TestAKeyThatOnlyFoldsToAWordOfAnotherVocabularyIsIgnored(t *testing.T) {
	withTop := func(text, member string) string { return strings.Replace(text, "{\n", "{\n  "+member+",\n", 1) }
	once := func(old, new string) func(string) string {
		return func(text string) string { return strings.Replace(text, old, new, 1) }
	}
	// the record types and the members of the first record type, under another key: the contract's scope resolves only through that key
	moved := func(group, members string) func(string) string {
		return func(text string) string {
			return strings.Replace(strings.Replace(text, `"records": {`, `"`+group+`": {`, 1), `"fields": {`, `"`+members+`": {`, 1)
		}
	}
	type edit struct {
		earlier bool
		change  func(string) string
		want    string // "" when the document is accepted
	}
	const resolve = "does not resolve exactly"
	cases := map[string]edit{
		"records only under Entities":           {false, moved("Entities", "properties"), resolve},
		"records only under ENTITIES":           {false, moved("ENTITIES", "properties"), resolve},
		"records only under the long s":         {false, moved("entitie\u017f", "properties"), resolve},
		"fields only under Properties":          {false, once(`"fields": {`, `"Properties": {`), resolve},
		"fields only under PROPERTIES":          {false, once(`"fields": {`, `"PROPERTIES": {`), resolve},
		"fields only under the long s":          {false, once(`"fields": {`, `"propertie\u017f": {`), resolve},
		"the long s as null beside the records": {false, func(text string) string { return withTop(text, `"entitie\u017f": null`) }, ""},
		"Entities as true beside the records":   {false, func(text string) string { return withTop(text, `"Entities": true`) }, ""},
		"Entities as a list beside the records": {false, func(text string) string { return withTop(text, `"Entities": []`) }, ""},
		"Entities with a record type beside the records": {false, func(text string) string {
			return withTop(text, `"Entities": {"Customer": {"properties": {"Country": {"type": "int"}}}}`)
		}, ""},
		"the long s as null in the record type":   {false, once(`"fields": {`, `"propertie\u017f": null, "fields": {`), ""},
		"Properties as a list in the record type": {false, once(`"fields": {`, `"Properties": [], "fields": {`), ""},
		"Entity on a field":                       {false, once(`"type": "string"`, `"Entity": "x", "type": "string"`), ""},
		"Records beside the entities":             {true, func(text string) string { return withTop(text, `"Records": null`) }, ""},
		"Fields in an entity":                     {true, once(`"properties": {`, `"Fields": [], "properties": {`), ""},
		"Record on a property":                    {true, once(`"type": "string"`, `"Record": "x", "type": "string"`), ""},
		"the identifier key in capitals":          {false, once(`"modelspec"`, `"MODELSPEC"`), "non-exact JSON field"},
		"the identifier key in capitals, earlier": {true, once(`"modelspec"`, `"MODELSPEC"`), "non-exact JSON field"},
	}
	for name, tc := range cases {
		for _, position := range []string{"source.modelspec.json", "target.modelspec.json"} {
			t.Run(name+"/"+position, func(t *testing.T) {
				base := fixtureModel(t, position)
				if !tc.earlier {
					base = currentSpelling(base)
				}
				asset := tc.change(base)
				if asset == base {
					t.Fatal("the edit changed nothing")
				}
				data, ctx := rewrittenFixture(t, map[string][]byte{position: []byte(asset)})
				_, err := Check(data, ctx)
				if tc.want == "" && err != nil {
					t.Fatalf("refused: %v\n%s", err, asset)
				}
				if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
					t.Fatalf("want a refusal that says %q, got %v\n%s", tc.want, err, asset)
				}
			})
		}
	}
}

// With several wrong keys, the refusal says the same thing on every run: the keys are looked at in order.
func TestTheRefusalOfSeveralWrongKeysIsTheSameEveryTime(t *testing.T) {
	text := currentSpelling(fixtureModel(t, "target.modelspec.json"))
	for _, name := range []string{"Countries", "CustomerCountries"} {
		text = strings.Replace(text, `"`+name+`": {`, `"`+name+`": {"properties": {},`, 1)
	}
	text = strings.Replace(text, `"type": "string"`, `"entity": "x", "type": "string"`, -1)
	var first string
	for i := 0; i < 200; i++ {
		err := wordsAgree(mustObject(t, text), currentWords)
		if err == nil {
			t.Fatal("admitted")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("%q then %q", first, err)
		}
	}
	if !strings.Contains(first, "Countries: ") {
		t.Fatalf("the first record type in order is not the one named: %s", first)
	}
	// exactKeys, too
	var firstKey string
	for i := 0; i < 200; i++ {
		err := exactKeys(map[string]any{"Modelspec": 1, "MODULE": 2, "ENTITIES": 3}, []string{"modelspec", "module", "entities"}, false)
		if i == 0 {
			firstKey = err.Error()
		} else if err.Error() != firstKey {
			t.Fatalf("%q then %q", firstKey, err)
		}
	}
	if !strings.Contains(firstKey, "ENTITIES") {
		t.Fatal(firstKey)
	}
}

func mustObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		t.Fatal(err)
	}
	return root
}

// An identifier that is neither of the two decides nothing: such a document does not resolve, whatever its keys, and so is none.
func TestADocumentUnderAnIdentifierThatIsNotKnownDoesNotResolve(t *testing.T) {
	earlier := fixtureModel(t, "source.modelspec.json")
	for name, text := range map[string]string{
		"earlier keys under 1.0-draft-3":                    strings.Replace(earlier, `"1.0-draft"`, `"1.0-draft-3"`, 1),
		"earlier keys under no identifier":                  strings.Replace(earlier, `  "modelspec": "1.0-draft",`+"\n", "", 1),
		"earlier keys under an identifier that is not text": strings.Replace(earlier, `"1.0-draft"`, `1`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if text == earlier {
				t.Fatal("the edit changed nothing")
			}
			data, ctx := rewrittenFixture(t, map[string][]byte{"source.modelspec.json": []byte(text)})
			_, err := Check(data, ctx)
			if err == nil || !strings.Contains(err.Error(), "does not resolve exactly") && !strings.Contains(err.Error(), "cannot unmarshal") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// The contract names things of a model in the current vocabulary by its own words, and each of them is held to the model.
func TestTheContractNamesThingsOfTheCurrentVocabularyByItsOwnWords(t *testing.T) {
	source, target := currentSpelling(fixtureModel(t, "source.modelspec.json")), currentSpelling(fixtureModel(t, "target.modelspec.json"))
	for name, mutate := range map[string]func(*Document){
		"wrong record type":  func(d *Document) { d.Contracts[0].Source.Entity = "Missing" },
		"wrong field":        func(d *Document) { d.Contracts[0].Source.Property = "Missing" },
		"wrong module":       func(d *Document) { d.Contracts[0].Source.Module = "wrong" },
		"wrong datatype":     func(d *Document) { d.Contracts[0].Source.Datatype = "int" },
		"wrong target type":  func(d *Document) { d.Contracts[0].Target.Entity = "Missing" },
		"wrong target field": func(d *Document) { d.Contracts[0].Target.Property = "Missing" },
		"wrong bridge table": func(d *Document) { d.Contracts[0].Bridge.Table = "Countries" },
		"wrong bridge raw":   func(d *Document) { d.Contracts[0].Bridge.RawLabelColumn = "missing" },
		"wrong serving":      func(d *Document) { d.Contracts[0].Bridge.ServingIdentityColumn = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			data, ctx := rewrittenFixture(t, map[string][]byte{"source.modelspec.json": []byte(source), "target.modelspec.json": []byte(target)})
			doc, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			mutate(doc)
			data, _ = json.Marshal(doc)
			if _, err := Check(data, ctx); err == nil {
				t.Fatal("admitted")
			}
		})
	}
	// The serving identity column of the fixture is a field of the bridge table: the correct scope is accepted.
	data, ctx := rewrittenFixture(t, map[string][]byte{"source.modelspec.json": []byte(source), "target.modelspec.json": []byte(target)})
	doc, _ := Parse(data)
	doc.Contracts[0].Bridge.ServingIdentityColumn = "serving_id"
	data, _ = json.Marshal(doc)
	if _, err := Check(data, ctx); err != nil {
		t.Fatal(err)
	}
}

// The three native and bridge fixtures with their models in the current vocabulary are accepted as the earlier ones are, and the native checks of the
// model (one required single-field key, a serving identity column) hold the same way.
func TestTheFixturesInTheCurrentVocabularyAreAccepted(t *testing.T) {
	for _, name := range []string{"real-ror-current", "real-geonames-current", "native-geonames-current"} {
		t.Run(name, func(t *testing.T) {
			data, ctx, _ := realFixture(t, name)
			if _, err := Check(data, ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
	d, ctx, _ := nativeFixtureOf(t, "real-ror-current")
	c := d.Contracts[0]
	if _, err := LookupNative(c, c.Source, ctx, "https://ror.org/000025p04", func(req NativeLookupRequest) ([]string, error) { return []string{req.Raw}, nil }); err != nil {
		t.Fatal(err)
	}
	_, _, assets := nativeFixtureOf(t, "real-ror-current")
	original := string(assets[c.Target.Model])
	for name, raw := range map[string]string{
		"key not a list":          strings.Replace(original, `"key": [`, `"key": "bad", "ignored": [`, 1),
		"not required":            strings.Replace(original, `"required": true`, `"required": false`, 1),
		"required not a bool":     strings.Replace(original, `"required": true`, `"required": "bad"`, 1),
		"empty key":               strings.Replace(original, `"key": [`, `"key": [], "ignored": [`, 1),
		"key in capitals":         strings.Replace(original, `"key": [`, `"KEY": [`, 1),
		"required in capitals":    strings.Replace(original, `"required": true`, `"REQUIRED": true`, 1),
		"properties of an entity": strings.Replace(original, `"fields": {`, `"properties": {}, "fields": {`, 1),
		"entity reference":        strings.Replace(original, `"record": "organizations"`, `"entity": "organizations"`, 1),
	} {
		if raw == original {
			t.Fatalf("%s: the case changed nothing", name)
		}
		t.Run(name, func(t *testing.T) {
			b, ctx := mutateNativeAssetOf(t, "real-ror-current", "model", []byte(raw))
			if _, err := Check(b, ctx); err == nil {
				t.Fatal("admitted")
			}
		})
	}
	for name, mutate := range map[string]func(*Contract){
		"wrong key field":       func(c *Contract) { c.Target.Property = "status" },
		"serving conflation":    func(c *Contract) { c.Native.ServingIdentityColumn = "id" },
		"missing serving field": func(c *Contract) { c.Native.ServingIdentityColumn = "serving_id" },
	} {
		t.Run(name, func(t *testing.T) {
			d, ctx, _ := nativeFixtureOf(t, "real-ror-current")
			mutate(&d.Contracts[0])
			b, _ := json.Marshal(d)
			if _, err := Check(b, ctx); err == nil {
				t.Fatal("admitted")
			}
		})
	}
	d, ctx, _ = nativeFixtureOf(t, "real-ror-current")
	d.Contracts[0].Native.ServingIdentityColumn = "status" // a field of the model, other than the key
	b, _ := json.Marshal(d)
	if _, err := Check(b, ctx); err != nil {
		t.Fatal(err)
	}
}

// A model in the current vocabulary read through asEarlier is the same document the earlier vocabulary has, in every shape that the decoding is sensitive to.
func TestAsEarlierRenamesOnlyTheTwoKeys(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"records and fields":           {`{"records": {"A": {"fields": {"a": {"type": "string", "record": "B"}}, "key": ["a"]}}, "x": 1}`, `{"entities": {"A": {"properties": {"a": {"type": "string", "record": "B"}}, "key": ["a"]}}, "x": 1}`},
		"no records":                   {`{"x": 1}`, `{"x": 1}`},
		"records not an object":        {`{"records": []}`, `{"entities": []}`},
		"a record type not an object":  {`{"records": {"A": null, "B": 5}}`, `{"entities": {"A": null, "B": 5}}`},
		"a record type with no fields": {`{"records": {"A": {"key": []}}}`, `{"entities": {"A": {"key": []}}}`},
	} {
		t.Run(name, func(t *testing.T) {
			var in, want map[string]any
			if err := json.Unmarshal([]byte(tc.in), &in); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if got := asEarlier(in); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
			if _, ok := in["entities"]; ok {
				t.Fatal("the input was changed")
			}
		})
	}
}

// The words are the ones that internal/publisher/repo reads (model.go), which this package cannot import.
func TestTheVocabulariesAreTheModelSpecOnes(t *testing.T) {
	if earlierWords != (vocabulary{"1.0-draft", "entities", "properties", "entity"}) || currentWords != (vocabulary{"1.0-draft-2", "records", "fields", "record"}) {
		t.Fatalf("%+v %+v", earlierWords, currentWords)
	}
	if words, known := vocabularyOf(map[string]any{"modelspec": "1.0-draft-2"}); words != currentWords || !known {
		t.Fatal("the current identifier")
	}
	if words, known := vocabularyOf(map[string]any{"modelspec": "1.0-draft"}); words != earlierWords || !known {
		t.Fatal("the earlier identifier")
	}
	for _, root := range []map[string]any{{}, {"modelspec": 1.0}, {"modelspec": "1.0-draft-3"}} {
		if words, known := vocabularyOf(root); words != earlierWords || known {
			t.Fatalf("%v is read as the earlier, unknown", root)
		}
	}
}

// The fixtures in the current vocabulary are the earlier ones with only their ModelSpec JSON documents renamed and the hashes that this changes: the models
// are the same models key for key, and no other byte differs once the new hashes are put back. A fixture that had drifted would prove less than it says.
func TestTheCurrentFixturesAreTheEarlierOnesRenamed(t *testing.T) {
	type entry struct {
		Reference Reference `json:"reference"`
		File      string    `json:"file"`
	}
	read := func(dir string) (index []entry, files map[string]string) {
		files = map[string]string{}
		listing, err := os.ReadDir("testdata/" + dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range listing {
			data, err := os.ReadFile("testdata/" + dir + "/" + f.Name())
			if err != nil {
				t.Fatal(err)
			}
			files[f.Name()] = string(data)
		}
		if err := json.Unmarshal([]byte(files["references.json"]), &index); err != nil {
			t.Fatal(err)
		}
		return index, files
	}
	// earlierOf is a model in the current vocabulary as the earlier vocabulary has it.
	earlierOf := func(model map[string]any) map[string]any {
		out := asEarlier(model)
		out["modelspec"] = earlierWords.identifier
		for _, raw := range out[earlierWords.records].(map[string]any) {
			for _, member := range raw.(map[string]any)[earlierWords.fields].(map[string]any) {
				member := member.(map[string]any)
				if reference, ok := member[currentWords.record]; ok {
					delete(member, currentWords.record)
					member[earlierWords.record] = reference
				}
			}
		}
		return out
	}
	for _, name := range []string{"real-ror", "real-geonames", "native-geonames"} {
		t.Run(name, func(t *testing.T) {
			was, before := read(name)
			now, after := read(name + "-current")
			if len(was) != len(now) {
				t.Fatalf("%d and %d references", len(was), len(now))
			}
			for file := range before {
				if _, kept := after[file]; !kept && file != "original-validation.json" { // the retained copy of ROR's original receipt is not repeated
					t.Fatalf("%s is missing", file)
				}
			}
			var pairs []string
			for i := range now {
				if was[i].Reference.Path != now[i].Reference.Path || was[i].File != now[i].File {
					t.Fatalf("reference %d differs in its path", i)
				}
				if was[i].Reference.SHA256 != now[i].Reference.SHA256 {
					pairs = append(pairs, now[i].Reference.SHA256, was[i].Reference.SHA256)
				}
			}
			back := strings.NewReplacer(pairs...) // the new hashes put back
			models := 0
			for file, text := range after {
				if file == "README.md" {
					continue
				}
				var current map[string]any
				if json.Unmarshal([]byte(text), &current) == nil && current["modelspec"] == currentWords.identifier {
					models++
					var earlier map[string]any
					if err := json.Unmarshal([]byte(before[file]), &earlier); err != nil || !reflect.DeepEqual(earlierOf(current), earlier) {
						t.Fatalf("%s is not the earlier model renamed: %v", file, err)
					}
					continue
				}
				if back.Replace(text) != before[file] {
					t.Fatalf("%s differs from the earlier fixture beyond hashes", file)
				}
			}
			if models != 2 {
				t.Fatalf("%d models in the current vocabulary, want the target model and the source schema", models)
			}
			if after["README.md"] == "" {
				t.Fatal("README.md is missing")
			}
		})
	}
}
