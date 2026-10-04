package manifest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meaninggraph/cli/pkg/meaning"
)

// The reader (github.com/meaninggraph/cli, package meaning) is one component, and every kind of refusal that comes from it, and every
// bound that is checked before it, is a property of the reader and not of a profile: both profiles read their documents with it, before any
// of their own rules run. So what it refuses is recorded once, by place, and holds for both profiles; what each profile can show of it is a
// count per profile, computed here from the corpus and printed in the README.
//
// A refusal is attributed to its place by the line of the reader that made it and by the caller of that line, not by the text of its message:
// the test runs the whole corpus through the reader as it is built (the module that go resolves, replace directives included), with the
// reader's source instrumented in a temporary copy so that each refusal carries the lines that led to it (see testdata/chain).

type readerPlace struct {
	id   string // the line of the reader that refuses, file:line, and after @ the caller of a function that several places call
	rule string // the rule of the refusal
	// via is a line that the chain of a refusal at this line must go through for it to be this row (the caller named after @); notVia is a
	// line that it must not go through (the row without a caller, when another row has the caller).
	via, notVia string
	// directoryNone and publisherNone say why a profile has no document that its reference accepts and the reader refuses at this place.
	// "proof: <name>" is a claim that proofs below checks; "evidence: <text>" says that the reference refuses every document of the corpus that
	// the reader refuses at this place, which the test checks by counting them. A profile with such documents has neither.
	directoryNone, publisherNone string
}

