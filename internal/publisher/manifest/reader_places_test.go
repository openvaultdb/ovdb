package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/meaninggraph/cli/pkg/meaning"
)

// The reader (github.com/meaninggraph/cli, package meaning) is one component, and every kind of refusal that comes from it, and
// every bound that is checked before it, is a property of the reader and not of a profile: both profiles read their documents
// with it, before any of their own rules run. So what it refuses is recorded once, by place, and holds for both profiles; what
// each profile can show of it is a count per profile, computed here from the corpus and printed in the README.

type readerPlace struct {
	id, rule, fragment string
	// directoryNone and publisherNone say why a profile has no document that its reference accepts and the reader refuses at this
	// place. "proof: <name>" is a claim that proofs below checks; "evidence: <text>" says that the reference refuses every document
	// of the corpus that reaches the place, which the test checks by counting them. A profile with such documents has neither.
	directoryNone, publisherNone string
}

// readerPlaces is every place of the reader that raises a refusal: one row for each line of meaninggraph/cli pkg/meaning (yaml.go,
// yaml_flow.go) that makes a *SyntaxError, and, where a function that several places call makes it (resolvePlain, scanQuoted,
// unescape, keyProblem), one row for each caller that an allowed key can reach. A row says the rule the place raises and a part of its
// message, which tell its refusals from the others; the documents of the corpus that are aimed at it are those of the family
// "reader place: <id>". directoryNone and publisherNone say why a profile has no document that the reference accepts and the reader
// refuses at this place: "proof: <name>" is a claim that a test of this file checks, "evidence: ..." is that the reference refuses every
// document of the corpus that reaches the place (a count the test checks), and neither can be left out.
var readerPlaces = []readerPlace{
	{id: "yaml.go:124", rule: "yaml-limit", fragment: "the file is", directoryNone: "proof: size", publisherNone: "proof: size"},
	{id: "yaml.go:148", rule: "yaml-encoding", fragment: "a byte order mark is followed by", directoryNone: "evidence: the reference counts the mark as a column and refuses too", publisherNone: "evidence: the reference counts the mark as a column and refuses too"},
	{id: "yaml.go:171", rule: "yaml-encoding", fragment: "the file is not UTF-8 text"},
	{id: "yaml.go:173", rule: "yaml-encoding", fragment: "the file holds a NUL character"},
	{id: "yaml.go:178", rule: "yaml-line-ending", fragment: "a carriage return that is not followed by a line feed"},
	{id: "yaml.go:182", rule: "yaml-character", fragment: "is not allowed; write it as an escape"},
	{id: "yaml.go:295", rule: "yaml-tab", fragment: "a tab here is not read: tabs are accepted inside quotes"},
	{id: "yaml.go:320", rule: "yaml-limit", fragment: "collections are nested more than", publisherNone: "proof: depth"},
	{id: "yaml.go:332", rule: "yaml-directive", fragment: "a directive (%YAML or %TAG)"},
	{id: "yaml.go:341", rule: "yaml-tab", fragment: "a tab after --- is not accepted"},
	{id: "yaml.go:345", rule: "yaml-documents", fragment: "text on the --- line"},
	{id: "yaml.go:352", rule: "yaml-documents", fragment: "a document marker (--- or ...)"},
	{id: "yaml.go:365", rule: "yaml", fragment: "this line is indented differently from the lines before it", directoryNone: "evidence: the reference refuses the same layouts", publisherNone: "evidence: the reference refuses the same layouts"},
	{id: "yaml.go:399", rule: "yaml-unsupported", fragment: "a comment line between a key (or a dash)"},
	{id: "yaml.go:436", rule: "yaml-tab", fragment: "a tab after a dash is not accepted"},
	{id: "yaml.go:462", rule: "yaml", fragment: "this line is indented more than the other entries of its mapping", directoryNone: "evidence: the reference refuses the same layouts", publisherNone: "evidence: the reference refuses the same layouts"},
	{id: "yaml.go:464", rule: "yaml-tab", fragment: "a tab after a dash is not accepted", directoryNone: "evidence: the reference refuses a dash where a key belongs", publisherNone: "evidence: the reference refuses a dash where a key belongs"},
	{id: "yaml.go:466", rule: "yaml", fragment: "a sequence entry (-) where", directoryNone: "evidence: the reference refuses a dash where a key belongs", publisherNone: "evidence: the reference refuses a dash where a key belongs"},
	{id: "yaml.go:473", rule: "yaml", fragment: "expected \"key: value\" here", directoryNone: "evidence: the reference refuses a line that is no entry", publisherNone: "evidence: the reference refuses a line that is no entry"},
	{id: "yaml.go:476", rule: "yaml-duplicate-key", fragment: "is repeated in one mapping", directoryNone: "evidence: a repeated key is an error of the reference too", publisherNone: "evidence: a repeated key is an error of the reference too"},
	{id: "yaml.go:509", rule: "yaml", fragment: "this line is indented more than the other entries of its sequence", directoryNone: "evidence: the reference refuses the same layouts", publisherNone: "evidence: the reference refuses the same layouts"},
	{id: "yaml.go:511", rule: "yaml-tab", fragment: "a tab after a dash is not accepted"},
	{id: "yaml.go:557", rule: "yaml-tab", fragment: "a tab between a key and its colon is not accepted"},
	{id: "yaml.go:563", rule: "yaml-tab", fragment: "a tab after a colon is not accepted"},
	{id: "yaml.go:571", rule: "yaml-anchor", fragment: "anchors and aliases (& and *) are not supported"},
	{id: "yaml.go:573", rule: "yaml-tag", fragment: "tags (!) are not supported"},
	{id: "yaml.go:578", rule: "yaml-unsupported", fragment: "explicit keys (?) are not supported"},
	{id: "yaml.go:586", rule: "yaml-tab", fragment: "a tab after a colon is not accepted"},
	{id: "yaml.go:594", rule: "yaml-key", fragment: "is not read as a string (YAML reads it as", publisherNone: "evidence: no key that the checker allows reads as a number, a boolean or null, and it refuses the others"},
	{id: "yaml.go:628", rule: "yaml-key", fragment: "a key written over", directoryNone: "evidence: the reference refuses a block key over 1024 characters too", publisherNone: "evidence: the reference refuses a block key over 1024 characters too"},
	{id: "yaml.go:628@flow key", rule: "yaml-key", fragment: "a key written over"},
	{id: "yaml.go:630", rule: "yaml-anchor", fragment: "merge keys (<<) are not supported", publisherNone: "evidence: << is not a key that the checker allows"},
	{id: "yaml.go:630@flow key", rule: "yaml-anchor", fragment: "merge keys (<<) are not supported", publisherNone: "evidence: << is not a key that the checker allows"},
	{id: "yaml.go:643", rule: "yaml-tab", fragment: "a tab here is not read: a value or a comment does not start with a tab"},
	{id: "yaml.go:646", rule: "yaml-unsupported", fragment: "must start on the line of its key or dash"},
	{id: "yaml.go:654", rule: "yaml-anchor", fragment: "anchors and aliases (& and *) are not supported"},
	{id: "yaml.go:656", rule: "yaml-tag", fragment: "tags (!) are not supported"},
	{id: "yaml.go:658", rule: "yaml", fragment: "a value cannot start with", directoryNone: "evidence: the reference refuses a value that starts so", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml.go:661", rule: "yaml-tab", fragment: "is not accepted; use a space", directoryNone: "evidence: the reference refuses a value that starts so", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml.go:664", rule: "yaml", fragment: "followed by a space cannot start a value here", directoryNone: "evidence: the reference refuses a value that starts so", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml.go:696", rule: "yaml", fragment: "a plain value that continues on a new line cannot continue with"},
	{id: "yaml.go:735", rule: "yaml", fragment: "a colon followed by a space inside a plain value", directoryNone: "evidence: the reference refuses a colon and a space in a plain value", publisherNone: "evidence: the reference refuses a colon and a space in a plain value"},
	{id: "yaml.go:784@block key", rule: "yaml-number", fragment: "is larger than 2^53", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:784@block value", rule: "yaml-number", fragment: "is larger than 2^53", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:784@flow key", rule: "yaml-number", fragment: "is larger than 2^53", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:784@flow value", rule: "yaml-number", fragment: "is larger than 2^53", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:788@block key", rule: "yaml-number", fragment: "is a hexadecimal or octal number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:788@block value", rule: "yaml-number", fragment: "is a hexadecimal or octal number"},
	{id: "yaml.go:788@flow key", rule: "yaml-number", fragment: "is a hexadecimal or octal number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:788@flow value", rule: "yaml-number", fragment: "is a hexadecimal or octal number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@block key", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@block value", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@flow key", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@flow value", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@block key", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@block value", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@flow key", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@flow value", rule: "yaml-number", fragment: "is not a finite number", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:825@block scalar header", rule: "yaml", fragment: "unexpected text", directoryNone: "evidence: the reference refuses text after the end of a value", publisherNone: "evidence: the reference refuses text after the end of a value"},
	{id: "yaml.go:825@flow", rule: "yaml", fragment: "unexpected text", directoryNone: "evidence: the reference refuses text after the end of a value", publisherNone: "evidence: the reference refuses text after the end of a value"},
	{id: "yaml.go:825@quoted value", rule: "yaml", fragment: "unexpected text", directoryNone: "evidence: the reference refuses text after the end of a value", publisherNone: "evidence: the reference refuses text after the end of a value"},
	{id: "yaml.go:864@block value", rule: "yaml-unsupported", fragment: "a quoted value must fit on one line"},
	{id: "yaml.go:864@flow", rule: "yaml-unsupported", fragment: "a quoted value must fit on one line"},
	{id: "yaml.go:871@block value", rule: "yaml-unsupported", fragment: "a backslash at the end of a line continues"},
	{id: "yaml.go:871@flow", rule: "yaml-unsupported", fragment: "a backslash at the end of a line continues"},
	{id: "yaml.go:878@block value", rule: "yaml-escape", fragment: "is not a YAML escape", directoryNone: "evidence: the reference refuses an escape that YAML does not have", publisherNone: "evidence: the reference refuses an escape that YAML does not have"},
	{id: "yaml.go:878@flow", rule: "yaml-escape", fragment: "is not a YAML escape", directoryNone: "evidence: the reference refuses an escape that YAML does not have", publisherNone: "evidence: the reference refuses an escape that YAML does not have"},
	{id: "yaml.go:882@block value", rule: "yaml-escape", fragment: "must be followed by", directoryNone: "evidence: the reference refuses an escape without its digits", publisherNone: "evidence: the reference refuses an escape without its digits"},
	{id: "yaml.go:882@flow", rule: "yaml-escape", fragment: "must be followed by", directoryNone: "evidence: the reference refuses an escape without its digits", publisherNone: "evidence: the reference refuses an escape without its digits"},
	{id: "yaml.go:892@block value", rule: "yaml-escape", fragment: "is half of a surrogate pair"},
	{id: "yaml.go:892@flow", rule: "yaml-escape", fragment: "is half of a surrogate pair"},
	{id: "yaml.go:897@block value", rule: "yaml-escape", fragment: "the escape does not name a character"},
	{id: "yaml.go:897@flow", rule: "yaml-escape", fragment: "the escape does not name a character"},
	{id: "yaml_flow.go:14", rule: "yaml-unsupported", fragment: "keep chomping"},
	{id: "yaml_flow.go:20", rule: "yaml-unsupported", fragment: "an indentation indicator"},
	{id: "yaml_flow.go:51", rule: "yaml-unsupported", fragment: "a blank line inside a block scalar"},
	{id: "yaml_flow.go:169", rule: "yaml", fragment: "is not closed before the end of the file", directoryNone: "evidence: the reference refuses a collection that is not closed", publisherNone: "evidence: the reference refuses a collection that is not closed"},
	{id: "yaml_flow.go:178", rule: "yaml-unsupported", fragment: "comments inside [ ] or { } are not supported"},
	{id: "yaml_flow.go:182", rule: "yaml", fragment: "a line that continues a [ ] or { } collection must be indented more", directoryNone: "evidence: the reference refuses a continuation that is not indented", publisherNone: "evidence: the reference refuses a continuation that is not indented"},
	{id: "yaml_flow.go:186", rule: "yaml-unsupported", fragment: "comments inside [ ] or { } are not supported"},
	{id: "yaml_flow.go:202", rule: "yaml-anchor", fragment: "anchors and aliases (& and *) are not supported"},
	{id: "yaml_flow.go:204", rule: "yaml-tag", fragment: "tags (!) are not supported"},
	{id: "yaml_flow.go:206", rule: "yaml", fragment: "here; put a value that starts with it in quotes", directoryNone: "evidence: the reference refuses an empty entry and the others", publisherNone: "evidence: the reference refuses an empty entry and the others"},
	{id: "yaml_flow.go:210", rule: "yaml", fragment: "a value cannot start with", directoryNone: "evidence: the reference refuses a value that starts so", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml_flow.go:240", rule: "yaml-tab", fragment: "is accepted in quotes only"},
	{id: "yaml_flow.go:254", rule: "yaml-unsupported", fragment: "a plain scalar inside [ ] or { } cannot continue on the next line"},
	{id: "yaml_flow.go:256", rule: "yaml", fragment: "expected a comma or", directoryNone: "evidence: the reference refuses text after a value", publisherNone: "evidence: the reference refuses text after a value"},
	{id: "yaml_flow.go:261", rule: "yaml-limit", fragment: "collections are nested more than", publisherNone: "proof: depth"},
	{id: "yaml_flow.go:286", rule: "yaml-unsupported", fragment: "inside [ ] is not supported", publisherNone: "evidence: recordsets and publish are lists of text, so the checker refuses a pair in them"},
	{id: "yaml_flow.go:306", rule: "yaml-duplicate-key", fragment: "is repeated in one mapping", directoryNone: "evidence: a repeated key is an error of the reference too", publisherNone: "evidence: a repeated key is an error of the reference too"},
	{id: "yaml_flow.go:310", rule: "yaml-unsupported", fragment: "is not followed by", publisherNone: "evidence: the checker refuses a null (a key with no value) for every key it allows"},
	{id: "yaml_flow.go:342", rule: "yaml-anchor", fragment: "anchors and aliases (& and *) are not supported"},
	{id: "yaml_flow.go:344", rule: "yaml-tag", fragment: "tags (!) are not supported"},
	{id: "yaml_flow.go:346", rule: "yaml", fragment: "cannot start a key here", publisherNone: "evidence: no key that the checker allows starts with these characters"},
	{id: "yaml_flow.go:354", rule: "yaml-key", fragment: "is not read as a string (YAML reads it as"},
}

// proofs are the claims of the "proof:" entries, each a function of the code of this package and of the reader.
var proofs = map[string]func() error{
	// A document over MaxDocumentBytes is refused before it is read, so the reader's own bound on the size of a file is never reached.
	"size": func() error {
		if MaxDocumentBytes >= meaning.MaxFileBytes {
			return fmt.Errorf("MaxDocumentBytes %d is not below the reader's %d", MaxDocumentBytes, meaning.MaxFileBytes)
		}
		return nil
	},
	// The deepest collection that a manifest the Publisher profile accepts can hold: the keys that it allows (allowedKeys) are mappings
	// down to meaning.graph, and scalars, except recordsets, a list of names; so a manifest it accepts nests collections at most
	// as deep as the longest path of allowedKeys plus one, and OVDB.md (a mapping of a number and a list of paths) two. Anything deeper
	// is a value that the checker refuses for the type of the key, which the documents of the table show (they are refused by both).
	"depth": func() error {
		deepest := 2
		for _, set := range allowedKeys {
			deepest = max(deepest, len(set.path)+1)
		}
		if deepest >= meaning.MaxYAMLDepth {
			return fmt.Errorf("an accepted manifest nests %d deep, the reader refuses %d", deepest, meaning.MaxYAMLDepth)
		}
		return nil
	},
}

// placeSite is the line of the reader that a place is named after.
func placeSite(id string) string { return strings.SplitN(id, "@", 2)[0] }

// readerSourceDir is the source of the reader at the version of go.mod, in the module cache.
func readerSourceDir(t testing.TB) string {
	t.Helper()
	mod, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*github\.com/meaninggraph/cli (v\S+)`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod does not require github.com/meaninggraph/cli")
	}
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			gopath = filepath.Join(home, "go")
		}
		cache = filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
	}
	dir := filepath.Join(cache, "github.com", "meaninggraph", "cli@"+string(m[1]), "pkg", "meaning")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the source of the reader is not in the module cache (%v): run go mod download", err)
	}
	return dir
}

var (
	ruleConstant  = regexp.MustCompile(`(Rule[A-Za-z]+)\s+=\s+"([a-z-]+)"`)
	ruleUse       = regexp.MustCompile(`\b(Rule[A-Za-z]+)\b`)
	siteCall      = regexp.MustCompile(`\bsyntax\(|\.fail\(|\btabError\(|\.tabAt\(|\.dashTabError\(`)
	stringLiteral = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// readerSites are the lines of the reader that make a refusal, with the rule: a constructor call that names a rule or has a message of
// its own, and the callers of the tab helper, which has none. The helpers that pass a message on (syntax, fail, tabError, tabAt) are not sites.
func readerSites(t testing.TB) map[string]string {
	t.Helper()
	dir := readerSourceDir(t)
	sites := map[string]string{}
	rules := map[string]string{}
	files := map[string][]string{}
	for _, name := range []string{"yaml.go", "yaml_flow.go"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = strings.Split(string(raw), "\n")
		for _, m := range ruleConstant.FindAllStringSubmatch(string(raw), -1) {
			rules[m[1]] = m[2]
		}
	}
	for name, lines := range files {
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "func ") || !siteCall.MatchString(line) {
				continue
			}
			id := name + ":" + strconv.Itoa(i+1)
			switch {
			case strings.Contains(line, "a tab after a dash is not accepted"), strings.Contains(line, `"column %d: "`): // the helpers that all the tab errors use
				continue
			case strings.Contains(line, "dashTabError(") || strings.Contains(line, "tabError(") || strings.Contains(line, ".tabAt("):
				if !stringLiteral.MatchString(line) && !strings.Contains(line, "dashTabError(") {
					continue // a helper passing its format on
				}
				sites[id] = "yaml-tab"
			default:
				use := ruleUse.FindString(line)
				if use == "" {
					continue // fail and syntax passing the rule on
				}
				rule, ok := rules[use]
				if !ok {
					t.Fatalf("%s: unknown rule %s", id, use)
				}
				sites[id] = rule
			}
		}
	}
	return sites
}

type placeCount struct{ stricter, agree, miss int }

// readerPlaceCounts says, for each place and profile, how many documents of its family the reader refuses there while the reference accepts (stricter)
// or refuses too (agree), and how many do not reach the place at all (miss).
func readerPlaceCounts(t testing.TB) map[string]map[Profile]placeCount {
	t.Helper()
	_, _, manifests, mds := loadReference(t)
	byID := map[string]readerPlace{}
	for _, p := range readerPlaces {
		byID[p.id] = p
	}
	counts := map[string]map[Profile]placeCount{}
	record := func(c referenceCase, profile Profile, accept func(Profile) (bool, Finding), verdict bool) {
		id, ok := strings.CutPrefix(c.Family, "reader place: ")
		if !ok {
			return
		}
		place, known := byID[id]
		if !known {
			t.Errorf("the family %q names no place of the table", c.Family)
			return
		}
		if counts[id] == nil {
			counts[id] = map[Profile]placeCount{}
		}
		n := counts[id][profile]
		ok, first := accept(profile)
		switch {
		case ok || first.Rule != place.rule || !strings.Contains(first.Message, place.fragment):
			n.miss++
		case verdict:
			n.stricter++
		default:
			n.agree++
		}
		counts[id][profile] = n
	}
	for _, c := range manifests {
		for _, profile := range []Profile{Directory, Publisher} {
			verdict := c.Verdict
			if profile == Publisher {
				verdict = c.Publisher
			}
			record(c, profile, func(p Profile) (bool, Finding) { return acceptManifest(p, c.Document) }, verdict)
		}
	}
	for _, c := range mds {
		for _, profile := range []Profile{Directory, Publisher} {
			verdict := c.Verdict
			if profile == Publisher {
				verdict = c.Publisher
			}
			record(c, profile, func(p Profile) (bool, Finding) { return acceptMd(p, c.Document, c.Path) }, verdict)
		}
	}
	return counts
}

var readmePlaceRow = regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([a-z-]+) \\| (\\d+) / (\\d+) \\| (\\d+) / (\\d+) \\| (.*) \\|$")

// Every place of the reader that raises a refusal has documents in the corpus, or says why not, and the README lists the places
// with what each profile shows of them.
func TestReaderPlaces(t *testing.T) {
	sites := readerSites(t)
	seen := map[string]bool{}
	for _, p := range readerPlaces {
		site := placeSite(p.id)
		rule, ok := sites[site]
		if !ok {
			t.Errorf("place %s: no line of the reader makes a refusal there (the table is of another version of the reader)", p.id)
		} else if rule != p.rule {
			t.Errorf("place %s raises %s, the table says %s", p.id, rule, p.rule)
		}
		seen[site] = true
		if p.fragment == "" {
			t.Errorf("place %s has no fragment", p.id)
		}
	}
	for site, rule := range sites {
		if !seen[site] {
			t.Errorf("the reader refuses at %s (%s), which the table of places does not list", site, rule)
		}
	}
	if ids := slices.Sorted(func(yield func(string) bool) {
		for _, p := range readerPlaces {
			if !yield(p.id) {
				return
			}
		}
	}); len(slices.Compact(ids)) != len(ids) {
		t.Error("a place is listed twice")
	}

	counts := readerPlaceCounts(t)
	readme := readReadme(t)
	rows := map[string][]string{}
	for _, m := range readmePlaceRow.FindAllStringSubmatch(readme, -1) {
		rows[m[1]] = m[2:]
	}
	var table []string
	for _, p := range readerPlaces {
		d, pub := counts[p.id][Directory], counts[p.id][Publisher]
		for _, side := range []struct {
			name, none string
			n          placeCount
		}{{"Directory", p.directoryNone, d}, {"Publisher", p.publisherNone, pub}} {
			switch kind, text, _ := strings.Cut(side.none, ": "); {
			case side.none == "" && side.n.stricter == 0:
				t.Errorf("place %s: the %s profile has no document that its reference accepts and the reader refuses here (%d agree, %d miss): add one, or say why there is none", p.id, side.name, side.n.agree, side.n.miss)
			case side.none != "" && side.n.stricter != 0:
				t.Errorf("place %s: the %s profile is said to have none (%s), and has %d", p.id, side.name, side.none, side.n.stricter)
			case kind == "evidence" && side.n.agree == 0:
				t.Errorf("place %s: the %s profile's evidence is that its reference refuses the documents that reach the place, and none does (%d miss)", p.id, side.name, side.n.miss)
			case kind == "proof":
				if check, ok := proofs[text]; !ok {
					t.Errorf("place %s: no proof %q", p.id, text)
				} else if err := check(); err != nil {
					t.Errorf("place %s: proof %q fails: %v", p.id, text, err)
				}
			case side.none != "" && kind != "evidence":
				t.Errorf("place %s: %q is neither a proof nor evidence", p.id, side.none)
			}
		}
		why := "both profiles have documents"
		switch {
		case p.directoryNone != "" && p.directoryNone == p.publisherNone:
			why = "both profiles: " + p.directoryNone
		case p.directoryNone != "" && p.publisherNone != "":
			why = "Directory: " + p.directoryNone + "; Publisher: " + p.publisherNone
		case p.directoryNone != "":
			why = "Directory: " + p.directoryNone
		case p.publisherNone != "":
			why = "Publisher: " + p.publisherNone
		}
		table = append(table, fmt.Sprintf("| `%s` | %s | %d / %d | %d / %d | %s |", p.id, p.rule, d.stricter, d.agree, pub.stricter, pub.agree, why))
		row, ok := rows[p.id]
		want := []string{p.rule, strconv.Itoa(d.stricter), strconv.Itoa(d.agree), strconv.Itoa(pub.stricter), strconv.Itoa(pub.agree), why}
		if !ok || !slices.Equal(row, want) {
			t.Errorf("README row of %s is %v, want %v", p.id, row, want)
		}
	}
	if len(rows) != len(readerPlaces) {
		t.Errorf("README lists %d places, the table has %d", len(rows), len(readerPlaces))
	}
	t.Logf("the rows of the README table:\n%s", strings.Join(table, "\n"))
}
