package cli_test

// RootRunE's interactive-vs-not branch is unit-tested in package cli
// (root_test.go, white-box, no cobra involved). This file exercises the
// non-interactive path end to end through cli.App.RootRunE itself — the
// branch that is safe to run in an automated test; launching the actual TUI
// needs a real terminal and is covered by the manual tmux check instead.

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
)

// TestRootRunEPrintsStatusWhenNotInteractive is
// first-run-onboarding#AC:tty-launches-tui and REQ:bare-ovdb-non-interactive:
// piped `ovdb` prints the same status a person would get from `ovdb status`
// and exits 0 without waiting for input.
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

	// Same content `ovdb status` (human) prints for a fresh, unstarted setup.
	statusRoot := &cobra.Command{Use: "ovdb", SilenceUsage: true, SilenceErrors: true}
	statusRoot.SetContext(context.Background())
	var wantOut bytes.Buffer
	statusRoot.SetOut(&wantOut)
	if err := e.app.Status(statusRoot, false); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if stdout.String() != wantOut.String() {
		t.Errorf("RootRunE (non-interactive) output =\n%s\nwant (same as `ovdb status`):\n%s", stdout.String(), wantOut.String())
	}
}
