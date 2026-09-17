package main

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/strongo/cli-helpers/cliinstall"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

// newUpgradeCmd returns the "upgrade" command, built from
// github.com/strongo/cli-helpers/cliinstall/cobracmd against ovdb's own
// catalog id, with HostConfig identical to what newSelfUpdateCmd builds
// (newSelfUpdateConfig) so `ovdb self-update` and `ovdb upgrade ovdb` reach
// the exact same library call (cli-install#req:self-update-equals-upgrade-self).
// ovdb's self-update has no after-update hook, so HostAfterUpdate is left
// unset for both. No "update" alias: that alias stays reserved for
// self-update alone (cli-install#req:update-alias-policy).
func newUpgradeCmd(currentVersion string) *cobra.Command {
	return cobracmd.NewUpgrade(cobracmd.UpgradeCommandOptions{
		Short:      "Upgrade installed fleet CLIs, including ovdb itself",
		Errors:     upgradeErrors{},
		HostID:     ovdbCatalogID,
		HostConfig: newSelfUpdateConfig(currentVersion),
	})
}

// upgradeErrors implements both cobracmd.ErrorMapper and
// cobracmd.UpgradeErrorMapper for ovdb's own two-code contract: zero for
// success, one for any finding or failure — the same contract install and
// self-update already keep (cli-install#req:host-owned-exit-codes).
type upgradeErrors struct{}

// Failure maps every upgrade failure onto ovdb's general-failure exit code.
// The three cli-install-only kinds get the same explicit "upgrade:"-prefixed
// mapping installErrors uses for them
// (cli-install#req:host-owned-exit-codes: "MUST map the three new kinds
// explicitly"); every kind self-update already handles reuses
// selfUpdateErrors.Failure UNCHANGED, so a shared failure — checksum,
// permission, ambiguous detection, a failed release lookup, a failed
// managed command — produces the exact same message and exit code whether
// it came from `ovdb self-update` or `ovdb upgrade ovdb`
// (cli-install#req:self-update-equals-upgrade-self).
func (upgradeErrors) Failure(err error) error {
	var usage *cobracmd.UsageError
	if errors.As(err, &usage) {
		return errors.New("upgrade: " + err.Error())
	}
	switch selfupdate.KindOf(err) {
	case selfupdate.KindUnknownTarget, selfupdate.KindNoInstallDir, selfupdate.KindDestinationExists:
		return errors.New("upgrade: " + err.Error())
	default:
		return selfUpdateErrors{}.Failure(err)
	}
}

// UpgradesAvailable reports the same finding self-update's own
// UpdateAvailable does — plan task-12 exit mapping: "keep the 0/1 contract
// for both commands" — an available or undetermined upgrade is a finding
// for ovdb, not a silent success, for either command
// (cli-install#req:upgrade-check). For the single-target case (in
// particular `upgrade ovdb --check`, mirroring `self-update --check`
// exactly) this reuses updateAvailableError verbatim, so the message and
// exit code are identical, not merely equivalent
// (cli-install#req:self-update-equals-upgrade-self).
func (upgradeErrors) UpgradesAvailable(results []cliinstall.UpgradeResult) error {
	if len(results) == 1 {
		r := results[0]
		return updateAvailableError(r.Current, r.Latest, r.Verdict == selfupdate.Undetermined)
	}
	names := make([]string, len(results))
	undetermined := false
	for i, r := range results {
		names[i] = r.Target
		if r.Verdict == selfupdate.Undetermined {
			undetermined = true
		}
	}
	if undetermined {
		return errors.New("self-update: undetermined or available upgrades for " + strings.Join(names, ", "))
	}
	return errors.New("self-update: upgrades available for " + strings.Join(names, ", "))
}
