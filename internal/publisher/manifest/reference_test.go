package manifest

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The parity test of the Directory profile: the Go functions against the
// verdicts that the Directory's JavaScript gave on the documents of
// testdata/reference (see generate.mjs there and the README of this package).

type referenceCorpus struct {
	Node        string                  `json:"node"`
	References  map[string]referencePin `json:"references"`
	MinedEdits  struct{ Found, Applied int }
	Families    []string            `json:"families"`
	Bases       map[string]string   `json:"bases"`
	ManifestRaw [][]json.RawMessage `json:"manifestCases"`
	MdRaw       [][]json.RawMessage `json:"mdCases"`
}

type referencePin struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type verdictFile struct {
	Node       string                  `json:"node"`
	References map[string]referencePin `json:"references"`
	Profile    string                  `json:"profile"`
	Thrown     int                     `json:"thrown"`
	Manifest   verdictSet              `json:"manifest"`
	Md         verdictSet              `json:"md"`
}

type verdictSet struct {
	Accepted int    `json:"accepted"`
	Refused  int    `json:"refused"`
	Verdicts string `json:"verdicts"`
}

type referenceCase struct {
	Document string
	Path     string // OVDB.md cases: the manifest path that must be listed
	Family   string
	Verdict  bool
}

func readGolden(t testing.TB, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/reference/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func decode(t testing.TB, raw []json.RawMessage, into ...any) {
	t.Helper()
	for i, target := range into {
		if err := json.Unmarshal(raw[i], target); err != nil {
			t.Fatal(err)
		}
	}
}

// rebuild applies a patch (start, count, replacement lines...) to a base and
// the flags (crlf, bom) to the result, as generate.mjs does.
func rebuild(base string, patch []json.RawMessage, flags string) string {
	lines := strings.Split(base, "\n")
	var start, count int
	_ = json.Unmarshal(patch[0], &start)
	_ = json.Unmarshal(patch[1], &count)
	replacement := make([]string, 0, len(patch)-2)
	for _, raw := range patch[2:] {
		var line string
		_ = json.Unmarshal(raw, &line)
		replacement = append(replacement, line)
	}
	lines = append(lines[:start], append(replacement, lines[start+count:]...)...)
	out := strings.Join(lines, "\n")
	if strings.Contains(flags, "crlf") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if strings.Contains(flags, "bom") {
		out = "\ufeff" + out
	}
	return out
}

func loadReference(t testing.TB) (corpus referenceCorpus, verdicts verdictFile, manifests, mds []referenceCase) {
	t.Helper()
	readGolden(t, "corpus.json", &corpus)
	readGolden(t, "directory.verdicts.json", &verdicts)
	for i, raw := range corpus.ManifestRaw {
		var base, flags string
		var patch []json.RawMessage
		var family int
		decode(t, raw, &base, &patch, &flags, &family)
		manifests = append(manifests, referenceCase{
			Document: rebuild(corpus.Bases[base], patch, flags),
			Family:   corpus.Families[family],
			Verdict:  verdicts.Manifest.Verdicts[i] == '1',
		})
	}
	for i, raw := range corpus.MdRaw {
		var base, flags, path string
		var patch []json.RawMessage
		var family int
		decode(t, raw, &base, &patch, &flags, &path, &family)
		mds = append(mds, referenceCase{
			Document: rebuild(corpus.Bases[base], patch, flags),
			Path:     path,
			Family:   corpus.Families[family],
			Verdict:  verdicts.Md.Verdicts[i] == '1',
		})
	}
	if len(manifests) != len(verdicts.Manifest.Verdicts) || len(mds) != len(verdicts.Md.Verdicts) {
		t.Fatalf("the corpus and the verdicts disagree on the number of documents: %d and %d manifests, %d and %d OVDB.md", len(manifests), len(verdicts.Manifest.Verdicts), len(mds), len(verdicts.Md.Verdicts))
	}
	return corpus, verdicts, manifests, mds
}

// stricterKinds are the ways in which the Go functions are stricter than the
// Directory's JavaScript on the corpus, each with its reason; the README of this
// package lists the same kinds, and a test holds the two together. A refusal of
// a document that the reference accepts is classified by kindOf, or the test
// fails; a kind that no document of the corpus shows fails it too.
var stricterKinds = map[string]string{
	"yaml-anchor":      "The reader refuses anchors and aliases (& and *): it reads a document once, as written, and expanding references is how a small file becomes a large one.",
	"yaml-character":   "The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the reference reads them into a string.",
	"yaml-directive":   "The reader refuses a %YAML or %TAG directive; the reference follows it.",
	"yaml-documents":   "The reader refuses a document end marker (`...`) and a second document; the reference reads the first document and ignores what follows.",
	"yaml-number":      "The reader refuses hexadecimal and octal numbers (0x1F, 0o17), which the reference reads as numbers; a value written so is never meant, and its value would depend on the YAML version.",
	"yaml-tab":         "The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation).",
	"yaml-tag":         "The reader refuses tags (!, !!), which the reference resolves; it reads plain values only.",
	"yaml-unsupported": "The reader refuses constructs outside its subset, such as explicit keys (`? key`).",
	"url-length":       "A URL longer than rules.MaxURLLength (2048 bytes) is refused; the reference has no bound.",
}

// kindOf names the kind of a refusal that the reference does not make.
func kindOf(f Finding) string {
	if f.Rule == "manifest-url" && strings.Contains(f.Message, "longer than") {
		return "url-length"
	}
	return f.Rule
}

type accounting struct {
	agree, stricter, looser int
	kinds                   map[string]int
	examples                map[string]string
}

func (a *accounting) record(c referenceCase, goAccepts bool, first Finding) bool {
	if a.kinds == nil {
		a.kinds, a.examples = map[string]int{}, map[string]string{}
	}
	switch {
	case goAccepts == c.Verdict:
		a.agree++
	case goAccepts:
		a.looser++
		return false
	default:
		a.stricter++
		kind := kindOf(first)
		a.kinds[kind]++
		if _, seen := a.examples[kind]; !seen {
			a.examples[kind] = fmt.Sprintf("%s: %s", c.Family, first.Message)
		}
	}
	return true
}

func manifestAccepted(doc string) (bool, Finding) {
	_, findings := CheckManifest([]byte(doc), "ovdb.yaml", Directory)
	if len(findings) == 0 {
		return true, Finding{}
	}
	return false, findings[0]
}

func mdAccepted(doc, path string) (bool, Finding) {
	md, findings := CheckOVDBMd([]byte(doc), Directory)
	if len(findings) == 0 && md.Lists(path) {
		return true, Finding{}
	}
	if len(findings) == 0 {
		return false, Finding{Rule: "ovdbmd-unlisted"}
	}
	return false, findings[0]
}

var readmeRow = regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| (\\d+) \\| (.+) \\|$")

func TestReferenceDirectory(t *testing.T) {
	corpus, verdicts, manifests, mds := loadReference(t)
	var total accounting
	for _, c := range manifests {
		ok, first := manifestAccepted(c.Document)
		if !total.record(c, ok, first) {
			t.Errorf("manifest accepted where the Directory refuses (%s): %q", c.Family, c.Document)
		}
	}
	for _, c := range mds {
		ok, first := mdAccepted(c.Document, c.Path)
		if !total.record(c, ok, first) {
			t.Errorf("OVDB.md accepted where the Directory refuses (%s, path %q): %q", c.Family, c.Path, c.Document)
		}
	}
	if total.looser != 0 {
		t.Fatalf("%d documents are accepted by Go and refused by the Directory", total.looser)
	}

	// The corpus is as large and as varied as the proof claims.
	if len(manifests) < 4000 || len(mds) < 400 {
		t.Errorf("the corpus holds %d manifests and %d OVDB.md documents, fewer than 4000 and 400", len(manifests), len(mds))
	}
	if verdicts.Manifest.Accepted == 0 || verdicts.Manifest.Refused == 0 || verdicts.Md.Accepted == 0 || verdicts.Md.Refused == 0 {
		t.Error("the corpus must hold accepted and refused documents of both kinds")
	}
	if corpus.MinedEdits.Applied < 100 || corpus.MinedEdits.Applied > corpus.MinedEdits.Found {
		t.Errorf("mined edits: %d of %d applied", corpus.MinedEdits.Applied, corpus.MinedEdits.Found)
	}
	if verdicts.Profile != "directory" {
		t.Errorf("verdicts are of profile %q", verdicts.Profile)
	}

	// Every stricter kind is documented, real, and counted in the README.
	for kind, count := range total.kinds {
		if _, ok := stricterKinds[kind]; !ok {
			t.Errorf("a stricter kind %q is not recorded (%d documents, e.g. %s): record it with its reason, or make Go agree", kind, count, total.examples[kind])
		}
	}
	for kind := range stricterKinds {
		if total.kinds[kind] == 0 {
			t.Errorf("the stricter kind %q is recorded but no document of the corpus shows it", kind)
		}
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]int{}
	for _, m := range readmeRow.FindAllStringSubmatch(string(readme), -1) {
		n, _ := strconv.Atoi(m[2])
		rows[m[1]] = n
		if want, ok := stricterKinds[m[1]]; ok && m[3] != want {
			t.Errorf("README reason of %q differs from the recorded one:\n  README: %s\n  test:   %s", m[1], m[3], want)
		}
	}
	for kind, count := range total.kinds {
		if rows[kind] != count {
			t.Errorf("README counts %d documents of the stricter kind %q; the corpus shows %d", rows[kind], kind, count)
		}
	}
	for kind := range rows {
		if _, ok := stricterKinds[kind]; !ok {
			t.Errorf("README lists the stricter kind %q, which is not recorded in the test", kind)
		}
	}
	for _, want := range []string{
		pinOf(corpus, "directory"), pinOf(corpus, "chinookdb"),
		fmt.Sprintf("%d manifests and %d OVDB.md documents", len(manifests), len(mds)),
		fmt.Sprintf("%d of %d", corpus.MinedEdits.Applied, corpus.MinedEdits.Found),
		fmt.Sprintf("%d agree", total.agree), fmt.Sprintf("%d stricter", total.stricter),
	} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README does not state %q", want)
		}
	}
	t.Logf("%d documents: agree %d, stricter %d, looser %d (mined edits %d of %d)", len(manifests)+len(mds), total.agree, total.stricter, total.looser, corpus.MinedEdits.Applied, corpus.MinedEdits.Found)
	for _, kind := range slices.Sorted(maps.Keys(total.kinds)) {
		t.Logf("stricter %-20s %5d  e.g. %s", kind, total.kinds[kind], total.examples[kind])
	}
}

