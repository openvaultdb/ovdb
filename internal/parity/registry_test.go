package parity

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/tui"
	"github.com/openvaultdb/ovdb/web"
)

// rootCommand registers the new commands next to stubs of `ovdb status`,
// `ovdb token …` and `ovdb databases [create]`; the real ones live in
// package main and cannot be imported.
func rootCommand() *cobra.Command {
	root := &cobra.Command{Use: "ovdb"}
	root.AddCommand(&cobra.Command{Use: "status", Run: func(*cobra.Command, []string) {}})
	token := &cobra.Command{Use: "token"}
	for _, name := range []string{"create", "list", "revoke"} {
		token.AddCommand(&cobra.Command{Use: name, Run: func(*cobra.Command, []string) {}})
	}
	root.AddCommand(token)
	databases := &cobra.Command{Use: "databases", Run: func(*cobra.Command, []string) {}}
	databases.AddCommand(&cobra.Command{Use: "create", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(databases)
	(&cli.App{Version: "test"}).AddCommands(root)
	return root
}

// TestRegistryNamesExistingCommandsAndEndpoints is
// configuration-parity#REQ:capability-registry's existence test: dropping a
// row's named CLI command, TUI screen, web route or local API endpoint
// fails here, naming the row and the interface (AC:missing-cell-fails).
func TestRegistryNamesExistingCommandsAndEndpoints(t *testing.T) {
	root := rootCommand()
	endpoints := localserver.Endpoints()
	screens := tui.ScreenIDs()
	routes := web.Routes()
	seen := map[string]bool{}
	for _, row := range Rows {
		if seen[row.ID] {
			t.Errorf("capability %s listed twice", row.ID)
		}
		seen[row.ID] = true
		for _, path := range row.CLI {
			args := strings.Fields(path)[1:]
			found, rest, err := root.Find(args)
			if err != nil || len(rest) != 0 || found.CommandPath() != path {
				t.Errorf("capability %s (%s): CLI command %q does not exist", row.ID, row.Capability, path)
			}
		}
		if row.TUI != "" && !slices.Contains(screens, row.TUI) {
			t.Errorf("capability %s (%s): TUI screen %q does not exist", row.ID, row.Capability, row.TUI)
		}
		if row.Web != "" && !slices.ContainsFunc(routes, func(r web.Route) bool { return r.Path == row.Web }) {
			t.Errorf("capability %s (%s): web route %q is not in web/routes.json", row.ID, row.Capability, row.Web)
		}
		for _, endpoint := range row.API {
			if !slices.Contains(endpoints, endpoint) {
				t.Errorf("capability %s (%s): endpoint %q does not exist", row.ID, row.Capability, endpoint)
			}
		}
		if slices.Contains(row.Exceptions, "E7") && row.Web != "" {
			t.Errorf("capability %s (%s) is CLI only (E7) but names web route %q", row.ID, row.Capability, row.Web)
		}
		for _, exception := range row.Exceptions {
			if len(exception) != 2 || exception[0] != 'E' || exception[1] < '1' || exception[1] > '7' {
				t.Errorf("capability %s: unknown exception %q", row.ID, exception)
			}
		}
		// A row is implemented only when every interface has it, or an
		// exception (a permanent gap) covers it.
		missing := row.TUI == "" || (row.Web == "" && len(row.Exceptions) == 0)
		if row.Implemented && missing {
			t.Errorf("capability %s (%s) is marked implemented with a missing cell and no exception", row.ID, row.Capability)
		}
	}
}
