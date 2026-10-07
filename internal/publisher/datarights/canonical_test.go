package datarights

import (
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
)

func TestCanonicalKeysAtEveryRightsBoundary(t *testing.T) {
	p, _, _, files := fixture(t)
	profile := string(encoded(p))
	for _, data := range []string{
		strings.Replace(profile, `"server":`, `"Server":`, 1),
		strings.Replace(profile, `"path":`, `"Path":`, 1),
		strings.Replace(profile, `"sha256":`, `"SHA256":`, 1),
		strings.Replace(profile, `"format":`, `"database":{"Text":"aliased terms"},"format":`, 1),
		strings.Replace(profile, `"text":`, `"TEXT":`, 1),
		strings.Replace(profile, `"format":`, `"for\u006dat":"shadow","format":`, 1),
	} {
		if _, err := ParseProfile([]byte(data), license.Publisher, []string{"rates"}); err == nil {
			t.Fatalf("accepted noncanonical profile: %s", data)
		}
	}
	provenance := string(files["provenance.json"])
	for _, data := range []string{
		strings.Replace(provenance, `"freeSource":`, `"FreeSource":`, 1),
		strings.Replace(provenance, `"url":`, `"URL":`, 1),
		strings.Replace(provenance, `"path":`, `"Path":`, 1),
		strings.Replace(provenance, `"inputs":`, `"inputs":[{"path":"input.xml","sha256":"`+strings.Repeat("a", 64)+`","Bytes":0}],"discardedInputs":`, 1),
		strings.Replace(provenance, `"text":`, `"text":"first","te\u0078t":`, 1),
	} {
		if _, err := ParseProvenance([]byte(data)); err == nil {
			t.Fatalf("accepted noncanonical provenance: %s", data)
		}
	}
}

func TestNativeRecordsetNamesRetainExactCase(t *testing.T) {
	p, _, _, _ := fixture(t)
	p.Recordsets = map[string]license.Declaration{
		"rates": declaration(t, `{"text":"lowercase terms"}`),
		"RATES": declaration(t, `{"text":"uppercase terms"}`),
	}
	parsed, err := ParseProfile(encoded(p), license.Publisher, []string{"rates", "RATES"})
	if err != nil || parsed.Recordsets["rates"].Text != "lowercase terms" || parsed.Recordsets["RATES"].Text != "uppercase terms" {
		t.Fatal("native names were folded or refused:", parsed, err)
	}
	if _, err := ParseProfile(encoded(p), license.Publisher, []string{"rates"}); err == nil {
		t.Fatal("case alias of an unlisted native name accepted")
	}
}

func TestStrictDecodeRefusesObjectsForScalarFields(t *testing.T) {
	var value struct {
		Text string `json:"text"`
	}
	if err := Decode([]byte(`{"text":{"nested":"wrong shape"}}`), &value); err == nil {
		t.Fatal("object decoded into text")
	}
}

func TestPinnedInvalidUTF8CannotProduceEvidence(t *testing.T) {
	for _, path := range []string{"server.json", "provenance.json"} {
		p, d, ctx, files := fixture(t)
		old := files[path]
		// Corrupt a text value, then repin it: hash equality is not text validation.
		needle := "Source credit"
		if path == "server.json" {
			needle = "Source terms"
		}
		files[path] = []byte(strings.Replace(string(old), needle, "bad\xfftext", 1))
		pin := ref(path, files[path])
		if path == "server.json" {
			p.Server = &pin
		} else {
			p.Provenance = pin
		}
		if rights, err := Verify(p, d, []string{"rates"}, ctx); err == nil || len(rights) != 0 {
			t.Fatalf("pinned invalid UTF-8 yielded evidence: %+v, %v", rights, err)
		}
	}
}

func TestPinnedServerMarkerRequiresCanonicalKeys(t *testing.T) {
	p, d, ctx, files := fixture(t)
	files["server.json"] = []byte(strings.Replace(string(files["server.json"]), `"data_rights":{"format":`, `"data_rights":{"Format":`, 1))
	pin := ref("server.json", files["server.json"])
	p.Server = &pin
	if rights, err := Verify(p, d, []string{"rates"}, ctx); err == nil || len(rights) != 0 {
		t.Fatalf("aliased server marker yielded evidence: %+v, %v", rights, err)
	}
}
