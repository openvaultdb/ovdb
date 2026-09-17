package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/strongo/cli-helpers/daemonlifecycle"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

// childEnv makes the test binary act as `ovdb` for the detached server.
const childEnv = "OVDB_CLI_TEST_CHILD"

const testVersion = "1.0.0-test"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		root := newRoot(&cli.App{Version: testVersion})
		root.SetArgs(os.Args[1:])
		if err := root.Execute(); err != nil {
			cli.Render(err, os.Args[1:], os.Stdout, os.Stderr)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func newRoot(app *cli.App) *cobra.Command {
	root := &cobra.Command{Use: "ovdb", SilenceUsage: true, SilenceErrors: true}
	app.AddCommands(root)
	return root
}

type env struct {
	t      *testing.T
	dirs   paths.Dirs
	vars   map[string]string
	app    *cli.App
	opened []string // URLs `ovdb open` launched
	// browserErr is what the fake browser launch returns.
	browserErr error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{t: t, dirs: paths.Dirs{
		Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data"),
	}}
	e.vars = map[string]string{
		paths.EnvHome: e.dirs.Home, paths.EnvRuntimeDir: e.dirs.Runtime, paths.EnvDataHome: e.dirs.Data,
		"OVDB_PORT": strconv.Itoa(freePort(t)),
	}
	e.app = &cli.App{
		Version: testVersion, Getenv: func(key string) string { return e.vars[key] },
		Executable: os.Args[0], ChildEnv: []string{childEnv + "=1"},
		OpenBrowser: func(url string) error {
			e.opened = append(e.opened, url)
			return e.browserErr
		},
	}
	t.Cleanup(func() { _, _ = runtime.Stop(context.Background(), e.dirs.Runtime, 0) })
	return e
}

func (e *env) port() int {
	port, _ := strconv.Atoi(e.vars["OVDB_PORT"])
	return port
}

type result struct {
	stdout, stderr string
	code           int
}

func (e *env) run(args ...string) result {
	e.t.Helper()
	var stdout, stderr bytes.Buffer
	root := newRoot(e.app)
	root.SetArgs(args)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	code := 0
	if err := root.ExecuteContext(context.Background()); err != nil {
		if !cli.Render(err, args, &stdout, &stderr) {
			e.t.Fatalf("ovdb %v returned a non-envelope error: %v", args, err)
		}
		code = 1
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// api calls the local API with the instance secret, as a reference body.
func (e *env) api(method, path string) string {
	e.t.Helper()
	state, err := runtime.Inspect(context.Background(), e.dirs.Runtime)
	if err != nil || !state.Running {
		e.t.Fatalf("server not running: %+v %v", state, err)
	}
	request, _ := http.NewRequest(method, runtime.BaseURL(state.Record.Port)+path, nil)
	request.Header.Set("Authorization", "Bearer "+state.Secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	return string(body)
}

func freePort(t *testing.T) int {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func decodeError(t *testing.T, r result, code envelope.Code) *envelope.Error {
	t.Helper()
	if r.code != 1 {
		t.Fatalf("exit %d, want 1; stdout %s stderr %s", r.code, r.stdout, r.stderr)
	}
	e := envelope.Decode([]byte(r.stdout))
	if e == nil || e.Code != code {
		t.Fatalf("stdout %q, want one %s envelope (stderr %s)", r.stdout, code, r.stderr)
	}
	if strings.Count(strings.TrimSpace(r.stdout), "\n") != 0 {
		t.Errorf("stdout has more than one JSON document: %q", r.stdout)
	}
	return e
}

// AC:cli-json-matches-api for the 1a commands, plus the whole lifecycle.
func TestJSONEqualsAPIThroughLifecycle(t *testing.T) {
	e := newEnv(t)

	start := e.run("server", "start", "--json")
	if start.code != 0 {
		t.Fatalf("start: %+v", start)
	}
	if want := e.api(http.MethodGet, "/api/local/v1/server"); start.stdout != want {
		t.Errorf("server start --json\n got %s\nwant %s", start.stdout, want)
	}
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"server", "status", "--json"}, "/api/local/v1/server"},
		{[]string{"config", "get", "server.port", "--json"}, "/api/local/v1/config"},
	} {
		got := e.run(tc.args...)
		if want := e.api(http.MethodGet, tc.path); got.code != 0 || got.stdout != want {
			t.Errorf("ovdb %v\n got %+v\nwant %s", tc.args, got, want)
		}
	}
	// `ovdb status` itself lives in package main; its preview branch is App.Status.
	var statusOut bytes.Buffer
	statusCmd := &cobra.Command{Use: "status"}
	statusCmd.SetOut(&statusOut)
	statusCmd.SetContext(context.Background())
	if err := e.app.Status(statusCmd, true); err != nil {
		t.Fatal(err)
	}
	if want := e.api(http.MethodGet, "/api/local/v1/status"); statusOut.String() != want {
		t.Errorf("status --json\n got %s\nwant %s", statusOut.String(), want)
	}

	// Human start output: both addresses, the sign-in hint, no login code.
	again := e.run("server", "start")
	for _, want := range []string{"already running at http://ovdb.localhost:", "Also at http://127.0.0.1:", "Sign in with `ovdb open`"} {
		if !strings.Contains(again.stdout, want) {
			t.Errorf("start output lacks %q: %s", want, again.stdout)
		}
	}
	if strings.Contains(again.stdout, "code=") {
		t.Error("start printed a login link")
	}

	open := e.run("open", "--print-url", "--json")
	var link struct {
		Schema      int    `json:"schema"`
		URL         string `json:"url"`
		FallbackURL string `json:"fallback_url"`
	}
	if err := json.Unmarshal([]byte(open.stdout), &link); err != nil || link.Schema != 1 ||
		!strings.HasPrefix(link.URL, "http://ovdb.localhost:") || !strings.HasPrefix(link.FallbackURL, "http://127.0.0.1:") {
		t.Errorf("open --json = %+v", open)
	}
	if len(e.opened) != 0 {
		t.Errorf("--print-url launched a browser: %v", e.opened)
	}

	// Without --print-url the browser opens the primary link, or the
	// fallback with --host 127.0.0.1; both links are printed either way.
	launched := e.run("open")
	if launched.code != 0 || len(e.opened) != 1 || !strings.HasPrefix(e.opened[0], "http://ovdb.localhost:") ||
		!strings.Contains(launched.stdout, "Opening your browser…") || !strings.Contains(launched.stdout, "http://127.0.0.1:") {
		t.Errorf("open = %+v, opened %v", launched, e.opened)
	}
	if fallback := e.run("open", "--host", "127.0.0.1", "--json"); fallback.code != 0 || len(e.opened) != 2 || !strings.HasPrefix(e.opened[1], "http://127.0.0.1:") {
		t.Errorf("open --host 127.0.0.1 = %+v, opened %v", fallback, e.opened)
	}
	e.browserErr = errors.New("no display")
	if printOnly := e.run("open"); printOnly.code != 0 || strings.Contains(printOnly.stdout, "Opening") ||
		!strings.Contains(printOnly.stdout, "sign-in link") || !strings.Contains(printOnly.stdout, "/login?code=") {
		t.Errorf("open without a browser = %+v", printOnly)
	}
	e.browserErr = nil

	set := e.run("config", "set", "server.port", "7777")
	defer func() {
		if same := e.run("config", "set", "server.port", "7777"); !strings.Contains(same.stdout, "server.port is already 7777. No change.") || strings.Contains(same.stdout, "restart") {
			t.Errorf("unchanged config set = %+v", same)
		}
	}()
	if set.code != 0 || !strings.Contains(set.stdout, "Saved server.port = 7777.") || !strings.Contains(set.stdout, "ovdb server restart") {
		t.Errorf("config set = %+v", set)
	}

	stop := e.run("server", "stop")
	if stop.code != 0 || !strings.Contains(stop.stdout, "Ask your AI assistant to start OVDB again, or run `ovdb open`.") {
		t.Errorf("stop = %+v", stop)
	}
	stopped := e.run("server", "status", "--json")
	if stopped.code != 0 || !strings.Contains(stopped.stdout, `"state":"not_running"`) {
		t.Errorf("status after stop = %+v", stopped)
	}
	get := e.run("config", "get", "server.port")
	if get.code != 0 || get.stdout != "server.port = 7777\n" {
		t.Errorf("config get while stopped = %+v", get)
	}
}

// REQ:auto-start: a command that needs the server starts it with one line on
// stderr and keeps stdout pure JSON; --no-start refuses.
func TestAutoStartAndNoStart(t *testing.T) {
	e := newEnv(t)
	refused := e.run("open", "--no-start", "--json")
	_ = decodeError(t, refused, envelope.ServerNotRunning)

	opened := e.run("open", "--json")
	if opened.code != 0 || !strings.HasPrefix(opened.stdout, `{"schema":1,"url":`) {
		t.Fatalf("open = %+v", opened)
	}
	if want := "Started the OVDB server at http://ovdb.localhost:" + strconv.Itoa(e.port()) + "\n"; opened.stderr != want {
		t.Errorf("stderr = %q, want %q", opened.stderr, want)
	}
}

// AC:error-envelope-shape: a non-OVDB program holds the port.
func TestPortInUseJSON(t *testing.T) {
	e := newEnv(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:"+e.vars["OVDB_PORT"])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	delete(e.vars, "OVDB_PORT")
	port := listener.Addr().(*net.TCPAddr).Port
	r := e.run("server", "start", "--port", strconv.Itoa(port), "--json")
	failure := decodeError(t, r, envelope.PortInUse)
	if failure.Next[0].Command != "ovdb server start --port "+strconv.Itoa(port+1) {
		t.Errorf("next = %+v", failure.Next)
	}

	human := e.run("server", "start", "--port", strconv.Itoa(port))
	for _, want := range []string{
		"Couldn't start the OVDB server", "Why: Port " + strconv.Itoa(port) + " is already used by another program.",
		"What you can do:", "ovdb config set server.port " + strconv.Itoa(port+1),
	} {
		if human.code != 1 || !strings.Contains(human.stderr, want) {
			t.Errorf("human output lacks %q: %+v", want, human)
		}
	}
	if human.stdout != "" {
		t.Errorf("problem printed to stdout: %q", human.stdout)
	}

	// The offered fix works while the port stays busy.
	fix := e.run("config", "set", "server.port", strconv.Itoa(port+1), "--json")
	if fix.code != 0 || !strings.Contains(fix.stdout, `"port":`+strconv.Itoa(port+1)) {
		t.Errorf("config set while stopped = %+v", fix)
	}
}

// AC:usage-error-exits-one.
func TestUsageErrorsAreInvalidArgument(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"server", "start", "--bogus", "--json"},
		{"server", "stop", "extra", "--json"},
		{"config", "get", "--json"},
		{"config", "set", "server.port", "--json"},
		{"config", "set", "server.port", "zero", "--json"},
		{"config", "get", "server.bogus", "--json"},
		{"config", "set", "server.cors", "ftp://x.example", "--json"},
		{"open", "--port", "1", "--json"},
		{"open", "--host", "evil.example", "--json"},
		{"server", "status", "--port", "--json"},
	} {
		failure := decodeError(t, e.run(args...), envelope.InvalidArgument)
		if len(failure.Next) == 0 || failure.Next[0].Command == "" {
			t.Errorf("ovdb %v: next = %+v", args, failure.Next)
		}
	}
	human := e.run("server", "start", "--bogus")
	if human.code != 1 || !strings.Contains(human.stderr, "Couldn't run ovdb server start") || !strings.Contains(human.stderr, "unknown flag: --bogus") {
		t.Errorf("human usage error = %+v", human)
	}
}

// REQ:start-failure-in-restricted-environments, through the CLI.
func TestStartFailedJSON(t *testing.T) {
	e := newEnv(t)
	e.app.ChildEnv = append(e.app.ChildEnv, cli.EnvStartFault+"=1")
	failure := decodeError(t, e.run("server", "start", "--json"), envelope.ServerStartFailed)
	if failure.Reason != "test fault: exiting before readiness" {
		t.Errorf("reason = %q", failure.Reason)
	}
}

// REQ:client-values-and-mismatch.
func TestHomeMismatch(t *testing.T) {
	e := newEnv(t)
	if r := e.run("server", "start"); r.code != 0 {
		t.Fatalf("start: %+v", r)
	}
	e.vars[paths.EnvHome] = filepath.Join(t.TempDir(), "other")
	for _, args := range [][]string{
		{"server", "start", "--json"}, {"open", "--json"}, {"server", "stop", "--json"}, {"server", "status", "--json"},
		{"config", "get", "server.port", "--json"}, {"config", "set", "server.port", "7001", "--json"},
	} {
		failure := decodeError(t, e.run(args...), envelope.ServerConfigMismatch)
		if failure.Next[len(failure.Next)-1].Command != "ovdb server restart" {
			t.Errorf("ovdb %v next = %+v", args, failure.Next)
		}
	}
}

func TestCommandsHiddenWithoutPreview(t *testing.T) {
	t.Setenv(preview.EnvVar, "")
	root := newRoot(&cli.App{})
	for _, name := range []string{"server", "open", "config"} {
		found, _, err := root.Find([]string{name})
		if err != nil || !found.Hidden {
			t.Errorf("%s: hidden = %v, %v", name, found.Hidden, err)
		}
	}
	t.Setenv(preview.EnvVar, "1")
	root = newRoot(&cli.App{})
	if found, _, _ := root.Find([]string{"server"}); found.Hidden {
		t.Error("server hidden with the preview on")
	}
	if found, _, _ := root.Find([]string{"server", "run"}); !found.Hidden {
		t.Error("server run is offered")
	}
}

// Restart replaces a running server, and treats a stale record whose pid now
// belongs to another process as "not running" (review finding 2).
func TestRestartAndStaleRecord(t *testing.T) {
	e := newEnv(t)
	if r := e.run("server", "start"); r.code != 0 {
		t.Fatalf("start: %+v", r)
	}
	first, _ := runtime.ReadRecord(e.dirs.Runtime)
	restarted := e.run("server", "restart", "--json")
	second, _ := runtime.ReadRecord(e.dirs.Runtime)
	if restarted.code != 0 || second == nil || second.InstanceID == first.InstanceID || restarted.stdout != e.api(http.MethodGet, "/api/local/v1/server") {
		t.Fatalf("restart = %+v, record %+v", restarted, second)
	}
	if r := e.run("server", "stop"); r.code != 0 {
		t.Fatalf("stop: %+v", r)
	}

	// A crash leaves server.json behind and the pid is reused by this test.
	stale := fmt.Sprintf(`{"schema":1,"instance_id":"gone","home":%q,"pid":%d,"process_identity":"reused","port":%d,"version":"x"}`,
		e.dirs.Home, os.Getpid(), e.port())
	for name, content := range map[string]string{runtime.RecordFile: stale, runtime.SecretFile: "old"} {
		if err := paths.WriteFilePrivate(filepath.Join(e.dirs.Runtime, name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	stopped := e.run("server", "stop")
	if stopped.code != 0 || !strings.Contains(stopped.stdout, "The OVDB server isn't running.") {
		t.Errorf("stop over a stale record = %+v", stopped)
	}
	for name, content := range map[string]string{runtime.RecordFile: stale, runtime.SecretFile: "old"} {
		if err := paths.WriteFilePrivate(filepath.Join(e.dirs.Runtime, name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if r := e.run("server", "restart"); r.code != 0 || !strings.Contains(r.stdout, "OVDB server is running at") {
		t.Errorf("restart over a stale record = %+v", r)
	}
}

// A non-private runtime directory refuses a locked config write with the
// config copy and still prints the directory warnings (review finding 7).
func TestConfigSetWithOpenRuntimeDir(t *testing.T) {
	e := newEnv(t)
	for _, dir := range []string{e.dirs.Home, e.dirs.Runtime} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(dir, 0o755)
	}
	if daemonlifecycle.ValidateOwnerOnly(e.dirs.Runtime) == nil {
		t.Skip("the runtime directory is owner-only on this runner")
	}
	r := e.run("config", "set", "server.port", "7001", "--json")
	failure := decodeError(t, r, envelope.Forbidden)
	if failure.Message != "Couldn't change the setting" || !strings.Contains(r.stderr, paths.PrivacyFix(e.dirs.Home)) ||
		!strings.Contains(r.stderr, paths.PrivacyFix(e.dirs.Runtime)) {
		t.Errorf("config set = %+v", r)
	}
}
