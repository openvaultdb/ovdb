package tui

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// Try a demo in the TUI (capabilities 18 and 19): Home's first option shows
// where the lists will be stored, installs on Enter, and the Result offers
// Open TODO app and Done; o signs the browser in to /apps/todo/
// (todo-demo#REQ:demo-next-actions, first-run-onboarding#REQ:home-menu-options).
func TestTryADemoInstallsAndOpensTheApp(t *testing.T) {
	m := realModel(t, freePort(t))
	var opened []string
	m.openBrowser = func(link string) error { opened = append(opened, link); return nil }
	built := false
	m.local.ConsoleBuilt = func() bool { return built }

	if view := flat(m.View().Content); !strings.Contains(view, "> Try a demo See a small TODO app and an AI agent share the same data. Create a database") {
		t.Fatalf("Home:\n%s", view)
	}
	m = send(t, m, key("enter"))
	location := filepath.Join(m.local.Dirs.Data, "demos", "todo")
	view := flat(m.View().Content)
	for _, want := range []string{"Try a demo", "two lists, To buy and To watch", "The lists will be stored as readable files in", "Enter install · Esc back"} {
		if m.screen != ScreenDemo || !strings.Contains(view, want) {
			t.Errorf("demo screen (%s) lacks %q:\n%s", m.screen, want, view)
		}
	}
	if !strings.Contains(strings.ReplaceAll(view, " ", ""), location) {
		t.Errorf("demo screen lacks the location %s:\n%s", location, view)
	}

	m = send(t, m, key("enter"))
	view = flat(m.View().Content)
	for _, want := range []string{"The TODO demo is ready", "Two lists, To buy and To watch, are stored as files in", "Your apps and AI agents can use them through the OVDB server.",
		"What next? • Open TODO app ovdb demo open • Install TODO AI skill (ask the person first) ovdb skills install todo-demo", "o open the TODO app · s install TODO AI skill · Enter done"} {
		if m.screen != ScreenResult || !strings.Contains(view, want) {
			t.Errorf("result (%s) lacks %q:\n%s", m.screen, want, view)
		}
	}

	// A binary without the console says so instead of opening a blank page.
	m = send(t, m, key("o"))
	if m.screen != ScreenProblem || m.problem.err.Code != envelope.Unsupported || len(opened) != 0 {
		t.Fatalf("not built: screen %s problem %+v opened %v", m.screen, m.problem.err, opened)
	}
	built = true
	m = send(t, m, key("esc"))
	m = send(t, m, key("enter")) // Try a demo again: already installed
	if view := flat(m.View().Content); m.screen != ScreenResult || !strings.Contains(view, "The TODO demo is already installed") {
		t.Fatalf("again (%s):\n%s", m.screen, view)
	}
	m = send(t, m, key("o"))
	if len(opened) != 1 {
		t.Fatalf("opened %v", opened)
	}
	link, err := url.Parse(opened[0])
	if err != nil || link.Path != "/login" || link.Query().Get("next") != "/apps/todo/" {
		t.Errorf("opened %s", opened[0])
	}
	if view := flat(m.View().Content); !strings.Contains(view, "If it didn't open, use this sign-in link") || !strings.Contains(strings.ReplaceAll(view, " ", ""), opened[0]) {
		t.Errorf("links not shown:\n%s", view)
	}
	m = send(t, m, key("enter"))
	if m.screen != ScreenHome {
		t.Errorf("Done went to %s", m.screen)
	}
}

func TestDemoScreenSizes(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 40}} {
		m := testModel(t, size[0], size[1])
		m = send(t, m, key("enter"))
		if m.screen != ScreenDemo {
			t.Fatalf("screen = %s", m.screen)
		}
		for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
			if n := len([]rune(line)); n > size[0] {
				t.Errorf("%dx%d: line of %d: %q", size[0], size[1], n, line)
			}
		}
	}
}