// readerPlaces is every place of the reader that raises a refusal: one row for each line of meaninggraph/cli pkg/meaning (yaml.go,
// yaml_flow.go) that makes a *SyntaxError, and, where a function that several places call makes it (resolvePlain, scanQuoted, unescape,
// keyProblem, endOfLine), one row for each caller that an allowed key can reach.
var readerPlaces = []readerPlace{
	{id: "yaml.go:124", rule: "yaml-limit", directoryNone: "proof: size", publisherNone: "proof: size"},
	{id: "yaml.go:148", rule: "yaml-encoding"},
	{id: "yaml.go:171", rule: "yaml-encoding"},
	{id: "yaml.go:173", rule: "yaml-encoding"},
	{id: "yaml.go:178", rule: "yaml-line-ending"},
	{id: "yaml.go:182", rule: "yaml-character"},
	{id: "yaml.go:295", rule: "yaml-tab"},
	{id: "yaml.go:320", rule: "yaml-limit", publisherNone: "proof: depth"},
	{id: "yaml.go:332", rule: "yaml-directive"},
	{id: "yaml.go:341", rule: "yaml-tab"},
	{id: "yaml.go:345", rule: "yaml-documents"},
	{id: "yaml.go:352", rule: "yaml-documents"},
	{id: "yaml.go:365", rule: "yaml", directoryNone: "evidence: the reference refuses the same layouts", publisherNone: "evidence: the reference refuses the same layouts"},
	{id: "yaml.go:399", rule: "yaml-unsupported"},
	{id: "yaml.go:436", rule: "yaml-tab"},
	{id: "yaml.go:462", rule: "yaml", directoryNone: "evidence: the reference refuses the same layouts", publisherNone: "evidence: the reference refuses the same layouts"},
	{id: "yaml.go:464", rule: "yaml-tab"},
	{id: "yaml.go:466", rule: "yaml", directoryNone: "evidence: the reference refuses a dash where a key belongs", publisherNone: "evidence: the reference refuses a dash where a key belongs"},
	{id: "yaml.go:473", rule: "yaml", publisherNone: "evidence: the reference refuses a line that is no entry"},
	{id: "yaml.go:476", rule: "yaml-duplicate-key", directoryNone: "evidence: a repeated key is an error of the reference too", publisherNone: "evidence: a repeated key is an error of the reference too"},
	{id: "yaml.go:509", rule: "yaml", directoryNone: "evidence: the reference refuses the same layouts", publisherNone: "evidence: the reference refuses the same layouts"},
	{id: "yaml.go:511", rule: "yaml-tab"},
	{id: "yaml.go:557", rule: "yaml-tab"},
	{id: "yaml.go:563", rule: "yaml-tab"},
	{id: "yaml.go:571", rule: "yaml-anchor"},
	{id: "yaml.go:573", rule: "yaml-tag"},
	{id: "yaml.go:578", rule: "yaml-unsupported"},
	{id: "yaml.go:586", rule: "yaml-tab"},
	{id: "yaml.go:594", rule: "yaml-key", publisherNone: "evidence: no key that the checker allows reads as a number, a boolean or null, and it refuses the others"},
	{id: "yaml.go:628", rule: "yaml-key", notVia: "yaml_flow.go:357", publisherNone: "evidence: the reference refuses a block key over 1024 characters too"},
	{id: "yaml.go:628@flow key", rule: "yaml-key", via: "yaml_flow.go:357"},
	{id: "yaml.go:630", rule: "yaml-anchor", notVia: "yaml_flow.go:357", publisherNone: "evidence: << is not a key that the checker allows"},
	{id: "yaml.go:630@flow key", rule: "yaml-anchor", via: "yaml_flow.go:357", publisherNone: "evidence: << is not a key that the checker allows"},
	{id: "yaml.go:643", rule: "yaml-tab"},
	{id: "yaml.go:646", rule: "yaml-unsupported"},
	{id: "yaml.go:654", rule: "yaml-anchor"},
	{id: "yaml.go:656", rule: "yaml-tag"},
	{id: "yaml.go:658", rule: "yaml", directoryNone: "evidence: the reference refuses a value that starts so", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml.go:661", rule: "yaml-tab", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml.go:664", rule: "yaml", directoryNone: "evidence: the reference refuses a value that starts so", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml.go:696", rule: "yaml"},
	{id: "yaml.go:735", rule: "yaml", publisherNone: "evidence: the reference refuses a colon and a space in a plain value"},
	{id: "yaml.go:784@block key", rule: "yaml-number", via: "yaml.go:589", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:784@block value", rule: "yaml-number", via: "yaml.go:717", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:784@flow key", rule: "yaml-number", via: "yaml_flow.go:349", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:784@flow value", rule: "yaml-number", via: "yaml_flow.go:212", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:788@block key", rule: "yaml-number", via: "yaml.go:589", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:788@block value", rule: "yaml-number", via: "yaml.go:717"},
	{id: "yaml.go:788@flow key", rule: "yaml-number", via: "yaml_flow.go:349", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:788@flow value", rule: "yaml-number", via: "yaml_flow.go:212"},
	{id: "yaml.go:790@block key", rule: "yaml-number", via: "yaml.go:589", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@block value", rule: "yaml-number", via: "yaml.go:717", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@flow key", rule: "yaml-number", via: "yaml_flow.go:349", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:790@flow value", rule: "yaml-number", via: "yaml_flow.go:212", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@block key", rule: "yaml-number", via: "yaml.go:589", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@block value", rule: "yaml-number", via: "yaml.go:717", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@flow key", rule: "yaml-number", via: "yaml_flow.go:349", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:794@flow value", rule: "yaml-number", via: "yaml_flow.go:212", publisherNone: "evidence: no key that the checker allows takes a number, and OVDB.md allows only ovdb: 1"},
	{id: "yaml.go:825@block scalar header", rule: "yaml", via: "yaml_flow.go:22", directoryNone: "evidence: the reference refuses text after the end of a value", publisherNone: "evidence: the reference refuses text after the end of a value"},
	{id: "yaml.go:825@flow", rule: "yaml", via: "yaml_flow.go:119", publisherNone: "evidence: the reference refuses text after the end of a value"},
	{id: "yaml.go:825@quoted value", rule: "yaml", via: "yaml.go:808", directoryNone: "evidence: the reference refuses text after the end of a value", publisherNone: "evidence: the reference refuses text after the end of a value"},
	{id: "yaml.go:864@block value", rule: "yaml-unsupported", via: "yaml.go:803"},
	{id: "yaml.go:864@flow", rule: "yaml-unsupported", via: "yaml_flow.go:145"},
	{id: "yaml.go:871@block value", rule: "yaml-unsupported", via: "yaml.go:803"},
	{id: "yaml.go:871@flow", rule: "yaml-unsupported", via: "yaml_flow.go:145"},
	{id: "yaml.go:878@block value", rule: "yaml-escape", via: "yaml.go:803"},
	{id: "yaml.go:878@flow", rule: "yaml-escape", via: "yaml_flow.go:145"},
	{id: "yaml.go:882@block value", rule: "yaml-escape", via: "yaml.go:803", directoryNone: "evidence: the reference refuses an escape without its digits", publisherNone: "evidence: the reference refuses an escape without its digits"},
	{id: "yaml.go:882@flow", rule: "yaml-escape", via: "yaml_flow.go:145", directoryNone: "evidence: the reference refuses an escape without its digits", publisherNone: "evidence: the reference refuses an escape without its digits"},
	{id: "yaml.go:892@block value", rule: "yaml-escape", via: "yaml.go:803"},
	{id: "yaml.go:892@flow", rule: "yaml-escape", via: "yaml_flow.go:145"},
	{id: "yaml.go:897@block value", rule: "yaml-escape", via: "yaml.go:803"},
	{id: "yaml.go:897@flow", rule: "yaml-escape", via: "yaml_flow.go:145"},
	{id: "yaml_flow.go:14", rule: "yaml-unsupported"},
	{id: "yaml_flow.go:20", rule: "yaml-unsupported"},
	{id: "yaml_flow.go:51", rule: "yaml-unsupported"},
	{id: "yaml_flow.go:169", rule: "yaml", directoryNone: "evidence: the reference refuses a collection that is not closed", publisherNone: "evidence: the reference refuses a collection that is not closed"},
	{id: "yaml_flow.go:178", rule: "yaml-unsupported"},
	{id: "yaml_flow.go:182", rule: "yaml", directoryNone: "evidence: the reference refuses a continuation that is not indented", publisherNone: "evidence: the reference refuses a continuation that is not indented"},
	{id: "yaml_flow.go:186", rule: "yaml-unsupported"},
	{id: "yaml_flow.go:202", rule: "yaml-anchor"},
	{id: "yaml_flow.go:204", rule: "yaml-tag"},
	{id: "yaml_flow.go:206", rule: "yaml", directoryNone: "evidence: the reference refuses an empty entry and the others", publisherNone: "evidence: the reference refuses an empty entry and the others"},
	{id: "yaml_flow.go:210", rule: "yaml", publisherNone: "evidence: the reference refuses a value that starts so"},
	{id: "yaml_flow.go:240", rule: "yaml-tab"},
	{id: "yaml_flow.go:254", rule: "yaml-unsupported"},
	{id: "yaml_flow.go:256", rule: "yaml", directoryNone: "evidence: the reference refuses text after a value", publisherNone: "evidence: the reference refuses text after a value"},
	{id: "yaml_flow.go:261", rule: "yaml-limit", publisherNone: "proof: depth"},
	{id: "yaml_flow.go:286", rule: "yaml-unsupported", publisherNone: "evidence: recordsets and publish are lists of text, so the checker refuses a pair in them"},
	{id: "yaml_flow.go:306", rule: "yaml-duplicate-key", directoryNone: "evidence: a repeated key is an error of the reference too", publisherNone: "evidence: a repeated key is an error of the reference too"},
	{id: "yaml_flow.go:310", rule: "yaml-unsupported", publisherNone: "evidence: the checker refuses a null (a key with no value) for every key it allows"},
	{id: "yaml_flow.go:342", rule: "yaml-anchor"},
	{id: "yaml_flow.go:344", rule: "yaml-tag"},
	{id: "yaml_flow.go:346", rule: "yaml", publisherNone: "evidence: no key that the checker allows starts with these characters"},
	{id: "yaml_flow.go:354", rule: "yaml-key"},
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

const readerModule = "github.com/meaninggraph/cli"

// goTool is the go command on the path, which is the one that runs `go test`.
func goTool() string {
	if path, err := exec.LookPath("go"); err == nil {
		return path
	}
	return "go" // the failure to run it says what is missing
}

// runGo runs the go command in dir and returns its standard output; a failure fails the test with what the command said.
func runGo(t testing.TB, dir string, stdin []byte, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goTool(), args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes()
}

// readerSourceDir is the directory of package meaning of the reader that is built: the module as go resolves it, which follows a replace
// directive (modfile is another go.mod to resolve with, for the test of that; empty for the module's own).
func readerSourceDir(t testing.TB, modfile string) string {
	t.Helper()
	args := []string{"list", "-m", "-json"}
	if modfile != "" {
		args = append(args, "-modfile="+modfile)
	}
	var module struct {
		Dir     string
		Replace *struct{ Dir string }
	}
	if err := json.Unmarshal(runGo(t, ".", nil, append(args, readerModule)...), &module); err != nil {
		t.Fatal(err)
	}
	dir := module.Dir
	if module.Replace != nil {
		dir = module.Replace.Dir
	}
	if dir == "" {
		t.Fatalf("go has no source for %s: run go mod download", readerModule)
	}
	return filepath.Join(dir, "pkg", "meaning")
}

var (
	ruleConstant  = regexp.MustCompile(`(Rule[A-Za-z]+)\s+=\s+"([a-z-]+)"`)
	ruleUse       = regexp.MustCompile(`\b(Rule[A-Za-z]+)\b`)
	siteCall      = regexp.MustCompile(`\bsyntax\(|\.fail\(|\btabError\(|\.tabAt\(|\.dashTabError\(`)
	stringLiteral = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// readerSites are the lines of the reader that make a refusal, with the rule: a constructor call that names a rule or has a message of
// its own, and the callers of the tab helper, which has none. The helpers that pass a message on (syntax, fail, tabError, tabAt) are not sites.
func readerSites(t testing.TB, dir string) map[string]string {
	t.Helper()
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

// placeErrors says what is wrong with the table of places against the source of the reader in dir: a line that makes a refusal and is not a
// row, a row that is not such a line, a row with the wrong rule, a row listed twice.
func placeErrors(t testing.TB, dir string) []string {
	t.Helper()
	sites := readerSites(t, dir)
	var errs []string
	seen := map[string]bool{}
	listed := map[string]bool{}
	for _, p := range readerPlaces {
		if listed[p.id] {
			errs = append(errs, fmt.Sprintf("place %s is listed twice", p.id))
		}
		listed[p.id] = true
		site := placeSite(p.id)
		rule, ok := sites[site]
		if !ok {
			errs = append(errs, fmt.Sprintf("place %s: no line of the reader makes a refusal there (the table is of another version of the reader)", p.id))
		} else if rule != p.rule {
			errs = append(errs, fmt.Sprintf("place %s raises %s, the table says %s", p.id, rule, p.rule))
		}
		seen[site] = true
	}
	for site, rule := range sites {
		if !seen[site] {
			errs = append(errs, fmt.Sprintf("the reader refuses at %s (%s), which the table of places does not list", site, rule))
		}
	}
	slices.Sort(errs)
	return errs
}

// The instrumentation, in a copy of the reader's module: the line of yaml.go that makes every SyntaxError puts, in front of the message, the
// lines of yaml.go and yaml_flow.go that were running, innermost first, and nothing moves (the substitution stays on its line, the function
// is in a file of its own), so the lines of a chain are the lines of the source that the table names.
const (
	instrumentedLine = "return &SyntaxError{Line: line, Rule: rule, Message: fmt.Sprintf(format, args...)}"
	instrumentedWith = "return &SyntaxError{Line: line, Rule: rule, Message: zzChain() + fmt.Sprintf(format, args...)}"
	chainFile        = `package meaning

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func zzChain() string {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(3, pcs) // not Callers, zzChain and syntax
	frames := runtime.CallersFrames(pcs[:n])
	var parts []string
	for {
		f, more := frames.Next()
		if base := filepath.Base(f.File); base == "yaml.go" || base == "yaml_flow.go" {
			parts = append(parts, base+":"+strconv.Itoa(f.Line))
		}
		if !more || len(parts) >= 12 {
			break
		}
	}
	return "[@" + strings.Join(parts, "<") + "] "
}
`
)

type readerRefusal struct{ Rule, Chain string }

// workspaceProblem says what to do when a go.work is in effect (the value of `go env GOWORK`): the test builds the reader through a go.mod of its own
// (-modfile), which the go tool does not allow in workspace mode, and a workspace may replace the reader in a way that go.mod does not show.
func workspaceProblem(gowork string) string {
	if gowork == "" || gowork == "off" {
		return ""
	}
	return fmt.Sprintf("a go.work is in effect (%s): this test instruments the reader through a go.mod of its own, which a workspace does not allow; run it with GOWORK=off (and with the reader replaced in go.mod, not in the workspace), or without the go.work", gowork)
}

// replacedModfile writes, into dir, a go.mod and a go.sum that are those of the module (mod and sum are their contents) but for the reader, which
// they replace by the directory copyDir; and returns the path of the go.mod. Every replacement of the reader that the module has is dropped first,
// in whatever form (a line, a block, with or without a version): the go tool's own parser says what they are, and two replacements of one module
// are "conflicting replacements".
func replacedModfile(t testing.TB, dir string, mod, sum []byte, copyDir string) string {
	t.Helper()
	modfile := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modfile, mod, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o600); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Replace []struct {
			Old struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(runGo(t, dir, nil, "mod", "edit", "-json", modfile), &parsed); err != nil {
		t.Fatal(err)
	}
	args := []string{"mod", "edit"}
	for _, r := range parsed.Replace {
		if r.Old.Path == readerModule {
			drop := r.Old.Path
			if r.Old.Version != "" {
				drop += "@" + r.Old.Version
			}
			args = append(args, "-dropreplace="+drop)
		}
	}
	runGo(t, dir, nil, append(args, "-replace="+readerModule+"="+filepath.ToSlash(copyDir), modfile)...)
	return modfile
}

// readerChains runs the documents through the reader of dir (package meaning of the module that go builds), instrumented, and returns for each
// the rule that refused it and the chain of lines (empty when the document is read). The go tool cannot instrument a file of the module cache
// (an overlay may not replace one), so the module is copied into a temporary directory, instrumented there, and testdata/chain is built and run
// against the copy by a go.mod of its own (-modfile) that replaces the module; the module of this repository is not touched.
func readerChains(t testing.TB, dir string, inputs [][]byte) []readerRefusal {
	t.Helper()
	if problem := workspaceProblem(strings.TrimSpace(string(runGo(t, ".", nil, "env", "GOWORK")))); problem != "" {
		t.Fatal(problem)
	}
	tmp := t.TempDir()
	copyDir := filepath.Join(tmp, "reader")
	if err := os.CopyFS(copyDir, os.DirFS(filepath.Join(dir, "..", ".."))); err != nil {
		t.Fatal(err)
	}
	yaml := filepath.Join(copyDir, "pkg", "meaning", "yaml.go")
	original, err := os.ReadFile(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(original, []byte(instrumentedLine)) != 1 {
		t.Fatalf("yaml.go of the reader does not make its SyntaxError in the one line that the instrumentation changes (%s): change the instrumentation with it", instrumentedLine)
	}
	for path, content := range map[string][]byte{
		yaml: bytes.Replace(original, []byte(instrumentedLine), []byte(instrumentedWith), 1),
		filepath.Join(copyDir, "pkg", "meaning", "zz_chain.go"): []byte(chainFile),
	} {
		_ = os.Chmod(path, 0o600) // files of the module cache are read-only
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mod, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("../../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	modfile := replacedModfile(t, tmp, mod, sum, copyDir)
	encoded := make([]string, len(inputs))
	for i, in := range inputs {
		encoded[i] = base64.StdEncoding.EncodeToString(in)
	}
	stdin, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var out []readerRefusal
	if err := json.Unmarshal(runGo(t, ".", stdin, "run", "-modfile="+modfile, "./testdata/chain"), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != len(inputs) {
		t.Fatalf("the reader answered for %d of %d documents", len(out), len(inputs))
	}
	return out
}

// readerInput is the bytes that the reader is given for a document of the corpus: a manifest as it is, the front matter of an OVDB.md; false
// when the reader is not reached (a document over the size bound, an OVDB.md with no front matter).
func readerInput(c referenceCase, md bool) ([]byte, bool) {
	text := c.Document
	if md {
		var ok bool
		if text, ok = frontMatter(c.Document); !ok {
			return nil, false
		}
	}
	return text, len(text) <= MaxDocumentBytes
}

type placeCount struct{ stricter, agree int }

// refusalRow is the row of the table that a refusal belongs to: the innermost line of its chain that is a row, and the row of that line that
// the caller (or the lack of one) names. It returns "" and why when there is none.
func refusalRow(chain string, byLine map[string][]readerPlace) (string, string) {
	frames := strings.Split(chain, "<")
	for _, frame := range frames {
		rows, isSite := byLine[frame]
		if !isSite {
			continue
		}
		for _, p := range rows {
			if (p.via == "" || slices.Contains(frames, p.via)) && (p.notVia == "" || !slices.Contains(frames, p.notVia)) {
				return p.id, ""
			}
		}
		return "", fmt.Sprintf("the refusal at %s came through %s, which no row names", frame, chain)
	}
	return "", fmt.Sprintf("the refusal came through %s, which is no row", chain)
}

// readerPlaceCounts says, for each place and profile, how many documents of the corpus the reader refuses there while the profile's reference
// accepts them (stricter: the kind is real) and how many while it refuses them too (agree). Each refusal is attributed to the line that made
// it, and to the caller of that line.
func readerPlaceCounts(t testing.TB, dir string) (map[string]map[Profile]placeCount, map[string]int) {
	t.Helper()
	_, _, manifests, mds := loadReference(t)
	byLine := map[string][]readerPlace{}
	for _, p := range readerPlaces {
		byLine[placeSite(p.id)] = append(byLine[placeSite(p.id)], p)
	}
	type doc struct {
		c  referenceCase
		md bool
	}
	var docs []doc
	var inputs [][]byte
	for _, c := range manifests {
		if in, ok := readerInput(c, false); ok {
			docs, inputs = append(docs, doc{c, false}), append(inputs, in)
		}
	}
	for _, c := range mds {
		if in, ok := readerInput(c, true); ok {
			docs, inputs = append(docs, doc{c, true}), append(inputs, in)
		}
	}
	refusals := readerChains(t, dir, inputs)
	counts := map[string]map[Profile]placeCount{}
	aimed := map[string]int{} // for each family "reader place: <id>": its documents that the reader refuses at <id>
	reported := 0
	for i, d := range docs {
		r := refusals[i]
		if r.Rule == "" {
			continue
		}
		id, why := refusalRow(r.Chain, byLine)
		if id == "" {
			if reported++; reported <= 10 {
				t.Errorf("%s: %s", d.c.Family, why)
			}
			continue
		}
		row := byRow(id)
		if r.Rule != row.rule {
			t.Errorf("a refusal at %s has the rule %s, the table says %s", id, r.Rule, row.rule)
		}
		if counts[id] == nil {
			counts[id] = map[Profile]placeCount{}
		}
		for _, profile := range []Profile{Directory, Publisher} {
			accepts := d.c.Verdict
			if profile == Publisher {
				accepts = d.c.Publisher
			}
			n := counts[id][profile]
			if accepts {
				n.stricter++
			} else {
				n.agree++
			}
			counts[id][profile] = n
		}
		if d.c.Family == "reader place: "+id {
			aimed[id]++
		}
	}
	return counts, aimed
}

func byRow(id string) readerPlace {
	for _, p := range readerPlaces {
		if p.id == id {
			return p
		}
	}
	return readerPlace{}
}

var readmePlaceRow = regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([a-z-]+) \\| (\\d+) / (\\d+) \\| (\\d+) / (\\d+) \\| (.*) \\|$")

// Every place of the reader that raises a refusal has documents in the corpus, or says why not, and the README lists the places with what each
// profile shows of them. A document counts for the place that refused it, wherever the corpus has it from.
func TestReaderPlaces(t *testing.T) {
	dir := readerSourceDir(t, "")
	for _, e := range placeErrors(t, dir) {
		t.Error(e)
	}
	counts, aimed := readerPlaceCounts(t, dir)
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
				t.Errorf("place %s: the %s profile has no document that its reference accepts and the reader refuses here (%d agree): add one, or say why there is none", p.id, side.name, side.n.agree)
			case side.none != "" && side.n.stricter != 0:
				t.Errorf("place %s: the %s profile is said to have none (%s), and has %d", p.id, side.name, side.none, side.n.stricter)
			case kind == "evidence" && side.n.agree == 0:
				t.Errorf("place %s: the %s profile's evidence is that its reference refuses the documents that the reader refuses here, and there are none", p.id, side.name)
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
		if !strings.HasPrefix(p.directoryNone, "proof") && !strings.HasPrefix(p.publisherNone, "proof") && aimed[p.id] == 0 {
			t.Errorf("place %s: no document of the family %q is refused there, so the documents aimed at it miss", p.id, "reader place: "+p.id)
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

// The table of places is read from the reader that go builds, so a replace directive is followed: with the reader replaced by a copy that has
// one more line that refuses, the table is found out of date.
func TestReaderSourceFollowsReplace(t *testing.T) {
	real := readerSourceDir(t, "")
	copyDir := filepath.Join(t.TempDir(), "cli")
	if err := os.CopyFS(copyDir, os.DirFS(filepath.Join(real, "..", ".."))); err != nil {
		t.Fatal(err)
	}
	extra := "\nfunc zzExtraRefusal() *SyntaxError {\n\treturn syntax(1, RuleYAML, \"an extra refusal\")\n}\n"
	yaml := filepath.Join(copyDir, "pkg", "meaning", "yaml.go")
	original, err := os.ReadFile(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yaml, append(original, extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("../../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	modfile := replacedModfile(t, t.TempDir(), mod, sum, copyDir)
	dir := readerSourceDir(t, modfile)
	if want := filepath.Join(copyDir, "pkg", "meaning"); dir != want {
		t.Fatalf("with a replace, the reader is read from %s, want %s", dir, want)
	}
	line := strconv.Itoa(len(strings.Split(string(original), "\n")) + 2) // after the last line, a blank one, the func line, then the refusal
	if errs := placeErrors(t, dir); len(errs) != 1 || !strings.Contains(errs[0], "yaml.go:"+line) {
		t.Errorf("the extra refusing line yaml.go:%s is not found out: %v", line, errs)
	}
	if errs := placeErrors(t, real); len(errs) != 0 {
		t.Errorf("the table is not that of the reader that is built: %v", errs)
	}
}

// replacedModfile drops the replacements of the reader that a go.mod has, in whatever form, and puts its own: the false failure of the review ("conflicting
// replacements" with a block-form replace, although the reader is the same).
func TestReplacedModfileHandlesEveryFormOfReplace(t *testing.T) {
	base, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("../../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	real := readerSourceDir(t, "")
	copyDir := filepath.Join(t.TempDir(), "cli")
	if err := os.CopyFS(copyDir, os.DirFS(filepath.Join(real, "..", ".."))); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	for name, extra := range map[string]string{
		"none":          "",
		"a line":        "\nreplace " + readerModule + " => " + filepath.ToSlash(other) + "\n",
		"a block":       "\nreplace (\n\t" + readerModule + " => " + filepath.ToSlash(other) + "\n)\n",
		"with version":  "\nreplace " + readerModule + " v0.2.0 => " + filepath.ToSlash(other) + "\n",
		"block version": "\nreplace (\n\t" + readerModule + " v0.2.0 => " + filepath.ToSlash(other) + "\n\tgithub.com/example/unrelated => github.com/example/other v1.0.0\n)\n",
	} {
		modfile := replacedModfile(t, t.TempDir(), append(slices.Clone(base), extra...), sum, copyDir)
		if dir := readerSourceDir(t, modfile); dir != filepath.Join(copyDir, "pkg", "meaning") {
			t.Errorf("%s: the reader is read from %s", name, dir)
		}
		if got, _ := os.ReadFile(modfile); name == "block version" && !strings.Contains(string(got), "github.com/example/unrelated") {
			t.Errorf("%s: a replacement of another module was dropped:\n%s", name, got)
		}
	}
}

func TestWorkspaceProblem(t *testing.T) {
	for _, off := range []string{"", "off"} {
		if got := workspaceProblem(off); got != "" {
			t.Errorf("workspaceProblem(%q) = %q", off, got)
		}
	}
	if got := workspaceProblem("/src/go.work"); !strings.Contains(got, "GOWORK=off") || !strings.Contains(got, "/src/go.work") {
		t.Errorf("workspaceProblem = %q: it must say what to do", got)
	}
}
