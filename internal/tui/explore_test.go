package tui

import (
	"strings"
	"testing"
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
