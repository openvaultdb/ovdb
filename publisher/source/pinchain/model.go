package pinchain

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"text/scanner"

	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
)

type property struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Pattern  string `json:"pattern"`
}
type entity struct {
	Properties map[string]property `json:"properties"`
	Key        []string            `json:"key"`
}

// readHCL accepts only the exact literal entity/property subset authored in this
// baseline. Expressions, new attributes and duplicate declarations fail closed;
// it is not a general ModelSpec parser and does not compete with publisher PRs.
func readHCL(data []byte) (map[string]entity, error) {
	var cleaned strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			cleaned.WriteString(line)
			cleaned.WriteByte('\n')
		}
	}
	var s scanner.Scanner
	s.Init(strings.NewReader(cleaned.String()))
	s.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.SkipComments | scanner.ScanComments
	failed := false
	s.Error = func(*scanner.Scanner, string) { failed = true }
	next := func() string {
		if s.Scan() == scanner.EOF {
			return ""
		}
		return s.TokenText()
	}
	want := func(value string) bool { return next() == value }
	quoted := func() (string, error) { return strconv.Unquote(next()) }
	bad := func() (map[string]entity, error) { return nil, fmt.Errorf("unsupported or malformed baseline HCL") }
	entities := map[string]entity{}
	for token := next(); token != ""; token = next() {
		if token != "entity" {
			return bad()
		}
		name, err := quoted()
		if err != nil || name == "" || entities[name].Properties != nil || !want("{") {
			return bad()
		}
		e := entity{Properties: map[string]property{}}
		for token = next(); token != "}"; token = next() {
			if token != "property" {
				return bad()
			}
			field, err := quoted()
			if err != nil || field == "" || e.Properties[field].Type != "" || !want("{") {
				return bad()
			}
			p := property{}
			seen := map[string]bool{}
			for token = next(); token != "}"; token = next() {
				if seen[token] || !want("=") {
					return bad()
				}
				seen[token] = true
				switch token {
				case "type":
					p.Type, err = quoted()
				case "pattern":
					p.Pattern, err = quoted()
				case "required":
					value := next()
					if value != "true" && value != "false" {
						return bad()
					}
					p.Required = value == "true"
				default:
					return bad()
				}
				if err != nil {
					return bad()
				}
			}
			if len(seen) != 3 {
				return bad()
			}
			e.Properties[field] = p
		}
		entities[name] = e
	}
	if failed {
		return bad()
	}
	return entities, nil
}
func validateModels(hclData, jsonData []byte) error {
	hcl, err := readHCL(hclData)
	if err != nil {
		return err
	}
	var model struct {
		Modelspec string `json:"modelspec"`
		Module    struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"module"`
		Entities map[string]entity `json:"entities"`
	}
	if err = datarights.Decode(jsonData, &model); err != nil {
		return fmt.Errorf("invalid baseline ModelSpec JSON")
	}
	if model.Modelspec != "1.0-draft" || model.Module.Name != "ecb" || model.Module.ID != "github.com/openvaultdb/ovdb/publisher/source/model/ecb" || model.Module.Version != "0.1.0" || !reflect.DeepEqual(hcl, model.Entities) {
		return fmt.Errorf("HCL/JSON model equivalence drift")
	}
	expected := map[string]property{"time": {"string", true, "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}, "currency": {"string", true, "^[A-Z]{3}$"}, "rate": {"string", true, "^([0-9]+)([.][0-9]+)?$"}}
	if len(hcl) != 1 || !reflect.DeepEqual(hcl["FxReferenceQuote"].Properties, expected) {
		return fmt.Errorf("native required string properties drift")
	}
	return nil
}
