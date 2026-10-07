package manifest

import (
	"slices"
	"strings"
	"testing"
)

func usableFact(s string) Fact[string] {
	return Fact[string]{Present: true, Valid: true, Value: s, Line: 3}
}

func TestMeaning(t *testing.T) {
	const good = "id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts: []\n"
	wants := MeaningWants{File: "model/chinook.meaning.yaml", GraphID: usableFact("chinook"), Licence: usableFact("CC0-1.0"), Module: "chinook", ModelHCL: "model/chinook.modelspec.hcl"}
	entry := func(e string) string { return strings.Replace(good, "chinook.modelspec.hcl", e, 1) }
	for name, c := range map[string]struct {
		doc  string
		edit func(w *MeaningWants)
		rule string // the rule of the one finding, or "" for none
		line int
	}{
		"good":                 {doc: good},
		"empty":                {doc: "", rule: "meaning-shape", line: 1},
		"only comments":        {doc: "# c\n", rule: "meaning-shape", line: 1},
		"a list":               {doc: "- a\n", rule: "meaning-shape", line: 1},
		"not YAML":             {doc: "a: [\n", rule: "yaml"},
		"a reader refusal":     {doc: good + "x: &a 1\n", rule: "yaml-anchor"},
		"too big":              {doc: good + "# " + strings.Repeat("x", MaxDocumentBytes), rule: "document-size"},
		"another id":           {doc: strings.Replace(good, "id: chinook", "id: other", 1), rule: "meaning-id", line: 1},
		"no id":                {doc: strings.Replace(good, "id: chinook\n", "", 1), rule: "meaning-id", line: 1},
		"a number for id":      {doc: strings.Replace(good, "id: chinook", "id: 5", 1), rule: "meaning-id", line: 1},
		"id in another case":   {doc: strings.Replace(good, "id: chinook", "id: Chinook", 1), rule: "meaning-id", line: 1},
		"id not compared":      {doc: strings.Replace(good, "id: chinook", "id: other", 1), edit: func(w *MeaningWants) { w.GraphID = Fact[string]{Present: true} }},
		"another license":      {doc: strings.Replace(good, "CC0-1.0", "MIT", 1), rule: "meaning-license", line: 2},
		"no license":           {doc: strings.Replace(good, "license: CC0-1.0\n", "", 1), rule: "meaning-license", line: 1},
		"license not compared": {doc: strings.Replace(good, "CC0-1.0", "MIT", 1), edit: func(w *MeaningWants) { w.Licence = Fact[string]{} }},
		"no models":            {doc: "id: chinook\nlicense: CC0-1.0\nconcepts: []\n", rule: "meaning-models", line: 1},
		// The Directory refuses a meaning file whose concepts are not a list (directory.mjs 487); the line is the key's, or the mapping's when it is missing.
		"no concepts":                                {doc: strings.Replace(good, "concepts: []\n", "", 1), rule: "meaning-concepts", line: 1},
		"concepts null":                              {doc: strings.Replace(good, "concepts: []", "concepts:", 1), rule: "meaning-concepts", line: 5},
		"concepts a mapping":                         {doc: strings.Replace(good, "concepts: []", "concepts: {}", 1), rule: "meaning-concepts", line: 5},
		"concepts text":                              {doc: strings.Replace(good, "concepts: []", "concepts: none", 1), rule: "meaning-concepts", line: 5},
		"concepts listed":                            {doc: strings.Replace(good, "concepts: []", "concepts:\n  - id: a", 1)},
		"models a list":                              {doc: "id: chinook\nlicense: CC0-1.0\nmodels:\n  - a\nconcepts: []\n", rule: "meaning-models", line: 4},
		"models for another module":                  {doc: strings.Replace(good, "  chinook:", "  other:", 1), rule: "meaning-models", line: 4},
		"the entry a number":                         {doc: entry("5"), rule: "meaning-models", line: 4},
		"the entry blank":                            {doc: entry("' '"), rule: "meaning-models", line: 4},
		"the entry another file":                     {doc: entry("other.modelspec.hcl"), rule: "meaning-hcl", line: 4},
		"the entry with ./":                          {doc: entry("./chinook.modelspec.hcl")},
		"the entry through ..":                       {doc: entry("../model/chinook.modelspec.hcl")},
		"the entry through a dir":                    {doc: entry("x/../chinook.modelspec.hcl")},
		"the entry ending in /.":                     {doc: entry("chinook.modelspec.hcl/.")},
		"the entry with a slash":                     {doc: entry("chinook.modelspec.hcl/"), rule: "meaning-models", line: 4},
		"the entry with /./ at end":                  {doc: entry("chinook.modelspec.hcl/./"), rule: "meaning-models", line: 4},
		"the entry with //":                          {doc: entry(".//chinook.modelspec.hcl"), rule: "meaning-models", line: 4},
		"the entry with a leading /":                 {doc: entry("/model/chinook.modelspec.hcl"), rule: "meaning-models", line: 4},
		"the entry leaving the repo":                 {doc: entry("../../chinook.modelspec.hcl"), rule: "meaning-models", line: 4},
		"the entry is ..":                            {doc: entry(".."), rule: "meaning-models", line: 4},
		"the entry is .":                             {doc: entry("."), rule: "meaning-hcl", line: 4},
		"the entry with a space":                     {doc: entry("'a b'"), rule: "meaning-models", line: 4},
		"the entry with a glob":                      {doc: entry("'*.hcl'"), rule: "meaning-models", line: 4},
		"the entry in a flow mapping":                {doc: "id: chinook\nlicense: CC0-1.0\nmodels: {chinook: chinook.modelspec.hcl}\nconcepts: []\n"},
		"no module known":                            {doc: "id: chinook\nlicense: CC0-1.0\nconcepts: []\n", edit: func(w *MeaningWants) { w.Module = "" }},
		"no hcl that is a file":                      {doc: "id: chinook\nlicense: CC0-1.0\nconcepts: []\n", edit: func(w *MeaningWants) { w.ModelHCL = "" }},
		"the file in the top directory":              {doc: entry("chinook.modelspec.hcl"), edit: func(w *MeaningWants) { w.File = "m.yaml"; w.ModelHCL = "chinook.modelspec.hcl" }},
		"the file in the top directory, an entry up": {doc: entry("../x.hcl"), edit: func(w *MeaningWants) { w.File = "m.yaml"; w.ModelHCL = "x.hcl" }, rule: "meaning-models", line: 4},
	} {
		w := wants
		if c.edit != nil {
			c.edit(&w)
		}
		j, _ := NewJudge(Publisher)
		findings := j.Meaning([]byte(c.doc), w)
		if c.rule == "" {
			if len(findings) != 0 {
				t.Errorf("%s: findings %v", name, findings)
			}
			continue
		}
		if len(findings) != 1 || findings[0].Rule != c.rule || findings[0].Document != w.File || (c.line != 0 && findings[0].Line != c.line) || len(findings[0].Message) > MaxMessageBytes {
			t.Errorf("%s: findings %+v, want one of %s at %d", name, findings, c.rule, c.line)
		}
	}
}

