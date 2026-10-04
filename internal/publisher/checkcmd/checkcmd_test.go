package checkcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/publisher/exitcode"
	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// chinook is a repository of the real Chinook repository's own files (the base of the parity golden), in memory.
func chinook(t testing.TB) *repo.Memory {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "repo", "testdata", "reference", "repository.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Base map[string]string `json:"base"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil || len(golden.Base) == 0 {
		t.Fatalf("golden: %v", err)
	}
	m := &repo.Memory{Nodes: map[string]repo.Node{}, Untracked: map[string]bool{}}
	for path, text := range golden.Base {
		m.Nodes[path] = repo.Node{Kind: repo.File, Content: []byte(text)}
	}
	return m
}

// dirs is a file system with a directory and a file, for the fake Stat.
var dirs = fstest.MapFS{"repo/x": {Data: []byte("x")}, "afile": {Data: []byte("x")}}

// deps are the seams with a fake reader and a fake file system, and the catalogue and error envelope the binary has (the command is tested through the
// same text, but nothing here is the real git).
func deps(reader repo.Reader) Deps {
	return Deps{
		Open: func(string) repo.Reader { return reader },
		Stat: func(name string) (fs.FileInfo, error) { return fs.Stat(dirs, filepath.ToSlash(name)) },
		T:    uicopy.T,
		JSON: envelope.Marshal,
		Usage: func(cmd *cobra.Command, reason string) error {
			return exitcode.Usage(envelope.New(envelope.InvalidArgument, uicopy.T("usage.failed", map[string]string{"command": cmd.CommandPath()})).WithReason(reason))
		},
		Unrunnable: func(message, reason, next string) error {
			return exitcode.Usage(envelope.New(envelope.DependencyMissing, message).WithReason(reason).WithNext(envelope.Next{Label: next}))
		},
	}
}

// exitCode is the process exit code main gives err: 0, 1 for a refused repository, and the ExitCode of what the command returned otherwise.
func exitCode(err error) int {
	var coder interface{ ExitCode() int }
	switch {
	case err == nil:
		return 0
	case errors.As(err, &coder):
		return coder.ExitCode()
	}
	return 1
}

func execute(t *testing.T, d Deps, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := NewCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	for _, sub := range cmd.Commands() {
		sub.SilenceUsage, sub.SilenceErrors = true, true
	}
	err = cmd.Execute()
	return out.String(), err
}

func TestAnAcceptedRepository(t *testing.T) {
	out, err := execute(t, deps(chinook(t)), "check", "repo")
	want := "OK: commit " + strings.Repeat("0", 0)
	if err != nil || !strings.HasPrefix(out, want) || !strings.Contains(out, "1 manifest listed in OVDB.md, no problems.\n") || !strings.Contains(out, "so a pass here does not mean it will accept it.") {
		t.Errorf("err %v, output %q", err, out)
	}
	if strings.Contains(out, "manifests listed") {
		t.Errorf("one manifest is not told in the plural: %q", out)
	}
}

// pinnedReader gives a commit id, which the Memory reader does not have.
type pinnedMemory struct{ *repo.Memory }

func (p pinnedMemory) Head() (string, error) {
	if _, err := p.Memory.Head(); err != nil {
		return "", err
	}
	return "79e7bb0b1d6f0666dce465874990dec64348331f", nil
}

