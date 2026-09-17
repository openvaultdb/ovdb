// Package parity is the capability registry: per row of the capability
// matrix in spec/features/configuration-parity, the CLI command, TUI screen,
// web route and local API endpoints that implement it, and the documented
// exceptions for the cells that are missing on purpose. Its test fails when
// a named command or endpoint does not exist.
//
// A row stays Implemented=false — and its surface stays behind the preview
// gate — until every non-exception cell exists (REQ:increments-keep-parity).
// Increment 1a filled the CLI and API cells of rows 2–7; increment 1c (this
// one) adds row 1 and every TUI cell of rows 1–7, and turns on the registry
// test's TUI-screen and web-route checks from here on. A row whose Web cell
// still needs increment 1b is listed in PendingWebRows until that lane (or
// the architect, after landing both) fills it in.
package parity

// Row is one capability.
type Row struct {
	ID         string
	Capability string
	CLI        []string // command paths, e.g. "ovdb server start"
	TUI        string   // screen id; "" while pending
	Web        string   // route; "" while pending
	API        []string // "METHOD path"
	Exceptions []string // E1…E7 for intentionally missing cells
	// Implemented is true once every non-exception cell exists and is tested.
	Implemented bool
}

// PendingWebRows lists capability ids whose Web cell cannot be verified yet
// in this worktree because increment 1b (the web console) has not landed
// here: web/routes.json does not exist. The registry test tolerates a
// missing Web cell only for the ids listed here; the web lane or the
// architect removes an id from this list — and fills that row's Web field —
// once increment 1b merges and the route is real
// (configuration-parity#REQ:increments-keep-parity).
var PendingWebRows = []string{"1", "2", "4", "7"}

// Rows is the registry, in matrix order.
var Rows = []Row{
	{ID: "1", Capability: "Guided first run", CLI: []string{"ovdb"}, TUI: "home"},
	{ID: "2", Capability: "Whole-setup status", CLI: []string{"ovdb status"}, TUI: "home",
		API: []string{"GET /api/local/v1/status"}},
	{ID: "3", Capability: "Start server", CLI: []string{"ovdb server start"}, TUI: "server",
		Exceptions: []string{"E1"}, Implemented: true},
	{ID: "4", Capability: "Server status", CLI: []string{"ovdb server status"}, TUI: "server",
		API: []string{"GET /api/local/v1/server"}},
	{ID: "5", Capability: "Stop or restart server", CLI: []string{"ovdb server stop", "ovdb server restart"}, TUI: "server",
		API: []string{"POST /api/local/v1/server/shutdown"}, Exceptions: []string{"E2"}, Implemented: true},
	{ID: "6", Capability: "Open web console (login link)", CLI: []string{"ovdb open"}, TUI: "server",
		API: []string{"POST /api/local/v1/login-links"}, Exceptions: []string{"E1"}, Implemented: true},
	{ID: "7", Capability: "Change server port", CLI: []string{"ovdb config set", "ovdb config get"}, TUI: "settings",
		API: []string{"GET /api/local/v1/config", "PUT /api/local/v1/config"}},
}
