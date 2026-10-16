package manifest

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// The shape of the concepts of a meaning file, as the Directory holds it (validateConcept in meaning.mjs 58-80, called by analyseDatabase at directory.mjs
// 708-712 with bindings asked for), and the rule that a concept is declared once. The Chinook checker never reads the concepts. What the
// bindings, extends and values-of of a concept say is another rule (the bindings against the model, the chains inside the graph).

// The two formats of a meaning file (meaninggraph/core, FORMAT.md and decisions 0001 to 0003), which decide how a binding names the member of a record type
// that it holds: `property:` in meaning/draft-1 and `field:` in meaning/draft-2. A file whose format is anything else, or none, is read as meaning/draft-1
// always was; the format itself is the business of the MeaningGraph checks and the registry, not of this one.
const (
	meaningDraft1 = "meaning/draft-1"
	meaningDraft2 = "meaning/draft-2"
)

// bindingRoles are the roles a binding can have. `instances` and `reference` are the current names of `entity` and `foreign-key`, and both pairs are valid
// in both formats: the current names are new spellings that no earlier file uses, so meaning/draft-1 gained them in place, and meaning/draft-2 accepts the
// earlier names (FORMAT.md, "Bindings" and "How draft 1 differs"). A binding that holds a record type and no member has the role instances, or entity.
var bindingRoles = []string{"entity", "instances", "identifier", "display-name", "foreign-key", "reference", "value"}

// holdsRecordType says whether a role is the one of a binding that names a record type and no member of it.
func holdsRecordType(role string) bool { return role == "entity" || role == "instances" }

// memberOf is how a binding of a file of this format names the member of a record type that it holds, and what a finding calls it.
func memberOf(draft2 bool) string {
	if draft2 {
		return "field"
	}
	return "property"
}

// isConceptID is the Directory's /^[a-z][a-z0-9]*(?:-[a-z][a-z0-9]*)*$/ (meaning.mjs conceptId): lower-case words joined by single hyphens, each
// starting with a letter.
func isConceptID(s string) bool {
	if s == "" {
		return false
	}
	startOfWord := true
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z':
			startOfWord = false
		case c >= '0' && c <= '9':
			if startOfWord {
				return false
			}
		case c == '-':
			if startOfWord {
				return false // a leading hyphen, or two in a row
			}
			startOfWord = true
		default:
			return false
		}
	}
	return !startOfWord // not a trailing hyphen
}

// jsSpace is what JavaScript's String.prototype.trim removes: the characters of the ECMAScript WhiteSpace and LineTerminator productions.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// utf16Len is the length of s in UTF-16 code units, as JavaScript's String.length counts it.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// plainLabel is the Directory's rule for a label: text that is not blank, of at most 200 characters (UTF-16 code units), with no control character
// (U+0000 to U+001F, U+007F), no < and no >.
func plainLabel(n *Node) bool {
	if n.Kind != kindString || strings.TrimFunc(n.Text, jsSpace) == "" || utf16Len(n.Text) > 200 {
		return false
	}
	return !strings.ContainsFunc(n.Text, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '<' || r == '>' })
}

// concepts judges each concept of the list by its shape and holds the ids to being unique: a concept that has a shape problem is not counted, as the
// Directory does not.
func (c *collector) concepts(list *Node, w MeaningWants, draft2 bool) {
	seen := map[string]bool{}
	var good []*Node // the concepts with a good shape, declared once, in the order of the file
	var ids []string
	byID := map[string]*Node{}
	for position, concept := range list.Items {
		if concept.Kind != kindMap || concept.Field("id") == nil || concept.Field("id").Kind != kindString {
			c.add("meaning-concept", concept.Line, "concept #%d has no id: each concept is a mapping with an id of text, got %s", position+1, describe(concept))
			continue
		}
		id := concept.Field("id").Text
		if !isConceptID(id) {
			c.add("meaning-concept", concept.Field("id").Line, "concept id %s must be lower-case words joined by single hyphens (each word starts with a letter)", rules.Quote(id))
			continue
		}
		switch {
		case c.conceptShape(concept, id, draft2):
		case seen[id]:
			c.add("meaning-concept-duplicate", concept.Field("id").Line, "concept %s is declared twice", id)
		default:
			seen[id] = true
			good, ids, byID[id] = append(good, concept), append(ids, id), concept
			c.bindings(concept, id, w, draft2)
		}
	}
	c.chains(good, ids, byID, w)
}

