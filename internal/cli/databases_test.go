package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// previewEnv is newEnv with OVDB_PREVIEW=1 for this process and the server.
func previewEnv(t *testing.T) *env {
	t.Setenv(preview.EnvVar, "1")
	e := newEnv(t)
	e.app.ChildEnv = append(e.app.ChildEnv, preview.EnvVar+"=1")
	return e
}

// AC:auto-start-and-no-start, AC:cli-json-matches-api for the databases
// commands, AC:create-refuses-overwrite, AC:sqlite-points-to-schema and
// AC:result-lists-next-actions (CLI), through a real detached server.
func TestDatabasesThroughTheServer(t *testing.T) {
	e := previewEnv(t)

	// Pure reads start nothing.
	engines := e.run("engines", "--json")
	if engines.code != 0 || !strings.HasPrefix(engines.stdout, `{"schema":1,"engines":[{"id":"ingitdb"`) {
		t.Fatalf("engines = %+v", engines)
	}
	empty := e.run("databases", "--json")
	if empty.code != 0 || !strings.HasPrefix(empty.stdout, `{"schema":1,"databases":[],`) {
		t.Fatalf("databases = %+v", empty)
	}
	_ = decodeError(t, e.run("databases", "create", "notes", "--no-start", "--json"), envelope.ServerNotRunning)
	if _, err := os.Stat(filepath.Join(e.dirs.Runtime, "server.json")); !os.IsNotExist(err) {
		t.Fatalf("a read or --no-start started the server: %v", err)
	}

	// Create auto-starts with one line on stderr and prints the result.
	created := e.run("databases", "create", "notes")
	if created.code != 0 || !strings.Contains(created.stderr, "Started the OVDB server at http://ovdb.localhost:") ||
		strings.Contains(created.stderr, "code=") {
		t.Fatalf("create = %+v", created)
	}
	notes := filepath.Join(e.dirs.Data, "notes")
	for _, want := range []string{"Created database notes", "Stored in " + notes + string(filepath.Separator) + " as readable files with Git history.",
		"What next?", "See your databases", "ovdb databases", "Done"} {
		if !strings.Contains(created.stdout, want) {
			t.Errorf("create output lacks %q:\n%s", want, created.stdout)
		}
	}
	if info, err := os.Stat(notes); err != nil || !info.IsDir() {
		t.Errorf("data folder: %v", err)
	}

	// --json output equals the API body, success and failure alike.
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"engines", "--json"}, "/api/local/v1/engines"},
		{[]string{"databases", "--json"}, "/api/local/v1/databases"},
	} {
		if got, want := e.run(tc.args...), e.api(http.MethodGet, tc.path); got.code != 0 || got.stdout != want {
			t.Errorf("ovdb %v\n got %+v\nwant %s", tc.args, got, want)
		}
	}
	again := e.run("databases", "create", "notes", "--json")
	exists := decodeError(t, again, envelope.AlreadyExists)
	body, _ := json.Marshal(setup.CreateRequest{ID: "notes", Engine: "ingitdb", Path: notes})
	if want := e.apiBody(http.MethodPost, "/api/local/v1/databases", string(body)); again.stdout != want {
		t.Errorf("already_exists --json\n got %s\nwant %s", again.stdout, want)
	}
	if exists.Next[0].Command != "ovdb databases create notes-2" {
		t.Errorf("already_exists next = %+v", exists.Next)
	}

	// SQLite: the schema comes first, no record command.
	shop := e.run("databases", "create", "shop", "--engine", "sqlite", "--json")
	var result setup.DatabaseResult
	if err := json.Unmarshal([]byte(shop.stdout), &result); err != nil || shop.code != 0 {
		t.Fatalf("sqlite create = %+v", shop)
	}
	if result.Next[0].Command != "ovdb server restart" || !strings.Contains(result.Next[0].Label, "schema") {
		t.Errorf("sqlite next = %+v", result.Next)
	}
	if strings.Contains(shop.stdout, "ovdb add") || strings.Contains(shop.stdout, "ovdb set") {
		t.Errorf("sqlite result suggests a write: %s", shop.stdout)
	}

	// A folder with files is refused, and nothing is registered.
	diary := filepath.Join(e.dirs.Data, "diary")
	if err := os.MkdirAll(diary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diary, "mine.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := e.run("databases", "create", "diary")
	if refused.code != 1 || !strings.Contains(refused.stderr, "Couldn't create the database") ||
		!strings.Contains(refused.stderr, "already has files in it") || !strings.Contains(refused.stderr, "ovdb databases create diary-2") {
		t.Errorf("not empty = %+v", refused)
	}

	// PostgreSQL is set up with a manifest, refused before any server call.
	postgres := decodeError(t, e.run("databases", "create", "crm", "--engine", "postgres", "--json"), envelope.Unsupported)
	if postgres.Next[0].Command != "ovdb init --engine postgres --id <name>" {
		t.Errorf("postgres next = %+v", postgres.Next)
	}

	listed := e.run("databases")
	for _, want := range []string{"notes", "inGitDB", notes, "Ready", "shop", "SQLite"} {
		if !strings.Contains(listed.stdout, want) {
			t.Errorf("databases lacks %q:\n%s", want, listed.stdout)
		}
	}

	// Remove asks for confirmation off a terminal, then keeps the data.
	confirm := decodeError(t, e.run("databases", "remove", "notes", "--json"), envelope.ConfirmationRequired)
	if confirm.Next[0].Command != "ovdb databases remove notes --yes" {
		t.Errorf("confirmation next = %+v", confirm.Next)
	}
	removed := e.run("databases", "remove", "notes", "--yes")
	if removed.code != 0 || !strings.Contains(removed.stdout, "Removed database notes from OVDB") ||
		!strings.Contains(removed.stdout, "Your data is still in "+notes) {
		t.Errorf("remove = %+v", removed)
	}
	if _, err := os.Stat(notes); err != nil {
		t.Errorf("remove deleted the data: %v", err)
	}
	_ = decodeError(t, e.run("databases", "remove", "notes", "--yes", "--json"), envelope.NotFound)

	// Stopped: the list still works, without starting a server.
	if stop := e.run("server", "stop"); stop.code != 0 {
		t.Fatalf("stop = %+v", stop)
	}
	stopped := e.run("databases", "--json")
	if stopped.code != 0 || !strings.Contains(stopped.stdout, `"id":"shop"`) || !strings.Contains(stopped.stdout, `"state":"unknown"`) {
		t.Errorf("databases while stopped = %+v", stopped)
	}
	human := e.run("databases")
	if !strings.Contains(human.stdout, "Unknown (server not running)") || !strings.Contains(human.stdout, "ovdb server start") {
		t.Errorf("human list while stopped = %s", human.stdout)
	}
	if _, err := os.Stat(filepath.Join(e.dirs.Runtime, "server.json")); !os.IsNotExist(err) {
		t.Errorf("listing started the server: %v", err)
	}
	_ = decodeError(t, e.run("databases", "create", "other", "--no-start", "--json"), envelope.ServerNotRunning)
}

