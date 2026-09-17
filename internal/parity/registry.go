// Package parity is the capability registry: per row of the capability
// matrix in spec/features/configuration-parity, the CLI command, TUI screen,
// web route and local API endpoints that implement it, and the documented
// exceptions for the cells that are missing on purpose. Its test fails when
// a named command or endpoint does not exist.
//
// A row stays Implemented=false — and its surface stays behind the preview
// gate — until every non-exception cell exists (REQ:increments-keep-parity).
// Increment 1a filled the CLI and API cells of rows 2–7 and 1b the web
// routes (web/routes.json); 1c adds the TUI screens. Increment 2 adds rows
// 8, 9, 11 and 12.
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
	{ID: "2", Capability: "Whole-setup status", CLI: []string{"ovdb status"}, Web: "/",
		API: []string{"GET /api/local/v1/status", "GET /api/local/v1/home"}},
	{ID: "3", Capability: "Start server", CLI: []string{"ovdb server start"},
		Exceptions: []string{"E1"}},
	{ID: "4", Capability: "Server status", CLI: []string{"ovdb server status"}, Web: "/server",
		API: []string{"GET /api/local/v1/server"}},
	{ID: "5", Capability: "Stop or restart server", CLI: []string{"ovdb server stop", "ovdb server restart"},
		API: []string{"POST /api/local/v1/server/shutdown"}, Exceptions: []string{"E2"}},
	{ID: "6", Capability: "Open web console (login link)", CLI: []string{"ovdb open"},
		API: []string{"POST /api/local/v1/login-links"}, Exceptions: []string{"E1"}},
	{ID: "7", Capability: "Change server port", CLI: []string{"ovdb config set", "ovdb config get"}, Web: "/settings",
		API: []string{"GET /api/local/v1/config", "PUT /api/local/v1/config"}},
	{ID: "8", Capability: "Storage choices", CLI: []string{"ovdb engines"}, Web: "/databases/new",
		API: []string{"GET /api/local/v1/engines"}},
	{ID: "9", Capability: "Create database (inGitDB, SQLite)", CLI: []string{"ovdb databases create"}, Web: "/databases/new",
		API: []string{"POST /api/local/v1/databases"}},
	{ID: "11", Capability: "List databases", CLI: []string{"ovdb databases"}, Web: "/databases",
		API: []string{"GET /api/local/v1/databases"}},
	{ID: "12", Capability: "Remove database registration", CLI: []string{"ovdb databases remove"}, Web: "/databases",
		API: []string{"DELETE /api/local/v1/databases/{id}"}},
	// Tokens and CORS origins are developer settings: CLI only, and a console
	// session gets 403 on server.cors and /v1/tokens (E7).
	{ID: "25", Capability: "Access tokens and browser app origins",
		CLI: []string{"ovdb token create", "ovdb token list", "ovdb token revoke", "ovdb config set", "ovdb config get"},
		API: []string{"PUT /api/local/v1/config"}, Exceptions: []string{"E7"}},
}
