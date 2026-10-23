package pinchain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitRuntimeAdmissionAndCommandTrap(t *testing.T) {
	for _, version := range []string{"git version 2.44.9", "unknown", "git version 2.45", "git version 1.99.0", "git version 2.45.0\nextra"} {
		called := 0
		runtime, err := admitGit(context.Background(), func(string) (string, error) { return "/synthetic/git", nil }, func(_ context.Context, path, dir string, args ...string) ([]byte, error) {
			called++
			if path != "/synthetic/git" || dir != "" || strings.Join(args, " ") != "version" {
				t.Fatal("object call before admission")
			}
			return []byte(version), nil
		})
		if err == nil || runtime != nil || called != 1 {
			t.Fatal("old/unknown Git admitted")
		}
	}
	for _, version := range []string{"git version 2.45.0", "git version 3.0.0", "git version 2.54.0 (Apple Git-157)", "git version 2.45.0.windows.1", "git version 2.45.0.1"} {
		runtime, err := admitGit(context.Background(), func(string) (string, error) { return "/synthetic/git", nil }, func(context.Context, string, string, ...string) ([]byte, error) { return []byte(version), nil })
		if err != nil || runtime.Executable() != "/synthetic/git" || runtime.Version() != version {
			t.Fatal(err)
		}
	}
	if _, err := admitGit(context.Background(), func(string) (string, error) { return "", errors.New("missing") }, nil); err == nil {
		t.Fatal("missing Git accepted")
	}
	if _, err := admitGit(context.Background(), func(string) (string, error) { return "/git", nil }, func(context.Context, string, string, ...string) ([]byte, error) { return nil, errors.New("failed") }); err == nil {
		t.Fatal("failed probe accepted")
	}
	saved := absolutePath
	t.Cleanup(func() { absolutePath = saved })
	absolutePath = func(string) (string, error) { return "", errors.New("path unavailable") }
	if _, err := admitGit(context.Background(), func(string) (string, error) { return "git", nil }, nil); err == nil {
		t.Fatal("unresolvable path accepted")
	}
}

func TestAdmittedRuntimeNeverResolvesPATHAgain(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "git")
	// The wrapper logs calls and delegates to the one absolute real Git. No
	// source payload or remote process exists in this fixture.
	log := filepath.Join(bin, "commands")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\nexec '"+real+"' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	g, err := AdmitGit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g.Executable() != wrapper {
		t.Fatal("unexpected admitted executable")
	}
	t.Setenv("PATH", "/nonexistent")
	run := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command(real, append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("", "init", "--bare")
	run("", "remote", "add", "origin", "https://github.com/openvaultdb/ovdb.git")
	data := "synthetic metadata"
	blob := run(data, "hash-object", "-w", "--stdin")
	tree := run("100644 blob "+blob+"\tmetadata.json\n", "mktree")
	commit := run("synthetic", "commit-tree", tree)
	a := Artifact{Repository: "openvaultdb/ovdb", Commit: commit, Path: "metadata.json", Blob: blob, Bytes: len(data), SHA256: digest([]byte(data))}
	t.Setenv("GIT_DIR", "/nonexistent")
	if got, err := g.ReadArtifact(context.Background(), dir, a); err != nil || string(got) != data {
		t.Fatal(err)
	}
	if _, err := g.Validate(context.Background(), nil); err == nil {
		t.Fatal("missing repo accepted")
	}
	bad := a
	bad.SHA256 = "wrong"
	if _, err := g.ReadArtifact(context.Background(), dir, bad); err == nil {
		t.Fatal("drift accepted")
	}
	bad = a
	bad.Blob = "wrong"
	if _, err := g.ReadArtifact(context.Background(), dir, bad); err == nil {
		t.Fatal("bad pin accepted")
	}
	bad = a
	bad.Commit = strings.Repeat("0", 40)
	if _, err := g.ReadArtifact(context.Background(), dir, bad); err == nil {
		t.Fatal("missing commit accepted")
	}
	var zero GitRuntime
	if _, err := zero.Validate(context.Background(), nil); err == nil {
		t.Fatal("unadmitted runtime accepted")
	}
	if _, err := zero.ReadArtifact(context.Background(), dir, a); err == nil {
		t.Fatal("unadmitted artifact read")
	}
	if _, err := (*GitRuntime)(nil).Validate(context.Background(), nil); err == nil {
		t.Fatal("nil runtime accepted")
	}
	// A missing promisor object must refuse without executing the SSH fetch trap.
	missing := strings.Repeat("f", 40)
	trapMarker := filepath.Join(bin, "fetch-attempt")
	trapScript := filepath.Join(bin, "ssh-trap")
	if err := os.WriteFile(trapScript, []byte("#!/bin/sh\necho attempted > '"+trapMarker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := "[core]\n bare = true\n sshCommand = /bin/sh " + trapScript + "\n[remote \"origin\"]\n url = git@github.com:openvaultdb/ovdb.git\n promisor = true\n partialclonefilter = blob:none\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := gitCommand(context.Background(), wrapper, dir, "cat-file", "blob", missing); err == nil {
		t.Fatal("missing object accepted")
	}
	if _, err := os.Stat(trapMarker); !os.IsNotExist(err) {
		t.Fatal("no-lazy-fetch invoked SSH trap", err)
	}
	control := exec.Command(real, "--no-replace-objects", "-C", dir, "cat-file", "blob", missing)
	control.Env = []string{"PATH=" + bin, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0"}
	if err := control.Run(); err == nil {
		t.Fatal("missing-object control succeeded")
	}
	if _, err := os.Stat(trapMarker); err != nil {
		t.Fatal("control did not exercise fetch trap", err)
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "--no-replace-objects version") {
		t.Fatal("version admission missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AdmitGit(ctx); err == nil {
		t.Fatal("canceled/missing Git accepted")
	}
	if r, err := Validate(context.Background(), nil); err == nil || r != nil {
		t.Fatal("unavailable Git emitted receipt")
	}
}