func pinOf(c referenceCorpus, name string) string {
	return c.References[name].Repository + "@" + c.References[name].Commit
}

// Check judges a pair as its two documents are judged alone, and the real
// documents of the references are accepted as the Directory accepts them.
func TestCheckPairAgreesWithTheParts(t *testing.T) {
	corpus, _, manifests, mds := loadReference(t)
	if len(mds) == 0 || len(manifests) == 0 {
		t.Fatal("no corpus")
	}
	for i := 0; i < len(manifests) && i < 2000; i += 7 {
		md := mds[i%len(mds)]
		mdOK, _ := mdAccepted(md.Document, "ovdb.yaml")
		mOK, _ := manifestAccepted(manifests[i].Document)
		got := Check([]byte(md.Document), "ovdb.yaml", []byte(manifests[i].Document), Directory)
		if got.OK() != (mdOK && mOK) {
			t.Fatalf("pair %d: Check says %v, the parts say OVDB.md %v, manifest %v", i, got.OK(), mdOK, mOK)
		}
	}
	for _, pair := range [][2]string{{"chinook-md", "chinook-yaml"}, {"fixture-md", "fixture-yaml"}, {"hoster-md", "hoster-yaml"}} {
		if r := Check([]byte(corpus.Bases[pair[0]]), "ovdb.yaml", []byte(corpus.Bases[pair[1]]), Directory); !r.OK() {
			t.Errorf("%s with %s: %v", pair[0], pair[1], r.Findings)
		}
	}
}
