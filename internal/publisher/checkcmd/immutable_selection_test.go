package checkcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/repo"
)

type originalProviderFixture struct {
	repo.Reader
	original repo.Reader
}

func TestLegacyRealGitCLIUsesSelectedCommit(t *testing.T) {
	p := chinook(t)
	dir := t.TempDir()
	dependencyGit(t, dir, "init", "--quiet", "-b", "main")
	for name, node := range p.Nodes {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, node.Content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	dependencyGit(t, dir, "add", "-A")
	dependencyGit(t, dir, "commit", "--quiet", "-m", "legacy provider")
	seams := deps(p)
	seams.Open, seams.Stat = Real().Open, Real().Stat
	if out, err := execute(t, seams, "check", dir, "--json"); err != nil {
		t.Fatalf("legacy real Git CLI: %v %s", err, out)
	}
}

func (r originalProviderFixture) OriginalObjects() (repo.Reader, bool) { return r.original, true }

func TestOriginalDiscoveryKeepsCLIRefusalAndEnvironmentExit(t *testing.T) {
	for _, cause := range []error{repo.ErrCannotRun, repo.ErrOldGit, repo.TimeoutError{}, repo.ErrObjectMissing, repo.ErrObjectCorrupt} {
		legacy := chinook(t)
		original := chinook(t)
		original.BrokenBlobs = map[string]error{"OVDB.md": cause}
		seams := deps(originalProviderFixture{Reader: legacy, original: original})
		want := 1
		if cause == repo.ErrCannotRun || cause == repo.ErrOldGit {
			want = 2
		}
		if _, ok := cause.(repo.TimeoutError); ok {
			want = 2
		}
		if out, err := execute(t, seams, "check", "repo", "--json"); exitCode(err) != want {
			t.Fatalf("original discovery %v: exit=%d want=%d output=%s", cause, exitCode(err), want, out)
		}
	}
}
