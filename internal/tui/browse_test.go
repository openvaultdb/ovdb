package tui

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
)

func put(t *testing.T, local *client.Local, db, path, data string) {
	t.Helper()
	p, err := datapath.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	request := client.DataRequest{Op: client.DataOp{Database: db, Path: p}, Method: http.MethodPut, URLPath: client.RecordURL(db, p),
		Body: map[string]any{"data": map[string]any{"title": data}}}
	if _, err := local.Data(context.Background(), request, false); err != nil {
		t.Fatalf("put %s: %v", path, err)
	}
}

// visible is the rendered screen as plain text.
func visible(m Model) string { return stripANSI(m.View().Content) }

// AC:browse-own-data and AC:tui-and-web-select-database in the TUI, with
// paging (REQ:browse-data-read-only) and Use in this project, against a
// real server.
func TestBrowseDataAndUseInThisProject(t *testing.T) {
	m := realModel(t, freePort(t))
	project := dbcontext.Canonical(t.TempDir())
	m.local.Where = dbcontext.Request{Dirs: []string{project}, Root: project}
	ctx := context.Background()
	if _, err := m.local.CreateDatabase(ctx, setup.CreateRequest{ID: "notes"}, false); err != nil {
		t.Fatal(err)
	}
	put(t, m.local, "notes", "/items/x", "<script>alert(1)</script>")
	for i := range 119 {
		put(t, m.local, "notes", fmt.Sprintf("/items/r%03d", i), fmt.Sprintf("record %d", i))
	}

	// Home again, now that a database exists.
	m = New(ctx, m.local, nil, 80, 24)
	m = drain(t, m, m.Init())
	for m.home.document.Options[m.home.cursor].ID != "browse" {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	if view := visible(m); m.screen != ScreenBrowse || !strings.Contains(view, "> notes") {
		t.Fatalf("browse databases (%s):\n%s", m.screen, view)
	}
	m = send(t, m, key("enter"))
	if view := visible(m); !strings.Contains(view, "notes:/") || !strings.Contains(view, "ovdb list / --db notes") || !strings.Contains(view, "> items") {
		t.Fatalf("collections:\n%s", view)
	}
	m = send(t, m, key("enter"))
	view := visible(m)
	if len(m.browse.items) != browsePage || !m.browse.more || !strings.Contains(view, "Records 1–50") || !strings.Contains(view, "n next page") {
		t.Fatalf("page 1 (%d items, more %v):\n%s", len(m.browse.items), m.browse.more, view)
	}
	if !strings.Contains(view, "ovdb list /items --db notes") {
		t.Errorf("page 1 lacks the command:\n%s", view)
	}
	m = send(t, m, key("n"))
	m = send(t, m, key("n"))
	if view := visible(m); len(m.browse.items) != 20 || m.browse.more || !strings.Contains(view, "Records 101–120") || strings.Contains(view, "n next page") {
		t.Fatalf("page 3 (%d items):\n%s", len(m.browse.items), view)
	}
	m = send(t, m, key("n")) // no page 4
	m = send(t, m, key("p"))
	m = send(t, m, key("p"))
	if view := visible(m); m.browse.page != 0 || !strings.Contains(view, "Records 1–50") {
		t.Fatalf("back to page 1:\n%s", view)
	}
	m = send(t, m, key("n"))
	m = send(t, m, key("n"))
	// x sorts last, on page 3.
	for i := 0; i < len(m.browse.items) && m.browse.items[m.browse.cursor].path.Name() != "x"; i++ {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	view = visible(m)
	for _, want := range []string{"notes:/items/x", "ovdb get /items/x --db notes", `"title": "<script>alert(1)</script>"`} {
		if !strings.Contains(view, want) {
			t.Errorf("record lacks %q:\n%s", want, view)
		}
	}
	// Read-only: no key edits, and typing c opens a nested collection instead.
	for _, k := range []string{"e", "d", "delete"} {
		if m = send(t, m, key(k)); m.screen != ScreenBrowse || m.browse.view != browseRecord {
			t.Fatalf("%s changed the screen: %s %s", k, m.screen, m.browse.view)
		}
	}
	m = send(t, m, key("c"))
	m = typeText(t, m, "notes")
	m = send(t, m, key("enter"))
	if view := visible(m); m.browse.path.String() != "/items/x/notes" || !strings.Contains(view, "Nothing here yet") {
		t.Fatalf("nested collection %s:\n%s", m.browse.path, view)
	}
	for range 3 {
		m = send(t, m, key("esc"))
	}
	if m.browse.path.Kind() != datapath.Root {
		t.Fatalf("esc up: %s", m.browse.path)
	}
	m = send(t, m, key("esc"))
	m = send(t, m, key("esc"))
	if m.screen != ScreenHome {
		t.Fatalf("esc to Home: %s", m.screen)
	}

	// Use in this project from Databases, then Home names it.
	for m.home.document.Options[m.home.cursor].ID != "databases" {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	m = send(t, m, key("down"))
	m = send(t, m, key("enter")) // Use it in this project
	if m.screen != ScreenResult || m.result.title != "Now using notes for this project ("+project+")" {
		t.Fatalf("use: %s %+v %+v", m.screen, m.result, m.problem.err)
	}
	m = send(t, m, key("enter"))
	if view := visible(m); !strings.Contains(flat(view), "1 database · using notes (this project) · OVDB server running at") {
		t.Errorf("Home summary:\n%s", view)
	}
	got := dbcontext.Resolve(m.local.Dirs.Home, []string{"notes"}, dbcontext.Request{Dirs: []string{filepath.Join(project)}}).Context
	if got == nil || got.Scope != dbcontext.ScopeProject || got.Dir != project {
		t.Errorf("stored context = %+v", got)
	}
}

// Browse fits every supported size (first-run-onboarding#REQ:tui-keyboard-and-size).
func TestBrowseScreenSizes(t *testing.T) {
	var items []browseItem
	collection, _ := datapath.Parse("/lists/to-buy/items")
	for i := range browsePage {
		items = append(items, browseItem{path: collection.Child(fmt.Sprintf("id-%02d-with-a-longer-name", i)),
			detail: `{"done":false,"note":"a long note that keeps going well past the edge of any terminal window","title":"Tea"}`})
	}
	lines := strings.Split(prettyJSON(map[string]any{"title": strings.Repeat("long ", 40), "tags": []any{"a", "b"}, "n": 1}), "\n")
	states := map[string]browseScreen{
		"list":     {view: browseList, loaded: true, db: "todo", path: collection, items: items, page: 1, more: true, cursor: 30},
		"record":   {view: browseRecord, loaded: true, db: "todo", path: items[0].path, record: append(lines, lines...), typing: true, input: "notes"},
		"choose":   {view: browseDatabases, loaded: true, databases: []setup.Database{{ID: "todo"}, {ID: "notes"}}, currentDB: "todo"},
		"empty":    {view: browseList, loaded: true, db: "todo", path: collection},
		"top-last": {view: browseList, loaded: true, db: "todo", path: collection, items: items, cursor: browsePage - 1},
	}
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {60, 20}} {
		for name, state := range states {
			t.Run(screenSizeName(name, size.w, size.h), func(t *testing.T) {
				t.Parallel()
				m := testModel(t, size.w, size.h)
				m.screen, m.browse = ScreenBrowse, state
				content := m.View().Content
				noWiderThan(t, content, size.w)
				if lines := strings.Count(content, "\n") + 1; lines > size.h {
					t.Errorf("%d lines, want <= %d:\n%s", lines, size.h, stripANSI(content))
				}
				if state.view == browseList && len(state.items) > 0 {
					if want := "> " + itemLabel(state.items[state.cursor].path); !strings.Contains(stripANSI(content), want) {
						t.Errorf("selected %q not visible:\n%s", want, stripANSI(content))
					}
				}
			})
		}
	}
}
