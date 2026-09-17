package main

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/strongo/cli-helpers/cliinstall/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
)

// newInstallCmd returns the "install" command, built from
// github.com/strongo/cli-helpers/cliinstall/cobracmd against ovdb's own
// catalog id (cli-install#req:host-identity-from-catalog). `ovdb install`
// lists the fleet CLIs relevant to ovdb (currently `ingitdb` and `datatug`)
// with their live status, and `ovdb install <name>...` installs them the
// same way ovdb itself was installed. cobracmd.New panics if "ovdb" is
// absent from the compiled catalog — a programming error
// TestNewInstallCmdRegistration and TestOvdbCatalogEntryMatchesGoreleaser
// (catalog_identity_test.go) catch, never a runtime state a user sees.
func newInstallCmd() *cobra.Command {
	return cobracmd.New(cobracmd.CommandOptions{
		Short:  "List and install fleet CLIs relevant to ovdb",
		Errors: installErrors{},
		HostID: ovdbCatalogID,
	})
}

// installErrors implements cobracmd.ErrorMapper for ovdb's own two-code
// contract: zero for success, one for any finding or failure — the same
// contract selfUpdateErrors keeps for self-update
// (cli-install#req:host-owned-exit-codes). Every new kind cli-install added
// (selfupdate.KindUnknownTarget, KindNoInstallDir, KindDestinationExists) is
// mapped explicitly below, even though ovdb's own exit code for all of them
// is the same 1 a bare default branch would already produce, per that REQ's
// "MUST map ... explicitly, never through a self-update default branch".
type installErrors struct{}

// Failure maps every install failure onto ovdb's neutral general-failure
// message; main.commandExitCode falls back to exit 1 for any error without
// an ExitCode() method, which every branch below returns.
//
// cliinstall/cobracmd v0.21.0's own mapFailure short-circuits a nil error
// before ever calling opts.Errors.Failure (see that package's doc comment
// on mapFailure and its TestMapFailure_NeverCallsMapperWithNil), so the
// v0.19.0-era nil-guard this method used to carry is gone.
//
// The three cli-install-only kinds are named in their own explicit case,
// literally satisfying cli-install#req:host-owned-exit-codes ("MUST map the
// three new kinds explicitly... MUST NOT let them fall into a self-update
// default branch"), even though ovdb's own exit code for every kind — new
// or self-update-shared — is the same "install:"-prefixed exit 1.
func (installErrors) Failure(err error) error {
	var usage *cobracmd.UsageError
	if errors.As(err, &usage) {
		return errors.New("install: " + err.Error())
	}
	switch selfupdate.KindOf(err) {
	case selfupdate.KindUnknownTarget:
		// The underlying error already lists valid ids
		// (cli-install#req:unknown-target-refused), so no extra usage text
		// is added here.
		return errors.New("install: " + err.Error())
	case selfupdate.KindNoInstallDir, selfupdate.KindDestinationExists:
		// Both carry their own remedy text already.
		return errors.New("install: " + err.Error())
	default:
		// Every kind shared with self-update: same "install:" prefix.
		return errors.New("install: " + err.Error())
	}
}
