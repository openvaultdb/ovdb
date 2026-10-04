package main

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

// cli-helpers v0.26.0 changed three things `ovdb install` shows: the catalogue
// gained `sneat` (listed by --all, installable by name, one of the valid ids a
// refusal names), the protected directories of --dir come from one list
// (/usr/bin is refused naming /usr/bin, and /lib is no longer refused on macOS
// where it does not exist), and nothing else. These run ovdb's own install
// command (installOptions) with no real PATH, filesystem or network.

// runInstall runs `ovdb install args...` against a fake releases server that
// has one release, v9.9.9, and returns the output and the error.
func runInstall(t *testing.T, args ...string) (string, error) {
	t.Helper()
	srv, _ := releasesServer(t, "v9.9.9")
	options := installOptions()
	options.Env = fakeUpgradeEnv()
	options.Interactive = func() bool { return false }
	options.ConfigureRelease = func(_ cliinstall.Entry, cfg selfupdate.Config) selfupdate.Config {
		cfg.ReleasesAPIURL, cfg.HTTPClient = srv.URL, srv.Client()
		return cfg
	}
	cmd := cobracmd.New(options)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestInstallListsAndInstallsSneat(t *testing.T) {
	// The list of CLIs relevant to ovdb is what it was: sneat is not in it.
	relevant, err := runInstall(t)
	if err != nil || strings.Contains(relevant, "sneat") || !strings.Contains(relevant, "ingitdb: not installed") || !strings.Contains(relevant, "datatug: not installed") {
		t.Errorf("install = %q, %v", relevant, err)
	}

	// --all lists it, as not relevant to ovdb, in text and in JSON.
	all, err := runInstall(t, "--all")
	if err != nil || !strings.Contains(all, "sneat: not installed\n  Sneat.app command-line interface") || !strings.Contains(all, "Not listed as relevant to ovdb.") {
		t.Errorf("install --all = %q, %v", all, err)
	}
	jsonOut, err := runInstall(t, "--all", "--format", "json")
	var document struct {
		Targets []struct {
			Name     string `json:"name"`
			Relevant bool   `json:"relevant"`
			Status   string `json:"status"`
		} `json:"targets"`
	}
	if err != nil || json.Unmarshal([]byte(jsonOut), &document) != nil {
		t.Fatalf("install --all --format json = %q, %v", jsonOut, err)
	}
	found := false
	for _, target := range document.Targets {
		if target.Name == "sneat" {
			found = true
			if target.Relevant || target.Status != "not_installed" {
				t.Errorf("sneat target = %+v", target)
			}
		}
	}
	if !found {
		t.Errorf("install --all --format json lacks sneat: %s", jsonOut)
	}

	// It can be named: a dry run prints the plan (it was refused as an unknown
	// target at v0.21.0), and writes nothing.
	dir := t.TempDir()
	plan, err := runInstall(t, "sneat", "--dry-run", "--dir", dir)
	if err != nil || !strings.Contains(plan, "== sneat ==") || !strings.Contains(plan, "Plan: direct install, release v9.9.9 (tag v9.9.9), asset https://github.com/sneat-co/sneat-cli/releases/download/v9.9.9/sneat_9.9.9_") {
		t.Errorf("install sneat --dry-run = %q, %v", plan, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a dry run wrote %v", entries)
	}

	// An unknown name is refused with exit 1 naming the valid ids, sneat among them.
	refusal, err := runInstall(t, "nosuch")
	if err == nil || commandExitCode(err) != 1 || !strings.HasPrefix(err.Error(), "install: nosuch: not a known install target; valid ids: ") || !strings.Contains(err.Error(), ", sneat, ") {
		t.Errorf("install nosuch = %v (output %q)", err, refusal)
	}
}

func TestInstallDirProtectedDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the protected directories on Windows are %ProgramFiles%, %SystemRoot% and %ProgramData%")
	}
	// /usr/bin is refused, and the message names /usr/bin (it named /usr at v0.21.0).
	out, err := runInstall(t, "ingitdb", "--dry-run", "--dir", "/usr/bin")
	if err == nil || commandExitCode(err) != 1 || err.Error() != "install: /usr/bin is inside the protected directory /usr/bin; choose a different --dir" ||
		!strings.Contains(out, "Result: failed (no_install_dir): /usr/bin is inside the protected directory /usr/bin") {
		t.Errorf("--dir /usr/bin = %v (output %q)", err, out)
	}
	// The directories that were protected before still are.
	for _, dir := range []string{"/bin", "/sbin", "/usr/local/bin", "/opt/homebrew/bin"} {
		if _, err := runInstall(t, "ingitdb", "--dry-run", "--dir", dir); err == nil || !strings.Contains(err.Error(), "is inside the protected directory") {
			t.Errorf("--dir %s = %v, want it refused", dir, err)
		}
	}
	// /lib is on the list for Linux only: macOS has no /lib of its own.
	_, err = runInstall(t, "ingitdb", "--dry-run", "--dir", "/lib")
	if refused := err != nil && strings.Contains(err.Error(), "is inside the protected directory /lib"); refused != (runtime.GOOS == "linux") {
		t.Errorf("--dir /lib on %s: refused = %v (%v)", runtime.GOOS, refused, err)
	}
}
