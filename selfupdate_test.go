package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/strongo/selfupdate"
)

func TestNewSelfUpdateConfigIdentity(t *testing.T) {
	cfg := newSelfUpdateConfig("1.2.3")

	if cfg.BinaryName != "ovdb" {
		t.Errorf("BinaryName = %q, want ovdb", cfg.BinaryName)
	}
	if cfg.Repository != "openvaultdb/ovdb" {
		t.Errorf("Repository = %q, want openvaultdb/ovdb", cfg.Repository)
	}
	if cfg.CurrentVersion != "1.2.3" {
		t.Errorf("CurrentVersion = %q, want 1.2.3", cfg.CurrentVersion)
	}
	if len(cfg.UndeterminedVersions) != 1 || cfg.UndeterminedVersions[0] != "dev" {
		t.Errorf("UndeterminedVersions = %v, want [dev]", cfg.UndeterminedVersions)
	}
}

func TestNewSelfUpdateConfigMatchesReleaseAssets(t *testing.T) {
	cfg := newSelfUpdateConfig("1.2.3")

	if cfg.AssetName != nil {
		t.Error("AssetName is overridden; ovdb archives use selfupdate's GoReleaser-shaped default")
	}
	if cfg.DownloadURL != nil {
		t.Error("DownloadURL is overridden; ovdb releases use selfupdate's GitHub default")
	}
	if cfg.ChecksumsName == nil {
		t.Fatal("ChecksumsName is not overridden; ovdb publishes a flat checksums.txt")
	}
	if got := cfg.ChecksumsName("ovdb", "1.2.3"); got != "checksums.txt" {
		t.Errorf("ChecksumsName(...) = %q, want checksums.txt", got)
	}
	if len(cfg.VersionProbeArgs) != 1 || cfg.VersionProbeArgs[0] != "--version" {
		t.Errorf("VersionProbeArgs = %v, want [--version]", cfg.VersionProbeArgs)
	}
}

func TestNewSelfUpdateConfigPlatforms(t *testing.T) {
	cfg := newSelfUpdateConfig("1.2.3")
	want := map[selfupdate.Platform]bool{
		{GOOS: "darwin", GOARCH: "amd64"}:  true,
		{GOOS: "darwin", GOARCH: "arm64"}:  true,
		{GOOS: "linux", GOARCH: "amd64"}:   true,
		{GOOS: "linux", GOARCH: "arm64"}:   true,
		{GOOS: "windows", GOARCH: "amd64"}: true,
	}

	if len(cfg.SupportedPlatforms) != len(want) {
		t.Fatalf("SupportedPlatforms = %v, want exactly %d entries", cfg.SupportedPlatforms, len(want))
	}
	for _, platform := range cfg.SupportedPlatforms {
		if !want[platform] {
			t.Errorf("unexpected platform %+v", platform)
		}
		delete(want, platform)
	}
	if len(want) != 0 {
		t.Errorf("SupportedPlatforms is missing %v", want)
	}
}

func TestNewSelfUpdateConfigUsesHomebrewCask(t *testing.T) {
	cfg := newSelfUpdateConfig("1.2.3")

	if len(cfg.Managers) != 1 {
		t.Fatalf("Managers = %v, want exactly one Homebrew manager", cfg.Managers)
	}
	if got := cfg.Managers[0].UpgradeCommand; got != "brew upgrade --cask ovdb" {
		t.Errorf("Homebrew upgrade command = %q, want %q", got, "brew upgrade --cask ovdb")
	}
	if !cfg.Managers[0].CanExecuteUpgrade() {
		t.Fatal("Homebrew manager is redirect-only; ovdb self-update must delegate to brew")
	}
	if got := cfg.Managers[0].UpgradeExecutable; got != "brew" {
		t.Errorf("Homebrew executable = %q, want brew", got)
	}
	wantArgs := []string{"upgrade", "--cask", "ovdb"}
	if got := cfg.Managers[0].UpgradeArgs; !reflect.DeepEqual(got, wantArgs) {
		t.Errorf("Homebrew argv = %v, want %v", got, wantArgs)
	}
}

func TestNewSelfUpdateCmdRegistration(t *testing.T) {
	cmd := newSelfUpdateCmd("1.2.3")

	if cmd.Use != "self-update" {
		t.Errorf("Use = %q, want self-update", cmd.Use)
	}
	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "update" {
		t.Errorf("Aliases = %v, want [update]", cmd.Aliases)
	}
	for _, name := range []string{"check", "yes", "version", "allow-downgrade", "dry-run", "format"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
}

func TestAddRootCommandsRegistersSelfUpdateAndAlias(t *testing.T) {
	root := &cobra.Command{Use: "ovdb"}
	addRootCommands(root, "1.2.3")

	for _, invocation := range []string{"self-update", "update"} {
		found, _, err := root.Find([]string{invocation})
		if err != nil {
			t.Fatalf("ovdb %s: %v", invocation, err)
		}
		if found.Name() != "self-update" {
			t.Errorf("ovdb %s resolved to %q, want self-update", invocation, found.Name())
		}
	}
}

func TestSelfUpdateErrors(t *testing.T) {
	failure := errors.New("network unavailable")
	if got := (selfUpdateErrors{}).Failure(failure).Error(); !strings.Contains(got, "self-update: network unavailable") {
		t.Errorf("Failure(...) = %q", got)
	}

	available := selfupdate.CheckResult{Current: "1.2.3", Latest: "1.3.0", Verdict: selfupdate.UpdateAvailable}
	if got := (selfUpdateErrors{}).UpdateAvailable(available).Error(); !strings.Contains(got, "1.2.3 -> 1.3.0") {
		t.Errorf("UpdateAvailable(...) = %q", got)
	}
}
