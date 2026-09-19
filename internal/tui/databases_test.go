package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

func typeText(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		k := string(r)
		if r == ' ' {
			k = "space"
		}
		m = send(t, m, key(k))
	}
	return m
}

// flat collapses whitespace, so wrapped screen text compares as one line.
func flat(s string) string { return strings.Join(strings.Fields(stripANSI(s)), " ") }

// containsAcrossVisualWraps compares rendered text while ignoring whitespace
// inserted by the terminal renderer. It keeps every non-whitespace character,
// so long paths and their surrounding copy must still be complete and ordered.
func containsAcrossVisualWraps(rendered, want string) bool {
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, stripANSI(s))
	}
	return strings.Contains(compact(rendered), compact(want))
}

func visibleIDs(m Model) []string {
	var ids []string
	for _, engine := range m.create.visible() {
		ids = append(ids, engine.ID)
	}
	return ids
}

// openCreate goes from Home to Create a database, after Try a demo.
func openCreate(t *testing.T, m Model) Model {
	t.Helper()
	for m.home.document.Options[m.home.cursor].ID != "create" {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	if m.screen != ScreenCreate || !m.create.loaded {
		t.Fatalf("screen = %q loaded %v, want create", m.screen, m.create.loaded)
	}
	return m
}

// AC:pinned-then-alphabetical in the TUI.
func TestCreatePickerOrderAndFilter(t *testing.T) {
	t.Parallel()
	m := openCreate(t, testModel(t, 80, 24))
	if ids := visibleIDs(m); !slices.Equal(ids, []string{"ingitdb", "sqlite", "firestore", "mysql", "postgres"}) {
		t.Errorf("order = %v", ids)
	}
	view := stripANSI(m.viewCreate())
	for _, want := range []string{"Where should OVDB keep your data?", "Filter: _", "inGitDB", "Readable files in a folder", "─"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker lacks %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "SQLite") > strings.Index(view, "─") || strings.Index(view, "─") > strings.Index(view, "Firestore") {
		t.Errorf("pinned engines are not above the divider:\n%s", view)
	}

	m = typeText(t, m, "sql")
	if ids := visibleIDs(m); !slices.Equal(ids, []string{"sqlite", "mysql", "postgres"}) {
		t.Errorf("filter sql = %v", ids)
	}
	// "?", "q" and "j" are filter text here, not keys.
	m = typeText(t, m, "q?")
	if m.showHelp || m.screen != ScreenCreate || m.create.filter != "sqlq?" {
		t.Errorf("typing = filter %q help %v screen %s", m.create.filter, m.showHelp, m.screen)
	}
	if view := stripANSI(m.viewCreate()); !strings.Contains(view, `Nothing matches "sqlq?".`) {
		t.Errorf("no-match view:\n%s", view)
	}
	for range 5 {
		m = send(t, m, key("backspace"))
	}
	m = send(t, m, key("backspace")) // an empty filter goes back
	if m.screen != ScreenHome {
		t.Errorf("backspace on an empty filter: screen %s", m.screen)
	}
}

// AC:postgres-is-manifest-only in the TUI.
func TestCreatePostgresShowsManifestSteps(t *testing.T) {
	t.Parallel()
	m := openCreate(t, testModel(t, 80, 24))
	m = typeText(t, m, "postgres")
	m = send(t, m, key("enter"))
	if m.create.step != createManifest {
		t.Fatalf("step = %s", m.create.step)
	}
	view := stripANSI(m.View().Content)
	for _, want := range []string{"Set this up with a manifest file", "ovdb init --engine postgres --id <name>",
		"ovdb databases connect --manifest <absolute path>", setup.ManifestDocsURL} {
		if !strings.Contains(view, want) {
			t.Errorf("manifest view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Name:") || strings.Contains(view, "Location:") {
		t.Errorf("manifest view collects details:\n%s", view)
	}
	m = send(t, m, key("esc"))
	if m.create.step != createChoose {
		t.Errorf("esc: step %s", m.create.step)
	}
}

func TestCreateFormLocationFollowsName(t *testing.T) {
	t.Parallel()
	m := openCreate(t, testModel(t, 80, 24))
	m = typeText(t, m, "sql")
	m = send(t, m, key("enter")) // SQLite
	m = typeText(t, m, "shop")
	if want := filepath.Join(m.local.Dirs.Data, "shop.sqlite"); m.create.location != want {
		t.Errorf("location = %q, want %q", m.create.location, want)
	}
	m = send(t, m, key("tab"))
	m = typeText(t, m, "x")
	m = send(t, m, key("shift+tab"))
	m = typeText(t, m, "2")
	if !strings.HasSuffix(m.create.location, "shop.sqlitex") {
		t.Errorf("an edited location changed with the name: %q", m.create.location)
	}
}

// AC:create-ingitdb-default, AC:result-lists-next-actions and
// AC:remove-keeps-data through the TUI against a real server that the
// create starts.
func TestCreateThenRemoveThroughTheServer(t *testing.T) {
	m := realModel(t, freePort(t))
	m = openCreate(t, m)
	m = send(t, m, key("enter")) // inGitDB
	m = typeText(t, m, "notes")
	folder := filepath.Join(m.local.Dirs.Data, "notes")
	if m.create.location != folder {
		t.Fatalf("location = %q", m.create.location)
	}
	m = send(t, m, key("enter")) // to Location
	m = send(t, m, key("enter")) // create
	if m.screen != ScreenResult {
		t.Fatalf("screen = %s, problem %+v", m.screen, m.problem.err)
	}
	view := flat(m.View().Content)
	for _, want := range []string{"Created database notes", "What next?", "See your databases", "ovdb databases", "Started the OVDB server at"} {
		if !strings.Contains(view, want) {
			t.Errorf("result lacks %q:\n%s", want, view)
		}
	}
	if want := "Stored in " + folder + "/ as readable files with Git history."; !containsAcrossVisualWraps(m.View().Content, want) {
		t.Errorf("result lacks %q across visual wraps:\n%s", want, view)
	}
	manifest, err := os.ReadFile(setup.ManifestPath(m.local.Dirs.Home, "notes"))
	if err != nil || !strings.Contains(string(manifest), "schema_mode: schemaless") {
		t.Errorf("manifest = %s, %v", manifest, err)
	}

	// Creating it again is refused; the remedy returns to the form.
	m = send(t, m, key("enter")) // Home
	m = openCreate(t, m)
	m = send(t, m, key("enter"))
	m = typeText(t, m, "notes")
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	if m.screen != ScreenProblem || m.problem.err.Code != envelope.AlreadyExists {
		t.Fatalf("again: screen %s problem %+v", m.screen, m.problem.err)
	}
	m = send(t, m, key("enter")) // Use the name notes-2 instead → the form with notes-2
	if m.screen != ScreenCreate || m.create.step != createForm || m.create.field != 0 || m.create.name != "notes-2" ||
		m.create.location != filepath.Join(m.local.Dirs.Data, "notes-2") {
		t.Errorf("remedy: screen %s step %s field %d name %q location %q", m.screen, m.create.step, m.create.field, m.create.name, m.create.location)
	}

	// Databases: remove after confirming; the data stays.
	m = send(t, m, key("esc"))
	m = send(t, m, key("esc"))
	for m.home.document.Options[m.home.cursor].ID != "databases" && m.home.cursor < len(m.home.document.Options)-1 {
		m = send(t, m, key("down"))
	}
	if option := m.home.document.Options[m.home.cursor]; option.ID != "databases" {
		t.Fatalf("home option %d = %s", m.home.cursor, option.ID)
	}
	m = send(t, m, key("enter"))
	if m.screen != ScreenDatabases || len(m.databases.document.Databases) != 1 {
		t.Fatalf("databases: screen %s %+v", m.screen, m.databases.document)
	}
	if got := m.databases.document.Databases[0].Location; got != folder {
		t.Errorf("listed database location = %q, want %q", got, folder)
	}
	if view := flat(m.viewDatabases()); !strings.Contains(view, "notes [Ready]") ||
		!containsAcrossVisualWraps(view, filepath.Join("data", "notes")) {
		t.Errorf("list:\n%s", view)
	}
	m = send(t, m, key("enter")) // details, not straight to Remove
	if view := flat(m.viewDatabases()); m.databases.view != databasesDetails || !strings.Contains(view, "Manifest file:") ||
		!strings.Contains(view, "> Browse data Use it in this project Reload Remove Back") {
		t.Fatalf("details (%s):\n%s", m.databases.view, view)
	}
	m = send(t, m, key("down"))
	m = send(t, m, key("down"))
	m = send(t, m, key("enter")) // Reload
	if m.screen != ScreenResult || !strings.Contains(m.result.title, "Reloaded database notes") || m.result.lines[0] != "Ready" {
		t.Fatalf("reload: screen %s result %+v problem %+v", m.screen, m.result, m.problem.err)
	}
	m = send(t, m, key("d"))
	m = send(t, m, key("enter"))
	for range 3 {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter")) // Remove
	if m.databases.view != databasesConfirm || !m.databases.keep {
		t.Fatalf("confirm = %s keep %v", m.databases.view, m.databases.keep)
	}
	if view := flat(m.viewDatabases()); !strings.Contains(view, "Remove notes from OVDB?") ||
		!containsAcrossVisualWraps(view, "Its data stays where it is: "+folder) {
		t.Errorf("confirm view:\n%s", view)
	}
	m = send(t, m, key("down"))
	m = send(t, m, key("enter"))
	if m.screen != ScreenResult || !strings.Contains(m.result.title, "Removed database notes from OVDB") {
		t.Fatalf("remove: screen %s result %+v problem %+v", m.screen, m.result, m.problem.err)
	}
	if !containsAcrossVisualWraps(m.viewResult(), "Your data is still in "+folder) {
		t.Errorf("removed view:\n%s", m.viewResult())
	}
	if _, err := os.Stat(folder); err != nil {
		t.Errorf("data removed: %v", err)
	}
}

// Sizes for the new screens (first-run-onboarding#REQ:tui-keyboard-and-size).
func TestDatabasesScreenSizes(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {120, 40}, {60, 20}, {50, 15}}
	databases := setup.DatabasesDocument{Databases: []setup.Database{
		{ID: "notes", Engine: "ingitdb", Location: "/home/someone/with/a/rather/long/path/ovdb/notes", State: setup.MountMounted},
		{ID: "crm", Engine: "postgres", Location: "connection from $CRM_DSN", State: setup.MountNeedsAttention,
			Reason: "failed to open Postgres via $CRM_DSN: dalgo2postgres: PingContext(\"postgres://[redacted]@nohost/db\"): failed to connect"},
	}}
	many := setup.DatabasesDocument{}
	for i := range 12 {
		many.Databases = append(many.Databases, setup.Database{ID: "db" + itoa(i), Engine: "ingitdb", State: setup.MountNeedsAttention,
			Location: "/home/someone/with/a/rather/long/path/that/goes/on/and/on/ovdb/db" + itoa(i), Reason: "The data is missing."})
	}
	created := setup.DatabaseResult{
		Database: setup.Database{ID: "shop", Engine: "sqlite", Location: "/home/someone/ovdb/shop.sqlite"},
		Next:     setup.CreatedNext(setup.Database{ID: "shop", Engine: "sqlite", Manifest: "/home/someone/.config/ovdb/databases/shop.yaml"}),
	}
	states := []struct {
		name  string
		setup func(m Model) Model
	}{
		{"choose", func(m Model) Model { return m }},
		{"manifest", func(m Model) Model {
			m.create.chosen, _ = setup.FindEngine("postgres")
			m.create.step = createManifest
			return m
		}},
		{"form", func(m Model) Model {
			m.create.chosen, _ = setup.FindEngine("ingitdb")
			m.create.step = createForm
			m.create.name = "notes"
			m.create.location = "/home/someone/with/a/rather/long/path/that/goes/on/ovdb/notes"
			return m
		}},
		{"databases", func(m Model) Model {
			m.screen = ScreenDatabases
			m.databases = databasesScreen{loaded: true, document: databases}
			return m
		}},
		{"confirm", func(m Model) Model {
			m.screen = ScreenDatabases
			m.databases = databasesScreen{loaded: true, document: databases, view: databasesConfirm, keep: true}
			return m
		}},
		{"details", func(m Model) Model {
			m.screen = ScreenDatabases
			m.databases = databasesScreen{loaded: true, document: databases, cursor: 1, view: databasesDetails}
			return m
		}},
		{"many-top", func(m Model) Model {
			m.screen = ScreenDatabases
			m.databases = databasesScreen{loaded: true, document: many, view: databasesList}
			return m
		}},
		{"many-middle", func(m Model) Model {
			m.screen = ScreenDatabases
			m.databases = databasesScreen{loaded: true, document: many, cursor: 7, view: databasesList}
			return m
		}},
		{"many-create", func(m Model) Model {
			m.screen = ScreenDatabases
			m.databases = databasesScreen{loaded: true, document: many, cursor: len(many.Databases), view: databasesList}
			return m
		}},
		{"result", func(m Model) Model {
			m.screen = ScreenResult
			m.result = newCreatedResult(created)
			return m
		}},
	}
	for _, size := range sizes {
		for _, state := range states {
			t.Run(screenSizeName(state.name, size.w, size.h), func(t *testing.T) {
				t.Parallel()
				m := testModel(t, size.w, size.h)
				m.screen = ScreenCreate
				m.create = createScreen{loaded: true, step: createChoose, engines: setup.Engines()}
				m = state.setup(m)
				content := m.View().Content
				if size.w < minWidth || size.h < minHeight {
					if !strings.Contains(content, "Make the window a little bigger") {
						t.Errorf("%dx%d should show the too-small message:\n%s", size.w, size.h, content)
					}
					return
				}
				noWiderThan(t, content, size.w)
				if lines := strings.Count(content, "\n") + 1; lines > size.h {
					t.Errorf("%d lines, want <= %d:\n%s", lines, size.h, stripANSI(content))
				}
				// The selected row stays visible.
				if m.screen == ScreenDatabases && m.databases.view == databasesList {
					want := "Create a database"
					if m.databases.cursor < len(m.databases.document.Databases) {
						want = "> " + m.databases.document.Databases[m.databases.cursor].ID + " "
					}
					if !strings.Contains(stripANSI(content), want) {
						t.Errorf("selected row %q not visible:\n%s", want, stripANSI(content))
					}
				}
			})
		}
	}
}
