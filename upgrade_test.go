package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
	selfupdatecobracmd "github.com/strongo/cli-helpers/selfupdate/cobracmd"
)

func TestNewUpgradeCmdRegistration(t *testing.T) {
	cmd := newUpgradeCmd("1.2.3")

	if !strings.HasPrefix(cmd.Use, "upgrade") {
		t.Errorf("Use = %q, want it to start with upgrade", cmd.Use)
	}
	if cmd.HasAlias("update") {
		t.Error(`upgrade must not alias "update" (cli-install#req:update-alias-policy); self-update keeps that alias alone`)
	}
	for _, name := range []string{"all", "check", "yes", "dry-run", "format"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
	if cmd.Flags().Lookup("dir") != nil {
		t.Error("unexpected --dir flag; upgrade has no --dir")
	}
	if f := cmd.Flags().Lookup("yes"); f.Shorthand != "y" {
		t.Errorf("--yes shorthand = %q, want y", f.Shorthand)
	}
}

func TestAddRootCommandsRegistersUpgrade(t *testing.T) {
	root := &cobra.Command{Use: "ovdb"}
	addRootCommands(root, "1.2.3")

	found, _, err := root.Find([]string{"upgrade"})
	if err != nil {
		t.Fatalf("ovdb upgrade: %v", err)
	}
	if found.Name() != "upgrade" {
		t.Errorf("ovdb upgrade resolved to %q, want upgrade", found.Name())
	}
	// "update" must resolve to self-update, not upgrade.
	found, _, err = root.Find([]string{"update"})
	if err != nil {
		t.Fatalf("ovdb update: %v", err)
	}
	if found.Name() != "self-update" {
		t.Errorf(`ovdb update resolved to %q, want self-update`, found.Name())
	}
}

// --- upgradeErrors: reuses selfUpdateErrors for shared kinds ---

func TestUpgradeErrorsFailure_UsageError(t *testing.T) {
	usage := &cobracmd.UsageError{Err: errors.New(`invalid --format "yaml": expected text or json`)}
	got := (upgradeErrors{}).Failure(usage)
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

// TestUpgradeErrorsFailure_NewKindsMapExplicitly proves the three
// cli-install-only kinds map through upgrade's own explicit "upgrade:"
// branch, the same shape installErrors uses for them
// (cli-install#req:host-owned-exit-codes).
func TestUpgradeErrorsFailure_NewKindsMapExplicitly(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"unknown target", &selfupdate.Failure{Kind: selfupdate.KindUnknownTarget, Err: errors.New("nosuchcli: not a known install target; valid ids: datatug, ingitdb, ovdb")}},
		{"no install dir", &selfupdate.Failure{Kind: selfupdate.KindNoInstallDir, Err: errors.New("no per-user bin directory on PATH")}},
		{"destination exists", &selfupdate.Failure{Kind: selfupdate.KindDestinationExists, Err: errors.New("destination already exists")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (upgradeErrors{}).Failure(c.err)
			if got == nil {
				t.Fatal("Failure(...) = nil, want a non-nil error")
			}
			if !strings.HasPrefix(got.Error(), "upgrade: ") {
				t.Errorf("Failure(%v) = %q, want an \"upgrade: \" prefix", c.err, got.Error())
			}
			if code := commandExitCode(got); code != 1 {
				t.Errorf("commandExitCode(Failure(%v)) = %d, want 1", c.err, code)
			}
		})
	}
}

// TestUpgradeErrorsFailure_SharedKindsMatchSelfUpdate proves every kind
// self-update already handles reuses selfUpdateErrors.Failure UNCHANGED:
// same message, same exit code
// (cli-install#req:self-update-equals-upgrade-self).
func TestUpgradeErrorsFailure_SharedKindsMatchSelfUpdate(t *testing.T) {
	cases := []error{
		&selfupdate.Failure{Kind: selfupdate.KindAmbiguous, Err: errors.New("ambiguous")},
		&selfupdate.Failure{Kind: selfupdate.KindReleaseLookup, Err: errors.New("lookup failed")},
		&selfupdate.Failure{Kind: selfupdate.KindChecksum, Err: errors.New("checksum mismatch")},
		&selfupdate.Failure{Kind: selfupdate.KindPermission, Err: errors.New("permission denied")},
		&selfupdate.Failure{Kind: selfupdate.KindManagedCommand, Err: errors.New("brew failed")},
		errors.New("plain error"),
	}
	for _, err := range cases {
		got := (upgradeErrors{}).Failure(err)
		want := (selfUpdateErrors{}).Failure(err)
		if got.Error() != want.Error() {
			t.Errorf("Failure(%v) = %q, want %q (identical to selfUpdateErrors)", err, got.Error(), want.Error())
		}
		if commandExitCode(got) != commandExitCode(want) {
			t.Errorf("Failure(%v) exit code = %d, want %d", err, commandExitCode(got), commandExitCode(want))
		}
	}
}

