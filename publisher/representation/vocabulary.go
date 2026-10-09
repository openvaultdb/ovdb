package representation

import (
	"fmt"
	"maps"
)

// A ModelSpec JSON document that a contract refers to (a target model, a source schema) is read in either vocabulary of the format, and the
// identifier of the document says which: "1.0-draft" has entities, properties and entity; "1.0-draft-2" has records, fields and record. The contract
// itself is not in a vocabulary: its own fields (entity, property and the rest) keep their names and their meaning, and under "1.0-draft-2" the
// contract's entity names a record type and its property names a field.
//
// This is the same pair of vocabularies that internal/publisher/repo reads for the publisher's own model (model.go). That package imports this one, so
// this one cannot import it; the words are mirrored here, in this one place, and a test holds the two to the same strings.
type vocabulary struct {
	identifier string
	records    string // the top-level key of the record types
	fields     string // the key of the members of a record type
	record     string // the key of a member that references a record type
}

var (
	earlierWords = vocabulary{identifier: "1.0-draft", records: "entities", fields: "properties", record: "entity"}
	currentWords = vocabulary{identifier: "1.0-draft-2", records: "records", fields: "fields", record: "record"}
)

// withheldKeys are the top-level keys of the constructs that ModelSpec removed (collections, recordsets) or reserved with no content (migrations,
// projections). A document that has one is refused under either identifier.
var withheldKeys = []string{"collections", "recordsets", "projections", "migrations"}

// vocabularyOf is the vocabulary that the identifier of a document names. A document with any other identifier is read as the earlier one, as it always was
// (the caller refuses the identifier itself, so such a document never resolves); known says whether the identifier is one of the two.
func vocabularyOf(root map[string]any) (words vocabulary, known bool) {
	switch root["modelspec"] {
	case currentWords.identifier:
		return currentWords, true
	case earlierWords.identifier:
		return earlierWords, true
	}
	return earlierWords, false
}

// wordsAgree refuses a document whose keys disagree with its identifier: a removed or reserved top-level key, and a key of the other vocabulary at the
// three places where the vocabularies differ (the top-level group of record types, the members of a record type, and the reference of a member). Only
// objects are looked into. Like the Directory's reader, it does not look inside components, which a contract never reads.
func wordsAgree(root map[string]any, words vocabulary) error {
	for _, key := range withheldKeys {
		if _, ok := root[key]; ok {
			return fmt.Errorf("ModelSpec document has %q, which ModelSpec removed or reserved", key)
		}
	}
	foreign := currentWords
	if words == currentWords {
		foreign = earlierWords
	}
	wrong := func(where, key, own string) error {
		return fmt.Errorf("ModelSpec document says %q, so %s%q must be %q (it is a key of %q)", words.identifier, where, key, own, foreign.identifier)
	}
	if _, ok := root[foreign.records]; ok {
		return wrong("", foreign.records, words.records)
	}
	records, _ := root[words.records].(map[string]any)
	for name, raw := range records {
		record, _ := raw.(map[string]any)
		if _, ok := record[foreign.fields]; ok {
			return wrong(name+": ", foreign.fields, words.fields)
		}
		fields, _ := record[words.fields].(map[string]any)
		for member, raw := range fields {
			field, _ := raw.(map[string]any)
			if _, ok := field[foreign.record]; ok {
				return wrong(name+"."+member+": ", foreign.record, words.record)
			}
		}
	}
	return nil
}

// asEarlier is the document with the keys of the earlier vocabulary: the record types under entities and their fields under properties. The rest of the
// document is the same value, so that one set of types reads both vocabularies and a document in the current one is read exactly as the same document in
// the earlier one is. It is called only for a document that wordsAgree accepted, which has no key of the earlier vocabulary to collide with.
func asEarlier(root map[string]any) map[string]any {
	out := maps.Clone(root)
	records, present := out[currentWords.records]
	if !present {
		return out
	}
	delete(out, currentWords.records)
	if group, ok := records.(map[string]any); ok {
		renamed := make(map[string]any, len(group))
		for name, raw := range group {
			if record, ok := raw.(map[string]any); ok {
				record = maps.Clone(record)
				if fields, ok := record[currentWords.fields]; ok {
					delete(record, currentWords.fields)
					record[earlierWords.fields] = fields
				}
				raw = record
			}
			renamed[name] = raw
		}
		records = renamed
	}
	out[earlierWords.records] = records
	return out
}
