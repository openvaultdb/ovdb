package representation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf16"

	"gopkg.in/yaml.v3"
)

// exactKeys rejects aliases of the consumed protocol fields while permitting
// unrelated canonical fields. Dynamic entity/property names are never folded.
func exactKeys(object map[string]any, fields []string, closed bool) error {
	for _, key := range slices.Sorted(maps.Keys(object)) { // in order, so that the refusal of several keys says the same every time
		if slices.Contains(fields, key) {
			continue
		}
		for _, field := range fields {
			if strings.EqualFold(key, field) {
				return fmt.Errorf("non-exact JSON field %q", key)
			}
		}
		if closed {
			return fmt.Errorf("unknown JSON field %q", key)
		}
	}
	return nil
}

// exactModel checks a referenced ModelSpec JSON document and decodes it into out, whose types name the keys of the earlier vocabulary (entities,
// properties): a document in the current vocabulary is decoded as the same document with those keys (asEarlier). Its identifier decides the vocabulary, and a
// document whose keys disagree with it is refused (wordsAgree). The keys a contract consumes are exact in either vocabulary; the rest is unrelated.
func exactModel(data []byte, out any, native bool) error {
	var root map[string]any
	if err := strictJSON(data, MaxArtifactBytes, &root); err != nil {
		return err
	}
	words, known := vocabularyOf(root)
	if known {
		if err := wordsAgree(root, words); err != nil {
			return err
		}
	}
	if err := exactKeys(root, []string{"modelspec", "module", words.records}, false); err != nil {
		return err
	}
	module, _ := root["module"].(map[string]any)
	if err := exactKeys(module, []string{"name"}, false); err != nil {
		return err
	}
	records, _ := root[words.records].(map[string]any)
	for _, raw := range records {
		record, _ := raw.(map[string]any)
		recordFields := []string{words.fields}
		if native {
			recordFields = append(recordFields, "key")
		}
		if err := exactKeys(record, recordFields, false); err != nil {
			return err
		}
		fields, _ := record[words.fields].(map[string]any)
		for _, raw := range fields {
			field, _ := raw.(map[string]any)
			fieldKeys := []string{"type"}
			if native {
				fieldKeys = append(fieldKeys, "required")
			}
			if err := exactKeys(field, fieldKeys, false); err != nil {
				return err
			}
		}
	}
	if words == currentWords {
		data, _ = json.Marshal(asEarlier(root)) // a value that was just decoded from JSON encodes
	}
	return json.Unmarshal(data, out)
}

func exactSnapshot(data []byte, out any) error {
	var root map[string]any
	if err := strictJSON(data, MaxArtifactBytes, &root); err != nil {
		return err
	}
	if err := exactKeys(root, []string{"generator", "artifacts"}, false); err != nil {
		return err
	}
	generator, _ := root["generator"].(map[string]any)
	if err := exactKeys(generator, []string{"repository", "revision"}, false); err != nil {
		return err
	}
	artifacts, _ := root["artifacts"].([]any)
	for _, raw := range artifacts {
		artifact, _ := raw.(map[string]any)
		if err := exactKeys(artifact, []string{"path", "sha256"}, false); err != nil {
			return err
		}
	}
	return json.Unmarshal(data, out)
}

func exactClosed(data []byte, out any, fields []string, rows bool) error {
	var root map[string]any
	if err := strictJSON(data, MaxArtifactBytes, &root); err != nil {
		return err
	}
	if err := exactKeys(root, fields, true); err != nil {
		return err
	}
	if rows {
		array, _ := root["rows"].([]any)
		for _, raw := range array {
			row, _ := raw.(map[string]any)
			if err := exactKeys(row, []string{"raw_label", "target_key"}, true); err != nil {
				return err
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

// scalarEscapes preserves every valid scalar, including U+FFFD, and rejects
// lone UTF16 surrogates before encoding/json can replace them with U+FFFD.
func scalarEscapes(data []byte) error {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return fmt.Errorf("incomplete JSON string escape")
		}
		if data[i] != 'u' {
			continue
		}
		value, err := hexEscape(data, i+1)
		if err != nil {
			return err
		}
		i += 4
		if !utf16.IsSurrogate(rune(value)) {
			continue
		}
		if value >= 0xdc00 || i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired JSON surrogate")
		}
		low, err := hexEscape(data, i+3)
		if err != nil {
			return err
		}
		if low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}
func hexEscape(data []byte, start int) (uint16, error) {
	if start+4 > len(data) {
		return 0, fmt.Errorf("incomplete JSON Unicode escape")
	}
	var value uint16
	for _, c := range data[start : start+4] {
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			value += uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			value += uint16(c - 'A' + 10)
		default:
			return 0, fmt.Errorf("invalid JSON Unicode escape")
		}
	}
	return value, nil
}

func singleYAML(data []byte, out any) error {
	if len(data) > MaxArtifactBytes {
		return fmt.Errorf("YAML byte limit exceeded")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("YAML requires exactly one document: %v", err)
	}
	return nil
}
