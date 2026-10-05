package representation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf16"

	"gopkg.in/yaml.v3"
)

// exactKeys rejects aliases of the consumed protocol fields while permitting
// unrelated canonical fields. Dynamic entity/property names are never folded.
func exactKeys(object map[string]any, fields []string, closed bool) error {
	for key := range object {
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

func exactModel(data []byte, out any, native bool) error {
	var root map[string]any
	if err := strictJSON(data, MaxArtifactBytes, &root); err != nil {
		return err
	}
	if err := exactKeys(root, []string{"modelspec", "module", "entities"}, false); err != nil {
		return err
	}
	module, _ := root["module"].(map[string]any)
	if err := exactKeys(module, []string{"name"}, false); err != nil {
		return err
	}
	entities, _ := root["entities"].(map[string]any)
	for _, raw := range entities {
		entity, _ := raw.(map[string]any)
		entityFields := []string{"properties"}
		if native {
			entityFields = append(entityFields, "key")
		}
		if err := exactKeys(entity, entityFields, false); err != nil {
			return err
		}
		properties, _ := entity["properties"].(map[string]any)
		for _, raw := range properties {
			property, _ := raw.(map[string]any)
			propertyFields := []string{"type"}
			if native {
				propertyFields = append(propertyFields, "required")
			}
			if err := exactKeys(property, propertyFields, false); err != nil {
				return err
			}
		}
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