// TestUpgradeErrorsUpgradesAvailable_SingleTargetMatchesSelfUpdate proves
// UpgradesAvailable's single-target case is EXACTLY selfUpdateErrors.
// UpdateAvailable's own message and exit code — the case that matters for
// `upgrade ovdb --check` vs. `self-update --check`
// (cli-install#req:self-update-equals-upgrade-self).
func TestUpgradeErrorsUpgradesAvailable_SingleTargetMatchesSelfUpdate(t *testing.T) {
	cases := []struct {
		name    string
		result  cliinstall.UpgradeResult
		checkRe selfupdate.CheckResult
	}{
		{
			name:    "update available",
			result:  cliinstall.UpgradeResult{Target: "ovdb", Current: "1.2.3", Latest: "1.3.0", Verdict: selfupdate.UpdateAvailable},
			checkRe: selfupdate.CheckResult{Current: "1.2.3", Latest: "1.3.0", Verdict: selfupdate.UpdateAvailable},
		},
		{
			name:    "undetermined",
			result:  cliinstall.UpgradeResult{Target: "ovdb", Current: "dev", Latest: "1.3.0", Verdict: selfupdate.Undetermined},
			checkRe: selfupdate.CheckResult{Current: "dev", Latest: "1.3.0", Verdict: selfupdate.Undetermined},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (upgradeErrors{}).UpgradesAvailable([]cliinstall.UpgradeResult{c.result})
			want := (selfUpdateErrors{}).UpdateAvailable(c.checkRe)
			if got.Error() != want.Error() {
				t.Errorf("UpgradesAvailable(...) = %q, want %q (identical to UpdateAvailable)", got.Error(), want.Error())
			}
			if commandExitCode(got) != commandExitCode(want) {
				t.Errorf("exit code mismatch: %d != %d", commandExitCode(got), commandExitCode(want))
			}
		})
	}
}

// TestUpgradeErrorsUpgradesAvailable_MultiTarget proves the >1-target
// fallback still reports a finding (exit 1), naming every target, for both
// the plain-available and the undetermined-included cases.
func TestUpgradeErrorsUpgradesAvailable_MultiTarget(t *testing.T) {
	cases := []struct {
		name    string
		results []cliinstall.UpgradeResult
		wantSub string
	}{
		{
			name: "all update available",
			results: []cliinstall.UpgradeResult{
				{Target: "datatug", Current: "1.0.0", Latest: "1.1.0", Verdict: selfupdate.UpdateAvailable},
				{Target: "specscore", Current: "1.0.0", Latest: "1.1.0", Verdict: selfupdate.UpdateAvailable},
			},
			wantSub: "upgrades available for",
		},
		{
			name: "includes undetermined",
			results: []cliinstall.UpgradeResult{
				{Target: "datatug", Current: "1.0.0", Latest: "1.1.0", Verdict: selfupdate.UpdateAvailable},
				{Target: "ingitdb", Current: "dev", Latest: "1.1.0", Verdict: selfupdate.Undetermined},
			},
			wantSub: "undetermined or available upgrades for",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (upgradeErrors{}).UpgradesAvailable(c.results)
			if got == nil {
				t.Fatal("UpgradesAvailable(...) = nil, want a non-nil error")
			}
			if !strings.Contains(got.Error(), c.wantSub) {
				t.Errorf("UpgradesAvailable(...) = %q, want it to contain %q", got.Error(), c.wantSub)
			}
			for _, r := range c.results {
				if !strings.Contains(got.Error(), r.Target) {
					t.Errorf("UpgradesAvailable(...) = %q, want it to name %q", got.Error(), r.Target)
				}
			}
			if commandExitCode(got) != 1 {
				t.Errorf("commandExitCode(...) = %d, want 1", commandExitCode(got))
			}
		})
	}
}

// --- end-to-end exit-code contract, fully offline ---

// `upgrade nosuchcli` is validated against the catalog before any status
// probe or release lookup, mirroring install's own unknown-target path, so
// this is inherently offline (REQ: no-network-in-tests).
func TestUpgradeCmdNoSuchTarget_ExitCodeContract(t *testing.T) {
	cmd := newUpgradeCmd("1.2.3")
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{"nosuchcli"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown upgrade target")
	}
	if !strings.Contains(err.Error(), "nosuchcli") {
		t.Errorf("error %q does not name the unknown target", err.Error())
	}
	if code := commandExitCode(err); code != 1 {
		t.Errorf("commandExitCode(%v) = %d, want 1", err, code)
	}
}

