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
	errRecords   = errors.New("it has too many records")
)

// The two identifiers of the JSON form of a model, which decide its vocabulary (ModelSpec's spec/json-format.md, "Format Identity" and "The
// 1.0-draft Vocabulary"): the current one reads records, fields and record, the earlier one entities, properties and entity. A document with
// any other identifier, or none, is read in the earlier vocabulary, as before.
const (
	identifierCurrent = "1.0-draft-2"
	identifierEarlier = "1.0-draft"
)

// spelling is one vocabulary of the JSON form of a model: the keys it uses and the words a finding uses for them. The keys of the other vocabulary
// are named too, because a document that carries one of them under the identifier of this one is refused (RuleModelVocabulary).
type spelling struct {
	group, members, ref                string // the top-level key of the record types, the key of their members, and the key of a member that references a record type
	otherGroup, otherMembers, otherRef string // the same three keys of the other vocabulary
	record, member                     string // what a record type and a member are called in a finding
	memberPlural, kinds                string // the plural of member, and of record
	aRef                               string // a record type, with its article, as a member references it
	limit                              error  // the error of a group of more than MaxEntities record types
}

var (
	earlier = spelling{
		group: "entities", members: "properties", ref: "entity",
		otherGroup: "records", otherMembers: "fields", otherRef: "record",
		record: "entity", member: "property", memberPlural: "properties", kinds: "entities", aRef: "an entity", limit: errEntities,
	}
	current = spelling{
		group: "records", members: "fields", ref: "record",
		otherGroup: "entities", otherMembers: "properties", otherRef: "entity",
		record: "record", member: "field", memberPlural: "fields", kinds: "record types", aRef: "a record", limit: errRecords,
	}
)

// modelSpec is what the checker reads of a model file (its lines 408-416): the name of the module and the names of the entities. The Directory reads
// more of it (parseModelSpec, modelspec.mjs 92-118): the version, the names of entities and properties, the properties and their types and references;
// what it refuses of those is in issues. The names entities and properties are kept for what the earlier vocabulary calls them: a model in the current
// vocabulary has its records in entities and their fields in properties, and spelling says which words a finding uses.
type modelSpec struct {
	module      string   // module.name when it is a ModelSpec module name (a letter, then letters, digits and _), else ""
	hasEntities bool     // the group of record types (entities, or records) is an object
	entities    []string // its keys, sorted
	set         map[string]struct{}
	issues      []modelIssue                   // what parseModelSpec refuses beyond the module and the entities object, in the order of entity and property names
	size        int                            // about what this reading holds, in bytes: the names of the entities and of the properties, and the issues
	properties  map[string]map[string]struct{} // for each entity, the names of its properties as the Directory reads them (the bindings of a concept name them)
	spelling    spelling                       // the vocabulary the document is read in
}

// maxModelIssues is the most issues kept of one model file (see judgeEntities).
const maxModelIssues = 1000

// modelIssue is one refusal of parseModelSpec: the rule that reports it and the text.
type modelIssue struct{ rule, text string }

