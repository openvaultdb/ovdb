package tui

// These tests drive the root Model against a real local server, the same
// way internal/runtime's tests do: the test binary doubles as the detached
// child process, dispatched by TestMain on OVDB_TUI_TEST_CHILD. This
// exercises client.Local's real Start/Stop/SetConfig — the TUI has no
// server-vs-files logic of its own to fake.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

const (
	childEnv    = "OVDB_TUI_TEST_CHILD"
	testVersion = "9.9.9-test"
)

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "serve" {
		os.Exit(childServe())
	}
	os.Exit(m.Run())
}

func childServe() int {
	dirs, err := paths.Resolve(os.Getenv)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	port, _ := strconv.Atoi(os.Args[len(os.Args)-1])
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := localserver.Run(ctx, localserver.RunOptions{Dirs: dirs, Port: port, Version: testVersion, Log: os.Stdout}); err != nil {
		return 1
	}
	return 0
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// realModel builds a root Model over a *client.Local that can really start
// and stop a server: Command re-executes this test binary as the child.
func realModel(t *testing.T, port int) Model {
	t.Helper()
	base := t.TempDir()
	dirs := paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")}
	local := &client.Local{
		Dirs: dirs, Version: testVersion, Port: port, ExplicitPort: true,
		Command: func(port int) *exec.Cmd {
			command := exec.Command(os.Args[0], strconv.Itoa(port))
			command.Env = append(append(os.Environ(), childEnv+"=serve"), dirs.Env()...)
			return command
		},
	}
	t.Cleanup(func() {
		_, _ = runtime.Stop(context.Background(), dirs.Runtime, 0)
	})
	m := New(context.Background(), local, nil, 80, 24)
	return drain(t, m, m.Init())
}

// TestHomeServerStartOpenBrowserStop drives the exact manual flow the task
// brief asks for: Home -> OVDB server -> start -> Open in browser -> stop
// (AC:stop-copy), all through real client.Local calls.
func TestHomeServerStartOpenBrowserStop(t *testing.T) {
	port := freePort(t)
	m := realModel(t, port)

	m = send(t, m, key("down"))  // past Try a demo
	m = send(t, m, key("down"))  // past Create a database
	m = send(t, m, key("down"))  // past Connect an existing database
	m = send(t, m, key("enter")) // Home -> Server (not running)
	if m.screen != ScreenServer || m.server.server.State != setup.StateNotRunning {
		t.Fatalf("after opening Server screen: screen=%q state=%q", m.screen, m.server.server.State)
	}

	m = send(t, m, key("enter")) // Start
	if m.screen != ScreenServer {
		t.Fatalf("after start, screen = %q, want %q (busy=%v)", m.screen, ScreenServer, m.busy)
	}
	if m.server.server.State != setup.StateRunning {
		t.Fatalf("server.State = %q, want running", m.server.server.State)
	}
	if len(m.server.menu()) != 3 {
		t.Fatalf("running menu has %d items, want 3", len(m.server.menu()))
	}

	m = send(t, m, key("enter")) // menu[0] = Open in browser
	if !m.server.linkShown {
		t.Fatal("Open in browser should show the sign-in link")
	}
	if m.server.link.URL == "" || m.server.link.FallbackURL == "" {
		t.Errorf("login link missing a URL: %+v", m.server.link)
	}

	// menu is [Open in browser, Stop, Restart] (Next lists stop before
	// restart); one "down" selects Stop.
	m = send(t, m, key("down"))
	m = send(t, m, key("enter"))
	if m.screen != ScreenResult {
		t.Fatalf("after stop, screen = %q, want %q", m.screen, ScreenResult)
	}
	view := m.viewResult()
	if want := "Ask your AI assistant to start OVDB again, or run `ovdb open`."; !contains(view, want) {
		t.Errorf("result view missing stop-copy %q:\n%s", want, view)
	}
}

// TestPortConflictProblemThenUsePortRemedy occupies the target port with a
// plain listener (not an OVDB server) so Start fails deterministically with
// port_in_use, then drives the Problem screen's "Use port <N+1> instead"
// remedy end to end: it must persist the new port and successfully start on
// it (local-server-and-web-console#REQ:deterministic-port-conflict,
// first-run-onboarding#AC:problem-shows-why-and-fix).
func TestPortConflictProblemThenUsePortRemedy(t *testing.T) {
	port := freePort(t)
	occupied, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	m := realModel(t, port)
	m = send(t, m, key("down"))  // past Try a demo
	m = send(t, m, key("down"))  // past Create a database
	m = send(t, m, key("down"))  // past Connect an existing database
	m = send(t, m, key("enter")) // Home -> Server
	m = send(t, m, key("enter")) // Start -> fails: port in use

	if m.screen != ScreenProblem {
		t.Fatalf("screen = %q, want %q", m.screen, ScreenProblem)
	}
	if m.problem.err == nil || m.problem.err.Code != "port_in_use" {
		t.Fatalf("problem.err = %+v, want code port_in_use", m.problem.err)
	}
	view := m.viewProblem()
	for _, want := range []string{"Couldn't start the OVDB server", "Why:", strconv.Itoa(port), "Use port"} {
		if !contains(view, want) {
			t.Errorf("problem view missing %q:\n%s", want, view)
		}
	}

	n := m.problem.selected()
	if n == nil || n.Action != "use_port" {
		t.Fatalf("selected() = %+v, want the use_port remedy", n)
	}
	newPort, ok := parsePort(n.Command)
	if !ok {
		t.Fatalf("could not parse a port out of %q", n.Command)
	}

	m = send(t, m, key("enter")) // run the remedy
	if m.screen != ScreenServer {
		t.Fatalf("after the remedy, screen = %q, want %q (problem=%+v)", m.screen, ScreenServer, m.problem.err)
	}
	if m.server.server.State != setup.StateRunning {
		t.Fatalf("server.State = %q, want running", m.server.server.State)
	}
	if m.server.server.Port != newPort {
		t.Errorf("server started on port %d, want %d", m.server.server.Port, newPort)
	}

	// The port change must have persisted, exactly like `ovdb config set`.
	config, err := m.local.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	document, decErr := decodeConfig(config)
	if decErr != nil {
		t.Fatalf("decodeConfig: %v", decErr)
	}
	if document.Config.Server.Port != newPort {
		t.Errorf("persisted server.port = %d, want %d", document.Config.Server.Port, newPort)
	}
}

func contains(s, substr string) bool { return strings.Contains(s, substr) }
