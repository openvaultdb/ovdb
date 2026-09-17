package cli

import (
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"

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
	return a.Status(cmd, false)
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
