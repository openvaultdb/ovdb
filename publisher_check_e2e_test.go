package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// `ovdb publisher check` through the built binary, against real Git repositories made here and the real ChinookDB repository at the commit the parity
// goldens pin. It needs git (2.45 or newer) and, for ChinookDB, the network, so it runs only with OVDB_REAL_GIT set, which the goldens job of ci.yml does;
// there a skipped subtest fails, so a run that skipped everything is not a green one.

// gitIn runs git in dir with a clean environment and the identity of a test.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// chinookFiles are the real ChinookDB repository's own files (the base of the repository golden of package repo).
func chinookFiles(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("internal", "publisher", "repo", "testdata", "reference", "repository.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Base map[string]string `json:"base"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil || len(golden.Base) == 0 {
		t.Fatalf("golden: %v", err)
	}
	return golden.Base
}

// repositoryOf makes a Git repository of files and commits them.
func repositoryOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "--quiet", "-b", "main")
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "--quiet", "-m", "x")
	return dir
}

var commitLine = regexp.MustCompile(`commit [0-9a-f]{12}`)

func TestPublisherCheckEndToEnd(t *testing.T) {
	if os.Getenv("OVDB_REAL_GIT") == "" {
		t.Skip("set OVDB_REAL_GIT=1 to run ovdb publisher check against real repositories")
	}
	sub := func(name string, body func(t *testing.T)) {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() {
				if t.Skipped() {
					t.Error("a subtest skipped: with OVDB_REAL_GIT set nothing is skipped")
				}
			})
			body(t)
		})
	}
	files := chinookFiles(t)
	good := repositoryOf(t, files)
	head := gitIn(t, good, "rev-parse", "HEAD")

	sub("an accepted repository, for people", func(t *testing.T) {
		stdout, stderr, code := runOVDB(t, nil, "publisher", "check", good)
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "OK: commit "+head[:12]+", 1 manifest listed in OVDB.md, no problems.\n") || !strings.Contains(stdout, "is not its acceptance") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})
	sub("an accepted repository, as JSON, with --repository", func(t *testing.T) {
		stdout, stderr, code := runOVDB(t, nil, "publisher", "check", good, "--json", "--repository", "https://github.com/datatug/chinookdb")
		want := `{"schema":1,"command":"publisher check","commit":"` + head + `","profile":"publisher","ok":true,"manifests":1,"findings":[],"summary":{"errors":0,"capped":false,"omitted":0}}` + "\n"
		if code != 0 || stderr != "" || stdout != want {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})
	sub("the default path is the current directory", func(t *testing.T) {
		cmd := ovdbCommand("publisher", "check")
		cmd.Dir = good
		out, err := cmd.Output()
		if err != nil || !strings.HasPrefix(string(out), "OK: commit "+head[:12]) {
			t.Errorf("%v: %q", err, out)
		}
	})
	broken := map[string]string{}
	for name, text := range files {
		broken[name] = text
	}
	broken["ovdb.yaml"] = strings.Replace(files["ovdb.yaml"], "  - Track\n", "  - Tracks\n", 1)
	refused := repositoryOf(t, broken)
	sub("a refused repository, for people", func(t *testing.T) {
		stdout, stderr, code := runOVDB(t, nil, "publisher", "check", refused)
		if code != 1 || stderr != "" || !strings.Contains(stdout, "  [repo-recordsets]\n  recordsets lacks the ModelSpec entities") || !commitLine.MatchString(stdout) || !strings.Contains(stdout, "Refused: 2 problems at commit ") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})
	sub("a refused repository, as JSON", func(t *testing.T) {
		stdout, _, code := runOVDB(t, nil, "publisher", "check", refused, "--json")
		var doc struct {
			OK       bool `json:"ok"`
			Findings []struct{ Rule, Path string }
		}
		if code != 1 || json.Unmarshal([]byte(stdout), &doc) != nil || doc.OK || len(doc.Findings) != 2 || doc.Findings[0].Rule != "repo-recordsets" || doc.Findings[0].Path != "ovdb.yaml" {
			t.Errorf("exit %d, stdout %q", code, stdout)
		}
	})
	sub("--repository that the manifest does not say", func(t *testing.T) {
		stdout, _, code := runOVDB(t, nil, "publisher", "check", good, "--repository", "https://github.com/other/repo")
		if code != 1 || !strings.Contains(stdout, "[repo-repository]") {
			t.Errorf("exit %d, stdout %q", code, stdout)
		}
	})
	sub("not a Git repository, and no commit yet", func(t *testing.T) {
		plain := t.TempDir()
		stdout, _, code := runOVDB(t, nil, "publisher", "check", plain)
		if code != 1 || !strings.Contains(stdout, "[repo-unreadable]") || !strings.Contains(stdout, "Refused: git could not read a repository here.") {
			t.Errorf("not a repository: exit %d, stdout %q", code, stdout)
		}
		empty := t.TempDir()
		gitIn(t, empty, "init", "--quiet", "-b", "main")
		stdout, _, code = runOVDB(t, nil, "publisher", "check", empty)
		if code != 1 || !strings.Contains(stdout, "[repo-no-commit]") || !strings.Contains(stdout, "Refused: this repository has no commit yet.") {
			t.Errorf("no commit: exit %d, stdout %q", code, stdout)
		}
		bare := filepath.Join(t.TempDir(), "bare.git")
		gitIn(t, filepath.Dir(bare), "clone", "--quiet", "--bare", good, bare)
		stdout, _, code = runOVDB(t, nil, "publisher", "check", bare)
		if code != 1 || !strings.Contains(stdout, "[repo-bare]") || !strings.Contains(stdout, "Refused: this is a bare repository") {
			t.Errorf("bare: exit %d, stdout %q", code, stdout)
		}
		stdout, _, code = runOVDB(t, nil, "publisher", "check", filepath.Join(good, "model"))
		if code != 1 || !strings.Contains(stdout, "[repo-subdirectory]") || !strings.Contains(stdout, "Refused: this directory is inside a repository, not at its top.") {
			t.Errorf("subdirectory: exit %d, stdout %q", code, stdout)
		}
	})
	sub("git is missing", func(t *testing.T) {
		bin := t.TempDir() // no git in it
		stdout, stderr, code := runOVDB(t, []string{"PATH=" + bin}, "publisher", "check", good)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "Couldn't run the check") || !strings.Contains(stderr, "git could not be run") || strings.Contains(stderr, `\"`) {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		stdout, _, code = runOVDB(t, []string{"PATH=" + bin}, "publisher", "check", good, "--json")
		if code != 2 || !strings.Contains(stdout, `"code":"dependency_missing"`) {
			t.Errorf("--json: exit %d, stdout %q", code, stdout)
		}
	})
	sub("git older than 2.45", func(t *testing.T) {
		bin := t.TempDir()
		fake := filepath.Join(bin, "git")
		if runtime.GOOS == "windows" {
			t.Skip("a shell script stands in for git")
		}
		if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'git version 2.39.5'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, stderr, code := runOVDB(t, []string{"PATH=" + bin}, "publisher", "check", good)
		if code != 2 || !strings.Contains(stderr, "git is older than 2.45") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	sub("every usage error exits 2", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		for name, args := range map[string][]string{
			"an unknown flag":        {"publisher", "check", good, "--bogus"},
			"too many arguments":     {"publisher", "check", good, good},
			"a path that is missing": {"publisher", "check", filepath.Join(good, "nowhere")},
			"a path that is a file":  {"publisher", "check", file},
			"--repository not a URL": {"publisher", "check", good, "--repository", "chinookdb"},
			"an unknown subcommand":  {"publisher", "bogus"},
		} {
			stdout, stderr, code := runOVDB(t, nil, args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, "Couldn't run ovdb publisher") {
				t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
			}
		}
		stdout, _, code := runOVDB(t, nil, "publisher", "check", good, "--repository", "x", "--json")
		if code != 2 || !strings.Contains(stdout, `"code":"invalid_argument"`) {
			t.Errorf("--json usage error: exit %d, stdout %q", code, stdout)
		}
	})
	sub("a hostile file name is shown harmlessly", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a Windows file name cannot hold these characters")
		}
		hostile := map[string]string{}
		for name, text := range files {
			hostile[name] = text
		}
		hostile["evil\x1b[31m\x01name"] = "x"
		dir := repositoryOf(t, hostile)
		stdout, _, code := runOVDB(t, nil, "publisher", "check", dir)
		if code != 1 || !strings.Contains(stdout, `evil\x1b[31m\x01name`) {
			t.Fatalf("exit %d, stdout %q", code, stdout)
		}
		for _, r := range strings.TrimSuffix(stdout, "\n") {
			if r != '\n' && (r < 0x20 || r > 0x7e) {
				t.Errorf("the output has the character %U", r)
			}
		}
	})
	sub("the real ChinookDB repository at the pinned commit", func(t *testing.T) {
		pins, err := os.ReadFile(filepath.Join("internal", "publisher", "references.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`chinookdb: \{ repository: '([^']+)', commit: '([0-9a-f]{40})' \}`).FindSubmatch(pins)
		if m == nil {
			t.Fatal("references.mjs has no pin for chinookdb")
		}
		dir := t.TempDir()
		gitIn(t, dir, "init", "--quiet", "-b", "main")
		gitIn(t, dir, "fetch", "--quiet", "--depth", "1", "https://github.com/"+string(m[1])+".git", string(m[2]))
		gitIn(t, dir, "checkout", "--quiet", "FETCH_HEAD")
		stdout, stderr, code := runOVDB(t, nil, "publisher", "check", dir, "--repository", "https://github.com/"+string(m[1]))
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "OK: commit "+string(m[2])[:12]) {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})
}
