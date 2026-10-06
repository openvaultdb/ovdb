package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The parity test of the Directory profile: the Go functions against what the
// Directory's JavaScript said of the documents of testdata/reference, and the
// facts against what its own code derives from them (see generate.mjs there and
// the README of this package).

type referenceCorpus struct {
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
	Profile  string     `json:"profile"`
	Thrown   int        `json:"thrown"`
	Manifest verdictSet `json:"manifest"`
	Md       verdictSet `json:"md"`
}

type verdictSet struct {
	Accepted int    `json:"accepted"`
	Refused  int    `json:"refused"`
	Verdicts string `json:"verdicts"`
	// ChinookClasses is, per manifest (comma separated), what the frozen Chinook checker refuses it for: D, the rule that recordset names look like ModelSpec
	// entity names; K, recordset_entities as an unknown key; O, any other problem. Only the publisher golden has it.
	ChinookClasses string `json:"chinookClasses"`
}

type factsFile struct {
	Fields   []string                  `json:"fields"`
	Reads    map[string][]int          `json:"reads"`
	Bases    map[string]map[string]any `json:"bases"`
	Presence []string                  `json:"presence"`
	PBases   map[string]string         `json:"presenceBases"`
	Manifest []map[string]any          `json:"manifest"`
	Md       [][]string                `json:"md"`
}

type referenceCase struct {
	Base      string
	Document  []byte
	Path      string // OVDB.md cases: the manifest path that must be listed
	Family    string
	Verdict   bool // the Directory's
	Publisher bool // the Chinook checker's
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

// decode reads the elements of a case into the targets; a malformed entry fails.
func decode(t testing.TB, raw []json.RawMessage, into ...any) {
	t.Helper()
	if len(raw) != len(into) {
		t.Fatalf("a case has %d elements, want %d: %s", len(raw), len(into), raw)
	}
	for i, target := range into {
		if err := json.Unmarshal(raw[i], target); err != nil {
			t.Fatalf("a case element %d is malformed: %v", i, err)
		}
	}
}

// rebuild applies a patch (start, count, replacement lines...) to a base and the
// flags to the result, as generate.mjs does: crlf, bom, pad=N (a comment line
// makes the document exactly N bytes) and latin1 (the characters, all below
// U+0100, are written as single bytes), and returns the bytes of the document.
func rebuild(t testing.TB, base string, patch []json.RawMessage, flags string) []byte {
	t.Helper()
	if len(patch) < 2 {
		t.Fatalf("a patch has %d elements", len(patch))
	}
	var start, count int
	if err := json.Unmarshal(patch[0], &start); err != nil {
		t.Fatalf("a patch start is malformed: %v", err)
	}
	if err := json.Unmarshal(patch[1], &count); err != nil {
		t.Fatalf("a patch count is malformed: %v", err)
	}
	lines := strings.Split(base, "\n")
	if start < 0 || count < 0 || start+count > len(lines) {
		t.Fatalf("a patch (%d, %d) is outside a base of %d lines", start, count, len(lines))
	}
	replacement := make([]string, 0, len(patch)-2)
	for _, raw := range patch[2:] {
		var line string
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("a patch line is malformed: %v", err)
		}
		replacement = append(replacement, line)
	}
	out := strings.Join(append(lines[:start:start], append(replacement, lines[start+count:]...)...), "\n")
	flag := strings.Fields(flags)
	if slices.Contains(flag, "crlf") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if slices.Contains(flag, "bom") {
		out = "\ufeff" + out
	}
	for _, f := range flag {
		if n, ok := strings.CutPrefix(f, "pad="); ok {
			size, err := strconv.Atoi(n)
			if err != nil || size-len(out)-3 < 0 {
				t.Fatalf("a pad flag %q cannot be applied to %d bytes", f, len(out))
			}
			out += "# " + strings.Repeat("x", size-len(out)-3) + "\n"
		}
	}
	if slices.Contains(flag, "latin1") {
		raw := make([]byte, 0, len(out))
		for _, r := range out {
			if r > 0xff {
				t.Fatalf("latin1 flag on U+%04X", r)
			}
			raw = append(raw, byte(r))
		}
		return raw
	}
	return []byte(out)
}

func loadReference(t testing.TB) (corpus referenceCorpus, verdicts verdictFile, manifests, mds []referenceCase) {
	t.Helper()
	readGolden(t, "corpus.json", &corpus)
	readGolden(t, "directory.verdicts.json", &verdicts)
	var publisher verdictFile
	readGolden(t, "publisher.verdicts.json", &publisher)
	if len(publisher.Manifest.Verdicts) != len(verdicts.Manifest.Verdicts) || len(publisher.Md.Verdicts) != len(verdicts.Md.Verdicts) {
		t.Fatalf("the Directory's and the publisher's verdicts disagree on the number of documents")
	}
	for i, raw := range corpus.ManifestRaw {
		var base, flags string
		var patch []json.RawMessage
		var family int
		decode(t, raw, &base, &patch, &flags, &family)
		if _, ok := corpus.Bases[base]; !ok || family < 0 || family >= len(corpus.Families) || i >= len(verdicts.Manifest.Verdicts) {
			t.Fatalf("manifest case %d names base %q, family %d", i, base, family)
		}
		manifests = append(manifests, referenceCase{Base: base, Document: rebuild(t, corpus.Bases[base], patch, flags), Family: corpus.Families[family], Verdict: verdictOf(t, verdicts.Manifest.Verdicts[i]), Publisher: verdictOf(t, publisher.Manifest.Verdicts[i])})
	}
	for i, raw := range corpus.MdRaw {
		var base, flags, path string
		var patch []json.RawMessage
		var family int
		decode(t, raw, &base, &patch, &flags, &path, &family)
		if _, ok := corpus.Bases[base]; !ok || family < 0 || family >= len(corpus.Families) || i >= len(verdicts.Md.Verdicts) {
			t.Fatalf("OVDB.md case %d names base %q, family %d", i, base, family)
		}
		mds = append(mds, referenceCase{Base: base, Document: rebuild(t, corpus.Bases[base], patch, flags), Path: path, Family: corpus.Families[family], Verdict: verdictOf(t, verdicts.Md.Verdicts[i]), Publisher: verdictOf(t, publisher.Md.Verdicts[i])})
	}
	if len(manifests) != len(verdicts.Manifest.Verdicts) || len(mds) != len(verdicts.Md.Verdicts) {
		t.Fatalf("the corpus and the verdicts disagree on the number of documents: %d and %d manifests, %d and %d OVDB.md", len(manifests), len(verdicts.Manifest.Verdicts), len(mds), len(verdicts.Md.Verdicts))
	}
	for _, set := range []struct {
		verdicts verdictSet
		name     string
	}{{verdicts.Manifest, "manifest"}, {verdicts.Md, "OVDB.md"}, {publisher.Manifest, "publisher manifest"}, {publisher.Md, "publisher OVDB.md"}} {
		if strings.Count(set.verdicts.Verdicts, "1") != set.verdicts.Accepted || strings.Count(set.verdicts.Verdicts, "0") != set.verdicts.Refused {
			t.Fatalf("the %s verdicts do not add up to their counts", set.name)
		}
	}
	return corpus, verdicts, manifests, mds
}

