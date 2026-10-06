package pinchain

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/openvaultdb/ovdb/publisher/source"
	"gopkg.in/yaml.v3"
)

func validateChain(p pins, data map[string][]byte) error {
	byRole := map[string]Artifact{}
	for _, a := range p.Artifacts {
		byRole[a.Role] = a
	}
	modelPin := byRole["model-hcl"]
	for _, role := range []string{"model-json", "meaning", "descriptor"} {
		if byRole[role].Commit != modelPin.Commit || byRole[role].Repository != modelPin.Repository {
			return fmt.Errorf("OVDB artifacts must share one exact commit")
		}
	}
	for _, role := range []string{"modelspec-registry", "meaning-registry"} {
		var record map[string]any
		if err := yaml.Unmarshal(data[role], &record); err != nil {
			return fmt.Errorf("invalid %s record", role)
		}
		if record["status"] != "draft" || record["repository"] != "https://github.com/"+modelPin.Repository || record["commit"] != modelPin.Commit {
			return fmt.Errorf("%s OVDB commit/status drift", role)
		}
		if role == "modelspec-registry" {
			if record["module"] != "ecb" || record["source_file"] != modelPin.Path || record["json_file"] != byRole["model-json"].Path || record["licence"] != "CC0-1.0" {
				return fmt.Errorf("ModelSpec registry paths/licence drift")
			}
		} else {
			if !reflect.DeepEqual(record["meaning_files"], []any{byRole["meaning"].Path}) || !reflect.DeepEqual(record["model_files"], []any{modelPin.Path, byRole["model-json"].Path}) || record["meaning_licence"] != "CC0-1.0" || record["model_licence"] != "CC0-1.0" {
				return fmt.Errorf("MeaningGraph registry paths/licences drift")
			}
		}
	}
	if err := validateModels(data["model-hcl"], data["model-json"]); err != nil {
		return err
	}
	d, err := source.Parse(data["descriptor"])
	if err != nil {
		return err
	}
	r := d.Recordsets["FxReferenceQuote"]
	if len(d.Recordsets) != 1 || r.Decoder != p.DecoderVersion || r.Entity != "FxReferenceQuote" || r.Resource != "daily" || !reflect.DeepEqual(r.FieldMapping, map[string]string{"time": "time", "currency": "currency", "rate": "rate"}) || !reflect.DeepEqual(r.Context, map[string]string{"baseCurrency": "EUR", "rateKind": "indicative-reference"}) {
		return fmt.Errorf("descriptor model/context/decoder drift")
	}
	if err := validateMeaning(data["meaning"], data["core"], byRole["core"].Commit); err != nil {
		return err
	}
	if err := validateDecoder(p, data, byRole); err != nil {
		return err
	}
	var directory struct {
		Status          string   `yaml:"status"`
		Retention       string   `yaml:"retention"`
		ResourceURL     string   `yaml:"resource_url"`
		TermsURL        string   `yaml:"terms_url"`
		ModelSpecURL    string   `yaml:"modelspec_url"`
		MeaningGraphURL string   `yaml:"meaninggraph_url"`
		Blockers        []string `yaml:"activation_blockers"`
	}
	if err := yaml.Unmarshal(data["directory"], &directory); err != nil {
		return fmt.Errorf("invalid Directory record")
	}
	if directory.Status != "inactive" || directory.Retention != "none" || directory.ResourceURL != d.Resources["daily"].URL || directory.TermsURL != d.Rights.Declaration.URL || directory.ModelSpecURL != "https://modelspec.org/registry/models/ecb-daily/" || directory.MeaningGraphURL != "https://meaninggraph.io/graphs/ecb-daily/" || len(directory.Blockers) != 4 {
		return fmt.Errorf("directory state/link drift")
	}
	for i, b := range directory.Blockers {
		if !strings.HasPrefix(b, fmt.Sprintf("B%d: ", i+1)) {
			return fmt.Errorf("directory admission blocker drift")
		}
	}
	return nil
}