func TestTheDocumentsArePinnedByGoldenFiles(t *testing.T) {
	good := pinnedMemory{chinook(t)}
	refused := chinook(t)
	refused.Nodes["ovdb.yaml"] = repo.Node{Kind: repo.File, Content: bytes.ReplaceAll(refused.Nodes["ovdb.yaml"].Content, []byte("  - Track\n"), []byte("  - Tracks\n"))}
	hostile := chinook(t)
	hostile.Nodes["evil\x1b[31m\nname\u00e9\xff"] = repo.Node{Kind: repo.File}
	for name, c := range map[string]struct {
		reader repo.Reader
		args   []string
		code   int
	}{
		"accepted":     {good, []string{"check", "repo", "--json"}, 0},
		"accepted-url": {good, []string{"check", "repo", "--json", "--repository", "https://github.com/datatug/chinookdb"}, 0},
		"refused":      {pinnedMemory{refused}, []string{"check", "repo", "--json"}, 1},
		"hostile":      {pinnedMemory{hostile}, []string{"check", "repo", "--json"}, 1},
		"no-commit":    {&repo.Memory{Err: repo.ErrNoCommit}, []string{"check", "repo", "--json"}, 1},
	} {
		out, err := execute(t, deps(c.reader), c.args...)
		if exitCode(err) != c.code {
			t.Errorf("%s: exit %d, want %d (%v)", name, exitCode(err), c.code, err)
		}
		golden := filepath.Join("testdata", name+".json")
		if os.Getenv("OVDB_UPDATE_GOLDEN") != "" {
			if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil || out != string(want) {
			t.Errorf("%s: the JSON differs from %s:\n got %s\nwant %s", name, golden, out, want)
		}
		var doc Document
		if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != 1 || doc.Command != "publisher check" || doc.Profile != "publisher" || doc.OK != (c.code == 0) {
			t.Errorf("%s: document %+v, %v", name, doc, err)
		}
	}
}

func TestARefusedRepositoryIsToldBlockByBlock(t *testing.T) {
	refused := chinook(t)
	refused.Nodes["ovdb.yaml"] = repo.Node{Kind: repo.File, Content: bytes.ReplaceAll(refused.Nodes["ovdb.yaml"].Content, []byte("  - Track\n"), []byte("  - Tracks\n"))}
	out, err := execute(t, deps(pinnedMemory{refused}), "check", "repo")
	if !errors.Is(err, ErrRefused) || exitCode(err) != 1 {
		t.Fatalf("err %v", err)
	}
	for _, want := range []string{"ovdb.yaml:", "  [repo-recordsets]\n", "  recordsets lacks the ModelSpec entities of ", "Refused: 2 problems at commit 79e7bb0b1d6f. Fix them, commit, and run the check again.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "OK:") {
		t.Errorf("a refused repository says OK:\n%s", out)
	}
}

func TestOneProblemIsToldInTheSingular(t *testing.T) {
	out, err := execute(t, deps(pinnedMemory{&repo.Memory{Nodes: map[string]repo.Node{}}}), "check", "repo")
	if exitCode(err) != 1 || !strings.Contains(out, "Refused: 1 problem at commit 79e7bb0b1d6f. Fix it,") {
		t.Errorf("err %v, output %q", err, out)
	}
	out, err = execute(t, deps(&repo.Memory{Err: repo.ErrNoCommit}), "check", "repo")
	if exitCode(err) != 1 || !strings.Contains(out, "Refused: 1 problem, and there is no commit to check.") {
		t.Errorf("err %v, output %q", err, out)
	}
	out, err = execute(t, deps(&repo.Memory{Err: errors.New("fatal: x")}), "check", "repo")
	_ = err
	if strings.Contains(out, "Refused: 1 problem at commit") {
		t.Errorf("output %q", out)
	}
}

// The exit codes of the README's table, each row.
func TestEveryRowOfTheExitCodeTable(t *testing.T) {
	notRepo := &repo.Memory{Err: errors.New("fatal: not a git repository")}
	for name, c := range map[string]struct {
		reader repo.Reader
		args   []string
		code   int
	}{
		"accepted":                   {chinook(t), []string{"check", "repo"}, 0},
		"the default path":           {chinook(t), []string{"check"}, 0}, // "." is the current directory
		"a refused repository":       {&repo.Memory{Nodes: map[string]repo.Node{}}, []string{"check", "repo"}, 1},
		"not a git repository":       {notRepo, []string{"check", "repo"}, 1},
		"no commit yet":              {&repo.Memory{Err: repo.ErrNoCommit}, []string{"check", "repo"}, 1},
		"a bare repository":          {&repo.Memory{Err: repo.ErrBare}, []string{"check", "repo"}, 1},
		"a subdirectory":             {&repo.Memory{Err: repo.ErrSubdirectory}, []string{"check", "repo"}, 1},
		"git cannot be run":          {&repo.Memory{Err: repo.ErrCannotRun}, []string{"check", "repo"}, 2},
		"git older than 2.45":        {&repo.Memory{Err: repo.ErrOldGit}, []string{"check", "repo"}, 2},
		"an unknown flag":            {chinook(t), []string{"check", "repo", "--bogus"}, 2},
		"too many arguments":         {chinook(t), []string{"check", "repo", "repo"}, 2},
		"a path that does not exist": {chinook(t), []string{"check", "nowhere"}, 2},
		"an empty path":              {chinook(t), []string{"check", ""}, 2},
		"a path that is a file":      {chinook(t), []string{"check", "afile"}, 2},
		"--repository not a URL":     {chinook(t), []string{"check", "repo", "--repository", "datatug/chinookdb"}, 2},
		"--repository with .git":     {chinook(t), []string{"check", "repo", "--repository", "https://github.com/datatug/chinookdb.git"}, 2},
		"--repository empty":         {chinook(t), []string{"check", "repo", "--repository", ""}, 2},
		"--repository the wrong one": {chinook(t), []string{"check", "repo", "--repository", "https://github.com/other/repo"}, 1},
		"--repository the right one": {chinook(t), []string{"check", "repo", "--repository", "https://github.com/datatug/chinookdb"}, 0},
		"an argument to publisher":   {chinook(t), []string{"nothing"}, 2},
		"a flag of publisher":        {chinook(t), []string{"--bogus"}, 2},
	} {
		out, err := execute(t, deps(c.reader), c.args...)
		if got := exitCode(err); got != c.code {
			t.Errorf("%s: exit %d, want %d (err %v, output %q)", name, got, c.code, err, out)
		}
		if c.code == 2 && out != "" {
			t.Errorf("%s: a usage error printed on standard output: %q", name, out)
		}
	}
}

func TestGitThatCannotRunIsToldWithItsReason(t *testing.T) {
	_, err := execute(t, deps(&repo.Memory{Err: repo.ErrOldGit}), "check", "repo")
	var e interface{ Error() string }
	if !errors.As(err, &e) || !strings.Contains(err.Error(), "Couldn't run the check") || !strings.Contains(err.Error(), "git is older than 2.45") {
		t.Errorf("err %v", err)
	}
}

func TestTheGroupWithoutACommandShowsItsHelp(t *testing.T) {
	out, err := execute(t, deps(chinook(t)))
	if err != nil || !strings.Contains(out, "check ") {
		t.Errorf("err %v, output %q", err, out)
	}
}

// Nothing from the repository or the command line reaches the output unescaped.
func TestAHostileNameIsShownHarmlessly(t *testing.T) {
	hostile := chinook(t)
	hostile.Nodes["a\x1b]0;title\x07\r\nb\u202e\x00"] = repo.Node{Kind: repo.File}
	out, _ := execute(t, deps(pinnedMemory{hostile}), "check", "repo")
	for _, r := range strings.TrimSuffix(out, "\n") {
		if r != '\n' && (r < 0x20 || r > 0x7e) {
			t.Fatalf("output has the character %U:\n%q", r, out)
		}
	}
	if !strings.Contains(out, `\x1b`) {
		t.Errorf("output %q", out)
	}
	// A path on the command line is escaped where it is repeated.
	_, err := execute(t, deps(chinook(t)), "check", "no\x1b[2Jwhere\u00e9")
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) || !strings.Contains(err.Error(), `no\x1b[2Jwhere\u00e9`) {
		t.Errorf("err %q", err)
	}
	if got := safe(strings.Repeat("x", 300)); len(got) != 203 || !strings.HasSuffix(got, "...") {
		t.Errorf("safe of a long string: %q", got)
	}
	if got := safe("\U0001F600 \xff"); got != `\U0001f600 \xff` {
		t.Errorf("safe = %q", got)
	}
}