// conceptShape reports the shape problems of a concept whose id is good and says whether it found any.
func (c *collector) conceptShape(concept *Node, id string, draft2 bool) bool {
	found := false
	problem := func(line int, format string, args ...any) {
		found = true
		c.add("meaning-concept", line, "concept %s: %s", id, fmt.Sprintf(format, args...))
	}
	if labels := concept.Field("labels"); labels != nil {
		if labels.Kind != kindMap {
			problem(labels.Line, "labels must map language codes to labels, got %s", describe(labels))
		} else {
			for _, language := range labels.Keys {
				if label := labels.Fields[language]; !plainLabel(label) {
					problem(label.Line, "the %s label must be a plain string of at most 200 characters (no control characters, < or >), got %s", rules.Quote(language), describe(label))
				}
			}
		}
	}
	for _, key := range []string{"extends", "values-of"} {
		if ref := concept.Field(key); ref != nil && ref.Kind != kindString {
			problem(ref.Line, "%s must be a concept reference (text), got %s", key, describe(ref))
		}
	}
	if bindings := concept.Field("bindings"); bindings != nil {
		switch {
		case bindings.Kind != kindSeq:
			problem(bindings.Line, "bindings must be a list, got %s", describe(bindings))
		case slices.ContainsFunc(bindings.Items, func(b *Node) bool { return b.Kind != kindMap }):
			problem(bindings.Line, "every binding must be a mapping with model, role and %s", memberOf(draft2))
		default:
			for _, binding := range bindings.Items {
				if role := binding.Field("role"); role == nil || role.Kind != kindString || !slices.Contains(bindingRoles, role.Text) {
					problem(fieldLine(binding, "role"), "binding role %s must be one of %s", describe(role), strings.Join(bindingRoles, ", "))
				}
				// A file uses the word property in one sense only, and its format says which: a meaning/draft-2 file writes field.
				if draft2 {
					if word := binding.Field("property"); word != nil {
						problem(word.Line, "binding has the key property, which belongs to %s; in %s it is written field", meaningDraft1, meaningDraft2)
					}
				}
			}
		}
	}
	return found
}

// aRecordType is how a finding calls a record type of the model: an entity in a meaning/draft-1 file, a record type in a meaning/draft-2 one.
func aRecordType(draft2 bool) string {
	if draft2 {
		return "a record type"
	}
	return "an entity"
}

// modelRef is the Directory's parseModelRef (modelspec.mjs 14): modelspec://{host}/{org}/{repo}/{module}.{Entity}, with the repository and its slash left out for
// the model of the same repository (modelspec:///{module}.{Entity}), and an optional ?ref=. The module and the entity start with a letter.
var modelRef = regexp.MustCompile(`^modelspec://((?:[A-Za-z0-9.-]+(?:/[A-Za-z0-9._-]+)+)?)/([A-Za-z][A-Za-z0-9_]*)\.([A-Za-z][A-Za-z0-9_]*)(?:\?ref=([A-Za-z0-9._/-]+))?$`)

// bindings holds the bindings of a concept to the model (directory.mjs 728-754): each names an entity of this database's own ModelSpec and, unless its role
// is entity, a property of it. The concept has a good shape (every binding is a mapping with a role of the list). Nothing is held when the model file is
// not one the Directory reads.
func (c *collector) bindings(concept *Node, id string, w MeaningWants, draft2 bool) {
	list := concept.Field("bindings")
	if list == nil || list.Kind != kindSeq || w.Model == nil || w.Module == "" {
		return
	}
	for _, binding := range list.Items {
		problem := func(line int, format string, args ...any) {
			c.add("meaning-binding", line, "concept %s: %s", id, fmt.Sprintf(format, args...))
		}
		model := binding.Field("model")
		var ref []string
		if model != nil && model.Kind == kindString {
			ref = modelRef.FindStringSubmatch(model.Text)
		}
		switch {
		case ref == nil:
			problem(fieldLine(binding, "model"), "binding model %s is not a modelspec:///{module}.{Entity} reference (the module and the entity name in a reference start with a letter, so an entity or module whose name starts with _ cannot be bound)", describe(model))
			continue
		case ref[1] != "":
			problem(model.Line, "binding %s names a model outside this database; bindings name this repository's own ModelSpec", model.Text)
			continue
		case ref[2] != w.Module:
			problem(model.Line, "binding %s names module %s, but the ModelSpec is module %s", model.Text, ref[2], w.Module)
			continue
		}
		properties, ok := w.Model.Entities[ref[3]]
		if !ok {
			problem(model.Line, "binding %s names %s that is not in the ModelSpec", model.Text, aRecordType(draft2))
			continue
		}
		member := memberOf(draft2)
		property := binding.Field(member)
		role := binding.Field("role").Text
		switch {
		case property == nil && !holdsRecordType(role):
			problem(binding.Line, "binding %s with role %s must name a %s", model.Text, role, member)
		case property != nil:
			if _, has := properties[property.Text]; property.Kind != kindString || !has {
				problem(property.Line, "binding %s names %s %s, which %s does not have in the ModelSpec", model.Text, member, describe(property), ref[3])
			}
		}
	}
}
