package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
)

// in makes dir (created, canonical) the working directory of later commands.
func (e *env) in(dir string) string {
	e.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	dir = dbcontext.Canonical(dir)
	e.app.Getwd = func() (string, error) { return dir, nil }
	return dir
}

func (e *env) ok(args ...string) result {
	e.t.Helper()
	r := e.run(args...)
	if r.code != 0 {
		e.t.Fatalf("ovdb %s: exit %d\nstdout %s\nstderr %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func (e *env) fails(args ...string) result {
	e.t.Helper()
	r := e.run(args...)
	if r.code != 1 {
		e.t.Fatalf("ovdb %s: exit %d, want 1\nstdout %s\nstderr %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

// dataEnv has databases todo and notes (inGitDB) and two Git projects.
func dataEnv(t *testing.T) (e *env, a, b string) {
	e = previewEnv(t)
	base := t.TempDir()
	a, b = filepath.Join(base, "p", "a"), filepath.Join(base, "p", "b")
	for _, dir := range []string{filepath.Join(a, ".git"), filepath.Join(b, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.in(base)
	e.ok("databases", "create", "todo")
	e.ok("databases", "create", "notes")
	return e, dbcontext.Canonical(a), dbcontext.Canonical(b)
}

// AC:use-is-project-scoped, AC:precedence-ladder, AC:no-context-error,
// AC:cd-examples and REQ:database-named-in-output through the CLI.
func TestUseCdPwdThroughTheServer(t *testing.T) {
	e, a, b := dataEnv(t)

	// No context with two databases: not_found listing both.
	e.in(a)
	failure := e.fails("list")
	for _, want := range []string{"No database chosen", "Registered databases: notes, todo.", "ovdb use <database>"} {
		if !strings.Contains(failure.stderr, want) {
			t.Errorf("no context lacks %q:\n%s", want, failure.stderr)
		}
	}
	_ = decodeError(t, e.run("list", "--json"), envelope.NotFound)

	e.in(filepath.Join(a, "src"))
	if r := e.ok("use", "todo"); r.stdout != "Now using todo for this project ("+a+")\n" {
		t.Errorf("use todo = %q", r.stdout)
	}
	e.in(b)
	e.ok("use", "notes")
	e.in(a)
	if r := e.ok("pwd"); r.stdout != "todo:/ (project context from "+a+")\n" {
		t.Errorf("pwd in /p/a = %q", r.stdout)
	}
	pwd := e.ok("pwd", "--json")
	var document dbcontext.Document
	if err := json.Unmarshal([]byte(pwd.stdout), &document); err != nil || document.Context == nil ||
		*document.Context != (dbcontext.Context{Database: "todo", Path: "/", Scope: dbcontext.ScopeProject, Dir: a}) {
		t.Errorf("pwd --json = %s (%v)", pwd.stdout, err)
	}
	if r := e.fails("use", "shop"); !strings.Contains(r.stderr, "No database named shop is registered. Registered databases: notes, todo.") {
		t.Errorf("use shop:\n%s", r.stderr)
	}

	// The ladder: OVDB_DATABASE over the project, --db over both.
	e.vars[dbcontext.EnvDatabase] = "notes"
	if r := e.ok("pwd"); r.stdout != "notes:/ (from OVDB_DATABASE)\n" {
		t.Errorf("pwd with OVDB_DATABASE = %q", r.stdout)
	}
	if r := e.ok("pwd", "--db", "todo"); r.stdout != "todo:/ (from --db)\n" {
		t.Errorf("pwd --db todo = %q", r.stdout)
	}
	delete(e.vars, dbcontext.EnvDatabase)
	e.vars[dbcontext.EnvPath] = "/lists"
	if r := e.ok("pwd"); !strings.Contains(r.stderr, "Ignoring OVDB_PATH") || r.stdout != "todo:/ (project context from "+a+")\n" {
		t.Errorf("OVDB_PATH alone = %+v", r)
	}
	delete(e.vars, dbcontext.EnvPath)

	// cd from todo:/lists/to-buy.
	e.ok("set", "/lists/to-buy/items/tea", `{"title":"Tea"}`)
	e.ok("cd", "/lists/to-buy")
	for _, tc := range []struct{ arg, want string }{
		{"items", "/lists/to-buy/items"}, {"..", "/lists"}, {"to-watch", "/lists/to-watch"}, {"../../../..", "/"},
	} {
		if r := e.ok("cd", tc.arg); !strings.HasPrefix(r.stdout, "todo:"+tc.want+" (project context from "+a+")\n") {
			t.Errorf("cd %s = %q", tc.arg, r.stdout)
		}
		if tc.arg == "items" {
			e.ok("cd", "/lists/to-buy/items/..")
			e.ok("cd", "/lists/to-buy")
		}
	}
	if r := e.ok("cd", "/lists/new-list/items"); !strings.HasSuffix(r.stdout, "\nNothing here yet\n") {
		t.Errorf("cd into a new collection = %q", r.stdout)
	}
	if r := e.ok("cd", "/lists/to-buy/items"); strings.Contains(r.stdout, "Nothing here yet") {
		t.Errorf("cd into items = %q", r.stdout)
	}
	// Relative paths follow the context.
	if r := e.ok("get", "tea"); !strings.HasPrefix(r.stdout, "todo:/lists/to-buy/items/tea\n") {
		t.Errorf("get tea = %q", r.stdout)
	}
	if r := e.ok("use", "notes"); r.stdout != "Now using notes for this project ("+a+")\n" {
		t.Errorf("use notes = %q", r.stdout)
	}
	if r := e.ok("pwd"); r.stdout != "notes:/ (project context from "+a+")\n" {
		t.Errorf("use resets the path: %q", r.stdout)
	}

	// Global default and removal clearing contexts.
	e.in(filepath.Dir(a))
	e.ok("use", "--global", "todo")
	if r := e.ok("pwd"); r.stdout != "todo:/ (global default)\n" {
		t.Errorf("global = %q", r.stdout)
	}
	e.ok("databases", "remove", "todo", "--yes")
	if r := e.ok("pwd"); r.stdout != "notes:/ (the only registered database)\n" {
		t.Errorf("after removing todo = %q", r.stdout)
	}
	if r := e.ok("delete", "/nothing/here"); r.stdout != "notes: deleted /nothing/here\n" {
		t.Errorf("only database is named: %q", r.stdout)
	}
	if r := e.fails("cd", "items", "--db", "notes"); !strings.Contains(r.stderr, "unknown flag") {
		t.Errorf("cd --db = %+v", r)
	}
}

// AC:add-set-get-delete, AC:kind-mismatch-hint, AC:list-kinds and
// AC:escaped-ids-round-trip: stdout carries only results, notices go to
// stderr, and --json bodies are /v1's.
func TestDataCommandsThroughTheServer(t *testing.T) {
	e, a, _ := dataEnv(t)
	e.in(a)

	add := e.ok("add", "/lists/to-buy/items", `{"title":"Tea","done":false}`, "--db", "todo")
	match := regexp.MustCompile(`^todo: added (/lists/to-buy/items/[a-z0-9]{6})\n$`).FindStringSubmatch(add.stdout)
	if match == nil {
		t.Fatalf("add = %q", add.stdout)
	}
	p := match[1]
	addJSON := e.ok("add", "/lists/to-buy/items", `{"title":"Milk"}`, "--db", "todo", "--json")
	if !regexp.MustCompile(`^\{"key":"/lists/to-buy/items/[a-z0-9]{6}"\}\n$`).MatchString(addJSON.stdout) || addJSON.stderr != "" {
		t.Errorf("add --json = %+v", addJSON)
	}
	e.ok("add", "/lists/to-buy/items", `{"title":"Bread"}`, "--db", "todo", "--id", "bread")
	if r := e.ok("set", p, "--field", "done=true", "--db", "todo", "--json"); r.stdout != `{"key":"`+p+`"}`+"\n" {
		t.Errorf("set --json = %q", r.stdout)
	}
	get := e.ok("get", p, "--db", "todo", "--json")
	if want := e.api(http.MethodGet, "/v1/databases/todo/records"+p); get.stdout != want || !strings.Contains(get.stdout, `"done":true`) {
		t.Errorf("get --json = %s, want the /v1 body %s", get.stdout, want)
	}
	if r := e.ok("delete", p, "--db", "todo", "--json"); r.stdout != `{"key":"`+p+`"}`+"\n" {
		t.Errorf("delete --json = %q", r.stdout)
	}
	gone := e.fails("get", p, "--db", "todo")
	if !strings.Contains(gone.stderr, "Couldn't read todo:"+p) || gone.stdout != "" {
		t.Errorf("get after delete = %+v", gone)
	}
	goneJSON := e.fails("get", p, "--db", "todo", "--json")
	if want := e.api(http.MethodGet, "/v1/databases/todo/records"+p); goneJSON.stdout != want || !strings.HasPrefix(want, `{"error":{"code":"not_found"`) {
		t.Errorf("get --json after delete = %s, want %s", goneJSON.stdout, want)
	}

	// Kind mismatch.
	mismatch := e.fails("get", "/lists/to-buy/items", "--db", "todo")
	if !strings.Contains(mismatch.stderr, "/lists/to-buy/items is a collection") || !strings.Contains(mismatch.stderr, "ovdb list /lists/to-buy/items --db todo") {
		t.Errorf("kind mismatch:\n%s", mismatch.stderr)
	}
	_ = decodeError(t, e.run("get", "/lists/to-buy/items", "--db", "todo", "--json"), envelope.InvalidArgument)
	_ = decodeError(t, e.run("add", "/lists/to-buy", "{}", "--db", "todo", "--json"), envelope.InvalidArgument)

	// List kinds.
	e.ok("set", "/lists/to-buy", `{"title":"To buy"}`, "--db", "todo")
	e.ok("set", "/lists/to-watch", `{"title":"To watch"}`, "--db", "todo")
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/", []string{"todo:/\n", "  /lists\n"}},
		{"/lists", []string{"  /lists/to-buy  {\"title\":\"To buy\"}", "  /lists/to-watch  {\"title\":\"To watch\"}"}},
		{"/lists/to-buy", []string{"\"title\": \"To buy\"", "Listing collections inside a record isn't supported yet"}},
		{"/lists/to-buy/items", []string{"  /lists/to-buy/items/bread  {\"title\":\"Bread\"}"}},
		{"/empty", []string{"todo:/empty\nNothing here yet\n"}},
	} {
		r := e.ok("ls", tc.path, "--db", "todo")
		for _, want := range tc.want {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("list %s lacks %q:\n%s", tc.path, want, r.stdout)
			}
		}
	}
	items := e.ok("list", "/lists/to-buy/items", "--db", "todo", "--json")
	if !strings.HasPrefix(items.stdout, `{"records":[`) || strings.Count(items.stdout, `"key"`) != 2 {
		t.Errorf("list --json = %s", items.stdout)
	}
	if r := e.ok("list", "/lists/to-buy/items", "--db", "todo", "--limit", "1"); !strings.Contains(r.stdout, "… more records; show more with --limit 2") {
		t.Errorf("--limit 1 = %s", r.stdout)
	}

	// Escaped ids: the server id is unescaped, output shows the escaped form.
	e.ok("set", "/files/a%2Eb%24c", `{"n":1}`, "--db", "notes")
	if got := e.api(http.MethodGet, "/v1/databases/notes/records/files/a%2Eb%24c"); !strings.Contains(got, `"n":1`) {
		t.Errorf("record = %s", got)
	}
	if r := e.ok("list", "/files", "--db", "notes"); !strings.Contains(r.stdout, "  /files/a%2Eb%24c  {\"n\":1}") {
		t.Errorf("escaped listing = %s", r.stdout)
	}
	e.ok("set", "/files/a%2Fb%2Etxt", `{"n":2}`, "--db", "notes")
	if r := e.ok("get", "/files/a%2Fb%2Etxt", "--db", "notes"); !strings.HasPrefix(r.stdout, "notes:/files/a%2Fb%2Etxt\n") {
		t.Errorf("get escaped = %s", r.stdout)
	}
	_ = decodeError(t, e.run("get", "/files/50%off", "--db", "notes", "--json"), envelope.InvalidArgument)

	// Bad input never reaches the server.
	_ = decodeError(t, e.run("set", "/x/y", "[1]", "--db", "notes", "--json"), envelope.InvalidArgument)
	_ = decodeError(t, e.run("set", "/x/y", "--field", "nope", "--db", "notes", "--json"), envelope.InvalidArgument)
}

// AC:strict-mode-error: a SQLite database without schemas.
func TestStrictModeError(t *testing.T) {
	e := previewEnv(t)
	e.in(t.TempDir())
	e.ok("databases", "create", "shop", "--engine", "sqlite")
	body := e.fails("add", "/orders", `{"total":1}`, "--db", "shop", "--json")
	if !strings.HasPrefix(body.stdout, `{"error":{"code":"schema_validation","message":"collection \"orders\": no schema declared`) {
		t.Errorf("--json = %s", body.stdout)
	}
	human := e.fails("add", "/orders", `{"total":1}`, "--db", "shop")
	for _, want := range []string{"Couldn't add shop:/orders/", "Describe the orders collection in the manifest of shop", "ovdb databases reload shop"} {
		if !strings.Contains(human.stderr, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stderr)
		}
	}
}

// AC:data-commands-auto-start and AC:sandboxed-start-fails-clearly: `ovdb
// list` starts the server with the notice on stderr, and a start that dies
// fails with server_start_failed and the sandbox guidance.
func TestListAutoStarts(t *testing.T) {
	e := previewEnv(t)
	e.in(t.TempDir())
	e.ok("databases", "create", "todo")
	e.ok("set", "/lists/to-buy", `{"title":"To buy"}`, "--db", "todo")
	e.ok("server", "stop")

	_ = decodeError(t, e.run("list", "/lists", "--db", "todo", "--no-start", "--json"), envelope.ServerNotRunning)
	r := e.ok("list", "/lists", "--db", "todo")
	if !strings.Contains(r.stderr, "Started the OVDB server at") || strings.Contains(r.stdout, "Started") || !strings.Contains(r.stdout, "/lists/to-buy") {
		t.Errorf("auto-start = %+v", r)
	}
	e.ok("server", "stop")

	e.app.ChildEnv = append(e.app.ChildEnv, cli.EnvStartFault+"=1")
	failure := e.fails("list", "/", "--db", "todo")
	if !strings.Contains(failure.stderr, "Couldn't start the OVDB server") || !strings.Contains(failure.stderr, "If you are an AI agent in a sandbox") {
		t.Errorf("start fault:\n%s", failure.stderr)
	}
	_ = decodeError(t, e.run("list", "/", "--db", "todo", "--json"), envelope.ServerStartFailed)
}

// AC:returning-user-summary for non-interactive `ovdb` (`ovdb status`).
func TestStatusSummary(t *testing.T) {
	e, a, _ := dataEnv(t)
	e.in(a)
	e.ok("use", "todo")
	status := func(jsonOut bool) string {
		root := &cobra.Command{Use: "ovdb", SilenceUsage: true, SilenceErrors: true}
		root.SetContext(context.Background())
		var out bytes.Buffer
		root.SetOut(&out)
		if err := e.app.Status(root, jsonOut); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	r := result{stdout: status(false)}
	if !regexp.MustCompile(`(?m)^2 databases · using todo \(this project\) · OVDB server running at http://ovdb\.localhost:\d+$`).MatchString(r.stdout) {
		t.Errorf("status:\n%s", r.stdout)
	}
	if got := status(true); !strings.Contains(got, `"context":{"database":"todo","path":"/","scope":"project","dir":`) {
		t.Errorf("status --json = %s", got)
	}
}
