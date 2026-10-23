package manifest

import (
	"errors"
	"strings"
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
	// The reader's wording for meaning files is reworded; a quoted value over several lines is told to be a block scalar.
	for doc, want := range map[string]string{
		"a: \"x\n  y\"\n":        "write the value as a block scalar, >- or |-",
		"a: \"x\\\n  y\"\n":      "meaninggraph/cli#7",
		"---\na: 1\n---\nb: 2\n": "write one document",
	} {
		_, err := parseYAML([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "meaning file") || strings.Contains(err.Error(), "(use a block scalar") {
			t.Errorf("parseYAML(%q) = %v, want it to say %q", doc, err, want)
		}
	}
	if kindSeq == kindMap {
		t.Error("kinds are not distinct")
	}
}
