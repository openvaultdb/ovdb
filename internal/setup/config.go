package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

// ConfigFile is the configuration file in OVDB home.
const ConfigFile = "config.yaml"

// EnvPort overrides the configured port for one shell.
const EnvPort = "OVDB_PORT"

// KeyServerPort is the only configuration key increment 1a implements;
// server.cors, telemetry and the global context follow in later increments.
const KeyServerPort = "server.port"

// Config is config.yaml. Unset values are omitted and mean "default".
type Config struct {
	Server ServerConfig `yaml:"server,omitempty" json:"server"`
}

// ServerConfig is the server section of config.yaml.
type ServerConfig struct {
	Port int `yaml:"port,omitempty" json:"port,omitempty"`
}

// ConfigDocument is the body of GET/PUT /api/local/v1/config and the --json
// output of `ovdb config get|set`.
type ConfigDocument struct {
	Schema int             `json:"schema"`
	Config Config          `json:"config"`
	Next   []envelope.Next `json:"next"`
}

// ConfigChange is the body of PUT /api/local/v1/config.
type ConfigChange struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// LoadConfig reads config.yaml; a missing file is the empty configuration.
func LoadConfig(home string) (Config, error) {
	var config Config
	data, err := os.ReadFile(filepath.Join(home, ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("read %s: %w", filepath.Join(home, ConfigFile), err)
	}
	return config, nil
}

// NewConfigDocument wraps config. A change made while the server runs gets
// the restart that applies it as its next action.
func NewConfigDocument(config Config, changedWhileRunning bool) ConfigDocument {
	next := []envelope.Next{}
	if changedWhileRunning {
		next = append(next, envelope.Next{Label: uicopy.T("next.restart_to_apply", nil), Command: "ovdb server restart"})
	}
	return ConfigDocument{Schema: envelope.Schema, Config: config, Next: next}
}

// ApplyConfigChange validates change, writes config.yaml owner-only and
// returns the resulting document. The caller must be the home's single
// writer: the running server, or a client holding home.lock (serverRunning
// false).
func ApplyConfigChange(dirs paths.Dirs, change ConfigChange, serverRunning bool) (ConfigDocument, error) {
	config, err := LoadConfig(dirs.Home)
	if err != nil {
		return ConfigDocument{}, err
	}
	switch change.Key {
	case KeyServerPort:
		port, portErr := ParsePort(change.Value, KeyServerPort)
		if portErr != nil {
			return ConfigDocument{}, portErr
		}
		config.Server.Port = port
	default:
		return ConfigDocument{}, UnknownConfigKey(change.Key)
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return ConfigDocument{}, err
	}
	if err := paths.WriteFilePrivate(filepath.Join(dirs.Home, ConfigFile), data); err != nil {
		return ConfigDocument{}, err
	}
	return NewConfigDocument(config, serverRunning), nil
}

// UnknownConfigKey is invalid_argument naming the supported keys.
func UnknownConfigKey(key string) *envelope.Error {
	return envelope.New(envelope.InvalidArgument, uicopy.T("config.failed", nil)).
		WithReason(uicopy.T("config.unknown_key", map[string]string{"key": key, "keys": KeyServerPort})).
		WithNext(envelope.Next{Label: uicopy.T("next.config_get", nil), Command: "ovdb config get " + KeyServerPort})
}

// ParsePort validates a port number given through source (a flag, variable
// or key name).
func ParsePort(value, source string) (int, *envelope.Error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, envelope.New(envelope.InvalidArgument, uicopy.T("port.invalid.message", nil)).
			WithReason(uicopy.T("port.invalid.reason", map[string]string{"value": value, "source": source})).
			WithNext(envelope.Next{Label: uicopy.T("next.use_default_port", nil), Command: "ovdb server start --port " + strconv.Itoa(runtime.DefaultPort)})
	}
	return port, nil
}

// ResolvePort applies --port > OVDB_PORT > server.port > 6832
// (REQ:port-precedence). explicit is true for the flag and the variable,
// which must match a running server's port.
func ResolvePort(flagPort int, getenv paths.Getenv, config Config) (port int, explicit bool, err *envelope.Error) {
	switch {
	case flagPort != 0:
		port, err = ParsePort(strconv.Itoa(flagPort), "--port")
		return port, true, err
	case getenv(EnvPort) != "":
		port, err = ParsePort(getenv(EnvPort), EnvPort)
		return port, true, err
	case config.Server.Port != 0:
		return config.Server.Port, false, nil
	default:
		return runtime.DefaultPort, false, nil
	}
}
