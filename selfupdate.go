package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/selfupdate"
	"github.com/strongo/cli-helpers/selfupdate/cobracmd"
)

// ovdbCatalogID is ovdb's own id in the cliinstall catalog
// (cli-install#req:host-identity-from-catalog).
const ovdbCatalogID = "ovdb"

// newSelfUpdateConfig builds ovdb's release identity from its own
// cliinstall catalog entry rather than restating it, so ovdb's self-update
// and every other fleet CLI's `install ovdb` resolve releases identically
// (cli-install#req:catalog-identity-single-source). The catalog entry
// carries the same repository, executable `brew upgrade --cask ovdb`
// manager, supported platforms, version-probe args and flat checksums.txt
// naming this file used to declare directly. A missing catalog entry is a
// programming error caught by TestNewSelfUpdateConfigIdentity, never a
// runtime state a user can hit (cli-install#req:host-identity-from-catalog).
func newSelfUpdateConfig(currentVersion string) selfupdate.Config {
	entry, ok := cliinstall.ByID(ovdbCatalogID)
	if !ok {
		panic("ovdb: catalog id " + ovdbCatalogID + " is missing from cliinstall")
	}
	cfg := entry.Config(currentVersion)
	cfg.SystemPackageHintFor = systemPackageHint
	return cfg
}

// releasesURL is where ovdb's archives are published.
const releasesURL = "https://github.com/openvaultdb/ovdb/releases"

// systemPackageHint is what ovdb says to do about a copy inside a directory the
// operating system's package manager owns (selfupdate.Config.SystemPackageHintFor).
// ovdb is published as a zip on Windows and a tar.gz elsewhere, besides its
// Homebrew cask, and as no installer or distribution package, so the library's
// Windows text (Windows Update or an installer) names things that never put an
// ovdb there. An empty answer keeps the library's own text, which is right for
// macOS (a directory System Integrity Protection guards).
func systemPackageHint(goos, _ string) string {
	switch goos {
	case "windows":
		return "a new download of the ovdb zip from " + releasesURL + ", replacing the files (ovdb is published as a zip, not through Windows Update or an installer)"
	case "darwin":
		return ""
	default:
		return "the package manager that installed it or, for a copy extracted from a tar.gz archive, a new download of that archive from " + releasesURL
	}
}

func newSelfUpdateCmd(currentVersion string) *cobra.Command {
	return newSelfUpdateCmdFor(newSelfUpdateConfig(currentVersion))
}

// newSelfUpdateCmdFor is newSelfUpdateCmd for a given configuration: tests
// replace its release endpoint and run ovdb's own command against it.
func newSelfUpdateCmdFor(cfg selfupdate.Config) *cobra.Command {
	return cobracmd.New(cfg, cobracmd.CommandOptions{
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
	return updateAvailableError(result.Current, result.Latest, result.Verdict == selfupdate.Undetermined)
}

// updateAvailableError builds the "an update/upgrade exists" finding
// message shared by selfUpdateErrors.UpdateAvailable and
// upgradeErrors.UpgradesAvailable (upgrade.go), so `ovdb self-update
// --check` and `ovdb upgrade ovdb --check` report the exact same finding
// for the exact same verdict (cli-install#req:self-update-equals-upgrade-self,
// cli-install#req:upgrade-check: "mirroring self-update's UpdateAvailable
// mapping").
func updateAvailableError(current, latest string, undetermined bool) error {
	if undetermined {
		return fmt.Errorf("self-update: current version is undetermined (%s); latest stable is %s", current, latest)
	}
	return fmt.Errorf("self-update: update available (%s -> %s)", current, latest)
}
