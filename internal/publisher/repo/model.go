package repo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// maxJSONDepth is the deepest nesting of arrays and objects that a model file may have, the top object being the first level. JSON.parse, which
// the checker uses, has no bound; this one is recorded as a stricter kind (the model file's JSON is read by a recursive function, and a file
// of at most MaxFileBytes can nest 2 million levels).
const maxJSONDepth = 100

var (
	errNotObject = errors.New("it must be a JSON object")
	errTrailing  = errors.New("there is text after the JSON value")
	errDepth     = errors.New("it is nested too deep")
	errEntities  = errors.New("it has too many entities")
)

// modelSpec is what the checker reads of a model file (its lines 408-416): the name of the module and the names of the entities. The Directory reads
// more of it (parseModelSpec, modelspec.mjs 92-118): the version, the names of entities and properties, the properties and their types and references;
// what it refuses of those is in issues.
type modelSpec struct {
	module      string   // module.name when it is a ModelSpec module name (a letter, then letters, digits and _), else ""
	hasEntities bool     // entities is an object
	entities    []string // its keys, sorted
	set         map[string]struct{}
	issues      []modelIssue // what parseModelSpec refuses beyond the module and the entities object, in the order of entity and property names
}

// modelIssue is one refusal of parseModelSpec: the rule that reports it and the text.
type modelIssue struct{ rule, text string }

// entityInfo is what is kept of one entity of the model file, as the last member of its name has it (JSON.parse keeps the last of a repeated key): only what
// can be refused, so that a file of 4 MiB of valid properties keeps nothing.
type entityInfo struct {
	badName bool                // the name is not an identifier: the Directory refuses it and reads nothing of the entity
	noProps bool                // no properties object with a property in it
	props   map[string]propInfo // the properties that are refused, or that reference an entity (judged when every entity is known)
}

type propKind int

const (
	propBadName propKind = iota
	propBadType
	propNeither
	propReference
)

type propInfo struct {
	kind propKind
	text string // the type, or the entity that is referenced
}

// readModel reads a model file as JSON.parse does and nothing more: the value must be one JSON value and an object (a repeated key is the last of
// them, as JSON.parse has it; a string with a byte that is not UTF-8 or half of a surrogate pair is read, as it is there; a number is not
// converted, so 1e999 is read; a byte order mark is not white space, in either). It takes no more than maxJSONDepth levels. The decoder is
// used for its tokens only, which it checks as it reads them: no value is built that the file chooses the size of but the keys of entities.
func readModel(data []byte) (modelSpec, error) {
	var spec modelSpec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil {
		return spec, err
	}
	if open, ok := first.(json.Delim); !ok || open != '{' {
		return spec, errNotObject
	}
	var name string
	var versioned bool
	keys := map[string]*entityInfo{}
	err = members(dec, func(key string, tok json.Token) error {
		switch key {
		case "modelspec":
			_, versioned = tok.(string) // typeof doc.modelspec === 'string'; the last of a repeated key
		case "module":
			name = ""
			if open, ok := tok.(json.Delim); ok && open == '{' {
				return members(dec, func(key string, tok json.Token) error {
					if key == "name" {
						name = ""
						if s, ok := tok.(string); ok {
							name = s
							return nil
						}
					}
					return consume(dec, tok, 2)
				})
			}
		case "entities":
			spec.hasEntities, keys = false, map[string]*entityInfo{}
			if open, ok := tok.(json.Delim); ok && open == '{' {
				spec.hasEntities = true
				return members(dec, func(key string, tok json.Token) error {
					keys[key] = nil
					if len(keys) > MaxEntities {
						return errEntities
					}
					info, err := readEntity(dec, tok, key)
					keys[key] = info
					return err
				})
			}
		}
		return consume(dec, tok, 1)
	})
	if err != nil {
		return spec, err
	}
	if _, err := dec.Token(); err == nil {
		return spec, errTrailing
	} else if err != io.EOF {
		return spec, err
	}
	if isModulePattern(name) {
		spec.module = name
	}
	spec.set = make(map[string]struct{}, len(keys))
	for key := range keys {
		spec.entities = append(spec.entities, key)
		spec.set[key] = struct{}{}
	}
	slices.Sort(spec.entities)
	if !versioned {
		spec.issues = append(spec.issues, modelIssue{RuleModelVersion, `has no "modelspec" version (a text, such as "1.0-draft")`})
	}
	if spec.hasEntities {
		spec.judgeEntities(keys)
	}
	return spec, nil
}

