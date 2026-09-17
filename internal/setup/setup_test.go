package setup

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

func testDirs(t *testing.T) paths.Dirs {
	base := t.TempDir()
	return paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")}
}

// REQ:port-precedence: --port > OVDB_PORT > server.port > 6832.
func TestResolvePort(t *testing.T) {
	t.Parallel()
	env := func(port string) paths.Getenv {
		return func(key string) string {
			if key == EnvPort {
				return port
			}
			return ""
		}
	}
	configured := Config{Server: ServerConfig{Port: 7100}}
	for _, tc := range []struct {
		name     string
		flag     int
		env      string
		config   Config
		want     int
		explicit bool
	}{
		{"flag wins", 7300, "7200", configured, 7300, true},
		{"variable next", 0, "7200", configured, 7200, true},
		{"config next", 0, "", configured, 7100, false},
		{"default last", 0, "", Config{}, runtime.DefaultPort, false},
	} {
		port, explicit, err := ResolvePort(tc.flag, env(tc.env), tc.config)
		if err != nil || port != tc.want || explicit != tc.explicit {
			t.Errorf("%s: ResolvePort = %d, %v, %v; want %d, %v", tc.name, port, explicit, err, tc.want, tc.explicit)
		}
	}
	for _, bad := range []string{"0", "65536", "http", "-1"} {
		if _, _, err := ResolvePort(0, env(bad), Config{}); err == nil || err.Code != envelope.InvalidArgument {
			t.Errorf("OVDB_PORT=%q: err = %v, want invalid_argument", bad, err)
		}
	}
	if _, _, err := ResolvePort(70000, env(""), Config{}); err == nil {
		t.Error("--port 70000 accepted")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	if err := paths.EnsurePrivateDir(dirs.Home); err != nil {
		t.Fatal(err)
	}
	if config, err := LoadConfig(dirs.Home); err != nil || config.Server.Port != 0 {
		t.Fatalf("missing config = %+v, %v", config, err)
	}
	document, err := ApplyConfigChange(dirs, ConfigChange{Key: KeyServerPort, Value: " 7000 "}, false)
	if err != nil || document.Config.Server.Port != 7000 || len(document.Next) != 0 {
		t.Fatalf("ApplyConfigChange (stopped) = %+v, %v", document, err)
	}
	document, err = ApplyConfigChange(dirs, ConfigChange{Key: KeyServerPort, Value: "7001"}, true)
	if err != nil || len(document.Next) != 1 || document.Next[0].Command != "ovdb server restart" {
		t.Fatalf("ApplyConfigChange (running) = %+v, %v", document, err)
	}
	if config, _ := LoadConfig(dirs.Home); config.Server.Port != 7001 {
		t.Errorf("reloaded port = %d", config.Server.Port)
	}
	if _, err := ApplyConfigChange(dirs, ConfigChange{Key: "server.bogus", Value: "x"}, true); envelope.As(err) == nil || envelope.As(err).Code != envelope.InvalidArgument {
		t.Errorf("unknown key: %v", err)
	}
	document, err = ApplyConfigChange(dirs, ConfigChange{Key: KeyServerCORS, Value: " http://localhost:5173, HTTPS://App.Example "}, true)
	if err != nil || !slices.Equal(document.Config.Server.CORS, []string{"http://localhost:5173", "https://app.example"}) || document.Config.Server.Port != 7001 {
		t.Fatalf("server.cors = %+v, %v", document.Config, err)
	}
	for _, bad := range []string{"localhost:5173", "ftp://x.example", "http://x.example/path", "http://u:p@x.example", "http://x.example?q", "*"} {
		if _, err := ApplyConfigChange(dirs, ConfigChange{Key: KeyServerCORS, Value: bad}, true); envelope.As(err) == nil || envelope.As(err).Code != envelope.InvalidArgument {
			t.Errorf("server.cors %q accepted: %v", bad, err)
		}
	}
	if document, err = ApplyConfigChange(dirs, ConfigChange{Key: KeyServerCORS, Value: ""}, true); err != nil || document.Config.Server.CORS != nil {
		t.Errorf("clearing server.cors = %+v, %v", document.Config, err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Home, ConfigFile), []byte("server: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dirs.Home); err == nil {
		t.Error("malformed config.yaml accepted")
	}
}

func TestStatusNextListsImplementedOptionsInOrder(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	stopped := NewStatus("1.0.0", dirs, StoppedServer(6832, dirs))
	if len(stopped.Next) != 2 || stopped.Next[0].Command != "ovdb server start" || stopped.Next[1].Command != "ovdb open" {
		t.Errorf("stopped next = %+v", stopped.Next)
	}
	record := &runtime.Record{Port: 7000, Version: "1.0.0", PID: 5, StartedAt: time.Unix(0, 0).UTC()}
	running := NewStatus("1.0.0", dirs, RunningServer(record, dirs))
	if len(running.Next) != 1 || running.Next[0].Command != "ovdb open" {
		t.Errorf("running next = %+v", running.Next)
	}
	want := `{"schema":1,"server":{"state":"not_running","address":"http://ovdb.localhost:6832","fallback_address":"http://127.0.0.1:6832","port":6832,"log":"` +
		filepath.ToSlash(runtime.LogPath(dirs.Runtime)) + `"}}` + "\n"
	if got := string(envelope.Marshal(NewServerDocument(StoppedServer(6832, dirs)))); filepath.Separator == '/' && got != want {
		t.Errorf("stopped server document:\n got %s\nwant %s", got, want)
	}
}
