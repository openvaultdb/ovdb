package cli_test

// RootRunE's interactive-vs-not branch is unit-tested in package cli
// (root_test.go, white-box, no cobra involved). This file exercises the
// non-interactive path end to end through cli.App.RootRunE itself — the
// branch that is safe to run in an automated test; launching the actual TUI
// needs a real terminal and is covered by the manual tmux check instead.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRootRunEPrintsStatusWhenNotInteractive is
// first-run-onboarding#AC:tty-launches-tui and REQ:bare-ovdb-non-interactive:
// piped `ovdb` prints a compact status and the five ways to set up, and
// exits 0 without waiting for input.
func TestRootRunEPrintsStatusWhenNotInteractive(t *testing.T) {
	e := newEnv(t)
	e.app.IsTerminal = func(uintptr) bool { return false }

	root := &cobra.Command{Use: "ovdb", SilenceUsage: true, SilenceErrors: true, RunE: e.app.RootRunE}
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(nil)

	if err := root.Execute(); err != nil {
		t.Fatalf("RootRunE (non-interactive) returned an error: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatal("RootRunE (non-interactive) printed nothing")
	}

	// A compact status and exactly the five bootstrap entries
	// (REQ:bare-ovdb-non-interactive), not the whole `ovdb status`.
	for _, want := range []string{"OVDB server not running · Databases: none", "What you can do:",
		"• Set up in the terminal   ovdb\n", "• Open web setup           ovdb open\n", "ovdb databases create <name>\n",
		"ovdb demo install --yes\n", "(ask the person first)\n      ovdb skills install openvaultdb --yes\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("bare ovdb lacks %q:\n%s", want, stdout.String())
		}
	}
	if n := strings.Count(stdout.String(), "  • "); n != 5 {
		t.Errorf("%d next entries, want 5:\n%s", n, stdout.String())
	}
	if strings.Contains(stdout.String(), "ovdb server start") {
		t.Errorf("bare ovdb offers starting the server:\n%s", stdout.String())
	}
}