func TestUpgradeCmdInvalidFormat_IsUsageError(t *testing.T) {
	cmd := newUpgradeCmd("1.2.3")
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

// --- self-update ≡ upgrade ovdb, against a fake releases server ---

// fakeUpgradeEnv is a fully hermetic cliinstall.InstallEnv — no real PATH
// scan, no real filesystem, no real process execution
// (REQ: no-network-in-tests). The host row's classification/version come
// from HostConfig, never from this Env, but resolveUpgradeCandidates still
// calls Probe over every named entry for diagnostics, so every field is
// set to avoid a nil-func panic.
func fakeUpgradeEnv() cliinstall.InstallEnv {
	return cliinstall.InstallEnv{
		Env: cliinstall.Env{
			PathDirs:     func() []string { return nil },
			HostDir:      func() (string, error) { return "", errors.New("no host dir in test") },
			IsExecutable: func(string) bool { return false },
			EvalSymlinks: func(p string) (string, error) { return p, nil },
			Run: func(context.Context, string, []string) ([]byte, error) {
				return nil, errors.New("process execution disabled in test")
			},
		},
		UserHomeDir: func() (string, error) { return "", errors.New("disabled in test") },
		Getenv:      func(string) string { return "" },
		MkdirAll:    func(string, fs.FileMode) error { return errors.New("disabled in test") },
	}
}

func upgradeReleasesServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSelfUpdateEqualsUpgradeSelf_CheckContract proves `ovdb self-update
// --check` and `ovdb upgrade ovdb --check` reach the same exit-code
// contract for the same fake releases server, built from the exact same
// Config (newSelfUpdateConfig) — up to date, an update available, and a
// release-lookup failure — mirroring TestNewSelfUpdateConfigIdentity's own
// construction (cli-install#req:self-update-equals-upgrade-self,
// cli-install#req:upgrade-check).
func TestSelfUpdateEqualsUpgradeSelf_CheckContract(t *testing.T) {
	cases := []struct {
		name string
		ver  string
		body string
	}{
		{name: "up to date", ver: "1.0.0", body: `[{"tag_name":"v1.0.0","prerelease":false,"draft":false}]`},
		{name: "update available", ver: "1.0.0", body: `[{"tag_name":"v1.1.0","prerelease":false,"draft":false}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := upgradeReleasesServer(t, c.body, http.StatusOK)
			cfg := newSelfUpdateConfig(c.ver)
			cfg.ReleasesAPIURL = srv.URL
			cfg.HTTPClient = srv.Client()

			selfCmd := selfupdatecobracmd.New(cfg, selfupdatecobracmd.CommandOptions{Errors: selfUpdateErrors{}})
			selfCmd.SetOut(&strings.Builder{})
			selfCmd.SetErr(&strings.Builder{})
			selfCmd.SetArgs([]string{"--check"})
			selfErr := selfCmd.Execute()

			upCmd := cobracmd.NewUpgrade(cobracmd.UpgradeCommandOptions{
				HostID:     ovdbCatalogID,
				Errors:     upgradeErrors{},
				HostConfig: cfg,
				Env:        fakeUpgradeEnv(),
			})
			upCmd.SetOut(&strings.Builder{})
			upCmd.SetErr(&strings.Builder{})
			upCmd.SetArgs([]string{"ovdb", "--check"})
			upErr := upCmd.Execute()

			if (selfErr == nil) != (upErr == nil) {
				t.Fatalf("self-update --check err=%v, upgrade ovdb --check err=%v; want the same verdict", selfErr, upErr)
			}
			if selfErr != nil && commandExitCode(selfErr) != commandExitCode(upErr) {
				t.Errorf("exit codes differ: self-update=%d upgrade=%d", commandExitCode(selfErr), commandExitCode(upErr))
			}
		})
	}

	t.Run("release lookup failure fails both the same way", func(t *testing.T) {
		srv := upgradeReleasesServer(t, `not json`, http.StatusInternalServerError)
		cfg := newSelfUpdateConfig("1.0.0")
		cfg.ReleasesAPIURL = srv.URL
		cfg.HTTPClient = srv.Client()

		selfCmd := selfupdatecobracmd.New(cfg, selfupdatecobracmd.CommandOptions{Errors: selfUpdateErrors{}})
		selfCmd.SetOut(&strings.Builder{})
		selfCmd.SetErr(&strings.Builder{})
		selfCmd.SetArgs([]string{"--check"})
		selfErr := selfCmd.Execute()
		if selfErr == nil {
			t.Fatal("expected self-update --check to fail on a release-lookup error")
		}

		upCmd := cobracmd.NewUpgrade(cobracmd.UpgradeCommandOptions{
			HostID:     ovdbCatalogID,
			Errors:     upgradeErrors{},
			HostConfig: cfg,
			Env:        fakeUpgradeEnv(),
		})
		upCmd.SetOut(&strings.Builder{})
		upCmd.SetErr(&strings.Builder{})
		upCmd.SetArgs([]string{"ovdb", "--check"})
		upErr := upCmd.Execute()
		if upErr == nil {
			t.Fatal("expected upgrade ovdb --check to fail the same way")
		}
		if commandExitCode(selfErr) != commandExitCode(upErr) {
			t.Errorf("exit codes differ: self-update=%d upgrade=%d", commandExitCode(selfErr), commandExitCode(upErr))
		}
	})
}
