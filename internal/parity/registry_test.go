package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/tui"
)

// rootCommand registers the new commands next to a stub `ovdb status`; the
// real one lives in package main and cannot be imported.
func rootCommand() *cobra.Command {
	root := &cobra.Command{Use: "ovdb"}
	root.AddCommand(&cobra.Command{Use: "status", Run: func(*cobra.Command, []string) {}})
	(&cli.App{Version: "test"}).AddCommands(root)
	return root
}

// webRoutes reads web/routes.json (this repo's module root, resolved from
// this test file's own path rather than the working directory) into the
// list of routes the console registers. It returns ok=false — not an
// error — when the file does not exist yet, which increment 1c's own
// worktree never has: web/routes.json is increment 1b's, landing in
// parallel. See PendingWebRows.
func webRoutes(t *testing.T) (routes []string, ok bool) {
	t.Helper()
	_, file, _, found := runtime.Caller(0)
	if !found {
		t.Fatal("parity: could not determine the calling test file's path")
	}
	moduleRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	data, err := os.ReadFile(filepath.Join(moduleRoot, "web", "routes.json"))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("reading web/routes.json: %v", err)
	}
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatalf("parsing web/routes.json: %v", err)
	}
	return routes, true
}

// TestRegistryNamesExistingCommandsAndEndpoints is
// configuration-parity#REQ:capability-registry's existence test: dropping a
// row's named CLI command, TUI screen, web route or local API endpoint
// fails here, naming the row and the interface
// (AC:missing-cell-fails).
func TestRegistryNamesExistingCommandsAndEndpoints(t *testing.T) {
	root := rootCommand()
	endpoints := localserver.Endpoints()
	screens := tui.ScreenIDs()
	routes, haveRoutes := webRoutes(t)
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
		switch {
		case row.Web != "" && haveRoutes && !slices.Contains(routes, row.Web):
			t.Errorf("capability %s (%s): web route %q does not exist", row.ID, row.Capability, row.Web)
		case row.Web == "" && len(row.Exceptions) == 0 && !slices.Contains(PendingWebRows, row.ID):
			t.Errorf("capability %s (%s): missing web cell has no exception and is not on PendingWebRows", row.ID, row.Capability)
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
		// A row is implemented only when every interface has it, or an
		// exception (a permanent gap) covers it; a PendingWebRows gap is
		// increment 1b's, not a permanent exception, so it never counts as
		// implemented on its own.
		missing := row.TUI == "" || (row.Web == "" && len(row.Exceptions) == 0)
		if row.Implemented && missing {
			t.Errorf("capability %s (%s) is marked implemented with a missing cell and no exception", row.ID, row.Capability)
		}
	}
}
