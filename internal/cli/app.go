// Package cli is ovdb's command-line presentation of the onboarding and
// configuration capabilities: `ovdb server …`, `ovdb open`, `ovdb config …`
// and the preview `ovdb status`. Commands resolve environment-dependent
// inputs, call the local server (or the pure reads in internal/setup) and
// render the resulting documents; --json prints those documents unchanged.
//
// Every new command is hidden unless OVDB_PREVIEW=1, exits 0 on success and
// 1 on any failure, and reports failures in the shared error envelope.
package cli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// EnvStartFault is a hidden test-only variable: a server started with it set
// to "1" exits before readiness, so start fails with server_start_failed.
const EnvStartFault = "OVDB_TEST_START_FAULT"

// App carries what every command needs from the process.
type App struct {
	Version string
	Getenv  paths.Getenv // os.Getenv when nil
	// Executable runs the detached server; os.Executable() when empty.
	Executable string
	// ChildEnv is appended to the detached server's environment (tests).
	ChildEnv []string
	// IsTerminal reports whether fd is a real terminal; term.IsTerminal when
	// nil. Tests inject a fake so RootRunE's TUI-vs-non-interactive branch
	// does not depend on the process's actual stdio.
	IsTerminal func(fd uintptr) bool
	// TermSize resolves the TUI's starting width and height; term.GetSize
	// on os.Stdout when nil. Tests inject a fake for the same reason.
	TermSize func() (width, height int)
	// OpenBrowser launches a URL; browser.Opener{}.Open when nil.
	OpenBrowser func(url string) error
}

func (a *App) getenv(key string) string {
	if a.Getenv == nil {
		return os.Getenv(key)
	}
	return a.Getenv(key)
}

// AddCommands registers the new commands on root, hidden without the gate.
func (a *App) AddCommands(root *cobra.Command) {
	hidden := !preview.On()
	for _, command := range []*cobra.Command{a.serverCmd(), a.openCmd(), a.configCmd(), a.enginesCmd()} {
		command.Hidden = hidden
		command.SetFlagErrorFunc(flagError)
		root.AddCommand(command)
	}
	// `ovdb databases remove` joins the legacy `ovdb databases`.
	if databases, _, err := root.Find([]string{"databases"}); err == nil && databases != root {
		remove := a.databasesRemoveCmd()
		remove.Hidden = hidden
		remove.SetFlagErrorFunc(flagError)
		databases.AddCommand(remove)
	}
}

func (a *App) dirs() (paths.Dirs, error) {
	return paths.Resolve(a.getenv)
}

// target is the resolved server location for one command.
type target struct {
	dirs     paths.Dirs
	port     int
	explicit bool
}

// resolve applies the port precedence for flagPort (0 when not given).
func (a *App) resolve(flagPort int) (target, error) {
	dirs, err := a.dirs()
	if err != nil {
		return target{}, err
	}
	config, err := setup.LoadConfig(dirs.Home)
	if err != nil {
		return target{}, err
	}
	port, explicit, portErr := setup.ResolvePort(flagPort, a.getenv, config)
	if portErr != nil {
		return target{}, portErr
	}
	return target{dirs: dirs, port: port, explicit: explicit}, nil
}

// local is the client-side service for t; commands only render its documents.
func (a *App) local(cmd *cobra.Command, t target) *client.Local {
	return &client.Local{
		Dirs: t.dirs, Version: a.Version, Port: t.port, ExplicitPort: t.explicit, Notices: cmd.ErrOrStderr(),
		Command: func(port int) *exec.Cmd {
			executable := a.Executable
			if executable == "" {
				executable, _ = os.Executable()
			}
			command := exec.Command(executable, "server", "run", "--port", strconv.Itoa(port))
			command.Env = append(os.Environ(), a.ChildEnv...)
			return command
		},
	}
}

// run adapts a command body so every failure leaves as an envelope error.
func run(body func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := body(cmd, args)
		if err == nil || envelope.As(err) != nil {
			return err
		}
		return envelope.New(envelope.Internal, uicopy.T("usage.failed", map[string]string{"command": cmd.CommandPath()})).
			WithReason(err.Error())
	}
}

// noArgs is cobra.NoArgs as an invalid_argument envelope.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return usageError(cmd, "unexpected arguments: "+strings.Join(args, " "))
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		return usageError(cmd, "expected "+strconv.Itoa(n)+" argument(s), got "+strconv.Itoa(len(args)))
	}
}

// flagError turns a flag parse failure into invalid_argument, exit 1.
func flagError(cmd *cobra.Command, err error) error {
	return usageError(cmd, err.Error())
}

func usageError(cmd *cobra.Command, reason string) *envelope.Error {
	return envelope.New(envelope.InvalidArgument, uicopy.T("usage.failed", map[string]string{"command": cmd.CommandPath()})).
		WithReason(reason).
		WithNext(envelope.Next{Label: uicopy.T("next.help", nil), Command: cmd.CommandPath() + " --help"})
}
