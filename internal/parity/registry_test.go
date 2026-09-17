package parity

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/localserver"
)

// rootCommand registers the new commands next to a stub `ovdb status`; the
// real one lives in package main and cannot be imported.
func rootCommand() *cobra.Command {
	root := &cobra.Command{Use: "ovdb"}
	root.AddCommand(&cobra.Command{Use: "status", Run: func(*cobra.Command, []string) {}})
	(&cli.App{Version: "test"}).AddCommands(root)
	return root
}

func TestRegistryNamesExistingCommandsAndEndpoints(t *testing.T) {
	root := rootCommand()
	endpoints := localserver.Endpoints()
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
		for _, endpoint := range row.API {
			if !slices.Contains(endpoints, endpoint) {
				t.Errorf("capability %s (%s): endpoint %q does not exist", row.ID, row.Capability, endpoint)
			}
		}
		for _, exception := range row.Exceptions {
			if len(exception) != 2 || exception[0] != 'E' || exception[1] < '1' || exception[1] > '7' {
				t.Errorf("capability %s: unknown exception %q", row.ID, exception)
			}
		}
		// A row is implemented only when every interface has it or an
		// exception covers the gap.
		missing := row.TUI == "" || (row.Web == "" && len(row.Exceptions) == 0)
		if row.Implemented && missing {
			t.Errorf("capability %s (%s) is marked implemented with a missing cell and no exception", row.ID, row.Capability)
		}
	}
}
