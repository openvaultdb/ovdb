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
	Base     string
	Document []byte
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
	for i, raw := range corpus.ManifestRaw {
		var base, flags string
		var patch []json.RawMessage
		var family int
		decode(t, raw, &base, &patch, &flags, &family)
		if _, ok := corpus.Bases[base]; !ok || family < 0 || family >= len(corpus.Families) || i >= len(verdicts.Manifest.Verdicts) {
			t.Fatalf("manifest case %d names base %q, family %d", i, base, family)
		}
		manifests = append(manifests, referenceCase{Base: base, Document: rebuild(t, corpus.Bases[base], patch, flags), Family: corpus.Families[family], Verdict: verdictOf(t, verdicts.Manifest.Verdicts[i])})
	}
	for i, raw := range corpus.MdRaw {
		var base, flags, path string
		var patch []json.RawMessage
		var family int
		decode(t, raw, &base, &patch, &flags, &path, &family)
		if _, ok := corpus.Bases[base]; !ok || family < 0 || family >= len(corpus.Families) || i >= len(verdicts.Md.Verdicts) {
			t.Fatalf("OVDB.md case %d names base %q, family %d", i, base, family)
		}
		mds = append(mds, referenceCase{Base: base, Document: rebuild(t, corpus.Bases[base], patch, flags), Path: path, Family: corpus.Families[family], Verdict: verdictOf(t, verdicts.Md.Verdicts[i])})
	}
	if len(manifests) != len(verdicts.Manifest.Verdicts) || len(mds) != len(verdicts.Md.Verdicts) {
		t.Fatalf("the corpus and the verdicts disagree on the number of documents: %d and %d manifests, %d and %d OVDB.md", len(manifests), len(verdicts.Manifest.Verdicts), len(mds), len(verdicts.Md.Verdicts))
	}
	for _, set := range []struct {
		verdicts verdictSet
		name     string
	}{{verdicts.Manifest, "manifest"}, {verdicts.Md, "OVDB.md"}} {
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
	if len(want) != 4 {
		t.Fatalf("digests.json holds %d digests, want 4", len(want))
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

// stricterKinds are the ways in which the Go functions are stricter than the
// Directory's JavaScript on the corpus, each with the reason; the README of this
// package lists the same kinds, and a test holds the two together. A refusal of a
// document that the reference accepts is classified by kindOf, or the test fails;
// a kind that no document of the corpus shows fails it too.
var stricterKinds = map[string]string{
	"document-size":     "A document over 262144 bytes (MaxDocumentBytes) is refused before it is read; the references read files of any size.",
	"length-address":    "An address over 2048 bytes, or one that names a repository over 247 bytes; the references' address expressions have no bound.",
	"length-entry":      "A publish entry whose path after ./ is over 1024 bytes; the references have no bound.",
	"length-path":       "A file path over 1024 bytes in model.modelspec, model.hcl or meaning.file; the references have no bound.",
	"length-repository": "A publisher.repository over 255 bytes; the references have no bound.",
	"punycode":          "A homepage host with an xn-- label that does not spell Latin-1 letters (see the README of package rules); Node accepts the label.",
	"url-length":        "A URL longer than rules.MaxURLLength (2048 bytes) is refused; the reference has no bound.",
	"yaml":              "The reader accepts a subset of YAML, and a structure it cannot place (a flow collection as a key, as in [a]: x) is refused; the reference reads it.",
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

// kindOf names the kind of a refusal that the reference does not make.
func kindOf(f Finding) string {
	switch {
	case f.Rule == "manifest-url" && strings.Contains(f.Message, "longer than"):
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

func manifestAccepted(doc []byte) (bool, Finding) {
	_, findings := CheckManifest(doc, "ovdb.yaml", Directory)
	if len(findings) == 0 {
		return true, Finding{}
	}
	return false, findings[0]
}

func mdAccepted(doc []byte, path string) (bool, Finding) {
	md, findings := CheckOVDBMd(doc, Directory)
	if len(findings) == 0 && md.Lists(path) {
		return true, Finding{}
	}
	if len(findings) == 0 {
		return false, Finding{Rule: "ovdbmd-unlisted"}
	}
	return false, findings[0]
}

var readmeRow = regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (\\d+) \\| (.+) \\|$")

func TestReferenceDirectory(t *testing.T) {
	corpus, verdicts, manifests, mds := loadReference(t)
	var total accounting
	for _, c := range manifests {
		ok, first := manifestAccepted(c.Document)
		if !total.record(c, ok, first) {
			t.Errorf("manifest accepted where the Directory refuses (%s): %q", c.Family, c.Document)
		}
		checkGoFindings(t, c.Document)
	}
	for _, c := range mds {
		ok, first := mdAccepted(c.Document, c.Path)
		if !total.record(c, ok, first) {
			t.Errorf("OVDB.md accepted where the Directory refuses (%s, path %q): %q", c.Family, c.Path, c.Document)
		}
		_, findings := CheckOVDBMd(c.Document, Directory)
		assertFindings(t, findings)
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
	if verdicts.Profile != "directory" || verdicts.Thrown != 0 {
		t.Errorf("verdicts are of profile %q, and the reference threw on %d documents", verdicts.Profile, verdicts.Thrown)
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
	readme := readReadme(t)
	rows := map[string]int{}
	for _, m := range readmeRow.FindAllStringSubmatch(readme, -1) {
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
		if !strings.Contains(readme, want) {
			t.Errorf("README does not state %q", want)
		}
	}
	t.Logf("%d documents: agree %d, stricter %d, looser %d (mined edits %d of %d)", len(manifests)+len(mds), total.agree, total.stricter, total.looser, corpus.MinedEdits.Applied, corpus.MinedEdits.Found)
	for _, kind := range slices.Sorted(maps.Keys(total.kinds)) {
		t.Logf("stricter %-20s %5d  e.g. %s", kind, total.kinds[kind], total.examples[kind])
	}
}

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

func checkGoFindings(t testing.TB, doc []byte) {
	t.Helper()
	_, findings := CheckManifest(doc, "ovdb.yaml", Directory)
	assertFindings(t, findings)
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
		"recordsets":                 state(m.Recordsets, func(names []string) any { return names }),
		"recordsets_partial":         state(m.RecordsetsPartial, func(b bool) any { return b }),
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
	"recordsets": "Recordsets", "recordsets_partial": "RecordsetsPartial", "form": "Form",
	"model.address.repository": "ModelAddress.Repository", "model.address.module": "ModelAddress.Module", "model.address.ref": "ModelAddress.Ref",
	"meaning.address.repository": "MeaningAddress.Repository", "meaning.address.ref": "MeaningAddress.Ref",
}

var readmeField = regexp.MustCompile("(?m)^\\| `([a-z._]+)` \\| directory\\.mjs ([0-9, ]+) \\| `([A-Za-z.]+)` \\|$")

// Every fact is compared with what the Directory's own code derives from the same
// document, for every document both accept, and the README's table of the fields
// the Directory reads is the generator's own list of the lines that read them.
func TestFactsAgreeWithTheReference(t *testing.T) {
	_, verdicts, manifests, mds := loadReference(t)
	var facts factsFile
	readGolden(t, "directory.facts.json", &facts)

	accepted, compared := 0, 0
	for i, c := range manifests {
		if verdicts.Manifest.Verdicts[i] != '1' {
			continue
		}
		index := accepted
		accepted++
		m, findings := CheckManifest(c.Document, "ovdb.yaml", Directory)
		if len(findings) > 0 {
			continue
		}
		if index >= len(facts.Manifest) || facts.Bases[c.Base] == nil {
			t.Fatalf("the facts golden has no entry %d, or no base %q", index, c.Base)
		}
		want := maps.Clone(facts.Bases[c.Base])
		maps.Copy(want, facts.Manifest[index])
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(factValues(m))
		if !bytes.Equal(wantJSON, gotJSON) {
			t.Fatalf("facts differ from the Directory's for %s document %d:\n%q\n  go:  %s\n  ref: %s", c.Family, i, c.Document, gotJSON, wantJSON)
		}
		if !m.Read {
			t.Fatalf("an accepted manifest was not read: %q", c.Document)
		}
		compared++
	}
	if accepted != len(facts.Manifest) || compared < 800 {
		t.Fatalf("the facts golden holds %d entries for %d accepted manifests, and %d were compared", len(facts.Manifest), accepted, compared)
	}

	listed, comparedMd := 0, 0
	for i, c := range mds {
		if verdicts.Md.Verdicts[i] != '1' {
			continue
		}
		index := listed
		listed++
		md, findings := CheckOVDBMd(c.Document, Directory)
		if len(findings) > 0 {
			continue
		}
		if !slices.Equal(md.Entries, facts.Md[index]) || !slices.Equal(md.Publish.Value, facts.Md[index]) || !md.Version.Usable() || md.Version.Value != "1" || !md.Read {
			t.Fatalf("OVDB.md facts %+v differ from the Directory's set %v for %q", md, facts.Md[index], c.Document)
		}
		comparedMd++
	}
	if listed != len(facts.Md) || comparedMd < 80 {
		t.Fatalf("the facts golden holds %d lists for %d accepted OVDB.md, and %d were compared", len(facts.Md), listed, comparedMd)
	}

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
	if len(facts.Presence) != len(manifests) || len(facts.Fields) < 26 {
		t.Fatalf("the facts golden holds %d presence masks for %d manifests", len(facts.Presence), len(manifests))
	}
	names := facts.Fields[:26]
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
			if r := Check(md, "ovdb.yaml", doc, Directory); !r.OK() {
				t.Errorf("%s with %s (%q): %v", pair[0], pair[1], flags, r.Findings)
			}
		}
	}
}
