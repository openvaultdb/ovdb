package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/strongo/selfupdate"
	"github.com/strongo/selfupdate/cobracmd"
)

const selfUpdateHomebrewUpgradeCommand = "brew upgrade --cask ovdb"

func selfUpdateChecksumsName(_, _ string) string {
	return "checksums.txt"
}

// newSelfUpdateConfig supplies only ovdb's release-specific identity to the
// fleet-wide selfupdate implementation. Detection, release resolution,
// checksum verification, prompting, and atomic replacement remain owned by
// github.com/strongo/selfupdate.
func newSelfUpdateConfig(currentVersion string) selfupdate.Config {
	return selfupdate.Config{
		BinaryName:           "ovdb",
		Repository:           "openvaultdb/ovdb",
		CurrentVersion:       currentVersion,
		UndeterminedVersions: []string{"dev"},
		Managers: []selfupdate.Manager{
			selfupdate.Homebrew(selfUpdateHomebrewUpgradeCommand),
		},
		SupportedPlatforms: []selfupdate.Platform{
			{GOOS: "darwin", GOARCH: "amd64"},
			{GOOS: "darwin", GOARCH: "arm64"},
			{GOOS: "linux", GOARCH: "amd64"},
			{GOOS: "linux", GOARCH: "arm64"},
			{GOOS: "windows", GOARCH: "amd64"},
		},
		VersionProbeArgs: []string{"--version"},
		// .goreleaser.yaml publishes a single flat checksums.txt asset.
		ChecksumsName: selfUpdateChecksumsName,
	}
}

func newSelfUpdateCmd(currentVersion string) *cobra.Command {
	return cobracmd.New(newSelfUpdateConfig(currentVersion), cobracmd.CommandOptions{
		Short:      "Update the installed ovdb binary to the latest release",
		Aliases:    []string{"update"},
		JSONFormat: true,
		Errors:     selfUpdateErrors{},
	})
}

// selfUpdateErrors maps the shared library's outcomes onto ovdb's existing
// two-code contract: zero for success and one for any finding or failure.
type selfUpdateErrors struct{}

func (selfUpdateErrors) Failure(err error) error {
	return fmt.Errorf("self-update: %w", err)
}

func (selfUpdateErrors) UpdateAvailable(result selfupdate.CheckResult) error {
	if result.Verdict == selfupdate.Undetermined {
		return fmt.Errorf("self-update: current version is undetermined (%s); latest stable is %s", result.Current, result.Latest)
	}
	return fmt.Errorf("self-update: update available (%s -> %s)", result.Current, result.Latest)
}
