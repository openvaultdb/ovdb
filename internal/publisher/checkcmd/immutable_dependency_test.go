package checkcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/repo"
)

func dependencyGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDependencyReplacementCannotForgeMetadataOrData(t *testing.T) {
	for _, path := range []string{"source.modelspec.json", "core.meaning.json", "decision.md", "input/$records/rows.json"} {
		for _, kind := range []string{"commit", "tree", "blob"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				_, metadata := nativeCLIProvider(t, strings.Repeat("a", 40), []byte("expected raw bytes"))
				dependencyGit(t, dir, "init", "--quiet", "-b", "main")
				for name, node := range metadata.Nodes {
					file := filepath.Join(dir, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
						t.Fatal(err)
					}
					content := node.Content
					if name == path {
						content = []byte("original bytes differ from declared reference")
					}
					if err := os.WriteFile(file, content, 0600); err != nil {
						t.Fatal(err)
					}
				}
				dependencyGit(t, dir, "add", "-A")
				dependencyGit(t, dir, "commit", "--quiet", "-m", "original")
				a := dependencyGit(t, dir, "rev-parse", "HEAD")
				if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), metadata.Nodes[path].Content, 0600); err != nil {
					t.Fatal(err)
				}
				dependencyGit(t, dir, "commit", "--quiet", "-am", "substitution")
				b := dependencyGit(t, dir, "rev-parse", "HEAD")
				pair := [2]string{a, b}
				if kind == "tree" {
					pair = [2]string{dependencyGit(t, dir, "rev-parse", a+"^{tree}"), dependencyGit(t, dir, "rev-parse", b+"^{tree}")}
				}
				if kind == "blob" {
					pair = [2]string{dependencyGit(t, dir, "rev-parse", a+":"+path), dependencyGit(t, dir, "rev-parse", b+":"+path)}
				}
				dependencyGit(t, dir, "update-ref", "refs/heads/main", a)
				provider, _ := nativeCLIProvider(t, a, []byte("expected raw bytes"))
				seams := deps(provider)
				seams.Stat = Real().Stat
				args := []string{"check", "provider", "--json", "--dependency", bindingAt(a, dir)}
				// Provider uses the fake reader; its dependency opens the real checkout.
				seams.Open = func(name string) repo.Reader {
					if name == "provider" {
						return provider
					}
					return Real().Open(name)
				}
				stat := seams.Stat
				seams.Stat = func(name string) (os.FileInfo, error) {
					if name == "provider" {
						return stat(dir)
					}
					return stat(name)
				}
				if out, err := execute(t, seams, args...); exitCode(err) != 1 {
					t.Fatalf("original control: %v %s", err, out)
				}
				dependencyGit(t, dir, "replace", pair[0], pair[1])
				t.Setenv("GIT_NO_REPLACE_OBJECTS", "0")
				t.Setenv("GIT_REPLACE_REF_BASE", "refs/other-replacements/")
				if out, err := execute(t, seams, args...); exitCode(err) != 1 || strings.Contains(out, `"ok":true`) {
					t.Fatalf("replacement forged %s: %v %s", path, err, out)
				}
			})
		}
	}
}
