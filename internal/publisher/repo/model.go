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
	errNotObject    = errors.New("it must be a JSON object")
	errTrailing     = errors.New("there is text after the JSON value")
	errDepth        = errors.New("it is nested too deep")
	errEntities     = errors.New("it has too many entities")
	errRecords      = errors.New("it has too many records")
	errDeclarations = errors.New("it has too many components or enums")
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
	module                     string   // module.name when it is a ModelSpec module name (a letter, then letters, digits and _), else ""
	hasEntities                bool     // the group of record types (entities, or records) is an object
	entities                   []string // its keys, sorted
	set                        map[string]struct{}
	issues                     []modelIssue                   // what parseModelSpec refuses beyond the module and the entities object, in the order of entity and property names
	size                       int                            // about what this reading holds, in bytes: the names of the entities and of the properties, and the issues
	properties                 map[string]map[string]struct{} // for each entity, the names of its properties as the Directory reads them (the bindings of a concept name them)
	spelling                   spelling                       // the vocabulary the document is read in
	source                     []byte                         // original immutable JSON bytes, including constraints and authored order
	records, components, enums map[string]*entityInfo         // distinct source declaration origins
	inherited                  map[string]map[string]string   // effective field -> component source origin, own fields stay in records
	componentVocabulary        bool
	incomplete                 map[string][]modelDiagnostic // field capability is independent of source validity
}

// maxModelIssues is the most issues kept of one model file (see judgeEntities).
const maxModelIssues = 1000

// modelIssue is one refusal of parseModelSpec: the rule that reports it and the text.
type modelIssue struct{ rule, text string }

// entityInfo is what is kept of one entity of the model file, as the last member of its name has it (JSON.parse keeps the last of a repeated key): only what
// can be refused, so that a file of 4 MiB of valid properties keeps nothing.
type entityInfo struct {
	badName   bool // not a source concept name
	badObject bool // declaration is not an object
	badUse    bool
	use       []string
	noProps   bool                // an authored fields/properties value is not an object
	props     map[string]propInfo // the properties that are refused, or that reference an entity (judged when every entity is known)
	names     map[string]struct{} // the names of the properties that are not refused (the last of a repeated name)
}

type propKind int

const (
	propBadName propKind = iota
	propBadType
	propNeither
	propReference
	propComponent
	propEnum
	propShape
)

type propInfo struct {
	kind          propKind
	text          string // the authored type/reference
	enum          string // optional enum reference, kept independently
	badEnum       bool
	enumReference bool
}

// groupRead is what is read of one top-level group of record types, the one that a vocabulary names: entities or records. A document is read for both
// at once, because its identifier may come after them, and the group of the vocabulary that the identifier names is the one that is judged.
type groupRead struct {
	present      bool                   // the key is in the document, whatever its value
	object       bool                   // the last value of the key is an object
	keys         map[string]*entityInfo // its record types by name, the last of a repeated name
	otherMembers bool                   // a record type of it has the key of the members of the other vocabulary
	otherRef     bool                   // a member of one has the reference key of the other vocabulary
	ownRef       bool
}

// refKeys are the reference keys that members of the components have (a component has fields in both vocabularies, so only the reference key tells them
// apart).
type refKeys struct{ entity, record bool }

func (k refKeys) has(key string) bool {
	return key == "entity" && k.entity || key == "record" && k.record
}