// entityInfo is what is kept of one entity of the model file, as the last member of its name has it (JSON.parse keeps the last of a repeated key): only what
// can be refused, so that a file of 4 MiB of valid properties keeps nothing.
type entityInfo struct {
	badName bool                // the name is not an identifier: the Directory refuses it and reads nothing of the entity
	noProps bool                // no properties object with a property in it
	props   map[string]propInfo // the properties that are refused, or that reference an entity (judged when every entity is known)
	names   map[string]struct{} // the names of the properties that are not refused (the last of a repeated name)
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

// groupRead is what is read of one top-level group of record types, the one that a vocabulary names: entities or records. A document is read for both
// at once, because its identifier may come after them, and the group of the vocabulary that the identifier names is the one that is judged.
type groupRead struct {
	present      bool                   // the key is in the document, whatever its value
	object       bool                   // the last value of the key is an object
	keys         map[string]*entityInfo // its record types by name, the last of a repeated name
	otherMembers bool                   // a record type of it has the key of the members of the other vocabulary
	otherRef     bool                   // a member of one has the reference key of the other vocabulary
}

// refKeys are the reference keys that members of the components have (a component has fields in both vocabularies, so only the reference key tells them
// apart).
type refKeys struct{ entity, record bool }

func (k refKeys) has(key string) bool {
	return key == "entity" && k.entity || key == "record" && k.record
}

// readModel reads a model file as JSON.parse does and nothing more: the value must be one JSON value and an object (a repeated key is the last of
// them, as JSON.parse has it; a string with a byte that is not UTF-8 or half of a surrogate pair is read, as it is there; a number is not
// converted, so 1e999 is read; a byte order mark is not white space, in either). It takes no more than maxJSONDepth levels. The decoder is
// used for its tokens only, which it checks as it reads them: no value is built that the file chooses the size of but the keys of entities.
//
// The identifier of the document ("modelspec") decides the vocabulary it is read in: "1.0-draft-2" has records, fields and record, and anything else the
// entities, properties and entity that this check has always read (see spelling). A document under either identifier that carries a key of the other
// vocabulary, or a removed or reserved top-level key, is refused (RuleModelVocabulary, RuleModelRemoved), and so is a document under any other identifier
// that carries a key of the current vocabulary (RuleModelVersion). A key is noticed wherever it occurs, also in a repeated key that a later one overwrites.
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
	var name, version string
	var versioned bool
	var earlierRead, currentRead groupRead
	var components refKeys
	var withheld []string // the removed and reserved top-level keys the document has
	err = members(dec, func(key string, tok json.Token) error {
		switch key {
		case "modelspec":
			version, versioned = tok.(string) // typeof doc.modelspec === 'string'; the last of a repeated key
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
			return readGroup(dec, tok, earlier, &earlierRead)
		case "records":
			return readGroup(dec, tok, current, &currentRead)
		case "components":
			return readComponents(dec, tok, &components)
		case "collections", "recordsets", "migrations", "projections":
			// The top-level keys of the constructs that ModelSpec removed (collections, recordsets) and reserved with no content (migrations, projections): decision
			// 0019 of ModelSpec, and its spec/json-format.md, "Removed And Reserved Fields". A document that has one is refused under either identifier (RuleModelRemoved).
			withheld = append(withheld, key)
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
	words, group, other := earlier, earlierRead, currentRead
	if version == identifierCurrent {
		words, group, other = current, currentRead, earlierRead
	}
	spec.spelling, spec.hasEntities = words, group.object
	keys := group.keys
	spec.set = make(map[string]struct{}, len(keys))
	for key := range keys {
		spec.entities = append(spec.entities, key)
		spec.set[key] = struct{}{}
	}
	slices.Sort(spec.entities)
	if !versioned {
		spec.issues = append(spec.issues, modelIssue{RuleModelVersion, `has no "modelspec" version (a text, such as "1.0-draft")`})
	}
	if versioned && version != identifierCurrent && version != identifierEarlier {
		// An identifier that is neither is read in the earlier vocabulary, as it always was. A document that has a key of the current vocabulary under it is
		// not in the earlier one, and reading it so would say that it has no entities, which sends its author to the wrong key.
		var current []string
		if other.present {
			current = append(current, words.otherGroup)
		}
		if group.otherMembers {
			current = append(current, words.otherMembers)
		}
		if group.otherRef || components.has(words.otherRef) {
			current = append(current, words.otherRef)
		}
		if len(current) > 0 {
			spec.issues = append(spec.issues, modelIssue{RuleModelVersion, `"modelspec" is ` + rules.Quote(version) + ", which this check does not know: it reads " + rules.Quote(identifierEarlier) + " (entities, properties, entity) and " + rules.Quote(identifierCurrent) + " (records, fields, record), and the model has " + names(current) + ", which belong to " + rules.Quote(identifierCurrent) + ": write that identifier if the model is in that vocabulary"})
		}
	}
	if version == identifierCurrent || version == identifierEarlier {
		var stray []string // the keys of the other vocabulary, in the order of the keys of a spelling
		if other.present {
			stray = append(stray, words.otherGroup)
		}
		if group.otherMembers {
			stray = append(stray, words.otherMembers)
		}
		if group.otherRef || components.has(words.otherRef) {
			stray = append(stray, words.otherRef)
		}
		if len(stray) > 0 {
			spec.issues = append(spec.issues, modelIssue{RuleModelVocabulary, `"modelspec" is ` + rules.Quote(version) + ", so the model must use the keys " + words.group + ", " + words.members + " and " + words.ref + "; it has " + names(stray) + ", which belong to the other version of the format (a model uses the keys of the version it names)"})
		}
	}
	slices.Sort(withheld)
	for _, word := range slices.Compact(withheld) {
		text := "has " + rules.Quote(word) + ", which ModelSpec removed"
		switch word {
		case "collections":
			text += ": a stored set of rows is described with the database that holds it, not in the model"
		case "recordsets":
			text += ": the shape of a query result or a view is " + words.aRef + " with no key"
		default:
			text = "has " + rules.Quote(word) + ", a word that ModelSpec reserves and has given no content: a model cannot use it"
		}
		spec.issues = append(spec.issues, modelIssue{RuleModelRemoved, text})
	}
	if spec.hasEntities {
		spec.judgeEntities(keys)
		spec.properties = make(map[string]map[string]struct{}, len(keys))
		for name, info := range keys {
			spec.size += len(name) + 64
			if info != nil {
				spec.properties[name] = info.names
				for prop := range info.names {
					spec.size += len(prop) + 48
				}
			}
		}
		for _, issue := range spec.issues {
			spec.size += len(issue.text) + 32
		}
	}
	return spec, nil
}

// readGroup reads the value of a top-level group of record types, whose first token is first, in the vocabulary words: the record types are read one by
// one (see readEntity), and no more than MaxEntities of them. A repeated group is the last of them, as in JSON.parse.
func readGroup(dec *json.Decoder, first json.Token, words spelling, group *groupRead) error {
	group.present, group.object, group.keys = true, false, map[string]*entityInfo{}
	if open, ok := first.(json.Delim); !ok || open != '{' {
		return consume(dec, first, 1)
	}
	group.object = true
	return members(dec, func(key string, tok json.Token) error {
		group.keys[key] = nil
		if len(group.keys) > MaxEntities {
			return words.limit
		}
		info, err := readEntity(dec, tok, key, words, group)
		group.keys[key] = info
		return err
	})
}

// readComponents reads the value of the top-level components, whose first token is first, for the reference keys of the members of its components. Nothing
// else of a component is judged here: the check has never read the components.
func readComponents(dec *json.Decoder, first json.Token, found *refKeys) error {
	if open, ok := first.(json.Delim); !ok || open != '{' {
		return consume(dec, first, 1)
	}
	return members(dec, func(_ string, tok json.Token) error { // a component, level 3
		if open, ok := tok.(json.Delim); !ok || open != '{' {
			return consume(dec, tok, 2)
		}
		return members(dec, func(key string, tok json.Token) error {
			if open, ok := tok.(json.Delim); key != "fields" || !ok || open != '{' {
				return consume(dec, tok, 3)
			}
			return members(dec, func(_ string, tok json.Token) error { // a field of it, level 5
				if open, ok := tok.(json.Delim); !ok || open != '{' {
					return consume(dec, tok, 4)
				}
				return members(dec, func(key string, tok json.Token) error {
					found.entity = found.entity || key == "entity"
					found.record = found.record || key == "record"
					return consume(dec, tok, 5)
				})
			})
		})
	})
}

// judgeEntities turns what was kept of the entities into issues, the entities and their properties by name. The Directory reads an entity whose name is
// an identifier (the others it refuses), and refuses a reference to an entity that the model does not have by name, whatever that entity is like. A model in the
// current vocabulary is judged the same, and the findings call its entities records and its properties fields.
//
// It keeps at most maxModelIssues issues: the findings of a check are capped far below that (manifest.MaxFindings), a model file is refused as soon as it has
// one, and what is kept of a reading is held for every manifest that names the file, so what a hostile file can make it keep must not grow with the file.
func (spec *modelSpec) judgeEntities(keys map[string]*entityInfo) {
	words := spec.spelling
	for _, name := range spec.entities {
		if len(spec.issues) >= maxModelIssues {
			return
		}
		info := keys[name]
		switch {
		case info.badName:
			spec.issues = append(spec.issues, modelIssue{RuleModelEntity, words.record + " name " + rules.Quote(name) + " must be an identifier (letters, digits and _, not starting with a digit)"})
			continue
		case info.noProps:
			spec.issues = append(spec.issues, modelIssue{RuleModelEntity, words.record + " " + name + " has no " + words.memberPlural + " (an object with at least one " + words.member + ")"})
			continue
		}
		props := make([]string, 0, len(info.props))
		for prop := range info.props {
			props = append(props, prop)
		}
		slices.Sort(props)
		for _, prop := range props {
			if len(spec.issues) >= maxModelIssues {
				return
			}
			p := info.props[prop]
			switch at := name + "." + prop; p.kind {
			case propBadName:
				spec.issues = append(spec.issues, modelIssue{RuleModelProperty, words.member + " name " + rules.Quote(at) + " must be an identifier (letters, digits and _, not starting with a digit)"})
			case propBadType:
				spec.issues = append(spec.issues, modelIssue{RuleModelProperty, at + " has type " + rules.Quote(p.text) + ", which is not a type name (a word, optionally a list: string, int, datetime[])"})
			case propNeither:
				spec.issues = append(spec.issues, modelIssue{RuleModelProperty, at + " has neither a type nor " + words.aRef})
			case propReference:
				if _, ok := spec.set[p.text]; !ok {
					spec.issues = append(spec.issues, modelIssue{RuleModelProperty, at + " references " + words.record + " " + p.text + ", which the model does not have"})
				}
			}
		}
	}
}

// readEntity reads the value of the entity called name, whose first token is first, inside the entities object (level 2), and keeps what the
// Directory refuses of it. An entity whose name is not an identifier is read for its depth only. The members are under the key that the vocabulary words
// names; the key of the other vocabulary is noted in group and not read.
func readEntity(dec *json.Decoder, first json.Token, name string, words spelling, group *groupRead) (*entityInfo, error) {
	if !isIdentifier(name) {
		return &entityInfo{badName: true}, consume(dec, first, 2)
	}
	info := &entityInfo{noProps: true}
	if open, ok := first.(json.Delim); !ok || open != '{' {
		return info, consume(dec, first, 2) // null, text, a number or a list: no properties
	}
	err := members(dec, func(key string, tok json.Token) error {
		if key == words.otherMembers {
			group.otherMembers = true
		}
		if key != words.members {
			return consume(dec, tok, 3)
		}
		info.props, info.names, info.noProps = nil, nil, true // the last of a repeated properties
		if open, ok := tok.(json.Delim); !ok || open != '{' {
			return consume(dec, tok, 3)
		}
		count := 0
		return members(dec, func(prop string, tok json.Token) error {
			count++
			info.noProps = false
			delete(info.props, prop) // the last of a repeated property
			delete(info.names, prop)
			judged, kept, err := readProperty(dec, tok, prop, words, group)
			if !kept || judged.kind == propReference {
				if info.names == nil {
					info.names = map[string]struct{}{}
				}
				info.names[prop] = struct{}{}
			}
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
// property that is a reference, the entity it names, which is judged when every entity is known. The reference is under the key that the vocabulary
// words names; the key of the other vocabulary is noted in group and not read.
func readProperty(dec *json.Decoder, first json.Token, name string, words spelling, group *groupRead) (propInfo, bool, error) {
	var typ, entity string
	var hasType, hasEntity bool
	if open, ok := first.(json.Delim); ok && open == '{' {
		err := members(dec, func(key string, tok json.Token) error {
			switch key {
			case "type":
				typ, hasType = tok.(string)
			case words.ref:
				entity, hasEntity = tok.(string)
			case words.otherRef:
				group.otherRef = true
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
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' && (i == 0 || c < '0' || c > '9') {
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
