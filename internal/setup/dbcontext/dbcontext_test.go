package dbcontext

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return Canonical(dir)
}

func use(t *testing.T, home string, ids []string, change Change) Document {
	t.Helper()
	document, err := Apply(home, ids, change)
	if err != nil {
		t.Fatalf("Apply(%+v): %v", change, err)
	}
	return document
}

func resolveIn(home string, ids []string, cwd string) *Context {
	return Resolve(home, ids, Request{Dirs: Find(cwd).Dirs}).Context
}

// AC:use-is-project-scoped: `use` in /p/a/src stores for the Git root /p/a;
// another project keeps its own.
func TestUseIsProjectScoped(t *testing.T) {
	t.Parallel()
	base, home := t.TempDir(), t.TempDir()
	a, b := mkdir(t, base, "p", "a"), mkdir(t, base, "p", "b")
	mkdir(t, a, ".git")
	mkdir(t, b, ".git")
	src := mkdir(t, a, "src")
	ids := []string{"todo", "notes"}

	lookup := Find(src)
	if lookup.Root != a || !slices.Equal(lookup.Dirs, []string{src, a}) {
		t.Fatalf("Find(%s) = %+v", src, lookup)
	}
	document := use(t, home, ids, Change{Scope: ScopeProject, Dir: lookup.Root, Database: "todo"})
	if want := "Now using todo for this project (" + a + ")"; document.Message != want {
		t.Errorf("message = %q, want %q", document.Message, want)
	}
	use(t, home, ids, Change{Scope: ScopeProject, Dir: Find(b).Root, Database: "notes"})

	if got := resolveIn(home, ids, a); got == nil || *got != (Context{Database: "todo", Path: "/", Scope: ScopeProject, Dir: a}) {
		t.Errorf("in /p/a: %+v", got)
	}
	if got := resolveIn(home, ids, b); got == nil || got.Database != "notes" {
		t.Errorf("in /p/b: %+v", got)
	}
	// Nothing is written into the project.
	if entries, _ := os.ReadDir(a); len(entries) != 2 {
		t.Errorf("project /p/a gained files: %v", entries)
	}
}

// AC:walk-up-outside-git: outside Git the lookup walks to the top; the
// project context wins over the global default.
func TestWalkUpOutsideGit(t *testing.T) {
	t.Parallel()
	base, home := t.TempDir(), t.TempDir()
	proj := mkdir(t, base, "w", "proj")
	deep := mkdir(t, proj, "src", "deep")
	ids := []string{"notes", "todo"}
	use(t, home, ids, Change{Scope: ScopeProject, Dir: Find(proj).Root, Database: "todo"})
	use(t, home, ids, Change{Scope: ScopeGlobal, Database: "notes"})
	if got := resolveIn(home, ids, deep); got == nil || got.Database != "todo" || got.Dir != proj {
		t.Errorf("in deep: %+v", got)
	}
	if got := resolveIn(home, ids, base); got == nil || *got != (Context{Database: "notes", Path: "/", Scope: ScopeGlobal}) {
		t.Errorf("elsewhere: %+v", got)
	}
}

// Inside a repository the walk-up stops at the Git root: a context stored
// above it does not leak in.
func TestWalkUpStopsAtGitRoot(t *testing.T) {
	t.Parallel()
	base, home := t.TempDir(), t.TempDir()
	outer := mkdir(t, base, "outer")
	repo := mkdir(t, outer, "repo")
	mkdir(t, repo, ".git")
	use(t, home, []string{"a", "b"}, Change{Scope: ScopeProject, Dir: outer, Database: "a"})
	if got := resolveIn(home, []string{"a", "b"}, mkdir(t, repo, "x")); got != nil {
		t.Errorf("context above the Git root applied: %+v", got)
	}
}

// AC:worktree-is-own-project: a linked worktree has a `.git` file and is
// its own project.
func TestWorktreeIsOwnProject(t *testing.T) {
	t.Parallel()
	base, home := t.TempDir(), t.TempDir()
	main := mkdir(t, base, "r", "main")
	mkdir(t, main, ".git")
	wt := mkdir(t, main, ".worktrees", "wt")
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../../.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := []string{"notes", "todo"}
	use(t, home, ids, Change{Scope: ScopeProject, Dir: Find(main).Root, Database: "todo"})
	if lookup := Find(wt); lookup.Root != wt {
		t.Errorf("worktree root = %s", lookup.Root)
	}
	if got := resolveIn(home, ids, wt); got != nil {
		t.Errorf("worktree used main's context: %+v", got)
	}
}

