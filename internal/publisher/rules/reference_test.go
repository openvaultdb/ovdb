package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// This file is the proof that no function of this package accepts a string that
// a JavaScript reference refuses. The references' verdicts over a generated
// matrix are committed in testdata/reference/matrix.golden.json (made by
// generate.mjs, which runs the reference functions at the pinned commits); the
// test reads them and judges every verdict of the Go functions against them.
// It starts no process and needs no network, only Go.

const (
	goldenPath = "testdata/reference/matrix.golden.json"
	readmePath = "README.md"

	// referencesPath is the one place that names the references and their commits; the generators import it.
	referencesPath = "../references.mjs"
)

var pinPattern = regexp.MustCompile(`(?m)^\s+(directory|chinookdb): \{ repository: '[^']+', commit: '([0-9a-f]{40})' \},$`)

// pinnedCommits reads the commit of each reference from references.mjs: the tests hold the golden and the README to it.
func pinnedCommits(t testing.TB) map[string]string {
	t.Helper()
	text, err := os.ReadFile(referencesPath)
	if err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{}
	for _, m := range pinPattern.FindAllStringSubmatch(string(text), -1) {
		pins[m[1]] = m[2]
	}
	if len(pins) != 2 {
		t.Fatalf("%s does not name the commits of directory and chinookdb in the expected form: %v", referencesPath, pins)
	}
	return pins
}

// input is a string of the golden: plain text, or {"r": [prefix, unit, count,
// suffix]} for a long one.
type input string

func (in *input) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*in = input(text)
		return nil
	}
	var long struct {
		R [4]json.RawMessage `json:"r"`
	}
	if err := json.Unmarshal(data, &long); err != nil {
		return err
	}
	var prefix, unit, suffix string
	var count int
	for at, target := range []any{&prefix, &unit, &count, &suffix} {
		if err := json.Unmarshal(long.R[at], target); err != nil {
			return err
		}
	}
	*in = input(prefix + strings.Repeat(unit, count) + suffix)
	return nil
}

type span [2]int

