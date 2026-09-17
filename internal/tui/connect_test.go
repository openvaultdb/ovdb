package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// openConnect goes from Home to Connect an existing database.
func openConnect(t *testing.T, m Model) Model {
	t.Helper()
	for m.home.document.Options[m.home.cursor].ID != "connect" {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	if m.screen != ScreenConnect || !m.connect.loaded {
		t.Fatalf("screen = %q loaded %v, want connect", m.screen, m.connect.loaded)
	}
	return m
}

// AC:home-shows-options in the TUI: the four primary options in order.
func TestHomeShowsFourOptionsInOrder(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	view := flat(m.viewHome())
	last := -1
	for _, want := range []string{"Try a demo", "Create a database", "Connect an existing database", "Start the OVDB server", "Browse data"} {
		at := strings.Index(view, want)
		if at <= last {
			t.Errorf("%q is missing or out of order:\n%s", want, view)
		}
		last = at
	}
}

// The picker lists the catalogue, then Connect with a manifest file; a
// manifest-only engine shows its steps and asks only for the manifest's
// path (database-setup-and-providers#REQ:manifest-only-engines-are-honest).
func TestConnectPickerAndManifestOnlyEngine(t *testing.T) {
	t.Parallel()
	m := openConnect(t, testModel(t, 80, 24))
	view := stripANSI(m.View().Content)
	for _, want := range []string{"Connect an existing database", "What would you like to connect?", "Filter: _", "inGitDB", "PostgreSQL", "Connect with a manifest file"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker lacks %q:\n%s", want, view)
		}
	}
	noWiderThan(t, m.View().Content, 80)
	if lines := strings.Count(m.View().Content, "\n") + 1; lines > 24 {
		t.Errorf("%d lines at 80x24:\n%s", lines, view)
	}
	m = typeText(t, m, "postgres")
	m = send(t, m, key("enter"))
	if m.connect.step != connectManifest || m.connect.chosen.ID != setup.EnginePostgres {
		t.Fatalf("step %s chosen %q", m.connect.step, m.connect.chosen.ID)
	}
	view = stripANSI(m.View().Content)
	for _, want := range []string{"Set this up with a manifest file", "ovdb init --engine postgres --id <name>", "Manifest file:", "Type the path"} {
		if !strings.Contains(view, want) {
			t.Errorf("manifest step lacks %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Name:", "Folder:", "dsn", "password"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("manifest step asks for %q:\n%s", unwanted, view)
		}
	}
	// "q" and "?" are path text here.
	m = typeText(t, m, "/q?")
	if m.connect.manifest != "/q?" || m.showHelp || m.screen != ScreenConnect {
		t.Errorf("typing = %q help %v screen %s", m.connect.manifest, m.showHelp, m.screen)
	}
	m = send(t, m, key("esc"))
	m = send(t, m, key("down")) // past the one match: Connect with a manifest file
	m = send(t, m, key("enter"))
	if m.connect.step != connectManifest || m.connect.chosen.ID != "" {
		t.Errorf("manifest choice: step %s chosen %q", m.connect.step, m.connect.chosen.ID)
	}
}

func TestConnectNameFollowsTheLocation(t *testing.T) {
	t.Parallel()
	m := openConnect(t, testModel(t, 80, 24))
	m = send(t, m, key("enter")) // inGitDB
	if m.connect.step != connectForm || m.connect.field != 0 {
		t.Fatalf("step %s field %d", m.connect.step, m.connect.field)
	}
	m = typeText(t, m, "/home/me/my-notes")
	if m.connect.name != "my-notes" {
		t.Errorf("name = %q", m.connect.name)
	}
	m = typeText(t, m, ".d")
	if m.connect.name != "my-notes" {
		t.Errorf("name with an extension = %q", m.connect.name)
	}
	m = send(t, m, key("tab"))
	m = typeText(t, m, "2")
	m = send(t, m, key("shift+tab"))
	m = typeText(t, m, "x")
	if m.connect.name != "my-notes2" {
		t.Errorf("an edited name followed the location: %q", m.connect.name)
	}
}

// Create's manifest steps end in Connect with a manifest file.
func TestCreateManifestStepsLeadToConnect(t *testing.T) {
	t.Parallel()
	m := openCreate(t, testModel(t, 80, 24))
	m = typeText(t, m, "mysql")
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	if m.screen != ScreenConnect || m.connect.step != connectManifest || m.connect.chosen.ID != setup.EngineMySQL {
		t.Errorf("screen %s step %s chosen %q", m.screen, m.connect.step, m.connect.chosen.ID)
	}
}

// AC:result-next-actions and AC:connect-leaves-folder-untouched through the
// TUI against a real server the connect starts; a missing folder is a
// Problem whose remedy returns to the folder field.
func TestConnectThroughTheServer(t *testing.T) {
	m := realModel(t, freePort(t))
	folder := filepath.Join(t.TempDir(), "journal")
	if err := os.MkdirAll(filepath.Join(folder, setup.InGitDBDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "keep.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	m = openConnect(t, m)
	m = send(t, m, key("enter")) // inGitDB
	m = typeText(t, m, filepath.Join(folder, "missing"))
	m = send(t, m, key("enter")) // to Name
	m = send(t, m, key("enter")) // connect
	if m.screen != ScreenProblem || m.problem.err.Code != envelope.StorageUnavailable {
		t.Fatalf("missing folder: screen %s problem %+v", m.screen, m.problem.err)
	}
	m = send(t, m, key("enter")) // Choose another location
	if m.screen != ScreenConnect || m.connect.step != connectForm || m.connect.field != 0 {
		t.Fatalf("remedy: screen %s step %s field %d", m.screen, m.connect.step, m.connect.field)
	}
	for range len("missing") + 1 {
		m = send(t, m, key("backspace"))
	}
	if m.connect.name != "journal" {
		t.Fatalf("name = %q", m.connect.name)
	}
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	if m.screen != ScreenResult {
		t.Fatalf("screen = %s, problem %+v", m.screen, m.problem.err)
	}
	view := flat(m.View().Content)
	for _, want := range []string{"Connected database journal", "Your data stays where it is: " + folder, "What next?", "Browse data", "ovdb list / --db journal", "b browse"} {
		if !strings.Contains(view, want) {
			t.Errorf("result lacks %q:\n%s", want, view)
		}
	}
	entries, _ := os.ReadDir(folder)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{setup.InGitDBDir, "keep.txt"}) {
		t.Errorf("connect wrote into the folder: %v", names)
	}
	m = send(t, m, key("b"))
	if m.screen != ScreenBrowse || m.browse.db != "journal" {
		t.Errorf("b: screen %s database %q", m.screen, m.browse.db)
	}
}

// Sizes for the Connect screens (first-run-onboarding#REQ:tui-keyboard-and-size).
func TestConnectScreenSizes(t *testing.T) {
	postgres, _ := setup.FindEngine(setup.EnginePostgres)
	sqlite, _ := setup.FindEngine(setup.EngineSQLite)
	long := "/home/someone/with/a/rather/long/path/that/goes/on/and/on/ovdb/shop.sqlite"
	states := map[string]connectScreen{
		"choose":          {step: connectChoose},
		"choose-manifest": {step: connectChoose, cursor: 5},
		"form":            {step: connectForm, chosen: sqlite, location: long, name: "shop", field: 1},
		"manifest":        {step: connectManifest, chosen: postgres, manifest: long},
		"manifest-any":    {step: connectManifest, manifest: long},
	}
	for _, size := range []struct{ w, h int }{{80, 24}, {60, 20}, {120, 40}} {
		for name, state := range states {
			t.Run(screenSizeName(name, size.w, size.h), func(t *testing.T) {
				t.Parallel()
				m := testModel(t, size.w, size.h)
				m.screen = ScreenConnect
				state.loaded, state.engines = true, setup.Engines()
				m.connect = state
				content := m.View().Content
				noWiderThan(t, content, size.w)
				if lines := strings.Count(content, "\n") + 1; lines > size.h {
					t.Errorf("%d lines, want <= %d:\n%s", lines, size.h, stripANSI(content))
				}
			})
		}
	}
}

// F6: a connected SQLite file's Result says the whole file is readable.
func TestConnectedSQLiteResultIsHonest(t *testing.T) {
	t.Parallel()
	result := newConnectedResult(setup.DatabaseResult{Database: setup.Database{ID: "shop", Engine: setup.EngineSQLite, Location: "/data/shop.sqlite"}})
	if text := strings.Join(result.lines, " "); !strings.Contains(text, "The whole file is readable through its tables") {
		t.Errorf("lines = %q", result.lines)
	}
}
