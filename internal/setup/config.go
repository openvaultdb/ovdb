package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
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

// Configuration keys. Telemetry and the global context follow in later
// increments.
const (
	KeyServerPort = "server.port"
	// KeyServerCORS lists the browser app origins allowed to call /v1/… and
	// /token with bearer tokens (capability 25, CLI only: exception E7).
	KeyServerCORS = "server.cors"
)

// Keys lists the supported keys, for usage errors.
var Keys = []string{KeyServerPort, KeyServerCORS}

// Config is config.yaml. Unset values are omitted and mean "default".
type Config struct {
	Server ServerConfig `yaml:"server,omitempty" json:"server"`
}

// ServerConfig is the server section of config.yaml.
type ServerConfig struct {
	Port int      `yaml:"port,omitempty" json:"port,omitempty"`
	CORS []string `yaml:"cors,omitempty" json:"cors,omitempty"`
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
	case KeyServerCORS:
		origins, corsErr := ParseOrigins(change.Value)
		if corsErr != nil {
			return ConfigDocument{}, corsErr
		}
		config.Server.CORS = origins
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
		WithReason(uicopy.T("config.unknown_key", map[string]string{"key": key, "keys": strings.Join(Keys, ", ")})).
		WithNext(envelope.Next{Label: uicopy.T("next.config_get", nil), Command: "ovdb config get " + KeyServerPort})
}

// ParseOrigins parses a comma-separated list of browser origins
// (scheme://host[:port], http or https, nothing after the host). An empty
// value clears the list.
func ParseOrigins(value string) ([]string, *envelope.Error) {
	var origins []string
	for _, part := range strings.Split(value, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasSuffix(origin, "?") {
			return nil, envelope.New(envelope.InvalidArgument, uicopy.T("config.failed", nil)).
				WithReason(uicopy.T("config.cors_invalid", map[string]string{"value": origin})).
				WithNext(envelope.Next{Label: uicopy.T("next.config_get", nil), Command: "ovdb config get " + KeyServerCORS})
		}
		origins = append(origins, strings.ToLower(parsed.Scheme+"://"+parsed.Host))
	}
	return origins, nil
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
