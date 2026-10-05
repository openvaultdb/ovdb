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
	"time"

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
		TimedOut: func(message, reason, next string) error {
			return exitcode.Usage(envelope.New(envelope.Timeout, message).WithReason(reason).WithNext(envelope.Next{Label: next}))
		},
		WriteFailed: func(reason string) error {
			return exitcode.Usage(envelope.New(envelope.Internal, uicopy.T("publisher.write.failed", nil)).WithReason(reason))
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
	if err != nil || !strings.HasPrefix(out, want) || !strings.Contains(out, "1 manifest listed in OVDB.md, no problems.\n") || !strings.Contains(out, "so a pass here is not its acceptance.") {
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

// commitMemory is a Memory with a commit id of its own.
type commitMemory struct {
	*repo.Memory
	commit string
}

func (c commitMemory) Head() (string, error) {
	if _, err := c.Memory.Head(); err != nil {
		return "", err
	}
	return c.commit, nil
}

const refusedCommit = "98ff05b4119c5985cade0f961ecdb25e8b1277b6"

// renamedRecordset is the Chinook repository with one recordset renamed: two findings.
func renamedRecordset(t testing.TB) *repo.Memory {
	m := chinook(t)
	m.Nodes["ovdb.yaml"] = repo.Node{Kind: repo.File, Content: bytes.ReplaceAll(m.Nodes["ovdb.yaml"].Content, []byte("  - Track\n"), []byte("  - Tracks\n"))}
	return m
}

// The text form is pinned too: the README shows these two files (TestTheREADMEShowsTheGoldenFiles).
func TestTheTextFormIsPinnedByGoldenFiles(t *testing.T) {
	for name, c := range map[string]struct {
		reader repo.Reader
		code   int
	}{
		"accepted": {pinnedMemory{chinook(t)}, 0},
		"refused":  {commitMemory{renamedRecordset(t), refusedCommit}, 1},
	} {
		out, err := execute(t, deps(c.reader), "check", "repo")
		if exitCode(err) != c.code {
			t.Errorf("%s: exit %d", name, exitCode(err))
		}
		golden := filepath.Join("testdata", name+".txt")
		if os.Getenv("OVDB_UPDATE_GOLDEN") != "" {
			if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if want, err := os.ReadFile(golden); err != nil || out != string(want) {
			t.Errorf("%s: the text differs from %s:\n got %q\nwant %q", name, golden, out, want)
		}
	}
}

// The README's examples are copies of the golden files, byte for byte: each is a fenced block that follows its marker comment.
func TestTheREADMEShowsTheGoldenFiles(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accepted.txt", "refused.txt", "accepted.json", "refused.json"} {
		marker := "<!-- publisher-check-golden: " + name + " -->\n"
		_, after, found := strings.Cut(string(readme), marker)
		if !found {
			t.Errorf("README has no marker for %s", name)
			continue
		}
		_, block, _ := strings.Cut(after, "\n") // the fence line
		block, _, _ = strings.Cut(block, "```")
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil || block != string(want) {
			t.Errorf("README example %s differs from the golden file:\n got %q\nwant %q", name, block, want)
		}
	}
}

func TestTheDocumentsArePinnedByGoldenFiles(t *testing.T) {
	good := pinnedMemory{chinook(t)}
	refused := renamedRecordset(t)
	hostile := chinook(t)
	hostile.Nodes["evil\x1b[31m\nname\u00e9\xff"] = repo.Node{Kind: repo.File}
	for name, c := range map[string]struct {
		reader repo.Reader
		args   []string
		code   int
	}{
		"accepted":     {good, []string{"check", "repo", "--json"}, 0},
		"accepted-url": {good, []string{"check", "repo", "--json", "--repository", "https://github.com/datatug/chinookdb"}, 0},
		"refused":      {commitMemory{refused, refusedCommit}, []string{"check", "repo", "--json"}, 1},
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
}

// Each state that leaves no commit id to show has a summary that is true of it, and says what was found instead of a commit.
func TestEveryStateWithoutACommitHasATrueSummary(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"no commit yet":         {repo.ErrNoCommit, "Refused: this repository has no commit yet. Commit OVDB.md and the manifests it lists, then run the check again.\n"},
		"a bare repository":     {repo.ErrBare, "Refused: this is a bare repository. Run the check at the top of a clone that has a working tree.\n"},
		"a subdirectory":        {repo.ErrSubdirectory, "Refused: this directory is inside a repository, not at its top. Run the check at the top of the repository.\n"},
		"not a repository":      {errors.New("fatal: not a git repository"), "Refused: git could not read a repository here. Give the directory of a Git repository.\n"},
		"a damaged commit":      {repo.ErrObjectCorrupt, "Refused: git could not read the commit's objects from this repository. Run the check in a complete clone.\n"},
		"a missing commit":      {repo.ErrObjectMissing, "Refused: git could not read the commit's objects from this repository. Run the check in a complete clone.\n"},
		"borrowed objects gone": {repo.ErrAlternates, "Refused: git could not read the commit's objects from this repository. Run the check in a complete clone.\n"},
		"a partial clone":       {repo.ErrPartialClone, "Refused: git could not read the commit's objects from this repository. Run the check in a complete clone.\n"},
	} {
		out, err := execute(t, deps(&repo.Memory{Err: c.err}), "check", "repo")
		if exitCode(err) != 1 || !strings.HasSuffix(out, c.want) {
			t.Errorf("%s: exit %d, output %q", name, exitCode(err), out)
		}
		if strings.Contains(out, "no commit to check") || strings.Contains(out, "commit, and run") {
			t.Errorf("%s: the summary talks of a commit that is not the matter: %q", name, out)
		}
	}
}

// A partial clone whose commit can be read has the summary that says to run the check in a complete clone, not "commit": whether or not the commit id is known.
func TestAPartialCloneIsToldToUseACompleteClone(t *testing.T) {
	want := "Refused: git could not read the commit's objects from this repository. Run the check in a complete clone.\n"
	known := chinook(t)
	known.BrokenBlobs = map[string]error{"ovdb.yaml": repo.ErrPartialClone}
	out, err := execute(t, deps(pinnedMemory{known}), "check", "repo")
	if exitCode(err) != 1 || !strings.HasSuffix(out, want) || strings.Contains(out, "79e7bb0b1d6f") || strings.Contains(out, "Fix it, commit") {
		t.Errorf("known commit: exit %d, output %q", exitCode(err), out)
	}
	out, err = execute(t, deps(&repo.Memory{Err: repo.ErrPartialClone}), "check", "repo")
	if exitCode(err) != 1 || !strings.HasSuffix(out, want) {
		t.Errorf("no commit: exit %d, output %q", exitCode(err), out)
	}
	for _, e := range []error{repo.ErrObjectMissing, repo.ErrObjectCorrupt, repo.ErrAlternates} {
		m := chinook(t)
		m.BrokenBlobs = map[string]error{"ovdb.yaml": e}
		if out, _ := execute(t, deps(pinnedMemory{m}), "check", "repo"); !strings.HasSuffix(out, want) {
			t.Errorf("%v: output %q", e, out)
		}
	}
}

// At the cap the notice is not a problem: the count is of findings, and the text and the JSON say that more were left out.
func TestAtTheCapTheNoticeIsNotCounted(t *testing.T) {
	m := chinook(t)
	entries := make([]string, 400)
	for i := range entries {
		entries[i] = "../x"
	}
	m.Nodes["OVDB.md"] = repo.Node{Kind: repo.File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(entries, ", ") + "]\n---\n")}
	out, err := execute(t, deps(pinnedMemory{m}), "check", "repo")
	want := "Refused: 100 problems shown at commit 79e7bb0b1d6f, and 300 more are not shown (at most 100 are reported). Fix them, commit, and run the check again.\n"
	if exitCode(err) != 1 || !strings.HasSuffix(out, want) || strings.Contains(out, "findings-capped") || strings.Count(out, "[ovdbmd-") != 100 {
		t.Errorf("exit %d, output ends %q", exitCode(err), out[max(0, len(out)-300):])
	}
	out, _ = execute(t, deps(pinnedMemory{m}), "check", "repo", "--json")
	var doc Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Summary != (Summary{Errors: 100, Capped: true, Omitted: 300}) || len(doc.Findings) != 101 || doc.Findings[100].Rule != manifest.RuleCapped {
		t.Errorf("summary %+v, %d findings, %v", doc.Summary, len(doc.Findings), err)
	}
	// Not capped: the fields say so.
	out, _ = execute(t, deps(pinnedMemory{renamedRecordset(t)}), "check", "repo", "--json")
	if !strings.HasSuffix(out, `"summary":{"errors":2,"capped":false,"omitted":0}}`+"\n") {
		t.Errorf("output %q", out)
	}
}

// The text form shows the whole message a rule wrote: the advice at its end is not lost.
func TestTheTextFormShowsTheWholeMessage(t *testing.T) {
	out, _ := execute(t, deps(&repo.Memory{Err: repo.ErrPartialClone}), "check", "repo")
	if !strings.Contains(out, "(git checkout fetches them)\n") {
		t.Errorf("the advice of a partial clone is cut: %q", out)
	}
	// A refusal of a kind no rule gives a summary for is told by the fallback.
	var other bytes.Buffer
	command{deps(nil)}.writeHuman(&other, Document{Findings: []Finding{{Rule: "x-y", Message: "m"}}, Summary: Summary{Errors: 1}})
	if !strings.HasSuffix(other.String(), "Refused: the repository could not be read. The problem above says why.\n") {
		t.Errorf("output %q", other.String())
	}
	// A message of the longest length the rules allow, for a finding of any shape, ends with its own last word.
	m := chinook(t)
	m.Nodes["model/chinook.meaning.yaml"] = repo.Node{Kind: repo.File, Content: bytes.ReplaceAll(m.Nodes["model/chinook.meaning.yaml"].Content, []byte("  chinook: chinook.modelspec.hcl"), []byte(`  chinook: "a b"`))}
	r := repo.Check(m, repo.Options{Profile: manifest.Publisher})
	long := 0
	for _, f := range r.Findings {
		long = max(long, len(f.Message))
	}
	out, _ = execute(t, deps(pinnedMemory{m}), "check", "repo")
	for _, f := range r.Findings {
		if !strings.Contains(out, "\n  "+f.Message+"\n") {
			t.Errorf("the message is not shown whole: %q (output %q)", f.Message, out)
		}
	}
	if long < 200 {
		t.Errorf("the longest message is %d bytes: the test does not reach past the old cut", long)
	}
}

// Git that did not finish, or is too old, in any call, is "could not run": exit 2, and nothing printed about the repository.
type scripted struct{ failOn string }

func (s scripted) Run(args []string, limit int) ([]byte, error) {
	if args[0] == "--no-replace-objects" {
		args = args[1:]
	}
	if args[0] == "-c" { // the flags every call has
		args = args[2:]
	}
	switch args[0] {
	case "ls-tree":
		if s.failOn == "ls-tree" {
			return nil, repo.TimeoutError{After: 30 * time.Second}
		}
		return []byte("100644 blob " + strings.Repeat("b", 40) + "\tOVDB.md\x00"), nil
	case "version":
		return []byte("git version 2.50.0\n"), nil
	case "rev-parse":
		if args[1] == "--is-bare-repository" {
			return []byte("false\n\n"), nil
		}
		return []byte(strings.Repeat("a", 40) + "\n"), nil
	}
	if args[0] == s.failOn {
		return nil, repo.TimeoutError{After: 30 * time.Second}
	}
	return nil, errors.New("not scripted")
}

func TestGitThatDoesNotFinishIsCouldNotRun(t *testing.T) {
	for _, call := range []string{"ls-tree", "cat-file"} {
		out, err := execute(t, deps(repo.NewGit(scripted{failOn: call})), "check", "repo")
		if exitCode(err) != 2 || out != "" || !strings.Contains(err.Error(), "Couldn't finish the check") || !strings.Contains(err.Error(), "git was found, but it did not finish in 30 seconds") || errors.Is(err, ErrRefused) || envelope.As(err) == nil || envelope.As(err).Code != envelope.Timeout || len(envelope.As(err).Next) != 1 || !strings.Contains(envelope.As(err).Next[0].Label, "`git status`") || strings.Contains(envelope.As(err).Next[0].Label, "Install git") {
			t.Errorf("%s: exit %d, output %q, err %v", call, exitCode(err), out, err)
		}
	}
	// Git that is missing or too old is another code: the timeout's is its own.
	if _, err := execute(t, deps(&repo.Memory{Err: repo.ErrOldGit}), "check", "repo"); envelope.As(err) == nil || envelope.As(err).Code != envelope.DependencyMissing {
		t.Errorf("old git: %v", err)
	}
	// The same through a Blob that times out after the tree was listed.
	m := chinook(t)
	out, err := execute(t, deps(blobFails{m}), "check", "repo")
	if exitCode(err) != 2 || out != "" || !strings.Contains(err.Error(), "Couldn't finish the check") {
		t.Errorf("blob: exit %d, output %q, err %v", exitCode(err), out, err)
	}
}

type blobFails struct{ *repo.Memory }

func (blobFails) Blob(string, int) ([]byte, error) {
	return nil, repo.TimeoutError{After: 30 * time.Second}
}

// A result that cannot be written is not a pass: exit 2, however the check went, in both forms.
type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) { return 0, errors.New("write |1: broken pipe") }

