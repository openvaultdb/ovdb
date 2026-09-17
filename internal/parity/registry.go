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
// rows 1–7 — completing parity for rows 1–7. Increment 2 adds rows
// 8, 9, 11 and 12 in every interface.
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
	{ID: "8", Capability: "Storage choices", CLI: []string{"ovdb engines"}, TUI: "create", Web: "/databases/new",
		API: []string{"GET /api/local/v1/engines"}, Implemented: true},
	{ID: "9", Capability: "Create database (inGitDB, SQLite)", CLI: []string{"ovdb databases create"}, TUI: "create", Web: "/databases/new",
		API: []string{"POST /api/local/v1/databases"}, Implemented: true},
	// Connecting never writes into the storage; a manifest file is copied into
	// OVDB home with its storage paths made absolute.
	{ID: "10", Capability: "Connect existing inGitDB folder or SQLite file", CLI: []string{"ovdb databases connect"}, TUI: "connect", Web: "/databases/connect",
		API: []string{"POST /api/local/v1/databases/connect"}, Implemented: true},
	{ID: "10a", Capability: "Connect with a manifest file (any engine)", CLI: []string{"ovdb databases connect"}, TUI: "connect", Web: "/databases/connect",
		API: []string{"POST /api/local/v1/databases/connect"}, Implemented: true},
	{ID: "11", Capability: "List databases", CLI: []string{"ovdb databases"}, TUI: "databases", Web: "/databases",
		API: []string{"GET /api/local/v1/databases"}, Implemented: true},
	// Removing a database also clears every context that named it.
	{ID: "12", Capability: "Remove database registration", CLI: []string{"ovdb databases remove"}, TUI: "databases", Web: "/databases",
		API: []string{"DELETE /api/local/v1/databases/{id}"}, Implemented: true},
	// Not a matrix row yet (spec amendment pending): loading a database again
	// after editing its manifest or restoring its storage, and loading
	// manifests added by hand, without a server restart.
	{ID: "12a", Capability: "Reload database registration", CLI: []string{"ovdb databases reload"}, TUI: "databases", Web: "/databases",
		API: []string{"POST /api/local/v1/databases/{id}/reload", "POST /api/local/v1/databases/reload"}, Implemented: true},
	// The TUI chooses the database for the project it started in; the web
	// console, which has no working directory, sets only the default for
	// all projects, and the local API refuses a session's project-scope
	// write (E3).
	{ID: "13", Capability: "Choose current database", CLI: []string{"ovdb use"}, TUI: "databases", Web: "/databases",
		API: []string{"PUT /api/local/v1/context"}, Exceptions: []string{"E3"}, Implemented: true},
	// Home's status line shows what applies: in the TUI the project it
	// started in; in the web console, which has no project, the global
	// default or the only database (E3).
	{ID: "14", Capability: "Show current database", CLI: []string{"ovdb use", "ovdb pwd"}, TUI: "home", Web: "/",
		API: []string{"GET /api/local/v1/context", "GET /api/local/v1/home", "GET /api/local/v1/status"}, Implemented: true},
	// Reads go through the existing /v1 data API in every interface.
	{ID: "15", Capability: "Browse data (read-only)", CLI: []string{"ovdb list", "ovdb get"}, TUI: "browse", Web: "/browse",
		API:         []string{"GET /v1/databases/{db}", "POST /v1/databases/{db}/query", "POST /v1/databases/{db}/dtql", "GET /v1/databases/{db}/records/{key}"},
		Implemented: true},
	// A working directory is a shell concept; the TUI and web console show a
	// browsable tree instead (E4).
	{ID: "16", Capability: "Navigate paths", CLI: []string{"ovdb cd", "ovdb pwd"},
		API:        []string{"GET /api/local/v1/context", "PUT /api/local/v1/context", "GET /v1/databases/{db}", "POST /v1/databases/{db}/query", "GET /v1/databases/{db}/records/{key}"},
		Exceptions: []string{"E4"}, Implemented: true},
	// Editing records is for the CLI, agents and apps (E5).
	{ID: "17", Capability: "Write records", CLI: []string{"ovdb set", "ovdb add", "ovdb delete"},
		API:        []string{"PUT /v1/databases/{db}/records/{key}", "PATCH /v1/databases/{db}/records/{key}", "POST /v1/databases/{db}/records/{key}", "DELETE /v1/databases/{db}/records/{key}"},
		Exceptions: []string{"E5"}, Implemented: true},
	// Try a demo: the CLI installs with `ovdb demo install` (and reports with
	// `ovdb demo status`); the TUI's and web console's Try a demo show where the
	// lists will be stored, then install through the same endpoint.
	{ID: "18", Capability: "Install TODO demo", CLI: []string{"ovdb demo install", "ovdb demo status"}, TUI: "demo", Web: "/demo",
		API: []string{"GET /api/local/v1/demo", "POST /api/local/v1/demo/install"}, Implemented: true},
	// Open TODO app: the CLI and TUI create a login link that lands on
	// /apps/todo/; the web console, already signed in, links to it.
	{ID: "19", Capability: "Open TODO app", CLI: []string{"ovdb demo open"}, TUI: "demo", Web: "/demo",
		API: []string{"GET /api/local/v1/demo", "POST /api/local/v1/login-links"}, Implemented: true},
	// AI agent skills: every interface lists both skills with the agents they
	// are installed for. The CLI and TUI resolve directories from their own
	// environment and send them; the web console names only agents the
	// server found and never a directory. Installing is the person's
	// decision: a consent step in the TUI and web console, a question or
	// --yes on the CLI, which an agent relays only after the person's yes (E6).
	{ID: "20", Capability: "List AI skills", CLI: []string{"ovdb skills list"}, TUI: "skills", Web: "/skills",
		API: []string{"GET /api/local/v1/skills"}, Exceptions: []string{"E6"}, Implemented: true},
	{ID: "21", Capability: "Install AI skill", CLI: []string{"ovdb skills install"}, TUI: "skills", Web: "/skills",
		API: []string{"GET /api/local/v1/skills", "POST /api/local/v1/skills/install"}, Exceptions: []string{"E6"}, Implemented: true},
	// Tokens and CORS origins are developer settings: CLI only, and a console
	// session gets 403 on server.cors and /v1/tokens (E7). People grant an
	// app access on the connect flow's consent page (/authorize) instead.
	{ID: "25", Capability: "Access tokens and browser app origins",
		CLI:        []string{"ovdb token create", "ovdb token list", "ovdb token revoke", "ovdb config set", "ovdb config get"},
		API:        []string{"POST /v1/tokens", "GET /v1/tokens", "DELETE /v1/tokens/{id}", "GET /api/local/v1/config", "PUT /api/local/v1/config"},
		Exceptions: []string{"E7"}, Implemented: true},
}