// A number is not the text that it prints as: meaning.graph.id 5 is written as a string in the manifest and is a number in the file.
func TestMeaningIdIsTextAndNotANumber(t *testing.T) {
	w := MeaningWants{File: "m.yaml", GraphID: usableFact("5")}
	j, _ := NewJudge(Publisher)
	if got := j.Meaning([]byte("id: 5\nconcepts: []\n"), w); len(got) != 1 || got[0].Rule != "meaning-id" || !strings.Contains(got[0].Message, "\"5\" (a number, not text)") {
		t.Errorf("a number: %v", got)
	}
	if got := j.Meaning([]byte("id: '5'\nconcepts: []\n"), w); len(got) != 0 {
		t.Errorf("text: %v", got)
	}
}

func TestMeaningSharesTheBudget(t *testing.T) {
	j, _ := NewJudge(Directory)
	for range MaxFindings {
		j.Report("x", "r", 0, "m")
	}
	if got := j.Meaning([]byte("- a\n"), MeaningWants{File: "m.yaml"}); len(got) != 0 {
		t.Errorf("findings past the budget: %v", got)
	}
	if n := j.Notice("OVDB.md"); len(n) != 1 {
		t.Errorf("notice %v", n)
	}
}

// entrySpelling is /^[A-Za-z0-9_.\/-]+$/ with no leading slash, trailing slash or empty segment: each character of the set is accepted, and the empty
// entry and each character outside it is not.
func TestEntrySpelling(t *testing.T) {
	for _, s := range []string{"a", "Z", "0", "9", "_", "-", ".", "a/b", "a-b", "a_b", "a.b", "d-d_d.d9/../x.hcl", "A-Za-z0-9_.-"} {
		if !entrySpelling(s) {
			t.Errorf("%q is refused", s)
		}
	}
	for _, s := range []string{"", "/", "/a", "a/", "a//b", "a b", "a*", "a\\b", "a:b", "a@b", "a~b", "a+b", "a\x00b", "\u00e9", "a`b", "a{", "a[", "a\u00ff"} {
		if entrySpelling(s) {
			t.Errorf("%q is accepted", s)
		}
	}
	// The characters next to the ranges of letters and digits are not letters and digits.
	for _, c := range []byte("/:@[`{") {
		if c != '/' && entrySpelling("a"+string(c)) {
			t.Errorf("%q is accepted", c)
		}
	}
}

