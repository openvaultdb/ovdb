package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/tui"
)

// EnvNonInteractive forces bare `ovdb`'s non-interactive path even when
// stdin and stdout are both terminals — an AI agent harness that still
// attaches a tty sets it (first-run-onboarding#REQ:never-block-without-terminal).
const EnvNonInteractive = "OVDB_NON_INTERACTIVE"

// RootRunE implements first-run-onboarding#REQ:bare-ovdb-launches-tui and
// REQ:bare-ovdb-non-interactive: a real terminal opens the TUI on Home;
// anything else (a pipe, redirected stdin/stdout, or OVDB_NON_INTERACTIVE)
// prints the same status `ovdb status` would and exits 0 without waiting
// for input.
//
// main.go registers this on root only behind OVDB_PREVIEW=1, so bare `ovdb`
// keeps printing today's help without the gate — REQ:preview-gate — and the
// existing golden test is untouched.
func (a *App) RootRunE(cmd *cobra.Command, _ []string) error {
	if a.interactive() {
		return a.runTUI(cmd)
	}
	return a.bootstrap(cmd)
}

// bootstrap is non-interactive bare `ovdb`: a compact status and the five
// ways to set up, never waiting for input and starting nothing
// (first-run-onboarding#REQ:bare-ovdb-non-interactive).
func (a *App) bootstrap(cmd *cobra.Command) error {
	return run(func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).Status(cmd.Context())
		if err != nil {
			return err
		}
		var status setup.Status
		if err := json.Unmarshal(body, &status); err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		say(w, uicopy.T("status.title", map[string]string{"version": status.Version}))
		say(w, "")
		parts := []string{}
		for _, ref := range setup.StatusLine(status.Server, status.Databases, status.Context) {
			parts = append(parts, uicopy.T(ref.Key, ref.Params))
		}
		say(w, strings.Join(parts, " · "))
		say(w, "")
		say(w, uicopy.T("problem.what_you_can_do", nil))
		writeNext(w, setup.BootstrapNext())
		return nil
	})(cmd, nil)
}

func (a *App) isTerminal(fd uintptr) bool {
	if a.IsTerminal != nil {
		return a.IsTerminal(fd)
	}
	return term.IsTerminal(fd)
}

func (a *App) interactive() bool {
	if a.getenv(EnvNonInteractive) != "" {
		return false
	}
	return a.isTerminal(os.Stdin.Fd()) && a.isTerminal(os.Stdout.Fd())
}

// termSize resolves the starting size for the TUI, falling back to a
// reasonable default (the same one ingitdb-cli's TUI uses) when the size
// cannot be read — for example a pty without a reported size yet.
func (a *App) termSize() (width, height int) {
	if a.TermSize != nil {
		return a.TermSize()
	}
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w == 0 {
		return 80, 24
	}
	return w, h
}

func (a *App) runTUI(cmd *cobra.Command) error {
	t, err := a.resolve(0)
	if err != nil {
		return err
	}
	local := a.local(cmd, t)
	width, height := a.termSize()
	model := tui.New(cmd.Context(), local, a.openBrowser, width, height)
	program := tea.NewProgram(model, tea.WithContext(cmd.Context()))
	_, runErr := program.Run()
	return runErr
}
