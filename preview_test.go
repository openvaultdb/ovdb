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

	// Preview status is a pure read that starts nothing.
	stdout, stderr, code := runOVDB(t, preview, "status", "--json")
	if code != 0 || !strings.HasPrefix(stdout, `{"schema":1,"version":`) || !strings.Contains(stdout, `"state":"not_running"`) {
		t.Errorf("preview status: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
