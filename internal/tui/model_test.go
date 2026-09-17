package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// testModel builds a root Model over a *client.Local pointed at a fresh
// temporary home: pure reads (Status, Server, Config) work without a
// server, exactly like internal/client's own tests.
func testModel(t *testing.T, width, height int) Model {
	t.Helper()
	base := t.TempDir()
	local := &client.Local{
		Dirs: paths.Dirs{
			Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data"),
		},
		Version: "9.9.9-test", Port: 6832,
	}
	m := New(context.Background(), local, nil, width, height)
	// A real bubbletea Program calls Init() before any input reaches
	// Update, so Home already has its (pure-read) document loaded; tests
	// that navigate straight from Home rely on that, exactly like the TUI
	// itself in tmux.
	return drain(t, m, m.Init())
}

// drain runs cmd and keeps feeding whatever tea.Cmd(s) come back —
// including tea.Batch's BatchMsg, which the real bubbletea Program unpacks
// into its constituent Cmds rather than ever handing to Update — until
// nothing is left to run. That mirrors what a live Program does, so tests
// can drive a full start/stop/remedy round trip synchronously.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		cmd := pending[0]
		pending = pending[1:]
		if cmd == nil {
			continue
		}
		result := cmd()
		if result == nil {
			continue
		}
		if batch, ok := result.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
			continue
		}
		updated, next := m.Update(result)
		m2, ok := updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T, want Model", updated)
		}
		m = m2
		if next != nil {
			pending = append(pending, next)
		}
	}
	return m
}

// send feeds one message straight to Update and drains anything it returns.
func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	return drain(t, m, func() tea.Msg { return msg })
}

func key(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Text: s} }

// ---------------------------------------------------------------------------
// New / Init
// ---------------------------------------------------------------------------

func TestNewStartsOnHome(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	if m.screen != ScreenHome {
		t.Errorf("screen = %q, want %q", m.screen, ScreenHome)
	}
	if cmd := m.Init(); cmd == nil {
		t.Error("Init() should load the Home status")
	}
}

func TestInitLoadsHomeStatus(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m = send(t, m, homeLoadedMsg{document: setup.NewHome(setup.Server{State: setup.StateNotRunning, Port: 6832}, nil)})
	if !m.home.loaded {
		t.Error("home.loaded should be true after homeLoadedMsg")
	}
	if !strings.Contains(m.viewHome(), "OVDB server not running") {
		t.Errorf("Home view missing not-running status:\n%s", m.viewHome())
	}
}

// ---------------------------------------------------------------------------
// Home navigation
// ---------------------------------------------------------------------------

func TestHomeEnterOnStartServerLoadsServerScreen(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m = send(t, m, key("down")) // Create a database comes first
	m = send(t, m, key("enter"))
	if m.screen != ScreenServer {
		t.Fatalf("screen = %q, want %q", m.screen, ScreenServer)
	}
	if !m.server.loaded {
		t.Error("server screen should have loaded the current state")
	}
	if m.server.server.State != setup.StateNotRunning {
		t.Errorf("server.State = %q, want not_running (pure read, nothing started)", m.server.server.State)
	}
}

func TestHomeDownThenEnterOpensSettings(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m = send(t, m, key("down"))
	m = send(t, m, key("down"))
	if m.home.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.home.cursor)
	}
	m = send(t, m, key("enter"))
	if m.screen != ScreenSettings {
		t.Fatalf("screen = %q, want %q", m.screen, ScreenSettings)
	}
	if !m.settings.loaded {
		t.Error("settings screen should have loaded the current config")
	}
}

func TestHomeCursorDoesNotOverflow(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	for range 5 {
		m = send(t, m, key("down"))
	}
	if want := len(m.home.document.Options) - 1; m.home.cursor != want {
		t.Errorf("cursor = %d, want clamped to %d", m.home.cursor, want)
	}
	for range 5 {
		m = send(t, m, key("up"))
	}
	if m.home.cursor != 0 {
		t.Errorf("cursor = %d, want clamped to 0", m.home.cursor)
	}
}

func TestQQuitsOnlyOnHome(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Error("'q' on Home should quit")
	}
	m.screen = ScreenServer
	_, cmd = m.Update(key("q"))
	if cmd != nil {
		t.Error("'q' off Home should not quit")
	}
}

// ---------------------------------------------------------------------------
// Server screen
// ---------------------------------------------------------------------------

