package main

// preview_test.go checks the preview gate on the built binary (see
// legacy_test.go's TestMain): without OVDB_PREVIEW=1 the help ovdb prints is
// byte-for-byte what it printed before the new commands existed
// (configuration-parity#AC:gate-hides-incomplete,
// first-run-onboarding#AC:tty-launches-tui second half), and with it the new
// commands are offered and fail through the shared envelope with exit 1.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// runOVDB runs the built binary with env replacing any inherited OVDB_*
// variables, returning stdout, stderr and the exit code.
func runOVDB(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(ovdbBinPath, args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "OVDB_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	base := t.TempDir()
	cmd.Env = append(cmd.Env,
		"OVDB_HOME="+filepath.Join(base, "home"), "OVDB_RUNTIME_DIR="+filepath.Join(base, "run"),
		"OVDB_DATA_HOME="+filepath.Join(base, "data"))
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run ovdb %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

func TestHelpUnchangedWithoutPreview(t *testing.T) {
	for _, tc := range []struct {
		golden string
		args   []string
	}{
		{"help.golden", []string{"--help"}},
		{"help.golden", nil},
		{"status-help.golden", []string{"status", "--help"}},
	} {
		want, err := os.ReadFile(filepath.Join("testdata", tc.golden))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runOVDB(t, nil, tc.args...)
		if code != 0 || stdout != string(want) {
			t.Errorf("ovdb %v (exit %d, stderr %q) differs from %s:\n%s", tc.args, code, stderr, tc.golden, stdout)
		}
	}
}

func TestPreviewOffersNewCommandsAndUsesEnvelope(t *testing.T) {
	preview := []string{"OVDB_PREVIEW=1"}
	help, _, _ := runOVDB(t, preview, "--help")
	for _, command := range []string{"server [command]", "open [--flags]", "config [command]"} {
		if !strings.Contains(help, command) {
			t.Errorf("preview help lacks %q:\n%s", command, help)
		}
	}
	if strings.Contains(help, " run ") {
		t.Error("preview help offers the internal server run command")
	}

	stdout, _, code := runOVDB(t, preview, "server", "start", "--bogus", "--json")
	if e := envelope.Decode([]byte(stdout)); code != 1 || e == nil || e.Code != envelope.InvalidArgument {
		t.Errorf("unknown flag: exit %d stdout %q", code, stdout)
	}

	// Hidden commands stay callable without the gate.
	stdout, _, code = runOVDB(t, nil, "server", "status", "--json")
	if code != 0 || !strings.Contains(stdout, `"state":"not_running"`) {
		t.Errorf("server status without the gate: exit %d stdout %q", code, stdout)
	}

	// `ovdb token` uses the local server: without one and with --no-start
	// it fails as server_not_running, not by calling 127.0.0.1:6832.
	stdout, _, code = runOVDB(t, preview, "token", "list", "--no-start", "--json")
	if e := envelope.Decode([]byte(stdout)); code != 1 || e == nil || e.Code != envelope.ServerNotRunning {
		t.Errorf("preview token list --no-start: exit %d stdout %q", code, stdout)
	}

	// Preview status is a pure read that starts nothing.
	stdout, stderr, code := runOVDB(t, preview, "status", "--json")
	if code != 0 || !strings.HasPrefix(stdout, `{"schema":1,"version":`) || !strings.Contains(stdout, `"state":"not_running"`) {
		t.Errorf("preview status: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// TestBareOVDBNonInteractivePrintsStatus is
// first-run-onboarding#AC:tty-launches-tui (its non-terminal half) and
// REQ:bare-ovdb-non-interactive: exec.Command's stdout is always a pipe, so
// bare `ovdb` under the gate never waits for input here — it prints a
// compact status and the five bootstrap next entries and exits 0. See internal/tui's manual
// tmux check for the terminal half, which a non-interactive test process
// cannot exercise.
func TestBareOVDBNonInteractivePrintsStatus(t *testing.T) {
	// first-run-onboarding#REQ:bare-ovdb-non-interactive and
	// AC:agent-gets-next-not-prompt: a compact status and exactly the five
	// next entries, exit 0 within a second, never waiting for input.
	want := []string{
		"Set up in the terminal", "ovdb",
		"Open web setup", "ovdb open",
		"Set up with commands", "ovdb databases create <name>",
		"Try the demo", "ovdb demo install --yes",
		"Install the OpenVaultDB skill for your AI assistant (ask the person first)", "ovdb skills install openvaultdb --yes",
	}
	for _, env := range [][]string{{"OVDB_PREVIEW=1"}, {"OVDB_PREVIEW=1", "OVDB_NON_INTERACTIVE=1"}} {
		started := time.Now()
		stdout, stderr, code := runOVDB(t, env)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Errorf("%v: took %s", env, elapsed)
		}
		if code != 0 {
			t.Fatalf("%v: exit %d stderr %q", env, code, stderr)
		}
		lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
		var entries []string
		for _, line := range lines {
			if entry, ok := strings.CutPrefix(line, "  • "); ok {
				label, command, _ := strings.Cut(entry, "   ")
				entries = append(entries, strings.TrimSpace(label), strings.TrimSpace(command))
			} else if command, ok := strings.CutPrefix(line, "      "); ok && len(entries) > 0 && entries[len(entries)-1] == "" {
				// A long label puts its command on the next line.
				entries[len(entries)-1] = strings.TrimSpace(command)
			}
		}
		if strings.Join(entries, "|") != strings.Join(want, "|") {
			t.Errorf("%v: next entries = %q\nstdout:\n%s", env, entries, stdout)
		}
		if !strings.HasPrefix(stdout, "OpenVaultDB ") || !strings.Contains(stdout, "OVDB server not running · Databases: none") || strings.Contains(stdout, "OVDB home:") {
			t.Errorf("%v: not a compact status:\n%s", env, stdout)
		}
	}
}