// AC:dsn-never-leaks and AC:tolerant-registry: an unreachable PostgreSQL
// manifest and a broken one never stop the valid database, and the
// connection string appears nowhere: status, --json, the API, mounts.json
// or server.log.
func TestConnectionStringNeverLeaks(t *testing.T) {
	const dsn = "postgres://u:s3cret@nohost.invalid/db"
	e := previewEnv(t)
	e.app.ChildEnv = append(e.app.ChildEnv, "CRM_DSN="+dsn)
	if r := e.run("databases", "create", "todo"); r.code != 0 {
		t.Fatalf("create = %+v", r)
	}
	if r := e.run("server", "stop"); r.code != 0 {
		t.Fatalf("stop = %+v", r)
	}
	registry := setup.RegistryDir(e.dirs.Home)
	if err := os.WriteFile(filepath.Join(registry, "crm.yaml"), []byte("database:\n  id: crm\n  schema_mode: strict\nstorage:\n  engine: postgres\n"+
		"  postgres:\n    dsn_env: CRM_DSN\nschemas:\n  collections:\n    contacts:\n      fields:\n        name: {type: string}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registry, "broken.yaml"), []byte("storage: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := e.run("server", "start"); r.code != 0 {
		t.Fatalf("start = %+v", r)
	}

	var status setup.Status
	statusJSON := e.api(http.MethodGet, "/api/local/v1/status")
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, db := range status.Databases {
		states[db.ID] = db.State
	}
	if states["todo"] != "mounted" || states["crm"] != "needs_attention" || states["broken"] != "needs_attention" {
		t.Errorf("states = %v", states)
	}
	list := e.run("databases", "--json")
	human := e.run("databases")
	mounts, _ := os.ReadFile(setup.MountsPath(e.dirs.Runtime))
	log, _ := os.ReadFile(filepath.Join(e.dirs.Runtime, "server.log"))
	if !strings.Contains(string(log), "crm needs attention") {
		t.Errorf("server.log does not report crm: %s", log)
	}
	for name, text := range map[string]string{
		"status API": statusJSON, "databases --json": list.stdout, "databases": human.stdout + human.stderr,
		"databases API": e.api(http.MethodGet, "/api/local/v1/databases"), "home API": e.api(http.MethodGet, "/api/local/v1/home"),
		"mounts.json": string(mounts), "server.log": string(log),
	} {
		if strings.Contains(text, "s3cret") || strings.Contains(text, dsn) {
			t.Errorf("%s leaks the connection string:\n%s", name, text)
		}
	}
	if list.stdout != e.api(http.MethodGet, "/api/local/v1/databases") {
		t.Errorf("databases --json differs from the API")
	}

	// After stopping, every database is unknown.
	if r := e.run("server", "stop"); r.code != 0 {
		t.Fatalf("stop = %+v", r)
	}
	after := e.run("databases", "--json")
	if strings.Contains(after.stdout, `"needs_attention"`) || strings.Count(after.stdout, `"state":"unknown"`) != 3 {
		t.Errorf("after stop = %s", after.stdout)
	}
	if _, err := os.Stat(setup.MountsPath(e.dirs.Runtime)); !os.IsNotExist(err) {
		t.Errorf("mounts.json outlived the server: %v", err)
	}
}

// AC:version-mismatch-line and AC:home-or-port-mismatch for the databases
// commands.
func TestDatabasesVersionAndHomeMismatch(t *testing.T) {
	e := previewEnv(t)
	e.app.ChildEnv = append(e.app.ChildEnv, childVersionEnv+"=0.9.0-old")
	if r := e.run("databases", "create", "notes"); r.code != 0 {
		t.Fatalf("create = %+v", r)
	}
	listed := e.run("databases")
	if listed.code != 0 || !strings.Contains(listed.stdout, "notes") ||
		!strings.Contains(listed.stderr, "OVDB server is running version 0.9.0-old; restart it to use version "+testVersion+": ovdb server restart") {
		t.Errorf("databases from a newer client = %+v", listed)
	}

	e.vars[paths.EnvHome] = filepath.Join(t.TempDir(), "other")
	for _, args := range [][]string{{"databases", "create", "x", "--json"}, {"databases", "--json"}, {"databases", "remove", "notes", "--yes", "--json"}} {
		failure := decodeError(t, e.run(args...), envelope.ServerConfigMismatch)
		if failure.Next[len(failure.Next)-1].Command != "ovdb server restart" {
			t.Errorf("ovdb %v next = %+v", args, failure.Next)
		}
	}
}

// Without --addr the preview create never falls back to the legacy path;
// with it, it does (REQ:legacy-create-compatible). Without the gate nothing
// changes.
func TestLegacyDatabasesPaths(t *testing.T) {
	e := previewEnv(t)
	root := newRoot(e.app)
	root.SetArgs([]string{"databases", "create", "crm", "--addr", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil || err.Error() != "legacy create" {
		t.Errorf("--addr = %v, want the legacy path", err)
	}
	root = newRoot(e.app)
	root.SetArgs([]string{"databases", "--url", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil || err.Error() != "legacy databases" {
		t.Errorf("--url = %v, want the legacy path", err)
	}
	if entries, _ := os.ReadDir(setup.RegistryDir(e.dirs.Home)); len(entries) != 0 {
		t.Errorf("legacy path registered %v", entries)
	}

	t.Setenv(preview.EnvVar, "")
	root = newRoot(e.app)
	create, _, _ := root.Find([]string{"databases", "create"})
	if create.Flags().Lookup("engine") != nil {
		t.Error("create offers --engine without the gate")
	}
	if remove, _, _ := root.Find([]string{"databases", "remove"}); remove == nil || !remove.Hidden {
		t.Error("remove is offered without the gate")
	}
	if engines, _, _ := root.Find([]string{"engines"}); !engines.Hidden {
		t.Error("engines is offered without the gate")
	}
}
