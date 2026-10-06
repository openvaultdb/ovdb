package manifest

import (
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