type goldenFile struct {
	Format     string `json:"format"`
	Node       string `json:"node"`
	References map[string]struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
	} `json:"references"`
	ReferenceCalls struct {
		Thrown int `json:"thrown"`
	} `json:"referenceCalls"`
	MatrixSize  int               `json:"matrixSize"`
	SweepRanges map[string][]span `json:"sweepRanges"`
	Sweeps      []struct {
		Name     string            `json:"name"`
		Fn       string            `json:"fn"`
		Span     string            `json:"span"`
		Template string            `json:"template"`
		Accepted map[string][]span `json:"accepted"`
	} `json:"sweeps"`
	Products []struct {
		Name     string `json:"name"`
		Fn       string `json:"fn"`
		Template string `json:"template"`
		Alphabet string `json:"alphabet"`
		Min      int    `json:"min"`
		Max      int    `json:"max"`
		Size     int    `json:"size"`
		Stored   map[string]struct {
			Accepts []string `json:"accepts"`
			Refuses []string `json:"refuses"`
		} `json:"stored"`
	} `json:"products"`
	Lists []struct {
		Fns   []string             `json:"fns"`
		Cases [][2]json.RawMessage `json:"cases"`
	} `json:"lists"`
	Claims struct {
		Addresses []input  `json:"addresses"`
		Relations []string `json:"relations"`
	} `json:"claims"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var golden goldenFile
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// goFns are the Go functions under test, by the name the golden gives them.
var goFns = map[string]func(string) bool{
	"url":          func(s string) bool { return PublicHTTPSURL(s) == nil },
	"url-template": func(s string) bool { return PublicHTTPSURLTemplate(s) == nil },
	"homepage":     func(s string) bool { return Homepage(s) == nil },
	"id":           IsID,
	"commit":       IsCommit,
	"local-id":     IsLocalID,
	"repository":   func(s string) bool { _, ok := RepositoryKey(s); return ok },
	"path":         IsRepositoryPath,
	"publish":      IsPublishEntry,
	"engine":       IsEngine,
	"licence":      IsLicenceID,
	"text":         func(s string) bool { return !IsBlank(s) },
	// The page of a recordset, from a template that is a plain public URL (see recordsetPageTemplate in the generator).
	"global-id":      func(s string) bool { return GlobalDatabaseID(s) == nil },
	"recordset-name": func(s string) bool { return RecordsetName(s) == nil },
	"recordset-page": func(s string) bool { return RecordsetPage(recordsetPageTemplate, s) == nil },
}

const recordsetPageTemplate = "https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}"

// goErrs are the URL functions again, for the message of a refusal.
var goErrs = map[string]func(string) error{
	"url": PublicHTTPSURL, "url-template": PublicHTTPSURLTemplate, "homepage": Homepage,
	"global-id":      GlobalDatabaseID,
	"recordset-name": RecordsetName,
	"recordset-page": func(s string) error { return RecordsetPage(recordsetPageTemplate, s) },
}

// A difference is lifted when the same input, with the one limit that the
// difference names taken away from the Go function, is accepted: that is how a
// test proves that a recorded difference is exactly that limit and nothing else.
type difference struct {
	kind    string
	applies func(fn string) bool
	lifted  func(fn, s string) bool
}

const unbounded = 1 << 30

func urlFn(fn string) bool {
	return fn == "url" || fn == "url-template" || fn == "homepage" || fn == "global-id"
}

func urlLift(o options) func(fn, s string) bool {
	return func(fn, s string) bool {
		o.template = fn == "url-template"
		o.encoded, o.example = fn == "global-id", fn == "global-id"
		if o.maxLen == 0 {
			o.maxLen = MaxURLLength
		}
		if fn == "homepage" {
			return checkHomepage(s, o) == nil
		}
		return checkURL(s, o) == nil
	}
}

// differences are the recorded ways in which a Go function is stricter than a
// reference. README.md has a row for each; a case of the matrix that Go refuses
// and a reference accepts must be explained by one of them, and each of them
// must be needed by at least one case.
var differences = []difference{
	{"url-length", urlFn, urlLift(options{maxLen: unbounded})},
	{"punycode-decoded-hyphens", urlFn, urlLift(options{puny: punyAllowNested})},
	{"punycode-other-text", urlFn, urlLift(options{puny: punyWellFormed})},
	{"punycode-malformed", urlFn, urlLift(options{puny: punyUnchecked})},
	{"repository-length", func(fn string) bool { return fn == "repository" }, func(_, s string) bool { _, ok := repositoryKey(s, unbounded); return ok }},
	{"path-length", func(fn string) bool { return fn == "path" || fn == "publish" }, func(fn, s string) bool {
		if fn == "publish" {
			rest, ok := strings.CutPrefix(s, "./")
			return ok && isRepositoryPath(rest, unbounded)
		}
		return isRepositoryPath(s, unbounded)
	}},
}

func differenceKinds() []string {
	kinds := []string{"claim-incomparable"}
	for _, d := range differences {
		kinds = append(kinds, d.kind)
	}
	sort.Strings(kinds)
	return kinds
}

type tally struct {
	total        int
	agree        int
	agreeAccept  int
	refsDisagree int
	stricter     map[string]int
	example      map[string]string
	violations   []string
	unexplained  []string
	badMessages  []string
}

func newTally() *tally {
	return &tally{stricter: map[string]int{}, example: map[string]string{}}
}

func shorten(s string) string {
	if len(s) > 80 {
		return fmt.Sprintf("%.60q...(%d bytes)", s, len(s))
	}
	return fmt.Sprintf("%q", s)
}

// judge compares one Go verdict with the references' ('1' accepts, '0' refuses,
// '-' the reference has no such rule).
func (ty *tally) judge(fn, s string, goAccepts bool, verdicts string) {
	ty.total++
	accepting, refusing := 0, 0
	for i := 0; i < len(verdicts); i++ {
		switch verdicts[i] {
		case '1':
			accepting++
		case '0':
			refusing++
		}
	}
	if accepting > 0 && refusing > 0 {
		ty.refsDisagree++
	}
	switch {
	case goAccepts && refusing > 0:
		if len(ty.violations) < 20 {
			ty.violations = append(ty.violations, fmt.Sprintf("%s accepts %s, a reference refuses it", fn, shorten(s)))
		}
	case !goAccepts && refusing == 0:
		ty.recordStricter(fn, s)
		ty.checkMessage(fn, s)
	default:
		ty.agree++
		if goAccepts {
			ty.agreeAccept++
		} else {
			ty.checkMessage(fn, s)
		}
	}
}

// checkMessage holds the message of every refusal of the matrix to the rule that
// no message has a control character, an escape or a line break, and none is
// long: input reaches it only through show.
func (ty *tally) checkMessage(fn, s string) {
	check := goErrs[fn]
	if check == nil {
		return
	}
	err := check(s)
	if err == nil {
		return
	}
	if msg := err.Error(); !printableASCII(msg) || len(msg) > 400 {
		if len(ty.badMessages) < 20 {
			ty.badMessages = append(ty.badMessages, fmt.Sprintf("%s of %s: message %s", fn, shorten(s), shorten(msg)))
		}
	}
}

func (ty *tally) recordStricter(fn, s string) {
	var explained []string
	for _, d := range differences {
		if d.applies(fn) && d.lifted(fn, s) {
			explained = append(explained, d.kind)
		}
	}
	// The narrowest kind explains a case: a label that only the hyphen rule
	// refuses is that kind, whatever else would lift it too; one that is valid
	// punycode of other text is "other text", not "malformed".
	for _, narrower := range [][2]string{{"punycode-decoded-hyphens", "punycode-other-text"}, {"punycode-decoded-hyphens", "punycode-malformed"}, {"punycode-other-text", "punycode-malformed"}} {
		if slices.Contains(explained, narrower[0]) {
			explained = slices.DeleteFunc(explained, func(kind string) bool { return kind == narrower[1] })
		}
	}
	if len(explained) != 1 {
		ty.unexplained = append(ty.unexplained, fmt.Sprintf("%s refuses %s, which a reference accepts, and %d recorded differences explain it: %v", fn, shorten(s), len(explained), explained))
		return
	}
	kind := explained[0]
	ty.stricter[kind]++
	if _, seen := ty.example[kind]; !seen {
		ty.example[kind] = fmt.Sprintf("%s %s", fn, shorten(s))
	}
}

// cpString is the character U+0000..U+FFFF as the JavaScript strings of the
// matrix hold it; a lone surrogate, which is not a Unicode scalar value, is the
// three bytes that WTF-8 gives it (Go sees invalid UTF-8, the JavaScript a lone
// UTF-16 code unit; both are non-ASCII, which every rule refuses).
func cpString(code int) string {
	if code >= 0xD800 && code <= 0xDFFF {
		return string([]byte{0xED, byte(0xA0 | (code>>6)&0x1F), byte(0x80 | code&0x3F)})
	}
	return string(rune(code))
}

func inSpans(spans []span, code int) bool {
	for _, s := range spans {
		if code >= s[0] && code <= s[1] {
			return true
		}
	}
	return false
}

var references = []string{"directory", "chinookdb"}

func TestReferenceMatrix(t *testing.T) {
	golden := loadGolden(t)
	if golden.Format != "ovdb-publisher-rules-reference/1" {
		t.Fatalf("golden format %q", golden.Format)
	}
	for name, pin := range pinnedCommits(t) {
		if golden.References[name].Commit != pin {
			t.Errorf("the golden was made from %s at %s, not the pinned %s", name, golden.References[name].Commit, pin)
		}
	}
	if golden.ReferenceCalls.Thrown != 0 {
		t.Errorf("a reference threw on %d inputs while the golden was made", golden.ReferenceCalls.Thrown)
	}
	ty := newTally()

	// The sweeps: one character, U+0000 to U+FFFF, in a template.
	seenShapes := map[string]bool{}
	for _, sweep := range golden.Sweeps {
		seenShapes[sweep.Name] = true
		goFn := goFns[sweep.Fn]
		if goFn == nil {
			t.Fatalf("sweep %s: no Go function %q", sweep.Name, sweep.Fn)
		}
		spans := golden.SweepRanges[sweep.Span]
		for _, sp := range spans {
			for code := sp[0]; code <= sp[1]; code++ {
				s := strings.Replace(sweep.Template, "{C}", cpString(code), 1)
				var verdicts strings.Builder
				for _, ref := range references {
					accepted, has := sweep.Accepted[ref]
					switch {
					case !has:
						verdicts.WriteByte('-')
					case inSpans(accepted, code):
						verdicts.WriteByte('1')
					default:
						verdicts.WriteByte('0')
					}
				}
				ty.judge(sweep.Fn, s, goFn(s), verdicts.String())
			}
		}
	}
	for _, shape := range []string{"url-host", "url-path", "url-before-scheme", "url-after-end", "template-host", "template-path-after", "template-path-before", "template-before-scheme", "template-after-end", "homepage-host", "homepage-path", "id-middle", "commit-first", "repository-org", "path-middle", "publish-middle", "engine-middle", "licence-middle"} {
		if !seenShapes[shape] {
			t.Errorf("the golden has no sweep %q", shape)
		}
	}
	if got := golden.SweepRanges["wide"]; len(got) != 1 || got[0] != (span{0, 0xFFFF}) {
		t.Errorf("the wide sweep is %v, want every character U+0000 to U+FFFF", got)
	}

	// The products: every string of an alphabet between two lengths.
	for _, product := range golden.Products {
		goFn := goFns[product.Fn]
		if goFn == nil {
			t.Fatalf("product %s: no Go function %q", product.Name, product.Fn)
		}
		sets := map[string]map[string]bool{}
		accepts := map[string]bool{}
		for ref, stored := range product.Stored {
			set := map[string]bool{}
			for _, text := range append(slices.Clone(stored.Accepts), stored.Refuses...) {
				set[text] = true
			}
			sets[ref], accepts[ref] = set, stored.Accepts != nil || len(stored.Refuses) == 0
		}
		size := 0
		forEachString([]rune(product.Alphabet), product.Min, product.Max, func(text string) {
			size++
			s := strings.Replace(product.Template, "{S}", text, 1)
			var verdicts strings.Builder
			for _, ref := range references {
				set, has := sets[ref]
				switch {
				case !has:
					verdicts.WriteByte('-')
				case set[text] == accepts[ref]:
					verdicts.WriteByte('1')
				default:
					verdicts.WriteByte('0')
				}
			}
			ty.judge(product.Fn, s, goFn(s), verdicts.String())
			if product.Fn == "repository" && goFn(s) {
				if key, _ := RepositoryKey(s); key != strings.TrimPrefix(s, "https://") {
					t.Errorf("RepositoryKey(%s) = %s: the reference's key is the text after https://", shorten(s), shorten(key))
				}
			}
		})
		if size != product.Size {
			t.Errorf("product %s has %d strings, the golden says %d", product.Name, size, product.Size)
		}
	}

	// The lists: explicit inputs with a verdict per function and reference.
	for _, list := range golden.Lists {
		for _, raw := range list.Cases {
			var in input
			var verdicts string
			if err := json.Unmarshal(raw[0], &in); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw[1], &verdicts); err != nil {
				t.Fatal(err)
			}
			if len(verdicts) != len(list.Fns)*len(references) {
				t.Fatalf("%v: verdicts %q for %s", list.Fns, verdicts, shorten(string(in)))
			}
			for at, fn := range list.Fns {
				s := string(in)
				goFn := goFns[fn]
				if goFn == nil {
					t.Fatalf("no Go function %q", fn)
				}
				ty.judge(fn, s, goFn(s), verdicts[at*len(references):(at+1)*len(references)])
				if fn == "repository" && goFn(s) {
					if key, _ := RepositoryKey(s); key != strings.TrimPrefix(s, "https://") {
						t.Errorf("RepositoryKey(%s) = %s: the reference's key is the text after https://", shorten(s), shorten(key))
					}
				}
			}
		}
	}

	// The claims: the relation of every pair of addresses.
	relations := map[byte]Relation{'a': Apart, 's': Same, 'u': Under, 'o': Over}
	for i, row := range golden.Claims.Relations {
		for j := 0; j < len(row); j++ {
			ty.total++
			a, b := string(golden.Claims.Addresses[i]), string(golden.Claims.Addresses[j])
			got, want := Compare(a, b), relations[row[j]]
			switch got {
			case want:
				ty.agree++
				if got == Apart {
					ty.agreeAccept++
				}
			case Incomparable:
				if _, ok := ClaimedForm(a); ok {
					if _, ok := ClaimedForm(b); ok {
						t.Fatalf("Compare(%s, %s) is Incomparable for plain addresses", shorten(a), shorten(b))
					}
				}
				ty.stricter["claim-incomparable"]++
				if _, seen := ty.example["claim-incomparable"]; !seen {
					ty.example["claim-incomparable"] = fmt.Sprintf("Compare %s %s", shorten(a), shorten(b))
				}
			default:
				ty.violations = append(ty.violations, fmt.Sprintf("Compare(%s, %s) = %v, the reference says %v", shorten(a), shorten(b), got, want))
			}
		}
	}

	if ty.total != golden.MatrixSize {
		t.Errorf("the matrix has %d verdicts, the golden says %d: it is stale or the test misreads it", ty.total, golden.MatrixSize)
	}
	for _, line := range ty.violations {
		t.Error(line)
	}
	for _, line := range ty.unexplained {
		t.Error(line)
	}
	for _, line := range ty.badMessages {
		t.Error(line)
	}
	stricterTotal := 0
	for _, n := range ty.stricter {
		stricterTotal += n
	}
	t.Logf("reference matrix: %d verdicts (node %s); agree %d (%d accepted by both, %d refused by both); stricter %d; Go accepts where a reference refuses %d; references disagree with each other on %d",
		ty.total, golden.Node, ty.agree, ty.agreeAccept, ty.agree-ty.agreeAccept, stricterTotal, len(ty.violations), ty.refsDisagree)
	for _, kind := range differenceKinds() {
		t.Logf("  stricter, %s: %d (first: %s)", kind, ty.stricter[kind], ty.example[kind])
		if ty.stricter[kind] == 0 {
			t.Errorf("the recorded difference %q never happens in the matrix: a recorded difference must be real", kind)
		}
	}
	if ty.refsDisagree != 0 {
		t.Errorf("the two references disagree with each other on %d verdicts; README.md says they never do", ty.refsDisagree)
	}
	if ty.agree+stricterTotal+len(ty.violations) != ty.total {
		t.Errorf("the buckets %d+%d+%d do not add up to %d", ty.agree, stricterTotal, len(ty.violations), ty.total)
	}

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	// README.md records each difference with its count, and the size of the matrix.
	rows := regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (\\d+) \\|").FindAllStringSubmatch(string(readme), -1)
	documented := map[string]string{}
	var kinds []string
	for _, row := range rows {
		documented[row[1]] = row[2]
		kinds = append(kinds, row[1])
	}
	sort.Strings(kinds)
	if want := differenceKinds(); !slices.Equal(kinds, want) {
		t.Errorf("README.md records the differences %v, the tests know %v", kinds, want)
	}
	for kind, count := range ty.stricter {
		if documented[kind] != fmt.Sprint(count) {
			t.Errorf("README.md says %s has %s cases in the matrix, the matrix has %d", kind, documented[kind], count)
		}
	}
	if !strings.Contains(string(readme), fmt.Sprintf("**%d** verdicts", golden.MatrixSize)) {
		t.Errorf("README.md does not state the matrix size, **%d** verdicts", golden.MatrixSize)
	}
	for _, pin := range []string{pinnedCommits(t)["directory"], pinnedCommits(t)["chinookdb"], golden.Node} {
		if !strings.Contains(string(readme), pin) {
			t.Errorf("README.md does not name %s", pin)
		}
	}
}

// forEachString calls visit with every string over alphabet whose length is
// between min and max, shortest first.
func forEachString(alphabet []rune, min, max int, visit func(string)) {
	for length := min; length <= max; length++ {
		digits := make([]int, length)
		for {
			var b strings.Builder
			for _, d := range digits {
				b.WriteRune(alphabet[d])
			}
			visit(b.String())
			at := length - 1
			for at >= 0 && digits[at] == len(alphabet)-1 {
				digits[at] = 0
				at--
			}
			if at < 0 {
				break
			}
			digits[at]++
		}
	}
}

func TestGoldenFunctionsAreAllCovered(t *testing.T) {
	golden := loadGolden(t)
	used := map[string]bool{}
	for _, s := range golden.Sweeps {
		used[s.Fn] = true
	}
	for _, p := range golden.Products {
		used[p.Fn] = true
	}
	for _, l := range golden.Lists {
		for _, fn := range l.Fns {
			used[fn] = true
		}
	}
	for fn := range goFns {
		if !used[fn] {
			t.Errorf("the golden has no verdict for %q", fn)
		}
	}
	for fn := range used {
		if goFns[fn] == nil {
			t.Errorf("the golden has verdicts for %q, which the test cannot run", fn)
		}
	}
}
