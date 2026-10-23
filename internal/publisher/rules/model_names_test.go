package rules

import (
	"strings"
	"testing"
)

func TestModelNamesKeepSourceAndAddressGrammarsSeparate(t *testing.T) {
	for _, tc := range []struct {
		name            string
		source, concept bool
	}{
		{"", false, false}, {" \t\n\r\u0085\u00a0\u1680\u2000\u200a\u2028\u2029\u202f\u205f\u3000", false, false},
		{"\ufeff", true, true}, {" spaced ", true, true}, {"名", true, true}, {"9-a/b%", true, true}, {"A.B", true, false},
		{"records", true, false}, {"entities", true, false}, {"components", true, false}, {"enums", true, false}, {"collections", true, false}, {"recordsets", true, false}, {"Records", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModelSourceName(tc.name); got != tc.source {
				t.Fatalf("source %q: %v", tc.name, got)
			}
			if got := ModelConceptName(tc.name); got != tc.concept {
				t.Fatalf("concept %q: %v", tc.name, got)
			}
		})
	}
}

func TestModelLabelsQuoteBroaderNamesAndBoundDiagnostics(t *testing.T) {
	for _, name := range []string{"Az09_", strings.Repeat("A", 40)} {
		if got := ModelLabel(name); got != name {
			t.Fatalf("simple name %q changed: %q", name, got)
		}
	}
	for _, name := range []string{"", "a-b", "a.b", "名", "\ufeff", "a\n\x00b", strings.Repeat("A", 41), strings.Repeat("A", 10000)} {
		got := ModelLabel(name)
		if got != Quote(name) {
			t.Fatalf("label %q is not the bounded quoted name: %q", name, got)
		}
		if strings.ContainsAny(got, "\n\x00") || len(got) > 2048 {
			t.Fatalf("unsafe or unbounded diagnostic: %q", got)
		}
	}
}