func TestAResultThatCannotBeWrittenIsAnError(t *testing.T) {
	for _, args := range [][]string{{"check", "repo"}, {"check", "repo", "--json"}} {
		for name, reader := range map[string]repo.Reader{"a pass": chinook(t), "a refusal": renamedRecordset(t)} {
			cmd := NewCmd(deps(reader))
			cmd.SetOut(brokenPipe{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(args)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if exitCode(err) != 2 || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "Couldn't write the result") || !strings.Contains(err.Error(), "broken pipe") {
				t.Errorf("%s %v: exit %d, err %v", name, args, exitCode(err), err)
			}
		}
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
	hostile.Nodes["a\x1b]0;title\x07\r\nb\u202e\x00\x7f"] = repo.Node{Kind: repo.File}
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
	if got := safe("a\x7fb\x1fc\x80"); got != `a\x7fb\x1fc\x80` {
		t.Errorf("DEL and the controls: %q", got)
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
		"ok one":       {Document{OK: true, Commit: "abcdef0123456789", Manifests: 1}, "OK: commit abcdef012345, 1 manifest listed in OVDB.md, no problems.\n"},
		"ok many":      {Document{OK: true, Commit: "abc", Manifests: 2}, "OK: commit abc, 2 manifests listed in OVDB.md, no problems.\n"},
		"refused one":  {Document{Commit: "abc", Summary: Summary{Errors: 1}}, "Refused: 1 problem at commit abc. Fix it, commit, and run the check again.\n"},
		"refused many": {Document{Commit: "abc", Summary: Summary{Errors: 3}}, "Refused: 3 problems at commit abc. Fix them, commit, and run the check again.\n"},
	} {
		var out bytes.Buffer
		command{deps(nil)}.writeHuman(&out, c.doc)
		if !strings.HasPrefix(out.String(), c.want) {
			t.Errorf("%s: %q", name, out.String())
		}
	}
}