// The largest output: the most findings (manifest.MaxFindings and the notice that says more were left out), each of the longest message and a long
// document name. The bound is stated in the README.
func TestTheLargestOutputIsBounded(t *testing.T) {
	var findings []manifest.Finding
	long := strings.Repeat("x", manifest.MaxMessageBytes)
	for i := 0; i < manifest.MaxFindings+1; i++ {
		findings = append(findings, manifest.Finding{Rule: "manifest-recordsets", Severity: manifest.SeverityError, Document: strings.Repeat("\x01", rules.MaxPathLength), Line: 99999, Message: long})
	}
	doc := newDocument(strings.Repeat("a", 40), manifest.Result{Findings: findings})
	var human bytes.Buffer
	command{deps(nil)}.writeHuman(&human, doc)
	if human.Len() > MaxHumanBytes {
		t.Errorf("the human output is %d bytes, more than MaxHumanBytes = %d", human.Len(), MaxHumanBytes)
	}
	if got := len(envelope.Marshal(doc)); got > MaxJSONBytes {
		t.Errorf("the JSON is %d bytes, more than MaxJSONBytes = %d", got, MaxJSONBytes)
	}
	t.Logf("largest human output %d bytes, largest JSON %d bytes", human.Len(), len(envelope.Marshal(doc)))
}