func validateMeaning(data, coreData []byte, commit string) error {
	type binding struct {
		Model    string
		Property string
		Role     string
	}
	type concept struct {
		ID       string
		Extends  string
		Bindings []binding
	}
	var meaning struct {
		Concepts []concept
		Models   map[string]string
	}
	var core struct{ Concepts []concept }
	if yaml.Unmarshal(data, &meaning) != nil || yaml.Unmarshal(coreData, &core) != nil {
		return fmt.Errorf("invalid meaning/core metadata")
	}
	if !reflect.DeepEqual(meaning.Models, map[string]string{"ecb": "ecb-daily.modelspec.hcl"}) || len(meaning.Concepts) != 5 {
		return fmt.Errorf("meaning model/concept drift")
	}
	parents := map[string]bool{}
	for _, c := range core.Concepts {
		parents[c.ID] = true
	}
	expected := map[string]string{"ecb-fx-reference-quote": "fx-reference-quote", "ecb-reference-date": "fx-reference-date", "ecb-base-currency": "fx-base-currency", "ecb-quote-currency": "fx-quote-currency", "ecb-indicative-reference-rate": "indicative-fx-reference-rate"}
	fields := map[string]string{"ecb-reference-date": "time", "ecb-quote-currency": "currency", "ecb-indicative-reference-rate": "rate"}
	seen := map[string]bool{}
	for _, c := range meaning.Concepts {
		parent, ok := expected[c.ID]
		if !ok || seen[c.ID] || !parents[parent] || c.Extends != "meaning://github.com/meaninggraph/core/"+parent+"?ref="+commit {
			return fmt.Errorf("core extends pin drift")
		}
		seen[c.ID] = true
		expectedBindings := []binding(nil)
		if c.ID == "ecb-fx-reference-quote" {
			expectedBindings = []binding{{Model: "modelspec:///ecb.FxReferenceQuote", Role: "entity"}}
		} else if field := fields[c.ID]; field != "" {
			expectedBindings = []binding{{Model: "modelspec:///ecb.FxReferenceQuote", Property: field, Role: "value"}}
		}
		if !reflect.DeepEqual(c.Bindings, expectedBindings) {
			return fmt.Errorf("native meaning binding drift")
		}
	}
	return nil
}

func validateDecoder(p pins, data map[string][]byte, roles map[string]Artifact) error {
	for _, role := range []string{"decoder-contract", "decoder-tests", "decoder-module"} {
		if roles[role].Commit != roles["decoder"].Commit || roles[role].Repository != roles["decoder"].Repository {
			return fmt.Errorf("decoder pins must share one commit")
		}
	}
	if p.DecoderVersion != "ecb-eurofxref/1" || p.DecoderModuleVersion != "v0.3.0" || !bytes.HasPrefix(data["decoder-module"], []byte("module github.com/dal-go/dalgo2http\n")) {
		return fmt.Errorf("decoder version/module drift")
	}
	contract, err := parser.ParseFile(token.NewFileSet(), "decoder.go", data["decoder-contract"], 0)
	if err != nil {
		return fmt.Errorf("invalid decoder contract")
	}
	versionOK := false
	ast.Inspect(contract, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if name.Name == "DecoderECBEuroFXRef" && i < len(spec.Values) {
				value, ok := spec.Values[i].(*ast.BasicLit)
				if ok {
					decoded, _ := strconv.Unquote(value.Value)
					versionOK = decoded == p.DecoderVersion
				}
			}
		}
		return true
	})
	implementation, err := parser.ParseFile(token.NewFileSet(), "ecb_xml.go", data["decoder"], 0)
	if err != nil {
		return fmt.Errorf("invalid decoder implementation")
	}
	fieldsOK := false
	ast.Inspect(implementation, func(n ast.Node) bool {
		literal, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		fields := map[string]string{}
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.BasicLit)
			if !ok {
				continue
			}
			name, _ := strconv.Unquote(key.Value)
			value, ok := pair.Value.(*ast.Ident)
			if ok {
				fields[name] = value.Name
			}
		}
		if len(literal.Elts) == 3 && reflect.DeepEqual(fields, map[string]string{"time": "date", "currency": "currency", "rate": "rate"}) {
			fieldsOK = true
		}
		return true
	})
	if !versionOK || !fieldsOK {
		return fmt.Errorf("decoder version/native output drift")
	}
	tests, err := parser.ParseFile(token.NewFileSet(), "ecb_xml_test.go", data["decoder-tests"], 0)
	if err != nil {
		return fmt.Errorf("invalid pinned decoder tests")
	}
	testOK := false
	for _, decl := range tests.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "TestECBDailyDecoder" {
			testOK = true
		}
	}
	if !testOK {
		return fmt.Errorf("missing pinned synthetic decoder contract test")
	}
	return nil
}
