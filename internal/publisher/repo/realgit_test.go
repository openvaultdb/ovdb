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
	case "partial-blob", "partial-tree":
		git(t, root, nil, "config", "uploadpack.allowFilter", "true")
		git(t, root, nil, "config", "uploadpack.allowAnySHA1InWant", "true")
		dir = filepath.Join(parent, "partial")
		filter := map[string]string{"partial-blob": "blob:none", "partial-tree": "tree:0"}[m.location]
		git(t, parent, nil, "clone", "--quiet", "--filter="+filter, "--no-checkout", "file://"+root, dir)
	case "alternates-gone", "alternates-dangling":
		dir = filepath.Join(parent, "borrower")
		git(t, parent, nil, "clone", "--quiet", "--shared", "--no-checkout", root, dir)
		if m.location == "alternates-dangling" {
			git(t, dir, nil, "repack", "-a", "-d", "--quiet")
		}
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	case "subdirectory":
		dir = filepath.Join(root, "sub")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range m.breaks {
		breakObject(t, dir, b)
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

// breakObject deletes or damages one loose object of the repository in dir, after unpacking its packs.
func breakObject(t testing.TB, dir string, b [3]string) {
	t.Helper()
	gitDir := git(t, dir, nil, "rev-parse", "--absolute-git-dir")
	packs := filepath.Join(gitDir, "objects", "pack")
	names, _ := filepath.Glob(filepath.Join(packs, "*.pack"))
	for _, name := range names {
		pack, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		siblings, _ := filepath.Glob(strings.TrimSuffix(name, ".pack") + ".*")
		for _, sibling := range siblings {
			if err := os.Remove(sibling); err != nil {
				t.Fatal(err)
			}
		}
		git(t, dir, pack, "unpack-objects", "-q")
	}
	rev := "HEAD:" + b[1]
	switch {
	case b[0] == "break-tree" && b[1] == "":
		rev = "HEAD^{tree}"
	case b[0] == "break-commit":
		rev = "HEAD"
	}
	id := git(t, dir, nil, "rev-parse", rev)
	file := filepath.Join(gitDir, "objects", id[:2], id[2:])
	if b[2] == "missing" {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	content := []byte("garbage\n")
	if b[2] == "truncate" {
		whole, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		content = whole[:len(whole)/2]
	}
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}
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

// A repository's own configuration can name a program that git runs: core.fsmonitor, which `ls-files` runs when the untracked cache
// is on and a file is asked about (as Uncommitted does for a manifest that is only in the working tree). Without `-c core.fsmonitor=false`
// the reader would run it. The control is plain git, which does.
func TestRealGitDoesNotRunTheFsmonitorOfTheRepository(t *testing.T) {
	realGit(t)
	dir := buildReal(t, &model{tracked: map[string]Node{"OVDB.md": {Kind: File, Content: []byte(goodMD)}}, location: "normal"})
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf '\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "config", "core.fsmonitor", hook)
	git(t, dir, nil, "config", "core.untrackedCache", "true")
	if err := os.WriteFile(filepath.Join(dir, "ovdb.yaml"), []byte(ownManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "ls-files", "-z", "--cached", "--others", "--", "ovdb.yaml")
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run the file system monitor for ls-files: there is nothing to show")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	r := Check(NewGit(ExecRunner{Dir: dir}), publisher())
	if _, err := os.Stat(marker); err == nil {
		t.Error("the reader ran the repository's core.fsmonitor")
	}
	only(t, r, RuleManifest, "OVDB.md", 3, "it is in the working tree or the index but not committed")
}

// The mechanism behind MinGit, not the number: a repository whose promisor remote is a command (ext::), with an object missing, makes
// git run that command when it is allowed to fetch. The reader runs nothing. (The control is plain git with the variable unset; a git
// that needs no control to be told, for want of the feature, is the one MinGit refuses.)
func TestRealGitDoesNotRunThePromisorRemoteOfTheRepository(t *testing.T) {
	realGit(t)
	m := &model{tracked: goodRepository().Nodes, location: "normal"}
	dir := buildReal(t, m)
	breakObject(t, dir, [3]string{"break", modelPath, "missing"})
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "remote.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"core.repositoryformatversion": "1", "extensions.partialclone": "origin", "remote.origin.promisor": "true", "remote.origin.url": "ext::" + script, "protocol.ext.allow": "always"} {
		git(t, dir, nil, "config", k, v)
	}
	control := exec.Command("git", "-C", dir, "cat-file", "blob", "HEAD:"+modelPath)
	control.Env = slices.DeleteFunc(append(gitEnv(os.Environ()), "GIT_CONFIG_NOSYSTEM=1"), func(kv string) bool { return strings.HasPrefix(kv, "GIT_NO_LAZY_FETCH=") })
	_ = control.Run() // it fails: the object is not there, and the remote has nothing
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run the promisor remote when the variable is unset: there is nothing to show")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	r := Check(NewGit(ExecRunner{Dir: dir}), publisher())
	if _, err := os.Stat(marker); err == nil {
		t.Error("the reader ran the command of the repository's promisor remote")
	}
	only(t, r, RulePartial, "ovdb.yaml", 14, "cannot be read at the commit")
}

// The reason of an unreadable object is told from what git prints, not from the file names of the repository, and a truncated blob and a
// garbage commit have one (the reviewer's four inputs, on real git).
func TestRealGitTellsWhyAnObjectCannotBeReadWhateverTheFilesAreCalled(t *testing.T) {
	realGit(t)
	names := strings.NewReplacer("model/chinook.modelspec.json", "model/alternate.modelspec.json", "model/chinook.meaning.yaml", "model/corrupt.meaning.yaml")
	repository := func() *model {
		m := &model{tracked: goodRepository().Nodes, location: "normal"}
		for path, node := range m.tracked {
			if node.Kind == File {
				content := names.Replace(string(node.Content))
				delete(m.tracked, path)
				m.tracked[names.Replace(path)] = Node{Kind: File, Content: []byte(content)}
			}
		}
		return m
	}
	for name, c := range map[string]struct {
		spec [3]string
		rule string
		in   string
	}{
		"a missing blob at a path with alternate in it": {[3]string{"break", "model/alternate.modelspec.json", "missing"}, RuleObjectGone, "ovdb.yaml"},
		"a missing blob at a path with corrupt in it":   {[3]string{"break", "model/corrupt.meaning.yaml", "missing"}, RuleObjectGone, "ovdb.yaml"},
		"a truncated blob": {[3]string{"break", "model/alternate.modelspec.json", "truncate"}, RuleObjectBad, "ovdb.yaml"},
		"a garbage commit": {[3]string{"break-commit", "", "corrupt"}, RuleObjectBad, "repository"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := buildReal(t, repository())
			breakObject(t, dir, c.spec)
			r := Check(NewGit(ExecRunner{Dir: dir}), publisher())
			if len(r.Findings) != 1 || r.Findings[0].Rule != c.rule || r.Findings[0].Document != c.in {
				t.Errorf("findings %v, want one of %s in %s", r.Findings, c.rule, c.in)
			}
		})
	}
}
