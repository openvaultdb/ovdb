package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
)

func TestNewInstallCmdRegistration(t *testing.T) {
	cmd := newInstallCmd()

	if !strings.HasPrefix(cmd.Use, "install") {
		t.Errorf("Use = %q, want it to start with install", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty, want a description")
	}
	for _, name := range []string{"all", "yes", "dry-run", "dir", "format"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
	if f := cmd.Flags().Lookup("yes"); f.Shorthand != "y" {
		t.Errorf("--yes shorthand = %q, want y", f.Shorthand)
	}
}

func TestAddRootCommandsRegistersInstall(t *testing.T) {
	root := &cobra.Command{Use: "ovdb"}
	addRootCommands(root, "1.2.3")

	found, _, err := root.Find([]string{"install"})
	if err != nil {
		t.Fatalf("ovdb install: %v", err)
	}
	if found.Name() != "install" {
		t.Errorf("ovdb install resolved to %q, want install", found.Name())
	}
}

// TestInstallErrorsFailure_NilReturnsNil proves the defensive nil guard:
// cliinstall/cobracmd v0.19.0's runInstall calls
// mapFailure(opts, plan.Failure()) and mapFailure(opts, result.Failure())
// unconditionally, and both return nil for a fully successful batch
// (including a successful --dry-run), so installErrors.Failure(nil) is a
// real, reachable call on the ordinary success path, not just a defensive
// guard against a hypothetical caller (known cli-helpers v0.19.0 bug, to be
// fixed in its next release — see install.go's doc comment).
func TestInstallErrorsFailure_NilReturnsNil(t *testing.T) {
	if got := (installErrors{}).Failure(nil); got != nil {
		t.Errorf("Failure(nil) = %v, want nil", got)
	}
}

func TestInstallErrorsFailure_UsageError(t *testing.T) {
	usage := &cobracmd.UsageError{Err: errors.New("invalid --format \"yaml\": expected text or json")}
	got := (installErrors{}).Failure(usage)
	if got == nil {
		t.Fatal("Failure(usage error) = nil, want a non-nil error")
	}
	if !strings.Contains(got.Error(), "--format") {
		t.Errorf("Failure(usage error) = %q, want it to mention --format", got.Error())
	}
	if commandExitCode(got) != 1 {
		t.Errorf("commandExitCode(%v) = %d, want 1", got, commandExitCode(got))
	}
}

// TestInstallErrorsFailure_MapsEveryKindToOne proves every failure — usage,
// an unknown target, the two new cli-install-only kinds, and a self-update-
// shared kind — maps onto ovdb's single general-failure exit code, 1
// (cli-install#req:host-owned-exit-codes).
func TestInstallErrorsFailure_MapsEveryKindToOne(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"unknown target", errors.New("nosuchcli: not a known install target; valid ids: datatug, ingitdb, ovdb")},
		{"no install dir", errors.New("no per-user bin directory on PATH")},
		{"destination exists", errors.New("destination already exists")},
		{"plain error", errors.New("network unavailable")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (installErrors{}).Failure(c.err)
			if got == nil {
				t.Fatal("Failure(...) = nil, want a non-nil error")
			}
			if code := commandExitCode(got); code != 1 {
				t.Errorf("commandExitCode(Failure(%v)) = %d, want 1", c.err, code)
			}
		})
	}
}

// TestInstallCmdNoSuchTarget_ExitCodeContract runs the real command built
// exactly as main.go wires it (real, un-injected catalog and env) against an
// unknown target name. cliinstall.Plan validates every name against the
// compiled-in catalog BEFORE probing anything
// (cli-install#req:unknown-target-refused: "MUST fail before any
// confirmation, network request or write"), so this is inherently offline —
// no network/env seam is needed to keep it safe for CI.
func TestInstallCmdNoSuchTarget_ExitCodeContract(t *testing.T) {
	cmd := newInstallCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"nosuchcli"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown install target")
	}
	if !strings.Contains(err.Error(), "nosuchcli") {
		t.Errorf("error %q does not name the unknown target", err.Error())
	}
	if code := commandExitCode(err); code != 1 {
		t.Errorf("commandExitCode(%v) = %d, want 1", err, code)
	}
}

func TestInstallCmdInvalidFormat_IsUsageError(t *testing.T) {
	cmd := newInstallCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"--format", "yaml"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error for --format yaml")
	}
	if !strings.Contains(err.Error(), "--format") {
		t.Errorf("error %q does not mention --format", err.Error())
	}
	if code := commandExitCode(err); code != 1 {
		t.Errorf("commandExitCode(%v) = %d, want 1", err, code)
	}
}