func verdictOf(t testing.TB, b byte) bool {
	t.Helper()
	if b != '0' && b != '1' {
		t.Fatalf("a verdict is %q", b)
	}
	return b == '1'
}

// The goldens are held by their digests: a hand edit of any of them, of either
// slice, fails here until generate.mjs is run again.
func TestGoldenDigests(t *testing.T) {
	var want map[string]string
	readGolden(t, "digests.json", &want)
	if len(want) != 8 {
		t.Fatalf("digests.json holds %d digests, want 8", len(want))
	}
	for name, digest := range want {
		raw, err := os.ReadFile("../" + name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != digest {
			t.Errorf("%s does not match its digest in testdata/reference/digests.json: it was edited by hand, or its generator was run without generate.mjs of this package; run `node internal/publisher/manifest/testdata/reference/generate.mjs`", name)
		}
	}
}

// sharedKinds are the ways in which the Go functions are stricter than the references on the corpus, in either profile, each with the
// reason; the README lists the same kinds with a count for each profile, and a test holds the two together. They are shared by
// construction: the reader and the bound on a document's size act before a profile's rules run, and the Directory's rules (the
// lengths, the punycode rule) run first under the Publisher profile too, so a kind of this list is recorded once and counted for
// each profile, and cannot be recorded for one and forgotten for the other. Only kinds of rules that one profile alone has are
// listed apart (publisherKinds). A refusal of a document that a reference accepts is classified by kindOf, or the test fails.
var sharedKinds = map[string]string{
	"document-size":     "A document over 262144 bytes (MaxDocumentBytes) is refused before it is read; the references read files of any size.",
	"length-address":    "An address over 2048 bytes, or one that names a repository over 247 bytes; the references' address expressions have no bound.",
	"length-entry":      "A publish entry whose path after ./ is over 1024 bytes; the references have no bound.",
	"length-path":       "A file path over 1024 bytes in model.modelspec, model.hcl or meaning.file; the references have no bound.",
	"length-repository": "A publisher.repository over 255 bytes; the references have no bound.",
	"punycode":          "A homepage host with an xn-- label that does not spell Latin-1 letters (see the README of package rules); Node accepts the label.",
	"url-length":        "A URL longer than rules.MaxURLLength (2048 bytes) is refused; the reference has no bound.",
	"yaml":              "The reader accepts a subset of YAML and refuses a structure it cannot place: a plain value that continues on the next line with a character such as * or \" at its start, a flow collection used as a key, an explicit key or an entry with no value in a flow collection, and the other places of the table below; the references read them.",
	"yaml-anchor":       "The reader refuses anchors and aliases (& and *): it reads a document once, as written, and expanding references is how a small file becomes a large one.",
	"yaml-character":    "The reader refuses characters that YAML 1.2 does not allow in text, among them the C1 controls such as U+0085; the reference reads them into a string.",
	"yaml-directive":    "The reader refuses a %YAML or %TAG directive; the reference follows it.",
	"yaml-documents":    "The reader refuses a document end marker (`...`) and a second document; the reference reads the first document and ignores what follows.",
	"yaml-encoding":     "The reader refuses a file that is not UTF-8 text (a Latin-1 byte, a NUL character); the reference, which reads a file as UTF-8, replaces the bytes it cannot decode and goes on.",
	"yaml-escape":       "The reader refuses a double-quoted escape that is not a character, such as half of a surrogate pair (\\ud83c); the reference accepts it.",
	"yaml-key":          "The reader refuses a key that YAML reads as a number, a boolean or null (2024, true, null) and wants it in quotes; the reference accepts it as a key.",
	"yaml-line-ending":  "The reader refuses a carriage return that is not part of CRLF; the reference reads it as a line break.",
	"yaml-number":       "The reader refuses numbers it cannot hold exactly or that are not finite: hexadecimal and octal numbers, .inf, .nan, and integers beyond 2^53; the reference reads them as numbers.",
	"yaml-tab":          "The reader refuses a tab where YAML allows it but whose reading differs between parsers (after a colon, in indentation).",
	"yaml-tag":          "The reader refuses tags (!, !!), which the reference resolves; it reads plain values only.",
	"yaml-limit":        "The reader refuses collections nested more than 64 levels deep (63 is read); the reference reads any depth.",
	"yaml-unsupported":  "The reader refuses constructs outside its subset: explicit keys (`? key`), and a quoted value written over more than one line, which a YAML tool writes back for any long string (the message asks for a block scalar, `>-` or `|-`; meaninggraph/cli#7); the reference reads both.",
}

// readerKinds are the kinds of sharedKinds that the reader (or the bound checked before it) makes: the rules of its refusals, and
// document-size. TestReaderKinds holds them to the places of the reader (reader_places_test.go).
var readerKinds = map[string]bool{
	"document-size": true, "yaml": true, "yaml-anchor": true, "yaml-character": true, "yaml-directive": true, "yaml-documents": true,
	"yaml-encoding": true, "yaml-escape": true, "yaml-key": true, "yaml-limit": true, "yaml-line-ending": true, "yaml-number": true,
	"yaml-tab": true, "yaml-tag": true, "yaml-unsupported": true,
}

// kindOf names the kind of a refusal that the reference does not make.
func kindOf(f Finding) string {
	switch {
	case f.Rule == "representation-attachment" && strings.Contains(f.Message, "requires lower-case SHA256"):
		return "representation-hash-list"
	case strings.Contains(f.Message, "is longer than 2048 characters"):
		return "url-length"
	case f.Rule == "document-size":
		return f.Rule
	case strings.Contains(f.Message, "a path is at most"):
		return "length-path"
	case strings.Contains(f.Message, "the path after ./ is at most"):
		return "length-entry"
	case strings.Contains(f.Message, "a repository URL is at most"):
		return "length-repository"
	case strings.Contains(f.Message, "bytes; at most") || strings.Contains(f.Message, "names a repository of"):
		return "length-address"
	case strings.Contains(f.Message, "internationalised name"):
		return "punycode"
	case f.Rule == "manifest-meaning" && strings.Contains(f.Message, "(in any case), derived from publisher.repository"):
		return "graph-address-case"
	case f.Rule == "manifest-meaning" && strings.Contains(f.Message, "must be the graph's meaning:// address"):
		return "graph-address-scheme"
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

// d0Classes are the problems of the frozen Chinook checker that D0 explains Go not having: D, the rule that a recordset name looks like a ModelSpec entity
// name, which the Directory at its pin no longer has (it takes native names) and K, recordset_entities as an unknown key, which the Directory reads.
const d0Classes = "DK"

// d0Explained says whether a document that the Publisher profile accepts and the frozen Chinook checker refuses is explained by D0 (the Directory at its pin
// is the reference for both profiles): the Directory accepts it too (the caller checks) and every problem the checker found in it is of a class in d0Classes.
// A refused document whose classes include any other (O) is not explained, and nothing else about the document is looked at.
func d0Explained(classes string) bool {
	return classes != "" && strings.Trim(classes, d0Classes) == ""
}

func manifestAccepted(doc []byte) (bool, Finding) { return acceptManifest(Directory, doc) }

func mdAccepted(doc []byte, path string) (bool, Finding) { return acceptMd(Directory, doc, path) }

// acceptManifest says whether a manifest is judged right by a profile, and the first finding if not.
func acceptManifest(profile Profile, doc []byte) (bool, Finding) {
	_, findings := CheckManifest(doc, "ovdb.yaml", profile)
	if len(findings) == 0 {
		return true, Finding{}
	}
	return false, findings[0]
}

// acceptMd says whether an OVDB.md is judged right by a profile. The Directory's
// verdict is about the manifest at path, which it must list; the Chinook checker
// reads every manifest that OVDB.md lists and has no manifest of its own to find.
func acceptMd(profile Profile, doc []byte, path string) (bool, Finding) {
	md, findings := CheckOVDBMd(doc, profile)
	if len(findings) == 0 && (profile != Directory || md.Lists(path)) {
		return true, Finding{}
	}
	if len(findings) == 0 {
		return false, Finding{Rule: "ovdbmd-unlisted"}
	}
	return false, findings[0]
}

var readmeRow = regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (\\d+) \\| (.+) \\|$")

// referenceSpec is one profile and the reference that judges it.
type referenceSpec struct {
	name    string // "Directory" or "Publisher", as the tests and the README call it
	profile Profile
	refName string // what the README calls the reference in its totals
	column  int    // the column of the table of shared kinds that holds this profile's counts
	golden  string // the verdict file
	own     map[string]string
	verdict func(referenceCase) bool
	heading string // the heading of the README's table of `own`
}

const (
	sharedHeading = "### Recorded differences: shared by both profiles"
	ownHeading    = "### Recorded differences: the Publisher profile's own"
	driftHeading  = "### Recorded differences: not yet ported (the Directory profile)"
)

var directorySpec = referenceSpec{
	name: "Directory", profile: Directory, refName: "Directory", column: 0, golden: "directory.verdicts.json", own: driftKinds, heading: driftHeading,
	verdict: func(c referenceCase) bool { return c.Verdict },
}

// readmeKinds are the rows of the table that follows a heading of the README.
func readmeKinds(t testing.TB, readme, heading string) map[string][2]string {
	t.Helper()
	at := strings.Index(readme, heading+"\n")
	if at < 0 {
		t.Fatalf("README has no section %q", heading)
	}
	section := readme[at+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	if end := strings.Index(section, "\n### "); end >= 0 {
		section = section[:end]
	}
	rows := map[string][2]string{}
	for _, m := range readmeRow.FindAllStringSubmatch(section, -1) {
		rows[m[1]] = [2]string{m[2], m[3]}
	}
	return rows
}

var readmeSharedRow = regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (\\d+) \\| (\\d+) \\| (.+) \\|$")

// readmeSharedKinds are the rows of the table of the kinds that both profiles share: the counts of each profile, and the reason.
func readmeSharedKinds(t testing.TB, readme string) map[string][3]string {
	t.Helper()
	at := strings.Index(readme, sharedHeading+"\n")
	if at < 0 {
		t.Fatalf("README has no section %q", sharedHeading)
	}
	section := readme[at+len(sharedHeading):]
	if end := strings.Index(section, "\n### "); end >= 0 {
		section = section[:end]
	}
	rows := map[string][3]string{}
	for _, m := range readmeSharedRow.FindAllStringSubmatch(section, -1) {
		rows[m[1]] = [3]string{m[2], m[3], m[4]}
	}
	return rows
}

func runReference(t *testing.T, spec referenceSpec) {
	corpus, _, manifests, mds := loadReference(t)
	var verdicts verdictFile
	readGolden(t, spec.golden, &verdicts)
	var total accounting
	allowed := 0
	if spec.profile == Directory {
		allowed = driftCorpusLooser(t)
	}
	d0 := 0 // the documents of the corpus that the Publisher profile accepts and the frozen Chinook checker refuses, which D0 explains (see d0Explained)
	var chinookClasses []string
	if spec.profile == Publisher {
		chinookClasses = strings.Split(verdicts.Manifest.ChinookClasses, ",")
		if len(chinookClasses) != len(manifests) {
			t.Fatalf("the publisher golden has the checker's problem classes of %d manifests, the corpus holds %d", len(chinookClasses), len(manifests))
		}
	}
	for at, c := range manifests {
		ok, first := acceptManifest(spec.profile, c.Document)
		directoryAccepts := c.Verdict
		c.Verdict = spec.verdict(c)
		if !total.record(c, ok, first) && allowed == 0 {
			if spec.profile == Publisher && directoryAccepts && d0Explained(chinookClasses[at]) {
				d0++
			} else {
				t.Errorf("manifest accepted where the %s refuses (%s): %q", spec.refName, c.Family, c.Document)
			}
		}
		_, findings := CheckManifest(c.Document, "ovdb.yaml", spec.profile)
		assertFindings(t, findings)
	}
	for _, c := range mds {
		ok, first := acceptMd(spec.profile, c.Document, c.Path)
		c.Verdict = spec.verdict(c)
		if !total.record(c, ok, first) {
			t.Errorf("OVDB.md accepted where the %s refuses (%s, path %q): %q", spec.refName, c.Family, c.Path, c.Document)
		}
		_, findings := CheckOVDBMd(c.Document, spec.profile)
		assertFindings(t, findings)
	}
	if spec.profile == Publisher {
		allowed = d0
	}
	if total.looser != allowed {
		t.Fatalf("%d documents are accepted by Go and refused by the %s; %d are accounted for (the Directory profile: drift.json's looser entries; the Publisher profile: the documents that D0 explains, by the classes of the Chinook checker's problems) (a document that Go accepts and the reference refuses is a drift to list, in its slice's class, and one that Go has come to refuse is an entry to remove)", total.looser, spec.refName, allowed)
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
	if strings.ToLower(spec.name) != verdicts.Profile || verdicts.Thrown != 0 {
		t.Errorf("verdicts are of profile %q, and the reference threw on %d documents", verdicts.Profile, verdicts.Thrown)
	}

	// Every stricter kind is documented, real, and counted in the README: those of both profiles in the table of the shared kinds, with a
	// count for each profile, and those of this profile alone in a table of its own.
	known := func(kind string) bool {
		_, shared := sharedKinds[kind]
		_, own := spec.own[kind]
		return shared || own
	}
	for kind, count := range total.kinds {
		if !known(kind) {
			t.Errorf("a stricter kind %q is not recorded (%d documents, e.g. %s): record it with its reason, or make Go agree", kind, count, total.examples[kind])
		}
	}
	for kind := range spec.own {
		if total.kinds[kind] == 0 {
			t.Errorf("the stricter kind %q is recorded but no document of the corpus shows it", kind)
		}
	}
	for kind := range sharedKinds {
		if total.kinds[kind] != 0 {
			continue
		}
		// A kind of the reader may show none under a profile when each place of the reader that makes it is shown to have none there.
		places := 0
		for _, p := range readerPlaces {
			if !readerKinds[kind] || p.rule != kind {
				continue
			}
			places++
			none := p.directoryNone
			if spec.profile == Publisher {
				none = p.publisherNone
			}
			if none == "" {
				t.Errorf("the shared kind %q shows no document under the %s profile, but its place %s says it has some", kind, spec.name, p.id)
			}
		}
		if places == 0 {
			t.Errorf("the shared kind %q is recorded but no document of the corpus shows it under the %s profile", kind, spec.name)
		}
	}
	t.Logf("%s: %d documents: agree %d, stricter %d, looser %d (mined edits %d of %d)", spec.name, len(manifests)+len(mds), total.agree, total.stricter, total.looser, corpus.MinedEdits.Applied, corpus.MinedEdits.Found)
	for _, kind := range slices.Sorted(maps.Keys(total.kinds)) {
		t.Logf("stricter %-20s %5d  e.g. %s", kind, total.kinds[kind], total.examples[kind])
	}
	readme := readReadme(t)
	shared := readmeSharedKinds(t, readme)
	for kind, row := range shared {
		n, _ := strconv.Atoi(row[spec.column])
		if want, ok := sharedKinds[kind]; !ok {
			t.Errorf("README lists the shared kind %q, which is not recorded in the test", kind)
		} else if row[2] != want {
			t.Errorf("README reason of %q differs from the recorded one:\n  README: %s\n  test:   %s", kind, row[2], want)
		}
		if total.kinds[kind] != n {
			t.Errorf("README counts %d documents of the shared kind %q under the %s profile; the corpus shows %d", n, kind, spec.name, total.kinds[kind])
		}
	}
	for kind := range sharedKinds {
		if _, ok := shared[kind]; !ok {
			t.Errorf("README does not list the shared kind %q", kind)
		}
	}
	if spec.own != nil {
		own := readmeKinds(t, readme, spec.heading)
		for kind, row := range own {
			n, _ := strconv.Atoi(row[0])
			if want, ok := spec.own[kind]; !ok {
				t.Errorf("README lists the kind %q of the %s profile, which is not recorded in the test", kind, spec.name)
			} else if row[1] != want {
				t.Errorf("README reason of %q differs from the recorded one:\n  README: %s\n  test:   %s", kind, row[1], want)
			} else if total.kinds[kind] != n {
				t.Errorf("README counts %d documents of the kind %q; the corpus shows %d", n, kind, total.kinds[kind])
			}
		}
		for kind := range spec.own {
			if _, ok := own[kind]; !ok {
				t.Errorf("README does not list the kind %q of the %s profile", kind, spec.name)
			}
		}
	}
	flat := strings.Join(strings.Fields(readme), " ")
	for _, want := range []string{
		pinOf(corpus, "directory"), pinOf(corpus, "chinookdb"),
		fmt.Sprintf("%d manifests and %d OVDB.md documents", len(manifests), len(mds)),
		fmt.Sprintf("%d of %d", corpus.MinedEdits.Applied, corpus.MinedEdits.Found),
		fmt.Sprintf("**%d agree, %d stricter, 0 unrecorded Go acceptances where the %s refuses**", total.agree, total.stricter, spec.refName),
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("README does not state %q", want)
		}
	}
	if want := fmt.Sprintf("%d of the %d documents that the Publisher profile accepts and the Chinook checker refuses", d0, total.looser); spec.profile == Publisher && !strings.Contains(flat, want) {
		t.Errorf("README does not state %q", want)
	}
}

// publisherKinds are the ways in which the Publisher profile is stricter than the Chinook checker through rules that only it has (the
// others are sharedKinds).
var publisherKinds = map[string]string{
	"manifest-recordsets":  "Not a bound: D0 (the lead's default; the Directory at its pin is the reference for both profiles). The Directory refuses a recordset name over 256 UTF-16 code units (nativeRecordsetNameProblem, 1c7e126); the Chinook checker, which read every name as an entity identifier, accepted it.",
	"graph-address-case":   "An own-form meaning.graph.address is compared with the repository in ASCII case only (A to Z); the checker lower-cases with JavaScript's toLowerCase, which also folds non-ASCII letters, among them the Kelvin sign onto k. Go refuses what the checker accepts through such a fold, and never the other way round.",
	"graph-address-scheme": "An own-form meaning.graph.address must start with the literal meaning:// (a rule of the Directory); the checker only compares it in lower case and accepts MEANING:// or Meaning://.",
}

var publisherSpec = referenceSpec{
	name: "Publisher", profile: Publisher, refName: "Chinook checker", column: 1, golden: "publisher.verdicts.json",
	own: publisherKinds, heading: ownHeading, verdict: func(c referenceCase) bool { return c.Publisher },
}

// driftKinds are the ways in which the Directory profile is stricter than the Directory only because a rule of the Directory has not been ported: each
// is an entry of testdata/reference/drift.json and goes in the pull request of its slice. They are not bounds and not choices.
var driftKinds = map[string]string{}

// The kinds of the reader in sharedKinds are the rules of the places of the reader that have documents the references accept (and document-size, which is
// checked before the reader): the list of kinds cannot drift from the table of places.
func TestReaderKinds(t *testing.T) {
	counts, _ := readerPlaceCounts(t, readerSourceDir(t, ""))
	stricter := map[string]bool{"document-size": true}
	for _, p := range readerPlaces {
		for _, n := range counts[p.id] {
			if n.stricter > 0 {
				stricter[p.rule] = true
			}
		}
	}
	for kind := range readerKinds {
		if _, ok := sharedKinds[kind]; !ok {
			t.Errorf("the reader's kind %q is not recorded in sharedKinds", kind)
		}
		if !stricter[kind] {
			t.Errorf("the reader's kind %q is recorded, but no place of the reader that makes it has a document that a reference accepts", kind)
		}
	}
	for kind := range stricter {
		if !readerKinds[kind] {
			t.Errorf("a place of the reader that raises %q has documents that a reference accepts, and the kind is not one of readerKinds", kind)
		}
	}
	for kind := range sharedKinds {
		if readerKinds[kind] || strings.HasPrefix(kind, "yaml") {
			if !readerKinds[kind] {
				t.Errorf("%q is a kind of the reader and is not listed in readerKinds", kind)
			}
		}
	}
}

func TestReferenceDirectory(t *testing.T) { runReference(t, directorySpec) }

func TestReferencePublisher(t *testing.T) { runReference(t, publisherSpec) }

func readReadme(t testing.TB) string {
	t.Helper()
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(readme)
}

func pinOf(c referenceCorpus, name string) string {
	return c.References[name].Repository + "@" + c.References[name].Commit
}

// assertFindings holds every finding of a call to the bounds of the package: at
// most MaxFindings and the notice, the notice last, each message printable ASCII
// of at most MaxMessageBytes, and nothing that names a meaning file in a YAML
// finding about another kind of document.
func assertFindings(t testing.TB, findings []Finding) {
	t.Helper()
	if len(findings) > MaxFindings+1 {
		t.Fatalf("%d findings", len(findings))
	}
	for i, f := range findings {
		if f.Rule == "" || f.Message == "" || f.Severity != SeverityError || f.Line < 0 || f.Document == "" {
			t.Fatalf("a malformed finding %+v", f)
		}
		if len(f.Message) > MaxMessageBytes || !printable(f.Message) || !printable(f.Rule) {
			t.Fatalf("a finding with a message of %d bytes that is not printable ASCII or too long: %q", len(f.Message), f.Message)
		}
		if !printable(f.String()) {
			t.Fatalf("String() of %+v is not printable", f)
		}
		if f.Rule == RuleCapped && i != len(findings)-1 {
			t.Fatalf("the notice of findings left out is finding %d of %d", i+1, len(findings))
		}
		if strings.HasPrefix(f.Rule, "yaml") && strings.Contains(f.Message, "meaning file") {
			t.Fatalf("a YAML finding names the wrong kind of document: %s", f.Message)
		}
	}
	if len(findings) == MaxFindings+1 && findings[MaxFindings].Rule != RuleCapped {
		t.Fatalf("%d findings and the last is not the notice", len(findings))
	}
}

// ---- the facts ----

// state is the value of a fact for the comparison with the reference's: nil when
// the key is not written, the value when it is usable, and a marker otherwise.
func state[T any](f Fact[T], value func(T) any) any {
	switch {
	case !f.Present:
		return nil
	case !f.Valid:
		return "<present and not usable>"
	}
	return value(f.Value)
}

func str(s string) any { return s }

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// factValues are the facts of a manifest by the names of the README table.
func factValues(m Manifest) map[string]any {
	text := func(f Fact[string]) any { return state(f, str) }
	address := func(f Fact[Address], part func(Address) any) any { return state(f, part) }
	return map[string]any{
		"format": text(m.Format), "id": text(m.ID), "title": text(m.Title), "description": text(m.Description),
		"url": text(m.URL), "homepage": text(m.Homepage),
		"deployment.url": text(m.DeploymentURL), "deployment.engine": text(m.Engine), "deployment.discovery": text(m.Discovery), "deployment.recordset_page": text(m.RecordsetPage),
		"model.modelspec": text(m.ModelSpec), "model.hcl": text(m.ModelHCL), "model.name": text(m.ModelName),
		"model.address":   address(m.ModelAddress, func(a Address) any { return a.Text }),
		"meaning.address": address(m.MeaningAddress, func(a Address) any { return a.Text }),
		"meaning.file":    text(m.MeaningFile), "meaning.graph.id": text(m.GraphID), "meaning.graph.address": text(m.GraphAddress),
		"licences.model": text(m.LicenceModel), "licences.meaning": text(m.LicenceMeaning), "licences.data": text(m.LicenceData),
		"publisher.name": text(m.PublisherName), "publisher.url": text(m.PublisherURL), "publisher.repository": text(m.PublisherRepository),
		"recordsets":         state(m.Recordsets, func(names []string) any { return names }),
		"recordsets_partial": state(m.RecordsetsPartial, func(b bool) any { return b }),
		"recordset_entities": state(m.RecordsetEntities, func(entities map[string]string) any {
			out := make(map[string]any, len(entities))
			for name, entity := range entities {
				out[name] = entity
			}
			return out
		}),
		"form":                       string(m.Form),
		"model.address.repository":   address(m.ModelAddress, func(a Address) any { return orNil(a.Repository) }),
		"model.address.module":       address(m.ModelAddress, func(a Address) any { return orNil(a.Module) }),
		"model.address.ref":          address(m.ModelAddress, func(a Address) any { return orNil(a.Ref) }),
		"meaning.address.repository": address(m.MeaningAddress, func(a Address) any { return orNil(a.Repository) }),
		"meaning.address.ref":        address(m.MeaningAddress, func(a Address) any { return orNil(a.Ref) }),
	}
}

// factFields is which fact carries each field of the README table.
var factFields = map[string]string{
	"format": "Format", "id": "ID", "title": "Title", "description": "Description", "url": "URL", "homepage": "Homepage",
	"deployment.url": "DeploymentURL", "deployment.engine": "Engine", "deployment.discovery": "Discovery", "deployment.recordset_page": "RecordsetPage",
	"model.modelspec": "ModelSpec", "model.hcl": "ModelHCL", "model.name": "ModelName", "model.address": "ModelAddress",
	"meaning.address": "MeaningAddress", "meaning.file": "MeaningFile", "meaning.graph.id": "GraphID", "meaning.graph.address": "GraphAddress",
	"licences.model": "LicenceModel", "licences.meaning": "LicenceMeaning", "licences.data": "LicenceData",
	"publisher.name": "PublisherName", "publisher.url": "PublisherURL", "publisher.repository": "PublisherRepository",
	"recordsets": "Recordsets", "recordsets_partial": "RecordsetsPartial", "recordset_entities": "RecordsetEntities", "form": "Form",
	"model.address.repository": "ModelAddress.Repository", "model.address.module": "ModelAddress.Module", "model.address.ref": "ModelAddress.Ref",
	"meaning.address.repository": "MeaningAddress.Repository", "meaning.address.ref": "MeaningAddress.Ref",
}

var readmeField = regexp.MustCompile("(?m)^\\| `([a-z._]+)` \\| directory\\.mjs ([0-9, ]+) \\| `([A-Za-z.]+)` \\|$")

// Every fact is compared with what the Directory's own code derives from the same
// document, for every document both accept, and the README's table of the fields
// the Directory reads is the generator's own list of the lines that read them.
// compareFacts compares the facts of every document that the profile and its reference
// both accept with the values that the reference derives, from the facts golden.
func compareFacts(t *testing.T, spec referenceSpec, golden string) (facts factsFile, compared, comparedMd int) {
	_, _, manifests, mds := loadReference(t)
	readGolden(t, golden, &facts)

	accepted := 0
	for i, c := range manifests {
		if !spec.verdict(c) {
			continue
		}
		index := accepted
		accepted++
		m, findings := CheckManifest(c.Document, "ovdb.yaml", spec.profile)
		if len(findings) > 0 {
			continue
		}
		if index >= len(facts.Manifest) {
			t.Fatalf("the facts golden has no entry %d", index)
		}
		want := maps.Clone(facts.Bases[c.Base])
		if want == nil {
			want = map[string]any{}
		}
		maps.Copy(want, facts.Manifest[index])
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(factValues(m))
		if !bytes.Equal(wantJSON, gotJSON) {
			t.Fatalf("facts differ from the %s's for %s document %d:\n%q\n  go:  %s\n  ref: %s", spec.refName, c.Family, i, c.Document, gotJSON, wantJSON)
		}
		if !m.Read {
			t.Fatalf("an accepted manifest was not read: %q", c.Document)
		}
		compared++
	}
	if accepted != len(facts.Manifest) || compared < 800 {
		t.Fatalf("the facts golden holds %d entries for %d accepted manifests, and %d were compared", len(facts.Manifest), accepted, compared)
	}

	listed := 0
	for _, c := range mds {
		if !spec.verdict(c) {
			continue
		}
		index := listed
		listed++
		md, findings := CheckOVDBMd(c.Document, spec.profile)
		if len(findings) > 0 {
			continue
		}
		if !slices.Equal(md.Entries, facts.Md[index]) || !slices.Equal(md.Publish.Value, facts.Md[index]) || !md.Version.Usable() || md.Version.Value != "1" || !md.Read || len(md.Repeated) != 0 && spec.profile == Publisher {
			t.Fatalf("OVDB.md facts %+v differ from the %s's set %v for %q", md, spec.refName, facts.Md[index], c.Document)
		}
		comparedMd++
	}
	if listed != len(facts.Md) || comparedMd < 80 {
		t.Fatalf("the facts golden holds %d lists for %d accepted OVDB.md, and %d were compared", len(facts.Md), listed, comparedMd)
	}
	return facts, compared, comparedMd
}

func TestFactsAgreeUnderThePublisherReference(t *testing.T) {
	_, compared, comparedMd := compareFacts(t, publisherSpec, "publisher.facts.json")
	t.Logf("publisher facts compared on %d accepted manifests and %d OVDB.md documents", compared, comparedMd)
	if want := fmt.Sprintf("under the Publisher profile, %d manifests and %d OVDB.md documents have their facts compared", compared, comparedMd); !strings.Contains(strings.Join(strings.Fields(readReadme(t)), " "), want) {
		t.Errorf("README does not state %q", want)
	}
}

func TestFactsAgreeWithTheReference(t *testing.T) {
	facts, compared, comparedMd := compareFacts(t, directorySpec, "directory.facts.json")

	t.Logf("facts compared on %d accepted manifests and %d OVDB.md documents", compared, comparedMd)
	readme := readReadme(t)
	if want := fmt.Sprintf("%d manifests and %d OVDB.md documents have their facts compared", compared, comparedMd); !strings.Contains(readme, want) {
		t.Errorf("README does not state %q", want)
	}
	// The README table: field, where the Directory reads it, which fact carries it.
	rows := map[string]string{}
	for _, m := range readmeField.FindAllStringSubmatch(readme, -1) {
		rows[m[1]] = m[3]
		var lines []int
		for _, n := range strings.Split(m[2], ", ") {
			line, _ := strconv.Atoi(n)
			lines = append(lines, line)
		}
		if !slices.Equal(lines, facts.Reads[m[1]]) {
			t.Errorf("README says directory.mjs reads %s at lines %v; the generator's table says %v", m[1], lines, facts.Reads[m[1]])
		}
	}
	for _, field := range facts.Fields {
		if rows[field] != factFields[field] || factFields[field] == "" {
			t.Errorf("field %q: README names fact %q, the test %q", field, rows[field], factFields[field])
		}
		if _, ok := factValues(Manifest{})[field]; !ok {
			t.Errorf("field %q has no value in factValues", field)
		}
	}
	if len(rows) != len(facts.Fields) {
		t.Errorf("README lists %d fields, the generator %d", len(rows), len(facts.Fields))
	}
	// Every exported field of the facts structs is carried by a row of the table.
	carried := map[string]bool{}
	for _, name := range rows {
		carried[strings.SplitN(name, ".", 2)[0]] = true
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(Manifest{})} {
		for i := 0; i < typ.NumField(); i++ {
			if name := typ.Field(i).Name; name != "Read" && !carried[name] {
				t.Errorf("Manifest.%s is in no row of the README table", name)
			}
		}
	}
}

// Presence is held for every manifest the Go reader reads, accepted or not: the
// fields the Directory's parsed manifest has (the key is written) are the facts
// that are Present, so a written value that is refused is never an absent fact.
func TestPresenceAgreesWithTheReference(t *testing.T) {
	_, _, manifests, _ := loadReference(t)
	var facts factsFile
	readGolden(t, "directory.facts.json", &facts)
	if len(facts.Presence) != len(manifests) || len(facts.Fields) < 27 {
		t.Fatalf("the facts golden holds %d presence masks for %d manifests", len(facts.Presence), len(manifests))
	}
	names := facts.Fields[:27]
	compared, refused := 0, 0
	for i, c := range manifests {
		m, findings := CheckManifest(c.Document, "ovdb.yaml", Directory)
		if !m.Read {
			continue
		}
		mask := facts.Presence[i]
		if mask == "" {
			mask = facts.PBases[c.Base]
		}
		if mask == "-" {
			t.Fatalf("Go reads a document that the reference cannot: %q", c.Document)
		}
		want, err := strconv.ParseUint(mask, 16, 32)
		if err != nil {
			t.Fatalf("presence %q: %v", mask, err)
		}
		values := factValues(m)
		var got uint64
		for bit, name := range names {
			if values[name] != nil {
				got |= 1 << bit
			}
		}
		if got != want {
			t.Fatalf("%s document %d: the facts that are present are %x, the Directory's parsed manifest has %x (fields %v):\n%q", c.Family, i, got, want, names, c.Document)
		}
		if len(findings) > 0 {
			refused++
		}
		compared++
	}
	if compared < 3000 || refused < 2000 {
		t.Fatalf("presence compared on %d manifests, %d of them refused", compared, refused)
	}
	t.Logf("presence compared on %d manifests, %d of them refused", compared, refused)
	if want := fmt.Sprintf("presence of every field is compared on %d manifests, %d of them refused", compared, refused); !strings.Contains(strings.Join(strings.Fields(readReadme(t)), " "), want) {
		t.Errorf("README does not state %q", want)
	}
}

// Check judges a pair as its two documents are judged alone.
func TestCheckPairAgreesWithTheParts(t *testing.T) {
	corpus, _, manifests, mds := loadReference(t)
	for i := 0; i < len(manifests) && i < 2000; i += 7 {
		md := mds[i%len(mds)]
		mdOK, _ := mdAccepted(md.Document, "ovdb.yaml")
		mOK, _ := manifestAccepted(manifests[i].Document)
		got := Check(md.Document, "ovdb.yaml", manifests[i].Document, Directory)
		if got.OK() != (mdOK && mOK) {
			t.Fatalf("pair %d: Check says %v, the parts say OVDB.md %v, manifest %v", i, got.OK(), mdOK, mOK)
		}
		assertFindings(t, got.Findings)
	}
	for _, pair := range [][2]string{{"chinook-md", "chinook-yaml"}, {"fixture-md", "fixture-yaml"}, {"hoster-md", "hoster-yaml"}} {
		for _, flags := range []string{"", "crlf", "bom"} {
			md := rebuild(t, corpus.Bases[pair[0]], []json.RawMessage{json.RawMessage("0"), json.RawMessage("0")}, strings.ReplaceAll(flags, "bom", ""))
			doc := rebuild(t, corpus.Bases[pair[1]], []json.RawMessage{json.RawMessage("0"), json.RawMessage("0")}, flags)
			for _, profile := range []Profile{Directory, Publisher} {
				if r := Check(md, "ovdb.yaml", doc, profile); !r.OK() {
					t.Errorf("%s with %s (%q, profile %d): %v", pair[0], pair[1], flags, profile, r.Findings)
				}
			}
		}
	}
}

// The Publisher profile refuses every document and every pair that the Directory
// profile refuses, over the whole corpus of both goldens.
func TestPublisherRefusesWhatTheDirectoryRefuses(t *testing.T) {
	corpus, _, manifests, mds := loadReference(t)
	goodMd := []byte("---\novdb: 1\npublish: [./ovdb.yaml]\n---\n")
	chinook := []byte(corpus.Bases["chinook-yaml"])
	// pair holds both profiles to the same three inputs: it counts the pairs the Directory profile refuses.
	pairs, refusedPairs := 0, 0
	pair := func(md []byte, path string, manifest []byte) {
		pairs++
		if Check(md, path, manifest, Directory).OK() {
			return
		}
		refusedPairs++
		if Check(md, path, manifest, Publisher).OK() {
			t.Fatalf("the pair %q, %q, %q is refused by the Directory profile and accepted by the Publisher profile", md, path, manifest)
		}
	}
	refusedManifests, refusedMds := 0, 0
	for _, c := range manifests {
		_, d := CheckManifest(c.Document, "ovdb.yaml", Directory)
		_, p := CheckManifest(c.Document, "ovdb.yaml", Publisher)
		if len(d) > 0 {
			refusedManifests++
			if len(p) == 0 {
				t.Fatalf("the manifest is refused by the Directory profile and accepted by the Publisher profile (%s): %q", c.Family, c.Document)
			}
		}
		pair(goodMd, "ovdb.yaml", c.Document)
	}
	for _, c := range mds {
		_, d := CheckOVDBMd(c.Document, Directory)
		_, p := CheckOVDBMd(c.Document, Publisher)
		if len(d) > 0 {
			refusedMds++
			if len(p) == 0 {
				t.Fatalf("the OVDB.md is refused by the Directory profile and accepted by the Publisher profile (%s): %q", c.Family, c.Document)
			}
		}
		pair(c.Document, c.Path, chinook)
		pair(c.Document, "ovdb.yaml", chinook)
	}
	if refusedManifests < 3000 || refusedMds < 200 || refusedPairs < 3000 {
		t.Fatalf("only %d manifests, %d OVDB.md documents and %d pairs are refused by the Directory profile", refusedManifests, refusedMds, refusedPairs)
	}
	t.Logf("cross-profile: the Directory profile refuses %d manifests, %d OVDB.md documents and %d of %d pairs", refusedManifests, refusedMds, refusedPairs, pairs)
	want := fmt.Sprintf("the Publisher profile refuses every one of the %d manifests, %d OVDB.md documents and %d pairs (of %d) that the Directory profile refuses", refusedManifests, refusedMds, refusedPairs, pairs)
	if !strings.Contains(strings.Join(strings.Fields(readReadme(t)), " "), want) {
		t.Errorf("README does not state %q", want)
	}
}

// madeByRepo are the rules that need files or input, with the rules that make them (package repo, and for the meaning file Judge.Meaning of this
// package): the README's table has the same cell in the column of the rule of Go. Every rule of the table is made.
var madeByRepo = map[string]string{
	"the optional attachment has a locally checked structural precheck; external closure remains partial": "package repo: structural metadata associations and required format3 raw data proofs; no canonical admission",
	"a JSON database descriptor uses its separate pinned schema":                                          "outside the legacy Go manifest profile (`manifest-format`)",
	"the repository can be read at HEAD (it is a git repository with a commit)":                           "package repo: `repo-unreadable`, `repo-no-commit`, `repo-bare`, `repo-subdirectory`, `repo-git-version`",
	"OVDB.md is a tracked regular file":                                                                   "package repo: `repo-ovdbmd`",
	"OVDB.md can be read (and is not over 16 MB)":                                                         "package repo: `document-size`, `repo-object-missing`, `repo-object-corrupt`, `repo-partial-clone`, `repo-alternates`",
	"every manifest that OVDB.md lists is a tracked regular file":                                         "package repo: `repo-manifest`",
	"every manifest that OVDB.md lists can be read (and is not over 16 MB)":                               "package repo: `document-size`, `repo-object-missing`, `repo-object-corrupt`, `repo-partial-clone`, `repo-alternates`",
	"every manifest that OVDB.md lists is checked":                                                        "package repo: `manifest.Judge`",
	"every file a manifest names is a tracked regular file":                                               "package repo: `repo-file`",
	"every file a manifest names can be read (and is not over 16 MB)":                                     "package repo: `repo-file-size`, `repo-object-missing`, `repo-object-corrupt`, `repo-partial-clone`, `repo-alternates`",
	"publisher.repository is the repository the check is run in (the --repository option)":                "package repo: `repo-repository`",
	"the model file is JSON with a module name and entities":                                              "package repo: `repo-model-json`, `repo-model-depth`, `repo-model-module`, `repo-model-entities`",
	"own form: model.name is the module of the model file":                                                "package repo: `repo-model-name`",
	"own form: the module of model.address is the model file's":                                           "package repo: `repo-model-address`",
	"the meaning file is YAML whose id and license are the manifest's":                                    "package manifest, `Judge.Meaning`: `meaning-shape`, `meaning-id`, `meaning-license`, and the reader's rules",
	"the meaning file's models: entry for the module is model.hcl":                                        "package manifest, `Judge.Meaning`: `meaning-models`, `meaning-hcl`",
	"own form: recordsets are exactly the model's entities":                                               "package repo: `repo-recordsets`",
}

var readmeRule = regexp.MustCompile(`(?m)^\| (.+) \| (documents|files|input|dropped) \| (.+) \| ovdb-manifest\.mjs ([0-9, ]+) \|$`)

// The README lists every rule that the Chinook checker adds to the Directory's, who
// decides it (the two documents, other files, or the caller's input), the rule of
// Go that makes it, and the lines of the checker: the generator's own table.
func TestPublisherRulesTable(t *testing.T) {
	var golden struct {
		Rules []struct {
			ID    string `json:"id"`
			Who   string `json:"who"`
			Go    string `json:"go"`
			Lines []int  `json:"lines"`
		} `json:"rules"`
		NeedsFiles map[string]map[string]int `json:"needsFiles"`
	}
	readGolden(t, "publisher.verdicts.json", &golden)
	rows := map[string][4]string{}
	for _, m := range readmeRule.FindAllStringSubmatch(readReadme(t), -1) {
		rows[m[1]] = [4]string{m[2], m[3], m[4]}
	}
	documents, other, dropped := 0, 0, 0
	for _, rule := range golden.Rules {
		row, ok := rows[rule.ID]
		goCell := madeByRepo[rule.ID]
		if rule.Go != "" {
			goCell = "`" + strings.ReplaceAll(rule.Go, ", ", "`, `") + "`"
		}
		if rule.Who == "dropped" {
			goCell = "none (D0)"
		}
		var lines []string
		for _, n := range rule.Lines {
			lines = append(lines, strconv.Itoa(n))
		}
		if !ok || row[0] != rule.Who || row[1] != goCell || row[2] != strings.Join(lines, ", ") {
			t.Errorf("rule %q: README row %v, want %s, %s, %s", rule.ID, row, rule.Who, goCell, strings.Join(lines, ", "))
		}
		switch rule.Who {
		case "dropped":
			dropped++
			if rule.Go != "" {
				t.Errorf("a rule that Go no longer has names a rule of Go: %q", rule.ID)
			}
		case "documents":
			documents++
			if rule.Go == "" {
				t.Errorf("a rule decided by the documents has no rule of Go: %q", rule.ID)
			}
		default:
			other++
			if rule.Go != "" {
				t.Errorf("a rule that needs other files or input has a rule of Go: %q", rule.ID)
			}
		}
	}
	if len(rows) != len(golden.Rules) || documents < 15 || other < 8 || dropped != 1 {
		t.Errorf("README lists %d rules, the generator %d (%d by the documents, %d by other files or input, %d dropped)", len(rows), len(golden.Rules), documents, other, dropped)
	}
	// What only other files decide, recorded as classes with the number of accepted cases that a repository of wrong or missing files would refuse.
	classes := map[string]bool{}
	for _, set := range golden.NeedsFiles {
		for class := range set {
			classes[class] = true
		}
	}
	flat := strings.Join(strings.Fields(readReadme(t)), " ")
	for class := range classes {
		want := fmt.Sprintf("%s (%d manifests, %d OVDB.md documents)", class, golden.NeedsFiles["manifest"][class], golden.NeedsFiles["md"][class])
		if !strings.Contains(flat, want) {
			t.Errorf("README does not state the class of refusals that need other files %q", want)
		}
	}
	if len(classes) < 5 {
		t.Errorf("only %d classes of refusals need other files", len(classes))
	}
}
