package setup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
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
	stopped := NewStatus("1.0.0", dirs, StoppedServer(6832, dirs), nil, nil)
	if len(stopped.Next) != 4 || stopped.Next[0].Command != "ovdb server start" || stopped.Next[1].Command != "ovdb open" ||
		stopped.Next[2].Command != "ovdb databases create <name>" || stopped.Next[3].Command != "ovdb demo install --yes" {
		t.Errorf("stopped next = %+v", stopped.Next)
	}
	record := &runtime.Record{Port: 7000, Version: "1.0.0", PID: 5, StartedAt: time.Unix(0, 0).UTC()}
	running := NewStatus("1.0.0", dirs, RunningServer(record, dirs), nil, nil)
	if len(running.Next) != 3 || running.Next[0].Command != "ovdb open" || running.Demo.Installed ||
		running.Demo.Location != filepath.Join(dirs.Data, "demos", "todo") {
		t.Errorf("running next = %+v", running.Next)
	}
	// With the demo installed, trying it is no longer suggested.
	demoDB := Database{ID: "todo", Engine: EngineInGitDB, Location: filepath.Join(dirs.Data, "demos", "todo")}
	if unrecorded := NewStatus("1.0.0", dirs, RunningServer(record, dirs), []Database{demoDB}, nil); unrecorded.Demo.Installed {
		t.Errorf("a database in demos/todo that install did not record = %+v", unrecorded.Demo)
	}
	if err := RecordDemo(dirs.Home, DemoRecord{App: "todo", Database: "todo", Location: demoDB.Location}); err != nil {
		t.Fatal(err)
	}
	if installed := NewStatus("1.0.0", dirs, RunningServer(record, dirs), []Database{demoDB}, nil); !installed.Demo.Installed ||
		installed.Demo.Database != "todo" || len(installed.Next) != 2 {
		t.Errorf("status with the demo = %+v", installed)
	}
	want := `{"schema":1,"server":{"state":"not_running","address":"http://ovdb.localhost:6832","fallback_address":"http://127.0.0.1:6832","port":6832,"log":"` +
		filepath.ToSlash(runtime.LogPath(dirs.Runtime)) + `"},"next":[{"label":"Start the OVDB server","command":"ovdb server start"}]}` + "\n"
	if got := string(envelope.Marshal(NewServerDocument(StoppedServer(6832, dirs)))); filepath.Separator == '/' && got != want {
		t.Errorf("stopped server document:\n got %s\nwant %s", got, want)
	}
}

// first-run-onboarding#REQ:home-menu-options: the implemented options in
// the founder's order, with the E1 web wording and a badge from real state.
// Every copy key the document names must exist.
func TestHomeDocument(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	record := &runtime.Record{Port: 7000, Version: "1.0.0", PID: 5, StartedAt: time.Unix(0, 0).UTC()}
	running := NewHome(RunningServer(record, dirs), nil, nil)
	stopped := NewHome(StoppedServer(6832, dirs), nil, nil)
	for _, home := range []HomeDocument{running, stopped} {
		ids := []string{}
		keys := []string{home.QuestionKey}
		for _, line := range home.StatusLine {
			keys = append(keys, line.Key)
		}
		for _, option := range home.Options {
			ids = append(ids, option.ID+"/"+option.Group)
			keys = append(keys, option.LabelKey)
			for _, key := range []string{option.WebLabelKey, option.DescriptionKey} {
				if key != "" {
					keys = append(keys, key)
				}
			}
			if option.Badge != nil {
				keys = append(keys, option.Badge.LabelKey)
			}
		}
		if !slices.Equal(ids, []string{"demo/primary", "create/primary", "connect/primary", "server/primary", "browse/secondary", "settings/secondary"}) {
			t.Errorf("options = %v", ids)
		}
		for _, key := range keys {
			func() {
				defer func() {
					if recover() != nil {
						t.Errorf("copy key %q is not in copy/en.json", key)
					}
				}()
				uicopy.T(key, nil)
			}()
		}
	}
	if line := running.StatusLine[0]; line.Key != "home.status.server_running" || line.Params["address"] != "http://ovdb.localhost:7000" {
		t.Errorf("running status line = %+v", line)
	}
	if badge := running.Options[3].Badge; badge.Tone != "ok" || badge.LabelKey != "server.badge.running" {
		t.Errorf("running badge = %+v", badge)
	}
	if badge := stopped.Options[3].Badge; badge.Tone != "neutral" || stopped.StatusLine[0].Key != "home.status.server_not_running" {
		t.Errorf("stopped home = %+v", stopped)
	}
	if running.Options[3].DescriptionKey != "home.menu.server_help" || stopped.Options[3].DescriptionKey != "home.menu.server_help_stopped" {
		t.Errorf("server help: running %q, stopped %q", running.Options[3].DescriptionKey, stopped.Options[3].DescriptionKey)
	}
	if browse := running.Options[4]; !browse.Disabled || browse.DescriptionKey != "home.menu.needs_database" {
		t.Errorf("browse without databases = %+v", browse)
	}
	// A returning user: databases · current database · server, with Browse
	// data enabled (first-run-onboarding#REQ:returning-user-home).
	context := &dbcontext.Context{Database: "notes", Path: "/", Scope: dbcontext.ScopeProject, Dir: "/p/a"}
	withDatabases := NewHome(RunningServer(record, dirs), []Database{{ID: "notes", State: MountMounted}, {ID: "todo", State: MountMounted}}, context)
	var summary []string
	for _, ref := range withDatabases.StatusLine {
		summary = append(summary, uicopy.T(ref.Key, ref.Params))
	}
	if got := strings.Join(summary, " · "); got != "2 databases · using notes (this project) · OVDB server running at http://ovdb.localhost:7000" {
		t.Errorf("returning-user summary = %q", got)
	}
	if ids := []string{withDatabases.Options[4].ID, withDatabases.Options[5].ID}; ids[0] != "browse" || ids[1] != "databases" || withDatabases.Options[4].Disabled {
		t.Errorf("home with databases = %v", withDatabases.Options)
	}
	if option := running.Options[3]; option.LabelKey != "home.menu.start_server" || option.WebLabelKey != "home.menu.server" {
		t.Errorf("server option = %+v", option)
	}
}

func TestConfigChangeReportsNoChange(t *testing.T) {
	t.Parallel()
	dirs := testDirs(t)
	if err := paths.EnsurePrivateDir(dirs.Home); err != nil {
		t.Fatal(err)
	}
	first, err := ApplyConfigChange(dirs, ConfigChange{Key: KeyServerPort, Value: "7000"}, true)
	if err != nil || first.Changed == nil || !*first.Changed || len(first.Next) != 1 {
		t.Fatalf("first change = %+v, %v", first, err)
	}
	again, err := ApplyConfigChange(dirs, ConfigChange{Key: KeyServerPort, Value: "7000"}, true)
	if err != nil || again.Changed == nil || *again.Changed || len(again.Next) != 0 {
		t.Errorf("unchanged = %+v, %v", again, err)
	}
	_, err = ApplyConfigChange(dirs, ConfigChange{Key: KeyServerPort, Value: "abc"}, true)
	if e := envelope.As(err); e == nil || len(e.Next) != 1 || e.Next[0].Command != "ovdb config get server.port" {
		t.Errorf("bad port next = %+v", e)
	}
	if got := NormalizeOrigins([]string{" HTTPS://App.Example/ ", "not an origin", "http://localhost:5173"}); !slices.Equal(got, []string{"https://app.example", "http://localhost:5173"}) {
		t.Errorf("NormalizeOrigins = %v", got)
	}
}