func TestServerScreenBackGoesHome(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m = send(t, m, key("enter")) // Home -> Server
	m = send(t, m, key("esc"))
	if m.screen != ScreenHome {
		t.Errorf("screen = %q, want %q", m.screen, ScreenHome)
	}
}

func TestServerScreenShowsRunningMenuAfterStart(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m.screen = ScreenServer
	server := setup.Server{State: setup.StateRunning, Address: "http://ovdb.localhost:6832", FallbackAddress: "http://127.0.0.1:6832", Version: "9.9.9-test"}
	m.server = serverScreen{loaded: true, server: server, next: setup.NewServerDocument(server).Next}
	menu := m.server.menu()
	if len(menu) != 3 {
		t.Fatalf("running menu has %d items, want 3 (open browser, restart, stop)", len(menu))
	}
	view := m.viewServer()
	for _, want := range []string{"Open in browser", "Restart the OVDB server", "Stop the OVDB server"} {
		if !strings.Contains(view, want) {
			t.Errorf("server view missing %q:\n%s", want, view)
		}
	}
}

// TestOpenBrowserFailureShowsDistinctMessage is the review fix for a real
// bug: a launch failure must not render identically to a successful one, or
// a person has no way to tell "your browser is opening" from "open this
// link yourself".
func TestOpenBrowserFailureShowsDistinctMessage(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m.screen = ScreenServer
	server := setup.Server{State: setup.StateRunning, Address: "http://ovdb.localhost:6832", FallbackAddress: "http://127.0.0.1:6832"}
	m.server = serverScreen{loaded: true, server: server, next: setup.NewServerDocument(server).Next}
	link := localserver.LoginLink{URL: "http://ovdb.localhost:6832/login?code=x", FallbackURL: "http://127.0.0.1:6832/login?code=x"}

	failed := send(t, m, loginLinkMsg{link: link, openErr: errors.New("no opener on PATH")})
	failedView := failed.viewServer()
	if !strings.Contains(failedView, "Couldn't open a browser here") {
		t.Errorf("failed open should show the failure wording:\n%s", failedView)
	}
	if strings.Contains(failedView, "If the console didn't open") {
		t.Errorf("failed open should not show the success wording:\n%s", failedView)
	}

	opened := send(t, m, loginLinkMsg{link: link, openErr: nil})
	openedView := opened.viewServer()
	if !strings.Contains(openedView, "If the console didn't open") {
		t.Errorf("successful open should show the success wording:\n%s", openedView)
	}
	if strings.Contains(openedView, "Couldn't open a browser here") {
		t.Errorf("successful open should not show the failure wording:\n%s", openedView)
	}
}

// ---------------------------------------------------------------------------
// Problem screen and the "use_port" remedy parsing
// ---------------------------------------------------------------------------

func TestProblemScreenRendersWhyAndWhatYouCanDo(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	err := envelope.New(envelope.PortInUse, "Couldn't start the OVDB server").
		WithReason("Port 6832 is already used by another program.").
		WithNext(
			envelope.Next{Label: "Use port 6833 instead", Command: "ovdb server start --port 6833", Action: "use_port"},
			envelope.Next{Label: "Keep port 6833 for next time", Command: "ovdb config set server.port 6833"},
		)
	m.problem.setError(err)
	m.screen = ScreenProblem
	view := m.viewProblem()
	for _, want := range []string{
		"Couldn't start the OVDB server",
		"Why: Port 6832 is already used by another program.",
		"What you can do:",
		"Use port 6833 instead",
		"ovdb server start --port 6833",
		"Keep port 6833 for next time",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("problem view missing %q:\n%s", want, view)
		}
	}
}

func TestParsePort(t *testing.T) {
	t.Parallel()
	if port, ok := parsePort("ovdb server start --port 6833"); !ok || port != 6833 {
		t.Errorf("parsePort = %d, %v, want 6833, true", port, ok)
	}
	if _, ok := parsePort("ovdb config set server.port abc"); ok {
		t.Error("parsePort should reject a non-numeric trailing token")
	}
	if _, ok := parsePort(""); ok {
		t.Error("parsePort should reject an empty command")
	}
}

func TestProblemActionableSelectionSkipsNonActionableEntries(t *testing.T) {
	t.Parallel()
	var s problemScreen
	s.setError(envelope.New(envelope.PortInUse, "x").WithNext(
		envelope.Next{Label: "info only", Command: "ovdb config get server.port"},
		envelope.Next{Label: "Use port 6833 instead", Command: "ovdb server start --port 6833", Action: "use_port"},
	))
	n := s.selected()
	if n == nil || n.Action != "use_port" {
		t.Fatalf("selected() = %+v, want the use_port entry", n)
	}
}

