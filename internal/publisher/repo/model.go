package repo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
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

// modelSpec is what the checker reads of a model file (its lines 408-416): the name of the module and the names of the entities.
type modelSpec struct {
	module      string   // module.name when it is a ModelSpec module name (a letter, then letters, digits and _), else ""
	hasEntities bool     // entities is an object
	entities    []string // its keys, sorted
	set         map[string]struct{}
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
	keys := map[string]bool{}
	err = members(dec, func(key string, tok json.Token) error {
		switch key {
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
			spec.hasEntities, keys = false, map[string]bool{}
			if open, ok := tok.(json.Delim); ok && open == '{' {
				spec.hasEntities = true
				return members(dec, func(key string, tok json.Token) error {
					keys[key] = true
					if len(keys) > MaxEntities {
						return errEntities
					}
					return consume(dec, tok, 2)
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
	return spec, nil
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
