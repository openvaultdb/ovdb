package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// The wiring of `ovdb publisher check` in the binary, without git: the usage errors are the shared envelope errors and exit 2, in text and as JSON, and
// the command is in the root help. (The command itself is tested in package checkcmd; the whole of it against real repositories, in
// TestPublisherCheckEndToEnd.)
func TestPublisherCheckUsageErrorsAreEnvelopesWithExitCode2(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere")
	stdout, stderr, code := runOVDB(t, nil, "publisher", "check", missing)
	if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "Couldn't run ovdb publisher check\n") || !strings.Contains(stderr, "does not exist") || !strings.Contains(stderr, "ovdb publisher check --help") {
		t.Errorf("text: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	stdout, _, code = runOVDB(t, nil, "publisher", "check", missing, "--json")
	if e := envelope.Decode([]byte(stdout)); code != 2 || e == nil || e.Code != envelope.InvalidArgument {
		t.Errorf("json: exit %d, stdout %q", code, stdout)
	}
	if _, stderr, code = runOVDB(t, nil, "publisher", "check", "--bogus"); code != 2 || !strings.Contains(stderr, "unknown flag: --bogus") {
		t.Errorf("flag: exit %d, stderr %q", code, stderr)
	}
}

func TestPublisherIsInTheRootHelp(t *testing.T) {
	help, _, code := runOVDB(t, nil, "--help")
	if code != 0 || !strings.Contains(help, "publisher [command]") {
		t.Errorf("exit %d:\n%s", code, help)
	}
}

// A refused repository is exit 1 with the findings on standard output and nothing on standard error: main's error handler stays silent for it, which
// fang's own would not. A directory that is not a repository is refused by any git that can be run, so no repository is needed.
func TestPublisherCheckRefusalIsSilentOnStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	stdout, stderr, code := runOVDB(t, nil, "publisher", "check", t.TempDir())
	if code == 2 && strings.Contains(stderr, "older than 2.45") {
		t.Skip("git is older than 2.45")
	}
	if code != 1 || stderr != "" || !strings.Contains(stdout, "[repo-unreadable]") || !strings.Contains(stdout, "Refused: git could not read a repository here.") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// The JSON codes of the exit-2 errors of the command: a usage error is invalid_argument, git that is missing or too old is dependency_missing, git that
// did not finish is timeout, a result that could not be written is internal (the README lists them).
func TestPublisherCheckErrorCodes(t *testing.T) {
	d := publisherDeps()
	for name, c := range map[string]struct {
		err  error
		code envelope.Code
	}{
		"unrunnable": {d.Unrunnable("m", "r", "n"), envelope.DependencyMissing},
		"timed out":  {d.TimedOut("m", "r", "n"), envelope.Timeout},
		"write":      {d.WriteFailed("r"), envelope.Internal},
	} {
		var coder interface{ ExitCode() int }
		if e := envelope.As(c.err); e == nil || e.Code != c.code || !errors.As(c.err, &coder) || coder.ExitCode() != 2 {
			t.Errorf("%s: %v", name, c.err)
		}
	}
}