// judgeEntities turns what was kept of the entities into issues, the entities and their properties by name. The Directory reads an entity whose name is
// an identifier (the others it refuses), and refuses a reference to an entity that the model does not have by name, whatever that entity is like.
func (spec *modelSpec) judgeEntities(keys map[string]*entityInfo) {
	for _, name := range spec.entities {
		info := keys[name]
		switch {
		case info.badName:
			spec.issues = append(spec.issues, modelIssue{RuleModelEntity, "entity name " + rules.Quote(name) + " must be an identifier (letters, digits and _, not starting with a digit)"})
			continue
		case info.noProps:
			spec.issues = append(spec.issues, modelIssue{RuleModelEntity, "entity " + name + " has no properties (an object with at least one property)"})
			continue
		}
		props := make([]string, 0, len(info.props))
		for prop := range info.props {
			props = append(props, prop)
		}
		slices.Sort(props)
		for _, prop := range props {
			p := info.props[prop]
			switch at := name + "." + prop; p.kind {
			case propBadName:
				spec.issues = append(spec.issues, modelIssue{RuleModelProperty, "property name " + rules.Quote(at) + " must be an identifier (letters, digits and _, not starting with a digit)"})
			case propBadType:
				spec.issues = append(spec.issues, modelIssue{RuleModelProperty, at + " has type " + rules.Quote(p.text) + ", which is not a type name (a word, optionally a list: string, int, datetime[])"})
			case propNeither:
				spec.issues = append(spec.issues, modelIssue{RuleModelProperty, at + " has neither a type nor an entity"})
			case propReference:
				if _, ok := spec.set[p.text]; !ok {
					spec.issues = append(spec.issues, modelIssue{RuleModelProperty, at + " references entity " + p.text + ", which the model does not have"})
				}
			}
		}
	}
}

// readEntity reads the value of the entity called name, whose first token is first, inside the entities object (level 2), and keeps what the
// Directory refuses of it. An entity whose name is not an identifier is read for its depth only.
func readEntity(dec *json.Decoder, first json.Token, name string) (*entityInfo, error) {
	if !isIdentifier(name) {
		return &entityInfo{badName: true}, consume(dec, first, 2)
	}
	info := &entityInfo{noProps: true}
	if open, ok := first.(json.Delim); !ok || open != '{' {
		return info, consume(dec, first, 2) // null, text, a number or a list: no properties
	}
	err := members(dec, func(key string, tok json.Token) error {
		if key != "properties" {
			return consume(dec, tok, 3)
		}
		info.props, info.noProps = nil, true // the last of a repeated properties
		if open, ok := tok.(json.Delim); !ok || open != '{' {
			return consume(dec, tok, 3)
		}
		count := 0
		return members(dec, func(prop string, tok json.Token) error {
			count++
			info.noProps = false
			delete(info.props, prop) // the last of a repeated property
			judged, kept, err := readProperty(dec, tok, prop)
			if kept {
				if info.props == nil {
					info.props = map[string]propInfo{}
				}
				info.props[prop] = judged
			}
			return err
		})
	})
	return info, err
}

// readProperty reads the value of the property called name, whose first token is first, in the properties of an entity (level 4), and says what the
// Directory refuses of it, when it does: a name that is not an identifier, a type that is not a type name, neither a type nor an entity; and, for a
// property that is a reference, the entity it names, which is judged when every entity is known.
func readProperty(dec *json.Decoder, first json.Token, name string) (propInfo, bool, error) {
	var typ, entity string
	var hasType, hasEntity bool
	if open, ok := first.(json.Delim); ok && open == '{' {
		err := members(dec, func(key string, tok json.Token) error {
			switch key {
			case "type":
				typ, hasType = tok.(string)
			case "entity":
				entity, hasEntity = tok.(string)
			}
			return consume(dec, tok, 5)
		})
		if err != nil {
			return propInfo{}, false, err
		}
	} else if err := consume(dec, first, 4); err != nil {
		return propInfo{}, false, err
	}
	switch {
	case !isIdentifier(name):
		return propInfo{kind: propBadName}, true, nil
	case hasType && !isTypeName(typ):
		return propInfo{kind: propBadType, text: typ}, true, nil
	case hasType:
		return propInfo{}, false, nil
	case !hasEntity:
		return propInfo{kind: propNeither}, true, nil
	}
	return propInfo{kind: propReference, text: entity}, true, nil
}

// isIdentifier is the Directory's /^[A-Za-z_][A-Za-z0-9_]*$/ (modelspec.mjs identifierPattern).
func isIdentifier(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return s != ""
}

// isTypeName is the Directory's /^[A-Za-z][A-Za-z0-9_]*(?:\[\])?$/ (modelspec.mjs typePattern): a word, optionally a list.
func isTypeName(s string) bool {
	return isModulePattern(strings.TrimSuffix(s, "[]"))
}

// members reads the members of the object whose { was read, handing each key and the first token of its value to member, which
// reads the rest of the value.
func members(dec *json.Decoder, member func(key string, first json.Token) error) error {
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		first, err := dec.Token()
		if err != nil {
			return err
		}
		if err := member(key.(string), first); err != nil { // the decoder gives a string where a key must be
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// consume reads the rest of a value that begins with the token first, inside a container that is parent levels deep.
func consume(dec *json.Decoder, first json.Token, parent int) error {
	open, ok := first.(json.Delim)
	if !ok {
		return nil
	}
	if parent+1 > maxJSONDepth {
		return errDepth
	}
	if open == '{' {
		return members(dec, func(_ string, tok json.Token) error { return consume(dec, tok, parent+1) })
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if err := consume(dec, tok, parent+1); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// isModulePattern is the checker's /^[A-Za-z][A-Za-z0-9_]*$/.
func isModulePattern(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'; !letter && (i == 0 || c != '_' && (c < '0' || c > '9')) {
			return false
		}
	}
	return s != ""
}
