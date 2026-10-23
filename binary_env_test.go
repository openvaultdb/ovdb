package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/porttest"
)

// Every test that runs the built ovdb binary does it through ovdbCommand, which
// puts a directory of harmless browser openers (open, xdg-open, rundll32) first
// on that child's PATH. The Go browser guard (internal/browser) cannot see into
// a child process: a test that makes the binary open a browser, `ovdb open`,
// `ovdb cloud login`, would otherwise start the person's real one at a test
// server. With the stubs the child can only reach a script that records the
// call in calls.log.

// stubBin is the directory of opener stubs, created by TestMain.
var stubBin string

// installOpenerStubs writes the stubs into dir and returns it.
func installOpenerStubs(dir string) (string, error) {
	stubs := filepath.Join(dir, "stubbin")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		return "", err
	}
	for _, name := range []string{"open", "xdg-open", "rundll32"} {
		file, body, mode := name, "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> \"$(dirname \"$0\")/calls.log\"\nexit 0\n", os.FileMode(0o755)
		if runtime.GOOS == "windows" {
			file, body, mode = name+".cmd", "@echo off\r\necho %~n0 %* >> \"%~dp0calls.log\"\r\nexit /b 0\r\n", 0o644
		}
		if err := os.WriteFile(filepath.Join(stubs, file), []byte(body), mode); err != nil {
			return "", err
		}
	}
	return stubs, nil
}

// stubbedEnvironment is env with stubBin first on PATH.
func stubbedEnvironment(env []string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "PATH") {
			kv, found = key+"="+stubBin+string(os.PathListSeparator)+value, true
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+stubBin)
	}
	return out
}

// ovdbCommand is the built binary with args, in this process's environment but
// for the stub openers first on PATH. Callers that add variables append to
// cmd.Env.
func ovdbCommand(args ...string) *exec.Cmd {
	cmd := exec.Command(ovdbBinPath, args...)
	cmd.Env = stubbedEnvironment(os.Environ())
	return cmd
}

// The helper really keeps the real browser out of the child: `ovdb open`,
// started through it with a display (on Linux the opener is not even tried
// without one), reaches the stub and nothing else.
func TestBuiltBinaryReachesOnlyTheStubOpener(t *testing.T) {
	first := filepath.SplitList(stubbedEnvValue(ovdbCommand().Env, "PATH"))[0]
	if first != stubBin {
		t.Fatalf("first PATH entry of the child = %q, want the stub directory %q", first, stubBin)
	}
	log := filepath.Join(stubBin, "calls.log")
	before, _ := os.ReadFile(log)

	base := t.TempDir()
	port := porttest.Lease(t)
	env := []string{
		"OVDB_HOME=" + filepath.Join(base, "home"), "OVDB_RUNTIME_DIR=" + filepath.Join(base, "run"),
		"OVDB_DATA_HOME=" + filepath.Join(base, "data"), fmt.Sprintf("OVDB_PORT=%d", port),
		"OVDB_NON_INTERACTIVE=1", "OVDB_PREVIEW=1", "DISPLAY=:0",
	}
	if _, stderr, code := runOVDB(t, env, "server", "start", "--json"); code != 0 {
		t.Fatalf("server start: exit %d: %s", code, stderr)
	}
	t.Cleanup(func() { runOVDB(t, env, "server", "stop", "--json") })
	if _, stderr, code := runOVDB(t, env, "open"); code != 0 {
		t.Fatalf("open: exit %d: %s", code, stderr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		after, _ := os.ReadFile(log)
		if added := strings.TrimPrefix(string(after), string(before)); strings.Contains(added, "/login?code=") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stub opener was not called by `ovdb open`; calls.log:\n%s", before)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stubbedEnvValue(env []string, name string) string {
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
