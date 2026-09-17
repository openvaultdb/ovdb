package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/setup/explore"
)

// TestExploreScreenDataTugCLIAndApp drives Home -> Try a demo -> install ->
// Home -> Explore data -> DataTug CLI (four-key descriptor, no token,
// --no-policies) -> back -> DataTug.app (honest limitation, no control
// implying OVDB can open a database there), against a real local server
// (explore-data-handoff AC:menu-asks-intent-first, AC:demo-explore-copy,
// AC:descriptor-and-command, AC:datatug-app-is-honest).
func TestExploreScreenDataTugCLIAndApp(t *testing.T) {
	port := freePort(t)
	m := realModel(t, port)

	m = send(t, m, key("enter")) // Home -> Try a demo
	if m.screen != ScreenDemo {
		t.Fatalf("screen = %q, want demo", m.screen)
	}
	m = send(t, m, key("enter")) // install
	if m.screen != ScreenResult {
		t.Fatalf("after install, screen = %q", m.screen)
	}
	m = send(t, m, key("esc")) // Result -> Home, reloaded with todo registered

	// Home order once a database exists: demo, create, connect, server
	// (primary), browse, explore, databases, settings (secondary).
	for range 5 {
		m = send(t, m, key("down"))
	}
	if got := m.home.document.Options[m.home.cursor].ID; got != "explore" {
		t.Fatalf("cursor is on option %q, want explore (options=%v)", got, m.home.document.Options)
	}
	m = send(t, m, key("enter")) // Home -> Explore data
	if m.screen != ScreenExplore {
		t.Fatalf("screen = %q, want explore", m.screen)
	}
	view := flat(m.View().Content)
	if !strings.Contains(view, "Where would you like to explore todo's data?") {
		t.Fatalf("explore menu:\n%s", view)
	}
	if !strings.Contains(view, "DataTug shows your two lists, not their items yet") {
		t.Fatalf("demo-specific copy missing:\n%s", view)
	}

	m = send(t, m, key("enter")) // DataTug CLI (cursor 0)
	if m.screen != ScreenExplore || m.explore.view != exploreCLIView {
		t.Fatalf("screen=%q view=%v, want the DataTug CLI view", m.screen, m.explore.view)
	}
	view = flat(m.View().Content)
	for _, want := range []string{"OVDB_DATATUG_TOKEN", "ovdb token create --db todo --scope read-only", "--no-policies", "openvaultdb://"} {
		if !strings.Contains(view, want) {
			t.Errorf("DataTug CLI view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "ovdb_") {
		t.Errorf("DataTug CLI view leaks a token value:\n%s", view)
	}
	if m.explore.cli.Descriptor.DatabaseID != "todo" || m.explore.cli.Descriptor.TokenEnv != "OVDB_DATATUG_TOKEN" ||
		m.explore.cli.Descriptor.PrincipalID != "local-owner" {
		t.Errorf("descriptor = %+v", m.explore.cli.Descriptor)
	}

	m = send(t, m, key("esc")) // back to the menu
	if m.explore.view != exploreMenuView {
		t.Fatalf("view = %v, want the menu", m.explore.view)
	}
	m = send(t, m, key("down"))  // DataTug.app
	m = send(t, m, key("enter")) // choose it
	if m.explore.view != exploreAppView {
		t.Fatalf("view = %v, want the DataTug.app view", m.explore.view)
	}
	view = flat(m.View().Content)
	if !strings.Contains(view, "DataTug.app can't open an OpenVaultDB database directly yet.") {
		t.Fatalf("app view lacks the honesty copy:\n%s", view)
	}
	if !strings.Contains(view, "Use DataTug CLI instead") || !strings.Contains(view, "Open DataTug.app") {
		t.Fatalf("app view lacks its two actions:\n%s", view)
	}
	if strings.Contains(strings.ToLower(view), "open database") {
		t.Fatalf("app view implies OVDB can open a database there:\n%s", view)
	}

	// esc from the app view returns to the menu, and esc from the menu goes Home.
	m = send(t, m, key("esc"))
	if m.explore.view != exploreMenuView {
		t.Fatalf("view = %v, want the menu", m.explore.view)
	}
	m = send(t, m, key("esc"))
	if m.screen != ScreenHome {
		t.Fatalf("screen = %q, want home", m.screen)
	}
}

// F4 (review-inc-7.md): at the 80x24 floor every rendered line fits the
// window (lipgloss.JoinVertical pads every line in the joined view to the
// widest one, so a single overlong line would blow up the whole screen's
// apparent width, not just its own). A command line that does not fit is
// truncated for display with a trailing "…", never hard-wrapped with a bare
// newline that would corrupt it if pasted (seen live splitting the quoted
// descriptor path mid-string) — "c" copies the untouched, full text.
func TestExploreCLICommandsAreNeverHardWrapped(t *testing.T) {
	port := freePort(t)
	m := realModel(t, port)
	m = send(t, m, key("enter")) // Home -> Try a demo
	m = send(t, m, key("enter")) // install
	m = send(t, m, key("esc"))   // -> Home, reloaded
	for range 5 {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter")) // Home -> Explore data
	m = send(t, m, key("enter")) // DataTug CLI
	if m.screen != ScreenExplore || m.explore.view != exploreCLIView {
		t.Fatalf("screen=%q view=%v", m.screen, m.explore.view)
	}
	cli := m.explore.cli
	if len(cli.QueryCommand) < 90 {
		t.Fatalf("test setup: query command too short to exercise truncation (%d chars): %q", len(cli.QueryCommand), cli.QueryCommand)
	}

	view := m.View().Content // NOT flat(): line breaks matter here
	lines := strings.Split(stripANSI(view), "\n")
	for _, ln := range lines {
		if n := len([]rune(ln)); n > 80 {
			t.Errorf("a rendered line is %d columns wide at an 80-column width: %q", n, ln)
		}
	}
	// Every raw command line is shown either whole, or as a truncated
	// prefix ending "…" — never split with a bare newline.
	for _, want := range append(strings.Split(cli.ShellText, "\n"), strings.Split(cli.QueryCommand, "\n")...) {
		want = strings.TrimSpace(want)
		found := false
		for _, ln := range lines {
			got := strings.TrimPrefix(strings.TrimRight(ln, " "), "  ")
			if got == want || (strings.HasSuffix(got, "…") && strings.HasPrefix(want, strings.TrimSuffix(got, "…"))) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command line %q has no whole-or-truncated-prefix match in the view:\n%s", want, flat(view))
		}
	}
	if !strings.Contains(flat(view), "--no-policies") {
		t.Errorf("--no-policies is not visible at 80x24:\n%s", flat(view))
	}

	// The copy action carries the real, untouched text regardless of what
	// was visually truncated.
	_, cmd := m.updateExplore("c")
	if cmd == nil {
		t.Fatal("\"c\" produced no command")
	}
	msg := cmd()
	want := explore.CopyText(cli)
	if got := fmt.Sprintf("%v", msg); got != want {
		t.Errorf("clipboard content = %q, want %q", got, want)
	}
}
