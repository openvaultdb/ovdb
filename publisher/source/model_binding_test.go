package source

import (
	"encoding/json"
	"os"
	"testing"
)

// Check the example against the separately published native model. An alias
// that does not exist in the model must never be advertised as a binding.
func TestECBDailyNativeModelBinding(t *testing.T) {
	definitionBytes, err := os.ReadFile("ecb-daily.example.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(definitionBytes)
	if err != nil {
		t.Fatal(err)
	}
	modelBytes, err := os.ReadFile("model/ecb-daily.modelspec.json")
	if err != nil {
		t.Fatal(err)
	}
	var model struct {
		Records map[string]struct {
			Key    []string `json:"key"`
			Fields map[string]struct {
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal(modelBytes, &model); err != nil {
		t.Fatal(err)
	}
	r := d.Recordsets["FxReferenceQuote"]
	e, ok := model.Records[r.Entity]
	if !ok || len(e.Key) != 0 || len(e.Fields) != 3 {
		t.Fatal("native model must exist without a stable key or synthetic fields")
	}
	for _, native := range []string{"time", "currency", "rate"} {
		property, ok := e.Fields[r.FieldMapping[native]]
		if !ok || r.FieldMapping[native] != native || property.Type != "string" || !property.Required {
			t.Fatalf("native %s must bind to its preserved required string field", native)
		}
	}
	if len(r.FieldMapping) != 3 || r.Context["baseCurrency"] != "EUR" || r.Context["rateKind"] != "indicative-reference" || d.RequireExecution() == nil {
		t.Fatal("model binding changed context or enabled execution")
	}
}
