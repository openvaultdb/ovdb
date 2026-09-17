package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
)

func (e *env) listTitles(path string) map[string]bool {
	e.t.Helper()
	var body struct {
		Records []struct {
			Path string         `json:"path"`
			Data map[string]any `json:"data"`
		} `json:"records"`
	}
	r := e.ok("list", path, "--db", "todo", "--json")
	if err := json.Unmarshal([]byte(r.stdout), &body); err != nil {
		e.t.Fatalf("list %s --json = %s", path, r.stdout)
	}
	out := map[string]bool{}
	for _, rec := range body.Records {
		out[rec.Data["title"].(string)] = rec.Data["done"].(bool)
	}
	return out
}

// todo-demo AC:non-interactive-needs-yes: nothing is written and no server
// starts; the envelope names --yes.
func TestDemoInstallNeedsYesWithoutATerminal(t *testing.T) {
	e := previewEnv(t)
	e.app.IsTerminal = func(uintptr) bool { return false }
	failure := e.fails("demo", "install")
	for _, want := range []string{"Couldn't install the TODO demo", "ovdb demo install --yes"} {
		if !strings.Contains(failure.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, failure.stderr)
		}
	}
	if next := decodeError(t, e.run("demo", "install", "--json"), envelope.ConfirmationRequired).Next; len(next) != 1 || next[0].Command != "ovdb demo install --yes" {
		t.Errorf("next = %+v", next)
	}
	if next := decodeError(t, e.run("demo", "install", "--id", "todo-demo", "--json"), envelope.ConfirmationRequired).Next; next[0].Command != "ovdb demo install --id todo-demo --yes" {
		t.Errorf("next with --id = %+v", next)
	}
	if state, _ := runtime.Inspect(context.Background(), e.dirs.Runtime); state.Running {
		t.Error("a server started")
	}
	for _, dir := range []string{e.dirs.Data, filepath.Join(e.dirs.Home, "databases")} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s written: %v", dir, err)
		}
	}
}

// todo-demo AC:fresh-install-creates-data, AC:reinstall-keeps-changes and
// the status command, through the CLI and the server it starts.
func TestDemoInstallThroughTheServer(t *testing.T) {
	e := previewEnv(t)
	e.in(t.TempDir())
	location := filepath.Join(e.dirs.Data, "demos", "todo")

	status := e.ok("demo", "status")
	if !strings.Contains(status.stdout, "The TODO demo isn't installed. Its lists would be stored in "+location) {
		t.Errorf("status before = %q", status.stdout)
	}

	install := e.ok("demo", "install", "--yes")
	want := "The TODO demo is ready\n\nTwo lists, To buy and To watch, are stored as files in " + location + ".\n" +
		"Your apps and AI agents can use them through the OVDB server.\n\nWhat next?\n" +
		"  • Open TODO app   ovdb demo open\n  • Done\n"
	if install.stdout != want {
		t.Errorf("install =\n%s\nwant\n%s", install.stdout, want)
	}
	if !strings.Contains(install.stderr, "Started the OVDB server") {
		t.Errorf("no auto-start notice: %q", install.stderr)
	}
	databases := e.ok("databases", "--json")
	if !strings.Contains(databases.stdout, `"id":"todo","engine":"ingitdb","location":`+jsonString(location)) {
		t.Errorf("databases = %s", databases.stdout)
	}
	if got := e.listTitles("/lists/to-buy/items"); !maps(got, map[string]bool{"Milk": false, "Bananas": false, "Coffee": false}) {
		t.Errorf("to-buy = %v", got)
	}
	if got := e.listTitles("/lists/to-watch/items"); !maps(got, map[string]bool{"The Matrix": false, "Interstellar": false}) {
		t.Errorf("to-watch = %v", got)
	}

	e.ok("add", "/lists/to-buy/items", `{"title":"Tea","done":false}`, "--db", "todo")
	again := e.ok("demo", "install", "--yes")
	if !strings.HasPrefix(again.stdout, "The TODO demo is already installed\n") {
		t.Errorf("install again = %q", again.stdout)
	}
	if got := e.listTitles("/lists/to-buy/items"); !maps(got, map[string]bool{"Milk": false, "Bananas": false, "Coffee": false, "Tea": false}) {
		t.Errorf("reinstall changed the list: %v", got)
	}

	// --json equals the API.
	if got, want := e.ok("demo", "status", "--json").stdout, e.api(http.MethodGet, "/api/local/v1/demo"); got != want {
		t.Errorf("status --json\n got %s\nwant %s", got, want)
	}
	var document demo.Document
	if err := json.Unmarshal([]byte(e.ok("demo", "install", "--yes", "--json").stdout), &document); err != nil ||
		!document.AlreadyInstalled || document.Database != "todo" || document.Location != location {
		t.Errorf("install --json = %+v (%v)", document, err)
	}
	statusJSON := e.ok("demo", "status")
	if !strings.Contains(statusJSON.stdout, "The TODO demo is installed as todo in "+location) {
		t.Errorf("status after = %q", statusJSON.stdout)
	}
	if overall := e.api(http.MethodGet, "/api/local/v1/status"); !strings.Contains(overall, `"demo":{"installed":true,"database":"todo","location":`+jsonString(location)+`}`) ||
		strings.Contains(overall, "ovdb demo install") {
		t.Errorf("status document = %s", overall)
	}
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func maps(got, want map[string]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			return false
		}
	}
	return true
}