// The shape of the concepts, as the Directory's validateConcept holds it (meaning.mjs 58-80), and the rule that a concept is declared once.
func TestMeaningConcepts(t *testing.T) {
	const head = "id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts:\n"
	wants := MeaningWants{File: "m.yaml", GraphID: usableFact("chinook"), Licence: usableFact("CC0-1.0"), Module: "chinook", ModelHCL: "chinook.modelspec.hcl"}
	long := strings.Repeat("a", 200)
	for name, c := range map[string]struct {
		concepts string
		rules    []string // the rules of the findings, in order; none when the concepts are good
		text     string   // in the first message
	}{
		"good":                                    {concepts: "  - id: artist\n    labels:\n      en: Artist\n    extends: album\n    values-of: genre\n    bindings:\n      - model: x\n        role: entity\n  - id: a1-b2\n"},
		"a label of 200":                          {concepts: "  - id: a\n    labels: {en: " + long + "}\n"},
		"astral letters count two":                {concepts: "  - id: a\n    labels: {en: \"" + strings.Repeat("\U0001F600", 100) + "\"}\n"},
		"astral letters over 200":                 {concepts: "  - id: a\n    labels: {en: \"" + strings.Repeat("\U0001F600", 101) + "\"}\n", rules: []string{"meaning-concept"}, text: "the \"en\" label must be a plain string"},
		"a label of 201":                          {concepts: "  - id: a\n    labels: {en: " + long + "a}\n", rules: []string{"meaning-concept"}, text: "plain string of at most 200"},
		"a blank label":                           {concepts: "  - id: a\n    labels: {en: \"\\u00a0 \\ufeff\"}\n", rules: []string{"meaning-concept"}, text: "plain string"},
		"a label with a no-break space and text":  {concepts: "  - id: a\n    labels: {en: \"\\u00a0x\"}\n"},
		"a label with a control":                  {concepts: "  - id: a\n    labels: {en: \"a\\x01b\"}\n", rules: []string{"meaning-concept"}, text: "plain string"},
		"a label with DEL":                        {concepts: "  - id: a\n    labels: {en: \"a\\x7fb\"}\n", rules: []string{"meaning-concept"}, text: "plain string"},
		"a label with <":                          {concepts: "  - id: a\n    labels: {en: \"a<b\"}\n", rules: []string{"meaning-concept"}, text: "plain string"},
		"a label with >":                          {concepts: "  - id: a\n    labels: {en: \"a>b\"}\n", rules: []string{"meaning-concept"}, text: "plain string"},
		"a label that is a number":                {concepts: "  - id: a\n    labels: {en: 5}\n", rules: []string{"meaning-concept"}, text: "plain string"},
		"labels a list":                           {concepts: "  - id: a\n    labels: [x]\n", rules: []string{"meaning-concept"}, text: "labels must map language codes"},
		"labels null":                             {concepts: "  - id: a\n    labels:\n", rules: []string{"meaning-concept"}, text: "labels must map language codes"},
		"extends a number":                        {concepts: "  - id: a\n    extends: 5\n", rules: []string{"meaning-concept"}, text: "extends must be a concept reference"},
		"values-of null":                          {concepts: "  - id: a\n    values-of:\n", rules: []string{"meaning-concept"}, text: "values-of must be a concept reference"},
		"bindings text":                           {concepts: "  - id: a\n    bindings: x\n", rules: []string{"meaning-concept"}, text: "bindings must be a list"},
		"bindings null":                           {concepts: "  - id: a\n    bindings:\n", rules: []string{"meaning-concept"}, text: "bindings must be a list"},
		"a binding that is text":                  {concepts: "  - id: a\n    bindings:\n      - x\n", rules: []string{"meaning-concept"}, text: "every binding must be a mapping"},
		"a role that is not listed":               {concepts: "  - id: a\n    bindings:\n      - role: nope\n", rules: []string{"meaning-concept"}, text: "binding role \"nope\" must be one of"},
		"a role that is a number":                 {concepts: "  - id: a\n    bindings:\n      - role: 5\n", rules: []string{"meaning-concept"}, text: "binding role"},
		"no role":                                 {concepts: "  - id: a\n    bindings:\n      - model: x\n", rules: []string{"meaning-concept"}, text: "binding role nothing"},
		"every problem of a concept":              {concepts: "  - id: a\n    labels: x\n    extends: 1\n    bindings: y\n", rules: []string{"meaning-concept", "meaning-concept", "meaning-concept"}, text: "labels must map"},
		"a number":                                {concepts: "  - 1\n", rules: []string{"meaning-concept"}, text: "concept #1 has no id"},
		"null":                                    {concepts: "  - null\n", rules: []string{"meaning-concept"}, text: "concept #1 has no id"},
		"a list":                                  {concepts: "  - [a]\n", rules: []string{"meaning-concept"}, text: "concept #1 has no id"},
		"no id":                                   {concepts: "  - labels: {en: A}\n", rules: []string{"meaning-concept"}, text: "concept #1 has no id"},
		"an id that is a number":                  {concepts: "  - id: 5\n", rules: []string{"meaning-concept"}, text: "concept #1 has no id"},
		"a second concept with no id":             {concepts: "  - id: a\n  - x: 1\n", rules: []string{"meaning-concept"}, text: "concept #2 has no id"},
		"an id in capitals":                       {concepts: "  - id: Artist\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id with an underscore":                {concepts: "  - id: art_ist\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id with a leading hyphen":             {concepts: "  - id: \"-a\"\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id with a trailing hyphen":            {concepts: "  - id: a-\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id with a double hyphen":              {concepts: "  - id: a--b\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id with a word starting with a digit": {concepts: "  - id: a-1b\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id that starts with a digit":          {concepts: "  - id: 1a\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an empty id":                             {concepts: "  - id: ''\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"an id with a space":                      {concepts: "  - id: a b\n", rules: []string{"meaning-concept"}, text: "lower-case words"},
		"a concept declared twice":                {concepts: "  - id: a\n  - id: b\n  - id: a\n", rules: []string{"meaning-concept-duplicate"}, text: "concept a is declared twice"},
		"a concept with a problem is not counted": {concepts: "  - id: a\n    extends: 1\n  - id: a\n", rules: []string{"meaning-concept"}, text: "extends must be"},
	} {
		j, _ := NewJudge(Publisher)
		findings := j.Meaning([]byte(head+c.concepts), wants)
		var got []string
		for _, f := range findings {
			got = append(got, f.Rule)
		}
		if !slices.Equal(got, c.rules) || (len(findings) > 0 && !strings.Contains(findings[0].Message, c.text)) {
			t.Errorf("%s: findings %+v, want rules %v with %q", name, findings, c.rules, c.text)
		}
	}
}

// The bindings of a concept, held to the model (directory.mjs 728-754): what the Directory reads when it has read the model file.
func TestMeaningBindings(t *testing.T) {
	const head = "id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts:\n  - id: artist\n    bindings:\n"
	model := &ModelFacts{Entities: map[string]map[string]struct{}{"Artist": {"ArtistId": {}, "Name": {}}, "Album": {"AlbumId": {}}}}
	wants := MeaningWants{File: "m.yaml", GraphID: usableFact("chinook"), Licence: usableFact("CC0-1.0"), Module: "chinook", ModelHCL: "chinook.modelspec.hcl", Model: model}
	for name, c := range map[string]struct {
		binding string
		edit    func(w *MeaningWants)
		text    string // in the message of the one finding, or "" for none
	}{
		"an entity":                    {binding: "      - model: modelspec:///chinook.Artist\n        role: entity\n"},
		"a property":                   {binding: "      - model: modelspec:///chinook.Artist\n        property: Name\n        role: display-name\n"},
		"a pin on the same model":      {binding: "      - model: modelspec:///chinook.Artist?ref=abc\n        role: entity\n"},
		"a model that is text":         {binding: "      - model: chinook.Artist\n        role: entity\n", text: "is not a modelspec:///{module}.{Entity} reference"},
		"a model that is missing":      {binding: "      - role: entity\n", text: "binding model nothing"},
		"a model that is a number":     {binding: "      - model: 5\n        role: entity\n", text: "is not a modelspec:///"},
		"a module with an underscore":  {binding: "      - model: modelspec:///_chinook.Artist\n        role: entity\n", text: "is not a modelspec:///"},
		"an entity with an underscore": {binding: "      - model: modelspec:///chinook._Artist\n        role: entity\n", text: "is not a modelspec:///"},
		"another repository":           {binding: "      - model: modelspec://github.com/o/r/chinook.Artist\n        role: entity\n", text: "names a model outside this database"},
		"another module":               {binding: "      - model: modelspec:///other.Artist\n        role: entity\n", text: "names module other, but the ModelSpec is module chinook"},
		"an entity the model lacks":    {binding: "      - model: modelspec:///chinook.Nope\n        role: entity\n", text: "names an entity that is not in the ModelSpec"},
		"a role with no property":      {binding: "      - model: modelspec:///chinook.Artist\n        role: identifier\n", text: "with role identifier must name a property"},
		"a property the entity lacks":  {binding: "      - model: modelspec:///chinook.Artist\n        property: Nope\n        role: identifier\n", text: "which Artist does not have in the ModelSpec"},
		"a property of another entity": {binding: "      - model: modelspec:///chinook.Artist\n        property: AlbumId\n        role: identifier\n", text: "names property \"AlbumId\", which Artist does not have"},
		"a property that is a number":  {binding: "      - model: modelspec:///chinook.Artist\n        property: 5\n        role: identifier\n", text: "which Artist does not have"},
		"a property that is null":      {binding: "      - model: modelspec:///chinook.Artist\n        property:\n        role: identifier\n", text: "which Artist does not have"},
		"no model facts":               {binding: "      - model: modelspec:///chinook.Nope\n        role: entity\n", edit: func(w *MeaningWants) { w.Model = nil }},
		"no module":                    {binding: "      - model: modelspec:///chinook.Nope\n        role: entity\n", edit: func(w *MeaningWants) { w.Module = "" }},
	} {
		w := wants
		if c.edit != nil {
			c.edit(&w)
		}
		j, _ := NewJudge(Publisher)
		findings := j.Meaning([]byte(head+c.binding), w)
		if c.text == "" {
			if len(findings) != 0 {
				t.Errorf("%s: findings %+v", name, findings)
			}
			continue
		}
		if len(findings) != 1 || findings[0].Rule != "meaning-binding" || !strings.Contains(findings[0].Message, c.text) {
			t.Errorf("%s: findings %+v, want one of meaning-binding with %q", name, findings, c.text)
		}
	}
	// A concept with a bad shape, or declared twice, is not read for its bindings; a concept with no bindings has none to read.
	j, _ := NewJudge(Publisher)
	doc := "id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts:\n  - id: a\n    extends: 5\n    bindings:\n      - model: x\n        role: entity\n  - id: b\n  - id: b\n    bindings:\n      - model: x\n        role: entity\n"
	var rules []string
	for _, f := range j.Meaning([]byte(doc), wants) {
		rules = append(rules, f.Rule)
	}
	if !slices.Equal(rules, []string{"meaning-concept", "meaning-concept-duplicate"}) {
		t.Errorf("rules %v", rules)
	}
}
