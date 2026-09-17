// Package runtime owns the local OVDB server's process lifecycle and the
// files in the runtime directory: server.json, the instance secret, the
// home lock and server.log. It starts the server detached, proves a running
// server is this home's instance (authenticated whoami), and stops it
// without ever signalling a process it cannot identify.
//
// It knows nothing about HTTP routes beyond the two lifecycle endpoints
// (whoami and shutdown) and nothing about configuration files: callers pass
// the resolved port and the command that runs the server.
//
// See spec/features/local-server-and-web-console (Lifecycle, Port) and
// decision 0007.
package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openvaultdb/ovdb/internal/paths"
)

// File names inside the runtime directory.
const (
	RecordFile = "server.json"
	SecretFile = "secret"
	LockFile   = "home.lock"
	LogFile    = "server.log"
)

// Record is server.json: written by a server once it is listening, it tells
// clients where the server is and which process it is.
type Record struct {
	Schema     int    `json:"schema"`
	InstanceID string `json:"instance_id"`
	Home       string `json:"home"`
	PID        int    `json:"pid"`
	// ProcessIdentity is daemonlifecycle.ProcessIdentity(PID) at start; stop
	// signals PID only while it still matches.
	ProcessIdentity string    `json:"process_identity"`
	Port            int       `json:"port"`
	Version         string    `json:"version"`
	StartedAt       time.Time `json:"started_at"`
}

// ReadRecord returns the runtime record, or nil when there is none.
func ReadRecord(runtimeDir string) (*Record, error) {
	data, err := os.ReadFile(filepath.Join(runtimeDir, RecordFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("read %s: %w", RecordFile, err)
	}
	return &record, nil
}

func writeRecord(runtimeDir string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFilePrivate(filepath.Join(runtimeDir, RecordFile), append(data, '\n'))
}

// ReadSecret returns the instance secret, or "" when there is none.
func ReadSecret(runtimeDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runtimeDir, SecretFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// LogPath is server.log in runtimeDir.
func LogPath(runtimeDir string) string { return filepath.Join(runtimeDir, LogFile) }

// removeRuntimeFiles deletes the record and secret, leaving sessions and the
// log in place (REQ:stale-runtime-state keeps sessions.json).
func removeRuntimeFiles(runtimeDir string) {
	_ = os.Remove(filepath.Join(runtimeDir, RecordFile))
	_ = os.Remove(filepath.Join(runtimeDir, SecretFile))
}