// ---------------------------------------------------------------------------
// Stop copy (AC:stop-copy)
// ---------------------------------------------------------------------------

func TestStopResultShowsStoppedCopy(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m = send(t, m, stopResultMsg{wasRunning: true})
	if m.screen != ScreenResult {
		t.Fatalf("screen = %q, want %q", m.screen, ScreenResult)
	}
	view := m.viewResult()
	if !strings.Contains(view, "Ask your AI assistant to start OVDB again, or run `ovdb open`.") {
		t.Errorf("result view missing the stop-copy line:\n%s", view)
	}
}

func TestStopResultWhenAlreadyNotRunning(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m = send(t, m, stopResultMsg{wasRunning: false})
	view := m.viewResult()
	if !strings.Contains(view, "OVDB server isn't running.") {
		t.Errorf("result view missing the not-running title:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// Sizes (first-run-onboarding#REQ:tui-keyboard-and-size, AC:tui-sizes)
// ---------------------------------------------------------------------------

func noWiderThan(t *testing.T, content string, width int) {
	t.Helper()
	for _, line := range strings.Split(stripANSI(content), "\n") {
		if n := len([]rune(line)); n > width {
			t.Errorf("line %q is %d runes wide, want <= %d", line, n, width)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
		case r == '\x1b':
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// homeServerStates covers both Home status-line shapes: not running (short)
// and running (long — address plus fallback address concatenated is the
// review-reported overflow at 80 and 60 columns), so TestSizes exercises
// the one state whose status line is actually long.
func homeServerStates() []setup.Server {
	return []setup.Server{
		{State: setup.StateNotRunning, Port: 6832},
		{State: setup.StateRunning, Address: "http://ovdb.localhost:6832", FallbackAddress: "http://127.0.0.1:6832", Port: 6832, Version: "9.9.9-test"},
	}
}

func TestSizes(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {120, 40}, {60, 20}, {50, 15}}
	screens := []string{ScreenHome, ScreenServer, ScreenSettings, ScreenResult, ScreenProblem}
	for _, size := range sizes {
		for _, screen := range screens {
			for _, homeServer := range homeServerStates() {
				if screen != ScreenHome && homeServer.State == setup.StateRunning {
					continue // only Home's rendering varies by server state
				}
				t.Run(screenSizeName(screen, size.w, size.h)+"_home_"+homeServer.State, func(t *testing.T) {
					t.Parallel()
					m := testModel(t, size.w, size.h)
					m = send(t, m, homeLoadedMsg{document: setup.NewHome(homeServer, nil)})
					m.screen = screen
					runningServer := setup.Server{State: setup.StateRunning, Address: "http://ovdb.localhost:6832", FallbackAddress: "http://127.0.0.1:6832", Version: "9.9.9-test"}
					m.server = serverScreen{loaded: true, server: runningServer, next: setup.NewServerDocument(runningServer).Next}
					m.settings = settingsScreen{loaded: true}
					m.result = newStopResult(true)
					m.problem.setError(envelope.New(envelope.PortInUse, "Couldn't start the OVDB server").
						WithReason("Port 6832 is already used by another program.").
						WithNext(envelope.Next{Label: "Use port 6833 instead", Command: "ovdb server start --port 6833", Action: "use_port"}))

					view := m.View()
					content := view.Content

					if size.w < minWidth || size.h < minHeight {
						if !strings.Contains(content, "Make the window a little bigger") {
							t.Errorf("%dx%d should show the too-small message:\n%s", size.w, size.h, content)
						}
						return
					}
					noWiderThan(t, content, size.w)
					if strings.Contains(content, "Make the window a little bigger") {
						t.Errorf("%dx%d should not show the too-small message", size.w, size.h)
					}
				})
			}
		}
	}
}

func screenSizeName(screen string, w, h int) string {
	return screen + "_" + itoa(w) + "x" + itoa(h)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// noticeBuffer smoke test.
func TestNoticeBufferTakeDrains(t *testing.T) {
	t.Parallel()
	var b noticeBuffer
	_, _ = b.Write([]byte("one\ntwo\n"))
	lines := b.take()
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Errorf("take() = %v, want [one two]", lines)
	}
	if more := b.take(); len(more) != 0 {
		t.Errorf("second take() = %v, want empty (drained)", more)
	}
}
