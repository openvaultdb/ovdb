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

// Failure maps every upgrade failure onto ovdb's general-failure exit code,
// always with the "upgrade:" prefix — the fleet-wide rule that a message's
// prefix names the command the user actually ran, never a different one
// (task-22 review M2/M6 fleet ruling). This applies uniformly to the three
// cli-install-only kinds and to every kind self-update also handles: unlike
// UpgradesAvailable below, a *selfupdate.Failure/*cliinstall.BatchFailure
// carries no target identity at all (selfupdate.Failure has only Kind, Path
// and Err — no catalog id), so Failure has no reliable way to tell "this
// failure was ovdb's own" from "this failure was some other target's" —
// borrowing self-update's own "self-update:"-prefixed text here would risk
// mislabeling a non-ovdb target's failure (the bug this fixed: a checksum
// failure on `ovdb upgrade datatug` used to print "self-update: ..."). The
// exit code is unaffected either way — every kind still maps to 1.
func (upgradeErrors) Failure(err error) error {
	var usage *cobracmd.UsageError
	if errors.As(err, &usage) {
		return errors.New("upgrade: " + err.Error())
	}
	switch selfupdate.KindOf(err) {
	case selfupdate.KindUnknownTarget:
		return errors.New("upgrade: " + err.Error())
	case selfupdate.KindNoInstallDir, selfupdate.KindDestinationExists:
		return errors.New("upgrade: " + err.Error())
	default:
		return errors.New("upgrade: " + err.Error())
	}
}

// UpgradesAvailable reports the same finding self-update's own
// UpdateAvailable does — plan task-12 exit mapping: "keep the 0/1 contract
// for both commands" — an available or undetermined upgrade is a finding
// for ovdb, not a silent success, for either command
// (cli-install#req:upgrade-check). Unlike Failure, results here DOES carry
// each row's own catalog id (Target), so the self-vs-other distinction the
// fleet prefix rule requires (task-22 review M2/M6) is actually
// achievable: only when the single result IS ovdb itself does this reuse
// updateAvailableError verbatim, so `upgrade ovdb --check` and `self-update
// --check` print the identical "self-update:"-prefixed message
// (cli-install#req:self-update-equals-upgrade-self); a single OTHER target,
// or more than one target, always gets the "upgrade:" prefix instead — the
// bug this fixed reused "self-update:" even for `upgrade datatug --check`.
func (upgradeErrors) UpgradesAvailable(results []cliinstall.UpgradeResult) error {
	if len(results) == 1 && results[0].Target == ovdbCatalogID {
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
		return errors.New("upgrade: undetermined or available upgrades for " + strings.Join(names, ", "))
	}
	return errors.New("upgrade: upgrades available for " + strings.Join(names, ", "))
}
