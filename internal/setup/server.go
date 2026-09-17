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
// output of `ovdb server start|stop|restart|status`.
type ServerDocument struct {
	Schema int    `json:"schema"`
	Server Server `json:"server"`
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
	return ServerDocument{Schema: envelope.Schema, Server: server}
}

// Status is the body of GET /api/local/v1/status and of `ovdb status --json`
// (first-run-onboarding#REQ:status-command). Later increments add databases,
// context, demo, skills and telemetry as they are implemented.
type Status struct {
	Schema    int             `json:"schema"`
	Version   string          `json:"version"`
	Locations paths.Dirs      `json:"locations"`
	Server    Server          `json:"server"`
	Next      []envelope.Next `json:"next"`
}

// NewStatus builds the status for this ovdb version, locations and server.
// next lists only implemented options, in the founder's order.
func NewStatus(version string, dirs paths.Dirs, server Server) Status {
	next := []envelope.Next{}
	if server.State != StateRunning {
		next = append(next, envelope.Next{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"})
	}
	next = append(next, envelope.Next{Label: uicopy.T("next.open_web_setup", nil), Command: "ovdb open"})
	return Status{Schema: envelope.Schema, Version: version, Locations: dirs, Server: server, Next: next}
}
