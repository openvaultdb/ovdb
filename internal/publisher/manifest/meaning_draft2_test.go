package manifest

import (
	"strings"
	"testing"
)

// The bindings of a concept in the two formats of a meaning file (meaninggraph/core, FORMAT.md and decisions 0001 to 0003): the roles instances and
// reference are the current names of entity and foreign-key, and all four are valid in both formats; a binding of a meaning/draft-1 file names the member
// of the record type by property:, one of a meaning/draft-2 file by field:, and a word of the other format is not read.

const (
	meaningHead = "id: chinook\nlicense: CC0-1.0\nmodels:\n  chinook: chinook.modelspec.hcl\nconcepts:\n  - id: artist\n    bindings:\n"
	artistModel = "      - model: modelspec:///chinook.Artist\n"
)

func judgeMeaning(format, bindings string) []Finding {
	model := &ModelFacts{Entities: map[string]map[string]struct{}{"Artist": {"ArtistId": {}, "Name": {}}}}
	w := MeaningWants{File: "m.yaml", GraphID: usableFact("chinook"), Licence: usableFact("CC0-1.0"), Module: "chinook", ModelHCL: "chinook.modelspec.hcl", Model: model}
	j, _ := NewJudge(Publisher)
	return j.Meaning([]byte(format+meaningHead+bindings), w)
}

func TestTheRolesOfABindingInBothFormats(t *testing.T) {
	for _, format := range []string{"", "format: meaning/draft-1\n", "format: meaning/draft-2\n", "format: meaning/draft-3\n"} {
		member := "property"
		if format == "format: meaning/draft-2\n" {
			member = "field"
		}
		for _, c := range []struct {
			name, binding, text string
		}{
			{"the earlier name of instances", artistModel + "        role: entity\n", ""},
			{"instances", artistModel + "        role: instances\n", ""},
			{"the earlier name of reference", artistModel + "        " + member + ": ArtistId\n        role: foreign-key\n", ""},
			{"reference", artistModel + "        " + member + ": ArtistId\n        role: reference\n", ""},
			{"an identifier", artistModel + "        " + member + ": ArtistId\n        role: identifier\n", ""},
			{"a display name", artistModel + "        " + member + ": Name\n        role: display-name\n", ""},
			{"a value", artistModel + "        " + member + ": Name\n        role: value\n", ""},
			{"a reference with no member", artistModel + "        role: reference\n", "with role reference must name a " + member},
			{"a member the record type lacks", artistModel + "        " + member + ": Nope\n        role: reference\n", "names " + member + " \"Nope\", which Artist does not have in the ModelSpec"},
			{"a role that is no role", artistModel + "        role: instance\n", "binding role \"instance\" must be one of entity, instances, identifier, display-name, foreign-key, reference, value"},
		} {
			findings := judgeMeaning(format, c.binding)
			if c.text == "" {
				if len(findings) != 0 {
					t.Errorf("%q, %s: findings %v", format, c.name, findings)
				}
				continue
			}
			if len(findings) != 1 || !strings.Contains(findings[0].Message, c.text) {
				t.Errorf("%q, %s: findings %v, want %q", format, c.name, findings, c.text)
			}
		}
	}
}

func TestAWordOfTheOtherFormatIsNotRead(t *testing.T) {
	// meaning/draft-2: property: is a word of meaning/draft-1. A concept with a shape problem is not read for its bindings, so the member it names is not
	// read as a field, and the word is refused alone, whichever the role.
	for _, role := range []string{"identifier", "instances"} {
		findings := judgeMeaning("format: meaning/draft-2\n", artistModel+"        property: ArtistId\n        role: "+role+"\n")
		if len(findings) != 1 || findings[0].Rule != "meaning-concept" || findings[0].Message != "concept artist: binding has the key property, which belongs to meaning/draft-1; in meaning/draft-2 it is written field" {
			t.Errorf("%s: findings %v", role, findings)
		}
	}
	var findings []Finding
	// meaning/draft-1 and a file with no format: field: is not read (as before, a key that is not read is not an error here), so the member is missing.
	for _, format := range []string{"", "format: meaning/draft-1\n"} {
		findings = judgeMeaning(format, artistModel+"        field: ArtistId\n        role: identifier\n")
		if len(findings) != 1 || findings[0].Rule != "meaning-binding" || !strings.Contains(findings[0].Message, "with role identifier must name a property") {
			t.Errorf("%q: findings %v", format, findings)
		}
	}
}

func TestTheWordsOfAFindingAboutAMeaningDraft2File(t *testing.T) {
	for _, c := range []struct {
		format, binding, text string
	}{
		{"format: meaning/draft-2\n", "      - role: instances\n      - x\n", "every binding must be a mapping with model, role and field"},
		{"format: meaning/draft-1\n", "      - role: instances\n      - x\n", "every binding must be a mapping with model, role and property"},
		{"format: meaning/draft-2\n", "      - model: modelspec:///chinook.Nope\n        role: instances\n", "names a record type that is not in the ModelSpec"},
		{"format: meaning/draft-1\n", "      - model: modelspec:///chinook.Nope\n        role: instances\n", "names an entity that is not in the ModelSpec"},
		{"format: meaning/draft-2\n", artistModel + "        role: identifier\n", "with role identifier must name a field"},
		{"format: meaning/draft-2\n", artistModel + "        field: 5\n        role: identifier\n", "names field \"5\" (a number, not text), which Artist does not have"},
	} {
		findings := judgeMeaning(c.format, c.binding)
		if len(findings) == 0 || !strings.Contains(findings[0].Message, c.text) {
			t.Errorf("%q: findings %v, want %q", c.format, findings, c.text)
		}
	}
	// A format that is not a text is read as the earlier one.
	if findings := judgeMeaning("format: [meaning/draft-2]\n", artistModel+"        property: ArtistId\n        role: identifier\n"); len(findings) != 0 {
		t.Errorf("findings %v", findings)
	}
}