// readModel reads source JSON with the existing bounded token reader: the value must be one JSON value and an object (a repeated key is the last of
// them, as JSON.parse has it; a string with a byte that is not UTF-8 or half of a surrogate pair is read, as it is there; a number is not
// converted, so 1e999 is read; a byte order mark is not white space, in either). It takes no more than maxJSONDepth levels. The decoder is
// used for its tokens, retaining bounded declaration names, authored references/uses and the original source bytes; arbitrary constraints are not copied into a second AST.
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
	var componentGroup, enumGroup groupRead
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
			return readComponents(dec, tok, &components, &componentGroup)
		case "enums":
			return readEnums(dec, tok, &enumGroup)
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
	if rules.ModelSourceName(name) {
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
	spec.source = data
	spec.records, spec.components, spec.enums = keys, componentGroup.keys, enumGroup.keys
	spec.componentVocabulary = componentGroup.otherMembers
	if componentGroup.present && !componentGroup.object {
		spec.issues = append(spec.issues, modelIssue{RuleModelEntity, "components must be an object"})
	}
	if enumGroup.present && !enumGroup.object {
		spec.issues = append(spec.issues, modelIssue{RuleModelEntity, "enums must be an object"})
	}
	spec.size += len(data)
	spec.judgeSource()
	spec.composeFields()

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
// readComponents reads actual declarations while retaining both reference spellings
// for vocabulary checks even in JSON members later overwritten by duplicate keys.
func readComponents(dec *json.Decoder, first json.Token, found *refKeys, group *groupRead) error {
	words := earlier
	words.members, words.otherMembers, words.ref = "fields", "properties", "*"
	words.limit = errDeclarations
	err := readGroup(dec, first, words, group)
	found.entity = found.entity || group.ownRef
	found.record = found.record || group.otherRef
	return err
}

func readEnums(dec *json.Decoder, first json.Token, group *groupRead) error {
	group.present, group.object, group.keys = true, false, map[string]*entityInfo{}
	if open, ok := first.(json.Delim); !ok || open != '{' {
		return consume(dec, first, 1)
	}
	group.object = true
	return members(dec, func(name string, tok json.Token) error {
		info := &entityInfo{badName: !rules.ModelConceptName(name)}
		if open, ok := tok.(json.Delim); !ok || open != '{' {
			info.badObject = true
		}
		group.keys[name] = info
		if len(group.keys) > MaxEntities {
			return errDeclarations
		}
		return consume(dec, tok, 2)
	})
}

// readEntity retains source field names/references and record uses. Component
// values are declarations; this reader never recursively expands their values.
func readEntity(dec *json.Decoder, first json.Token, name string, words spelling, group *groupRead) (*entityInfo, error) {
	info := &entityInfo{badName: !rules.ModelConceptName(name)}
	if open, ok := first.(json.Delim); !ok || open != '{' {
		info.badObject = true
		return info, consume(dec, first, 2)
	}
	err := members(dec, func(key string, tok json.Token) error {
		if key == words.otherMembers {
			group.otherMembers = true
		}
		if key == "use" {
			return readUses(dec, tok, info)
		}
		if key != words.members {
			return consume(dec, tok, 3)
		}
		info.props, info.names, info.noProps = nil, nil, false
		if open, ok := tok.(json.Delim); !ok || open != '{' {
			info.noProps = true
			return consume(dec, tok, 3)
		}
		return members(dec, func(prop string, tok json.Token) error {
			delete(info.props, prop)
			delete(info.names, prop)
			judged, kept, err := readProperty(dec, tok, prop, words, group)
			if !kept || judged.kind == propReference || judged.kind == propComponent || judged.kind == propEnum {
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

func readUses(dec *json.Decoder, first json.Token, info *entityInfo) error {
	info.use, info.badUse = nil, false
	if open, ok := first.(json.Delim); !ok || open != '[' {
		info.badUse = true
		return consume(dec, first, 3)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if ref, ok := tok.(string); ok {
			info.use = append(info.use, ref)
		} else {
			info.badUse = true
		}
		if err := consume(dec, tok, 4); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

func readProperty(dec *json.Decoder, first json.Token, name string, words spelling, group *groupRead) (propInfo, bool, error) {
	var typ, entity, component, enum string
	var hasType, hasEntity, hasComponent, hasEnum, enumString, enumInline bool
	var typeWritten, entityWritten, componentWritten bool
	if open, ok := first.(json.Delim); ok && open == '{' {
		err := members(dec, func(key string, tok json.Token) error {
			switch key {
			case "type":
				typeWritten = true
				typ, hasType = tok.(string)
			case "entity", "record":
				group.ownRef = group.ownRef || key == words.ref || words.ref == "*" && key == "entity"
				group.otherRef = group.otherRef || key == words.otherRef
				if key == words.ref || words.ref == "*" {
					entityWritten = true
					entity, hasEntity = tok.(string)
				}
			case "component":
				componentWritten = true
				component, hasComponent = tok.(string)
			case "enum":
				hasEnum = true
				enum, enumString = tok.(string)
				enumInline = tok == json.Delim('[')
			}
			return consume(dec, tok, 5)
		})
		if err != nil {
			return propInfo{}, false, err
		}
	} else if err := consume(dec, first, 4); err != nil {
		return propInfo{}, false, err
	}
	if !rules.ModelSourceName(name) {
		return propInfo{kind: propBadName}, true, nil
	}
	count := 0
	for _, present := range []bool{typeWritten, entityWritten, componentWritten} {
		if present {
			count++
		}
	}
	if count > 1 {
		return propInfo{kind: propShape}, true, nil
	}
	if hasType && !isTypeName(typ) {
		return propInfo{kind: propBadType, text: typ}, true, nil
	}
	if !hasType && !hasEntity && !hasComponent {
		return propInfo{kind: propNeither}, true, nil
	}
	info := propInfo{enum: enum, enumReference: hasEnum && enumString, badEnum: hasEnum && !enumString && !enumInline}
	switch {
	case hasEntity:
		info.kind, info.text = propReference, entity
	case hasComponent:
		info.kind, info.text = propComponent, component
	case hasEnum && enumString || hasEnum && !enumInline:
		info.kind, info.text = propEnum, typ
	default:
		return propInfo{}, false, nil
	}
	return info, true, nil
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