// A project reached through a symlink has the same context.
func TestSymlinkedProjectSharesContext(t *testing.T) {
	t.Parallel()
	base, home := t.TempDir(), t.TempDir()
	real := mkdir(t, base, "real")
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ids := []string{"notes", "todo"}
	use(t, home, ids, Change{Scope: ScopeProject, Dir: Find(link).Root, Database: "todo"})
	if got := resolveIn(home, ids, real); got == nil || got.Database != "todo" || got.Dir != real {
		t.Errorf("via real path: %+v", got)
	}
}

// Windows paths differ only in case (drive letter included) share a key.
func TestWindowsKeysIgnoreCase(t *testing.T) {
	t.Parallel()
	if key("windows", `C:\Work\Shop`) != key("windows", `c:\work\shop`) {
		t.Error("windows keys differ by case")
	}
	if key("linux", "/Work") == key("linux", "/work") {
		t.Error("linux keys fold case")
	}
	if goruntime.GOOS == "windows" && Key(`D:\X`) != Key(`d:\x`) {
		t.Error("Key on windows is case-sensitive")
	}
}

// AC:precedence-ladder: flag and environment beat project and global; the
// only database applies last; nothing applies with two and no context.
func TestLadder(t *testing.T) {
	t.Parallel()
	base, home := t.TempDir(), t.TempDir()
	proj := mkdir(t, base, "proj")
	ids := []string{"notes", "todo"}
	use(t, home, ids, Change{Scope: ScopeProject, Dir: proj, Database: "todo", Path: "/lists"})
	use(t, home, ids, Change{Scope: ScopeGlobal, Database: "notes"})
	dirs := Find(proj).Dirs
	for _, tc := range []struct {
		request Request
		want    Context
	}{
		{Request{Dirs: dirs, Database: "notes", Scope: ScopeEnvironment}, Context{Database: "notes", Path: "/", Scope: ScopeEnvironment}},
		{Request{Dirs: dirs, Database: "TODO", Path: "items", Scope: ScopeFlag}, Context{Database: "todo", Path: "/items", Scope: ScopeFlag}},
		{Request{Dirs: dirs}, Context{Database: "todo", Path: "/lists", Scope: ScopeProject, Dir: proj}},
		{Request{}, Context{Database: "notes", Path: "/", Scope: ScopeGlobal}},
	} {
		if got := Resolve(home, ids, tc.request).Context; got == nil || *got != tc.want {
			t.Errorf("Resolve(%+v) = %+v, want %+v", tc.request, got, tc.want)
		}
	}
	if got := Resolve(t.TempDir(), []string{"todo"}, Request{}).Context; got == nil || got.Scope != ScopeOnly {
		t.Errorf("only database: %+v", got)
	}
	document := Resolve(t.TempDir(), ids, Request{})
	if document.Context != nil || len(document.Next) == 0 {
		t.Errorf("two databases, no context: %+v", document)
	}
	// AC:no-context-error's envelope lists both databases.
	if e := NoContext(ids); e.Code != envelope.NotFound || e.Reason != "Registered databases: notes, todo." || e.Next[0].Command != "ovdb use <database>" {
		t.Errorf("NoContext = %+v", e)
	}
}

func TestApplyRefusesAndClears(t *testing.T) {
	t.Parallel()
	home, dir := t.TempDir(), Canonical(t.TempDir())
	ids := []string{"notes", "todo"}
	for _, change := range []Change{
		{Scope: ScopeProject, Dir: dir, Database: "shop"},
		{Scope: ScopeProject, Dir: "relative", Database: "todo"},
		{Scope: "machine", Database: "todo"},
		{Scope: ScopeGlobal, Database: "todo", Path: "/x/50%off"},
	} {
		if _, err := Apply(home, ids, change); envelope.As(err) == nil {
			t.Errorf("Apply(%+v) = %v, want an envelope error", change, err)
		}
	}
	_, err := Apply(home, ids, Change{Scope: ScopeProject, Dir: dir, Database: "shop"})
	if e := envelope.As(err); e.Code != envelope.NotFound || e.Reason != "No database named shop is registered. Registered databases: notes, todo." {
		t.Errorf("unknown database = %+v", e)
	}

	use(t, home, ids, Change{Scope: ScopeProject, Dir: dir, Database: "todo"})
	use(t, home, ids, Change{Scope: ScopeGlobal, Database: "todo"})
	if err := ClearDatabase(home, "TODO"); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(home, ids, Request{Dirs: []string{dir}}); got.Context != nil || got.Global != nil {
		t.Errorf("after removing todo: %+v", got)
	}
	use(t, home, ids, Change{Scope: ScopeProject, Dir: dir, Database: "notes"})
	cleared := use(t, home, ids, Change{Scope: ScopeProject, Dir: dir, Clear: true})
	if cleared.Message != "Cleared the project context for "+dir+"." {
		t.Errorf("clear message = %q", cleared.Message)
	}
	if got := Resolve(home, ids, Request{Dirs: []string{dir}}).Context; got != nil {
		t.Errorf("after clear: %+v", got)
	}
}
