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
	return entry.Config(currentVersion)
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
