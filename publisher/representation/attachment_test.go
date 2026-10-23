package representation

import (
	"strings"
	"testing"
)

func TestAttachmentParser(t *testing.T) {
	for _, tc := range []struct {
		data           string
		present, valid bool
	}{
		{"format: ovdb-manifest/draft-1\n", false, true},
		{"representation_contract: {path: contracts.json, sha256: " + strings.Repeat("a", 64) + "}", true, true},
		{"representation_contract: null", false, false},
		{"representation_contract: {path: ../contracts.json, sha256: bad}", false, false},
		{"representation_contract: {path: contracts.json, sha256: bad}", false, false},
		{"representation_contract: {path: contracts.json, sha256: bad, secret: no}", false, false},
		{"representation_contract: [", false, false},
		{strings.Repeat(" ", MaxDocumentBytes+1), false, false},
	} {
		ref, err := ParseAttachment([]byte(tc.data))
		if (err == nil) != tc.valid || (ref != nil) != tc.present {
			t.Fatalf("attachment %v %v", ref, err)
		}
	}
}