// todo-demo AC:conflicting-todo-refused.
func TestDemoInstallRefusesAnotherTodo(t *testing.T) {
	e := previewEnv(t)
	e.ok("databases", "create", "todo")
	before := e.ok("databases", "--json").stdout
	failure := e.fails("demo", "install", "--yes")
	for _, want := range []string{"Couldn't install the TODO demo", "A database named todo already exists at " + filepath.Join(e.dirs.Data, "todo"), "ovdb demo install --id todo-demo"} {
		if !strings.Contains(failure.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, failure.stderr)
		}
	}
	if after := e.ok("databases", "--json").stdout; after != before {
		t.Errorf("databases changed:\n%s\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(e.dirs.Data, "demos")); !os.IsNotExist(err) {
		t.Errorf("demos folder written: %v", err)
	}
}

// todo-demo AC:open-starts-server-and-app (the CLI half; the browser half is
// web/e2e/todo.spec.ts) and local-server-and-web-console AC:not-built-fallback.
func TestDemoOpen(t *testing.T) {
	e := previewEnv(t)
	built := false
	e.app.ConsoleBuilt = func() bool { return built }

	// Without the console built in, open says so; data commands still work.
	failure := e.fails("demo", "open")
	for _, want := range []string{"Couldn't open the TODO app", "built without its web console and TODO app", "brew install --cask openvaultdb/tap/ovdb", "https://github.com/openvaultdb/ovdb/releases"} {
		if !strings.Contains(failure.stderr, want) {
			t.Errorf("not built stderr lacks %q:\n%s", want, failure.stderr)
		}
	}
	if len(e.opened) != 0 {
		t.Errorf("opened %v", e.opened)
	}
	built = true

	// Not installed yet: said without starting a server (review F8).
	if next := decodeError(t, e.run("demo", "open", "--json"), envelope.NotFound).Next; len(next) != 1 || next[0].Command != "ovdb demo install --yes" {
		t.Errorf("not installed next = %+v", next)
	}
	if state, _ := runtime.Inspect(context.Background(), e.dirs.Runtime); state.Running {
		t.Error("demo open started a server for a demo that isn't installed")
	}

	e.ok("demo", "install", "--yes")
	e.ok("server", "stop")
	opened := e.ok("demo", "open", "--print-url")
	if len(e.opened) != 0 || !strings.Contains(opened.stdout, "Open the TODO app with this sign-in link") {
		t.Errorf("--print-url opened %v; stdout %q", e.opened, opened.stdout)
	}
	if state, _ := runtime.Inspect(context.Background(), e.dirs.Runtime); !state.Running {
		t.Error("demo open did not start the server")
	}
	lines := strings.Fields(opened.stdout)
	var links []string
	for _, field := range lines {
		if strings.HasPrefix(field, "http://") {
			links = append(links, field)
		}
	}
	if len(links) != 2 || !strings.HasPrefix(links[0], "http://ovdb.localhost:") || !strings.HasPrefix(links[1], "http://127.0.0.1:") {
		t.Fatalf("links = %v", links)
	}
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Path != "/login" || parsed.Query().Get("next") != "/apps/todo/" || parsed.Query().Get("code") == "" {
			t.Errorf("link %s", link)
		}
	}

	e.ok("demo", "open")
	if len(e.opened) != 1 || !strings.Contains(e.opened[0], "next=%2Fapps%2Ftodo%2F") {
		t.Errorf("opened %v", e.opened)
	}
	if !slices.ContainsFunc(strings.Split(e.ok("demo", "open", "--json").stdout, ","), func(s string) bool { return strings.Contains(s, `"fallback_url"`) }) {
		t.Error("--json lacks fallback_url")
	}
}

// Review F1 through the CLI: a user's database in a folder named demos is not
// the demo; status says so and install installs the lists.
func TestDemoIsNotAnyDatabaseInADemosFolder(t *testing.T) {
	e := previewEnv(t)
	notes := filepath.Join(t.TempDir(), "work", "demos", "notes")
	e.ok("databases", "create", "notes", "--path", notes)
	e.ok("add", "/customers", `{"name":"ACME"}`, "--db", "notes")
	if r := e.ok("demo", "status"); !strings.Contains(r.stdout, "The TODO demo isn't installed") {
		t.Errorf("status = %q", r.stdout)
	}
	if r := e.ok("demo", "install", "--yes"); !strings.HasPrefix(r.stdout, "The TODO demo is ready") {
		t.Errorf("install = %q", r.stdout)
	}
	if got := e.listTitles("/lists/to-buy/items"); len(got) != 3 {
		t.Errorf("to-buy = %v", got)
	}
	if r := e.ok("demo", "status"); !strings.Contains(r.stdout, "installed as todo in "+filepath.Join(e.dirs.Data, "demos", "todo")) {
		t.Errorf("status after = %q", r.stdout)
	}
}

// Review F9: in a terminal, an installed demo is reported without asking.
func TestDemoInstallDoesNotAskWhenInstalled(t *testing.T) {
	e := previewEnv(t)
	e.ok("demo", "install", "--yes")
	e.app.IsTerminal = func(uintptr) bool { return true }
	r := e.ok("demo", "install")
	if !strings.HasPrefix(r.stdout, "The TODO demo is already installed") || strings.Contains(r.stderr, "Install the TODO demo?") {
		t.Errorf("install in a terminal = %+v", r)
	}
}
