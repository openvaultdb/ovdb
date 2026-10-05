package representation

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemaRegistryCoexistence(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	schemas := make([]*jsonschema.Schema, 2)
	ids := []string{"https://openvaultdb.com/schemas/representation-contract-1.json", "https://openvaultdb.com/schemas/representation-contract-2.json"}
	// Register both exported schemas under their declared canonical IDs before
	// compiling either. This reproduces a normal consumer's shared registry.
	for i, data := range [][]byte{Schema(), Schema2()} {
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		id, ok := value["$id"].(string)
		if !ok || id != ids[i] {
			t.Fatalf("unexpected schema identity %v", value["$id"])
		}
		if err := compiler.AddResource(id, value); err != nil {
			t.Fatal("schemas cannot coexist", err)
		}
	}
	for i, id := range ids {
		schema, err := compiler.Compile(id)
		if err != nil {
			t.Fatal(err)
		}
		schemas[i] = schema
	}
	legacy, _ := fixture(t)
	native, _, _ := realFixture(t, "real-ror")
	geo, _, _ := realFixture(t, "real-geonames")
	for _, tc := range []struct {
		data    []byte
		version int
	}{{legacy, 0}, {native, 1}, {geo, 1}} {
		var value any
		if err := json.Unmarshal(tc.data, &value); err != nil {
			t.Fatal(err)
		}
		if err := schemas[tc.version].Validate(value); err != nil {
			t.Fatal("correct registered version rejected", err)
		}
		if err := schemas[1-tc.version].Validate(value); err == nil {
			t.Fatal("wrong registered version accepted")
		}
	}
}
func TestSchema2LiteralRecordsComponents(t *testing.T) {
	schema, err := compileSchema("schema2.json", Schema2())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"ror.sqlite", true}, {"$records/item.json", true}, {"assertions/$records/item.json", true}, {"$records/$records/item.json", true},
		{"$HOME/x", false}, {"$recordsx/x", false}, {"embedded$dollar/x", false}, {"$records/item$.json", false}, {"assertions/$recordsuffix/item.json", false},
		{"assertions/${HOME}/x", false}, {"assertions/$(echo)/x", false}, {"assertions/%24records/x", false}, {"/$records/x", false}, {"$records/../x", false}, {"$records//x", false}, {"https://example.com/$records/x", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			d, _, _ := nativeFixture(t)
			d.Contracts[0].Native.Dataset.Path = tc.path
			data, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			_, parseErr := Parse(data)
			if (parseErr == nil) != tc.valid {
				t.Fatalf("Parse path %q: %v", tc.path, parseErr)
			}
			var value any
			if err = json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			validationErr := schema.Validate(value)
			if (validationErr == nil) != tc.valid {
				t.Fatalf("exported schema path %q: %v", tc.path, validationErr)
			}
		})
	}
	// Exercise the unchanged real canonical decision path, including $records.
	geo, _, _ := realFixture(t, "real-geonames")
	if _, err := Parse(geo); err != nil {
		t.Fatal("canonical decision path rejected", err)
	}
}