func TestAFindingWithoutAMessageHasAFallback(t *testing.T) {
	var out bytes.Buffer
	command{deps(nil)}.writeHuman(&out, Document{Findings: []Finding{{Rule: "x-y", Path: "p"}}, Summary: Summary{Errors: 1}, Commit: "abc"})
	if !strings.Contains(out.String(), "(no description: this is a bug in ovdb, please report the rule id)") {
		t.Errorf("output %q", out.String())
	}
}

func realDeps() Deps {
	d := deps(nil)
	r := Real()
	d.Open, d.Stat = r.Open, r.Stat
	return d
}

func TestTheRealSeams(t *testing.T) {
	d := Real()
	dir := t.TempDir()
	if info, err := d.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("Stat: %v", err)
	}
	if _, ok := d.Open(dir).(*repo.Git); !ok {
		t.Error("Open does not give git")
	}
	// Without a git repository the real reader gives a verdict, not a crash.
	t.Setenv("GIT_DIR", filepath.Join(dir, "nowhere"))
	out, err := execute(t, realDeps(), "check", dir)
	if exitCode(err) == 0 && !strings.Contains(out, "OK") {
		t.Errorf("err %v, out %q", err, out)
	}
}

func TestEveryRuleHasAMessageThatIsPrintable(t *testing.T) {
	// The messages of findings are made by the rules, are printable ASCII and at most manifest.MaxMessageBytes; the command adds nothing to them.
	m := chinook(t)
	m.Nodes["ovdb.yaml"] = repo.Node{Kind: repo.File, Content: []byte("not: [yaml\n")}
	r := repo.Check(m, repo.Options{Profile: manifest.Publisher})
	if r.OK() {
		t.Fatal("accepted")
	}
	for _, f := range r.Findings {
		if f.Message == "" || len(f.Message) > manifest.MaxMessageBytes || safe(f.Message) != f.Message {
			t.Errorf("finding %+v", f)
		}
	}
}

// The summary lines, each of the four refusals and both successes, from documents made by hand: a run without a commit has one finding, so the plural of it
// is reachable only this way, and a repository of two manifests only with a second one.
func TestEverySummaryLine(t *testing.T) {
	for name, c := range map[string]struct {
		doc  Document
		want string
	}{
		"ok one":          {Document{OK: true, Commit: "abcdef0123456789", Manifests: 1}, "OK: commit abcdef012345, 1 manifest listed in OVDB.md, no problems.\n"},
		"ok many":         {Document{OK: true, Commit: "abc", Manifests: 2}, "OK: commit abc, 2 manifests listed in OVDB.md, no problems.\n"},
		"refused one":     {Document{Commit: "abc", Summary: Summary{Errors: 1}}, "Refused: 1 problem at commit abc. Fix it, commit, and run the check again.\n"},
		"refused many":    {Document{Commit: "abc", Summary: Summary{Errors: 3}}, "Refused: 3 problems at commit abc. Fix them, commit, and run the check again.\n"},
		"no commit, one":  {Document{Summary: Summary{Errors: 1}}, "Refused: 1 problem, and there is no commit to check. Fix it, commit, and run the check again.\n"},
		"no commit, many": {Document{Summary: Summary{Errors: 2}}, "Refused: 2 problems, and there is no commit to check. Fix them, commit, and run the check again.\n"},
	} {
		var out bytes.Buffer
		command{deps(nil)}.writeHuman(&out, c.doc)
		if !strings.HasPrefix(out.String(), c.want) {
			t.Errorf("%s: %q", name, out.String())
		}
	}
}
