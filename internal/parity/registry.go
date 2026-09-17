// Package parity is the capability registry: per row of the capability
// matrix in spec/features/configuration-parity, the CLI command, TUI screen,
// web route and local API endpoints that implement it, and the documented
// exceptions for the cells that are missing on purpose. Its test fails when
// a named command or endpoint does not exist.
//
// A row stays Implemented=false — and its surface stays behind the preview
// gate — until every non-exception cell exists (REQ:increments-keep-parity).
// Increment 1a filled the CLI and API cells of rows 2–7, 1b the web routes
// (web/routes.json), and 1c (this one) adds row 1 and every TUI cell of
// rows 1–7 — completing parity for rows 1–7.
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

// Rows is the registry, in matrix order.
var Rows = []Row{
	{ID: "1", Capability: "Guided first run", CLI: []string{"ovdb"}, TUI: "home", Web: "/", Implemented: true},
	{ID: "2", Capability: "Whole-setup status", CLI: []string{"ovdb status"}, TUI: "home", Web: "/",
		API: []string{"GET /api/local/v1/status", "GET /api/local/v1/home"}, Implemented: true},
	{ID: "3", Capability: "Start server", CLI: []string{"ovdb server start"}, TUI: "server",
		Exceptions: []string{"E1"}, Implemented: true},
	{ID: "4", Capability: "Server status", CLI: []string{"ovdb server status"}, TUI: "server", Web: "/server",
		API: []string{"GET /api/local/v1/server"}, Implemented: true},
	{ID: "5", Capability: "Stop or restart server", CLI: []string{"ovdb server stop", "ovdb server restart"}, TUI: "server",
		API: []string{"POST /api/local/v1/server/shutdown"}, Exceptions: []string{"E2"}, Implemented: true},
	{ID: "6", Capability: "Open web console (login link)", CLI: []string{"ovdb open"}, TUI: "server",
		API: []string{"POST /api/local/v1/login-links"}, Exceptions: []string{"E1"}, Implemented: true},
	{ID: "7", Capability: "Change server port", CLI: []string{"ovdb config set", "ovdb config get"}, TUI: "settings", Web: "/settings",
		API: []string{"GET /api/local/v1/config", "PUT /api/local/v1/config"}, Implemented: true},
	// Tokens and CORS origins are developer settings: CLI only, and a console
	// session gets 403 on server.cors and /v1/tokens (E7).
	{ID: "25", Capability: "Access tokens and browser app origins",
		CLI: []string{"ovdb token create", "ovdb token list", "ovdb token revoke", "ovdb config set", "ovdb config get"},
		API: []string{"PUT /api/local/v1/config"}, Exceptions: []string{"E7"}},
}
