package main

import (
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
