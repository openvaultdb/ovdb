package manifest

import (
	"errors"
	"testing"
)

func TestParseYAML(t *testing.T) {
	node, err := parseYAML([]byte("a: 1\nb:\n  - x\n"))
	if err != nil || node.Kind != kindMap || node.Field("a").Kind != kindNumber || node.Field("b").Items[0].Kind != kindString {
		t.Fatalf("parseYAML = %+v, %v", node, err)
	}
	if node, err := parseYAML(nil); err != nil || node.Kind != kindNull {
		t.Errorf("an empty document = %+v, %v", node, err)
	}
	if node, err := parseYAML([]byte("a: true\nb:\nc: ~\n")); err != nil || node.Field("a").Kind != kindBool || node.Field("b").Kind != kindNull || node.Field("c").Kind != kindNull {
		t.Errorf("scalars = %+v, %v", node, err)
	}
	_, err = parseYAML([]byte("a: &x 1\n"))
	var syntax *syntaxError
	if !errors.As(err, &syntax) || syntax.Line != 1 || syntax.Rule == "" {
		t.Errorf("an anchor is refused with %v, want a *syntaxError with a line and a rule", err)
	}
	if kindSeq == kindMap {
		t.Error("kinds are not distinct")
	}
}
