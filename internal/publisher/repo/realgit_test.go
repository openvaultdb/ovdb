package repo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// The slower test: every case of the golden is built as a real git repository and read by the real
// git through Git and ExecRunner, and what is found must equal what the in-memory Reader finds. It
// needs git, and runs only when OVDB_REAL_GIT is set (the goldens job of ci.yml sets it): the
// default `go test` needs nothing but Go.

func git(t testing.TB, dir string, stdin []byte, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(gitEnv(os.Environ()), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// buildReal makes the repository of a model, and returns the directory to check.
func buildReal(t testing.TB, m *model) string {
	t.Helper()
	parent := t.TempDir()
	if m.location == "not-a-repository" {
		return parent
	}
	root := filepath.Join(parent, "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, nil, "init", "--quiet", "-b", "main")
	git(t, root, nil, "config", "core.ignorecase", "false") // two names that differ in case are two entries
	if m.location == "unborn" {
		return root
	}
	blobs := map[string]string{}
	blobID := func(content string) string {
		if _, ok := blobs[content]; !ok {
			blobs[content] = git(t, root, []byte(content), "hash-object", "-w", "--stdin")
		}
		return blobs[content]
	}
	prefix := ""
	if m.location == "subdirectory" {
		prefix = "sub/"
	}
	var index bytes.Buffer
	paths := make([]string, 0, len(m.tracked))
	for path := range m.tracked {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		node := m.tracked[path]
		mode, id := "100644", ""
		switch node.Kind {
		case Executable:
			mode = "100755"
		case Symlink:
			mode = "120000"
		case Submodule:
			mode, id = "160000", strings.Repeat("1", 40)
		}
		if id == "" {
			id = blobID(string(node.Content))
		}
		fmt.Fprintf(&index, "%s %s\t%s%s\x00", mode, id, prefix, path)
	}
	git(t, root, index.Bytes(), "update-index", "--add", "-z", "--index-info")
	tree := git(t, root, nil, "write-tree")
	first := git(t, root, nil, "commit-tree", "-m", "first", git(t, root, nil, "mktree"))
	commit := git(t, root, nil, "commit-tree", "-m", "case", "-p", first, tree)
	git(t, root, nil, "update-ref", "refs/heads/main", commit)
	dir := root
	switch m.location {
	case "bare":
		dir = filepath.Join(parent, "bare")
		git(t, parent, nil, "clone", "--quiet", "--bare", root, dir)
	case "shallow":
		dir = filepath.Join(parent, "shallow")
		git(t, parent, nil, "clone", "--quiet", "--depth", "1", "file://"+root, dir)
	case "detached":
		git(t, root, nil, "update-ref", "--no-deref", "HEAD", commit)
	case "subdirectory":
		dir = filepath.Join(root, "sub")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range m.untracked {
		if _, staged := m.tracked[path]; !staged {
			git(t, root, nil, "update-index", "--add", "--cacheinfo", "100644,"+blobID(text)+","+path)
		}
	}
	for _, files := range []map[string]string{m.untracked, m.dirty, ignoreFile(m)} {
		for path, text := range files {
			full := filepath.Join(root, prefix+path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func ignoreFile(m *model) map[string]string {
	if node, ok := m.tracked[".gitignore"]; ok {
		return map[string]string{".gitignore": string(node.Content)}
	}
	return nil
}

func realGit(t *testing.T) {
	if os.Getenv("OVDB_REAL_GIT") == "" {
		t.Skip("set OVDB_REAL_GIT=1 to read real repositories with git")
	}
}

func TestRealGitReadsEveryCaseAsTheMemoryReaderDoes(t *testing.T) {
	realGit(t)
	g := readGolden(t)
	for _, c := range g.Cases {
		t.Run(c.Group+"/"+c.Name, func(t *testing.T) {
			m := build(t, g, c.Ops)
			dir := buildReal(t, m)
			real := NewGit(ExecRunner{Dir: dir})
			memory := m.memory()
			for _, repository := range []*string{nil, c.Repository} {
				opts := Options{Profile: manifest.Publisher, Repository: repository}
				got, want := Check(real, opts), Check(memory, opts)
				if m.location == "not-a-repository" {
					if rulesOf(got)[0] != RuleUnreadable {
						t.Errorf("findings %v", got.Findings)
					}
					continue
				}
				if !reflect.DeepEqual(got.Findings, want.Findings) {
					t.Errorf("the real git finds\n  %v\nthe in-memory reader finds\n  %v", got.Findings, want.Findings)
				}
				if got.OK() && c.Verdict[0] != '1' {
					t.Errorf("accepted, and the checker refuses")
				}
			}
		})
	}
}

func TestRealGitFindsAnOversizeBlob(t *testing.T) {
	realGit(t)
	dir := buildReal(t, &model{tracked: map[string]Node{"OVDB.md": {Kind: File, Content: bytes.Repeat([]byte("x"), manifest.MaxDocumentBytes+100)}}, location: "normal"})
	only(t, Check(NewGit(ExecRunner{Dir: dir}), publisher()), "document-size", "OVDB.md", 0, "is more than 262144 bytes")
}
