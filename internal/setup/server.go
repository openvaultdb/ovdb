// Package setup holds the onboarding and configuration services: the
// documents the local API returns and the pure reads that build the same
// documents from state files when no server is running. Presentations (CLI,
// TUI, web) render these documents; they never compute next actions, order
// or validation themselves.
//
// See decision 0006 and spec/features/configuration-parity#REQ:json-equals-api.
package setup

import (
	"strconv"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// Server states.
const (
	StateRunning    = "running"
	StateNotRunning = "not_running"
	StateStopping   = "stopping" // only in the shutdown response
)

// Server describes the local OVDB server in status and server documents.
// It carries no uptime: a start time keeps two reads of the same server
// byte-identical, and presentations format the uptime from it.
type Server struct {
	State           string     `json:"state"`
	Address         string     `json:"address"`
	FallbackAddress string     `json:"fallback_address"`
	Port            int        `json:"port"`
	Version         string     `json:"version,omitempty"`
	PID             int        `json:"pid,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	Log             string     `json:"log"`
}

// ServerDocument is the body of GET /api/local/v1/server and the --json
// output of `ovdb server start|stop|restart|status`. Next holds the
// commands that change the server's state, so every presentation shows the
// same ones (the web console cannot run them itself: parity E1 and E2).
type ServerDocument struct {
	Schema int             `json:"schema"`
	Server Server          `json:"server"`
	Next   []envelope.Next `json:"next"`
}

// PrimaryAddress is the address people open.
func PrimaryAddress(port int) string { return "http://ovdb.localhost:" + strconv.Itoa(port) }

// FallbackAddress is the address that works where *.localhost does not.
func FallbackAddress(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

// RunningServer describes the server recorded in record.
func RunningServer(record *runtime.Record, dirs paths.Dirs) Server {
	startedAt := record.StartedAt
	return Server{
		State: StateRunning, Address: PrimaryAddress(record.Port), FallbackAddress: FallbackAddress(record.Port),
		Port: record.Port, Version: record.Version, PID: record.PID, StartedAt: &startedAt,
		Log: runtime.LogPath(dirs.Runtime),
	}
}

// StoppedServer describes a server that is not running and would start on port.
func StoppedServer(port int, dirs paths.Dirs) Server {
	return Server{
		State: StateNotRunning, Address: PrimaryAddress(port), FallbackAddress: FallbackAddress(port),
		Port: port, Log: runtime.LogPath(dirs.Runtime),
	}
}

// NewServerDocument wraps server in its document.
func NewServerDocument(server Server) ServerDocument {
	next := []envelope.Next{}
	switch server.State {
	case StateRunning:
		next = append(next,
			envelope.Next{Label: uicopy.T("next.stop_server", nil), Command: "ovdb server stop"},
			envelope.Next{Label: uicopy.T("next.restart_server", nil), Command: "ovdb server restart"})
	case StateNotRunning:
		next = append(next, envelope.Next{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"})
	}
	return ServerDocument{Schema: envelope.Schema, Server: server, Next: next}
}

// Badge is a server state as presentations show it: a tone and a copy key.
type Badge struct {
	Tone     string `json:"tone"` // ok, warn, neutral
	LabelKey string `json:"label_key"`
}

// StateBadge maps a server state to its badge.
func StateBadge(state string) Badge {
	switch state {
	case StateRunning:
		return Badge{Tone: "ok", LabelKey: "server.badge.running"}
	case StateStopping:
		return Badge{Tone: "warn", LabelKey: "server.badge.stopping"}
	default:
		return Badge{Tone: "neutral", LabelKey: "server.badge.not_running"}
	}
}

// CopyRef is copy a presentation renders: a catalogue key and its params.
type CopyRef struct {
	Key    string            `json:"key"`
	Params map[string]string `json:"params,omitempty"`
}

// HomeOption is one Home menu option. LabelKey is the terminal wording;
// WebLabelKey, when set, replaces it in the web console, which cannot start
// the server that serves it (parity E1).
type HomeOption struct {
	ID             string `json:"id"`
	Group          string `json:"group"` // primary, secondary
	LabelKey       string `json:"label_key"`
	WebLabelKey    string `json:"web_label_key,omitempty"`
	DescriptionKey string `json:"description_key,omitempty"`
	Badge          *Badge `json:"badge,omitempty"`
	// Disabled options are shown with DescriptionKey saying why.
	Disabled bool `json:"disabled,omitempty"`
}

// HomeDocument is the body of GET /api/local/v1/home: the status line and
// the implemented Home options in the founder's order
// (first-run-onboarding#REQ:home-menu-options, REQ:home-status-line). The
// TUI and the web console both render it, so neither builds the menu.
type HomeDocument struct {
	Schema      int          `json:"schema"`
	StatusLine  []CopyRef    `json:"status_line"`
	QuestionKey string       `json:"question_key"`
	Options     []HomeOption `json:"options"`
}

// NewHome builds Home for server, the registered databases and the context
// that applies to the caller (nil when none does). Options appear here only
// once they are implemented; later increments insert theirs in the founder's
// order.
//
// A returning user (at least one database) sees one summary line above the
// same question and menu: `2 databases · using todo (this project) · OVDB
// server running at …` (first-run-onboarding#REQ:returning-user-home).
func NewHome(server Server, databases []Database, context *dbcontext.Context) HomeDocument {
	badge := StateBadge(server.State)
	// What the server option is for depends on whether it runs.
	serverHelp := "home.menu.server_help_stopped"
	if server.State == StateRunning {
		serverHelp = "home.menu.server_help"
	}
	options := []HomeOption{
		{ID: "demo", Group: "primary", LabelKey: "home.menu.try_demo", DescriptionKey: "home.menu.try_demo_help"},
		{ID: "create", Group: "primary", LabelKey: "home.menu.create_database", DescriptionKey: "home.menu.create_database_help"},
		{ID: "connect", Group: "primary", LabelKey: "home.menu.connect_database", DescriptionKey: "home.menu.connect_database_help"},
		{ID: "server", Group: "primary", LabelKey: "home.menu.start_server", WebLabelKey: "home.menu.server",
			DescriptionKey: serverHelp, Badge: &badge},
	}
	browse := HomeOption{ID: "browse", Group: "secondary", LabelKey: "home.menu.browse"}
	if len(databases) == 0 {
		browse.Disabled, browse.DescriptionKey = true, "home.menu.needs_database"
	}
	options = append(options, browse)
	explore := HomeOption{ID: "explore", Group: "secondary", LabelKey: "home.menu.explore"}
	if len(databases) == 0 {
		explore.Disabled, explore.DescriptionKey = true, "home.menu.needs_database"
	}
	options = append(options, explore)
	if len(databases) > 0 {
		options = append(options, HomeOption{ID: "databases", Group: "secondary", LabelKey: "home.menu.databases"})
	}
	options = append(options, HomeOption{ID: "skills", Group: "secondary", LabelKey: "home.menu.skills"})
	options = append(options, HomeOption{ID: "settings", Group: "secondary", LabelKey: "home.menu.settings"})
	return HomeDocument{
		Schema: envelope.Schema, StatusLine: StatusLine(server, databases, context), QuestionKey: "home.question",
		Options: options,
	}
}

// StatusLine is Home's status line and `ovdb status`'s summary: server and
// databases for a first run; databases, current database and server once
// there is a database.
func StatusLine(server Server, databases []Database, context *dbcontext.Context) []CopyRef {
	line := CopyRef{Key: "home.status.server_not_running"}
	if server.State == StateRunning {
		line = CopyRef{Key: "home.status.server_running", Params: map[string]string{"address": server.Address}}
	}
	if len(databases) == 0 {
		return []CopyRef{line, DatabasesStatus(databases)}
	}
	return []CopyRef{DatabasesStatus(databases), ContextStatus(context), line}
}

// ContextStatus is the status line part naming the current database and its
// scope (first-run-onboarding#REQ:home-status-line).
func ContextStatus(context *dbcontext.Context) CopyRef {
	if context == nil {
		return CopyRef{Key: "home.status.using_none"}
	}
	params := map[string]string{"database": context.Database, "dir": context.Dir}
	switch context.Scope {
	case dbcontext.ScopeProject:
		return CopyRef{Key: "home.status.using_project", Params: params}
	case dbcontext.ScopeGlobal:
		return CopyRef{Key: "home.status.using_global", Params: params}
	case dbcontext.ScopeFlag, dbcontext.ScopeEnvironment:
		return CopyRef{Key: "home.status.using_environment", Params: params}
	default:
		return CopyRef{Key: "home.status.using_only", Params: params}
	}
}

// DatabasesStatus is the status line part counting databases and how many
// need attention (first-run-onboarding#REQ:home-status-line).
func DatabasesStatus(databases []Database) CopyRef {
	attention := 0
	for _, db := range databases {
		if db.State == MountNeedsAttention {
			attention++
		}
	}
	count := map[string]string{"count": strconv.Itoa(len(databases)), "attention": strconv.Itoa(attention)}
	switch {
	case len(databases) == 0:
		return CopyRef{Key: "home.status.databases_none"}
	case attention > 0 && len(databases) == 1:
		return CopyRef{Key: "home.status.database_one_attention"}
	case attention == 1:
		return CopyRef{Key: "home.status.databases_attention_one", Params: count}
	case attention > 0:
		return CopyRef{Key: "home.status.databases_attention", Params: count}
	case len(databases) == 1:
		return CopyRef{Key: "home.status.database_one"}
	default:
		return CopyRef{Key: "home.status.databases_many", Params: count}
	}
}

// Status is the body of GET /api/local/v1/status and of `ovdb status --json`
// (first-run-onboarding#REQ:status-command). Later increments add databases,
// context, demo, skills and telemetry as they are implemented.
type Status struct {
	Schema    int        `json:"schema"`
	Version   string     `json:"version"`
	Locations paths.Dirs `json:"locations"`
	Server    Server     `json:"server"`
	Databases []Database `json:"databases"`
	// Context is the database and path that apply where the client runs
	// (capability 14); null when none does.
	Context *dbcontext.Context `json:"context"`
	// Demo says whether the TODO demo is installed, and where.
	Demo DemoStatus `json:"demo"`
	// Skills lists each OVDB skill and the AI agents it is installed for,
	// as whoever built the document resolves their directories.
	Skills []skills.Installed `json:"skills"`
	// Telemetry is the usage statistics state, and why nothing is sent, as
	// the process that built the document evaluates it
	// (first-run-onboarding#REQ:status-command, telemetry-consent
	// #REQ:sender-process-decides).
	Telemetry StatusTelemetry `json:"telemetry"`
	Next      []envelope.Next `json:"next"`
}

// StatusTelemetry is the status document's telemetry group.
type StatusTelemetry struct {
	State      string `json:"state"`
	Sending    bool   `json:"sending"`
	Reason     string `json:"reason,omitempty"`
	ReasonText string `json:"reason_text,omitempty"`
}

// SetTelemetry sets the telemetry group from a decision.
func (s *Status) SetTelemetry(d telemetry.Decision, available bool) {
	document := telemetry.NewDocument(d, available)
	s.Telemetry = StatusTelemetry{State: d.State, Sending: d.Sending, Reason: d.Reason, ReasonText: document.Telemetry.ReasonText}
}

// NewStatus builds the status for this ovdb version, locations, server,
// registered databases and installed skills. next is the bootstrap list an
// agent without a skill relays (ai-agent-skills#REQ:agent-bootstrap-without-skill):
// terminal, web and command setup, then the demo and the storage skill while
// they are not installed.
func NewStatus(version string, dirs paths.Dirs, server Server, databases []Database, context *dbcontext.Context, installed []skills.Installed) Status {
	if databases == nil {
		databases = []Database{}
	}
	status := Status{Schema: envelope.Schema, Version: version, Locations: dirs, Server: server, Databases: databases, Context: context, Demo: NewDemoStatus(dirs, databases)}
	status.SetSkills(installed)
	return status
}

// BootstrapNext is what non-interactive bare `ovdb` offers, always all five
// (first-run-onboarding#REQ:bare-ovdb-non-interactive): set up in the
// terminal, in the browser or with commands, try the demo, or install the
// storage skill after asking the person.
func BootstrapNext() []envelope.Next {
	return []envelope.Next{
		{Label: uicopy.T("next.setup_terminal", nil), Command: "ovdb"},
		{Label: uicopy.T("next.open_web_setup", nil), Command: "ovdb open"},
		{Label: uicopy.T("next.setup_commands", nil), Command: "ovdb databases create <name>"},
		{Label: uicopy.T("next.try_demo", nil), Command: "ovdb demo install --yes"},
		{Label: uicopy.T("skills.next.install_storage", nil), Command: "ovdb skills install " + skills.Storage + " --yes"},
	}
}

// SetSkills replaces the skills field group and the next entries that
// depend on it. A client calls it with the skills it resolved itself, so
// `ovdb status` agrees with `ovdb skills list` whichever shell started the
// server (local-server-and-web-console#REQ:client-values-and-mismatch).
func (s *Status) SetSkills(installed []skills.Installed) {
	if installed == nil {
		installed = []skills.Installed{}
	}
	next := []envelope.Next{}
	if s.Server.State != StateRunning {
		next = append(next, envelope.Next{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"})
	}
	next = append(next,
		envelope.Next{Label: uicopy.T("next.setup_terminal", nil), Command: "ovdb"},
		envelope.Next{Label: uicopy.T("next.open_web_setup", nil), Command: "ovdb open"},
		envelope.Next{Label: uicopy.T("next.setup_commands", nil), Command: "ovdb databases create <name>"})
	if !s.Demo.Installed {
		next = append(next, envelope.Next{Label: uicopy.T("next.try_demo", nil), Command: "ovdb demo install --yes"})
	}
	storage := false
	for _, skill := range installed {
		storage = storage || skill.ID == skills.Storage && len(skill.InstalledFor) > 0
	}
	if !storage {
		next = append(next, envelope.Next{Label: uicopy.T("skills.next.install_storage", nil), Command: "ovdb skills install " + skills.Storage + " --yes"})
	}
	s.Skills, s.Next = installed, next
}
