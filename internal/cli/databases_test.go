package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// waitMounted waits until the server has settled every database: mounting
// runs in the background after it starts listening.
func (e *env) waitMounted() {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(e.api(http.MethodGet, "/api/local/v1/databases"), `"state":"mounting"`) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("databases still mounting after 15s")
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
	if result.Next[0].Command != "ovdb databases reload shop" || !strings.Contains(result.Next[0].Label, "Describe your data") {
		t.Errorf("sqlite next = %+v", result.Next)
	}
	if strings.Contains(shop.stdout, "ovdb add") || strings.Contains(shop.stdout, "ovdb set") {
		t.Errorf("sqlite result suggests a write: %s", shop.stdout)
	}
	reloaded := e.run("databases", "reload", "shop", "--json")
	if reloaded.code != 0 || !strings.HasPrefix(reloaded.stdout, `{"schema":1,"database":{"id":"shop"`) || !strings.Contains(reloaded.stdout, `"state":"mounted"`) {
		t.Errorf("reload = %+v", reloaded)
	}
	if all := e.run("databases", "reload", "--all", "--json"); all.code != 0 || all.stdout != e.api(http.MethodGet, "/api/local/v1/databases") {
		t.Errorf("reload --all = %+v", all)
	}
	_ = decodeError(t, e.run("databases", "reload", "nope", "--json"), envelope.NotFound)

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
		!strings.Contains(refused.stderr, "already has files in it") || !strings.Contains(refused.stderr, "ovdb databases create diary --path <another absolute path>") {
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
	e.waitMounted()

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
	// Legacy-only flags are refused on the local path, not ignored.
	for _, flag := range []string{"--label=x", "--token=t", "--owner-token=t"} {
		failure := decodeError(t, e.run("databases", "create", "crm", flag, "--no-start", "--json"), envelope.InvalidArgument)
		if !strings.Contains(failure.Reason, "works only with --addr") {
			t.Errorf("%s = %+v", flag, failure)
		}
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

// AC:connect-leaves-folder-untouched, AC:connect-postgres-manifest (the
// missing-variable half) and AC:dsn-never-leaks for `ovdb databases
// connect`, through a real detached server.
func TestDatabasesConnectThroughTheServer(t *testing.T) {
	e := previewEnv(t)

	// Checks that need no server fail before starting one.
	_ = decodeError(t, e.run("databases", "connect", "notes", "--json"), envelope.InvalidArgument)
	_ = decodeError(t, e.run("databases", "connect", "--manifest", "crm.yaml", "--path", "/x", "--json"), envelope.InvalidArgument)
	if _, err := os.Stat(filepath.Join(e.dirs.Runtime, "server.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused connect started the server: %v", err)
	}

	// An existing inGitDB folder: created, removed (its data kept), connected
	// again under another name without a file changing.
	if r := e.run("databases", "create", "source"); r.code != 0 {
		t.Fatalf("create = %+v", r)
	}
	folder := filepath.Join(e.dirs.Data, "source")
	if r := e.run("databases", "remove", "source", "--yes"); r.code != 0 {
		t.Fatalf("remove = %+v", r)
	}
	before := tree(t, folder)
	connected := e.run("databases", "connect", "journal", "--path", folder)
	if connected.code != 0 {
		t.Fatalf("connect = %+v", connected)
	}
	for _, want := range []string{"Connected database journal", "Your data stays where it is: " + folder, "What next?", "Browse data", "ovdb list / --db journal", "Done"} {
		if !strings.Contains(connected.stdout, want) {
			t.Errorf("connect output lacks %q:\n%s", want, connected.stdout)
		}
	}
	if after := tree(t, folder); after != before {
		t.Errorf("connect changed the folder:\nbefore %s\nafter  %s", before, after)
	}
	e.waitMounted()
	if listed := e.run("databases", "--json"); !strings.Contains(listed.stdout, `"id":"journal"`) || strings.Contains(listed.stdout, "needs_attention") {
		t.Errorf("databases = %s", listed.stdout)
	}

	// A text file named .sqlite is refused.
	text := filepath.Join(t.TempDir(), "x.sqlite")
	if err := os.WriteFile(text, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notSQLite := decodeError(t, e.run("databases", "connect", "x", "--engine", "sqlite", "--path", text, "--json"), envelope.StorageUnavailable)
	if !strings.Contains(notSQLite.Reason, "isn't a SQLite database file") {
		t.Errorf("x.sqlite = %+v", notSQLite)
	}

	// A PostgreSQL manifest whose variable the server lacks: the variable is
	// named, nothing is registered, and --json is the API body.
	manifestPath := filepath.Join(t.TempDir(), "crm.yaml")
	if err := os.WriteFile(manifestPath, []byte("database:\n  id: crm\n  schema_mode: strict\nstorage:\n  engine: postgres\n"+
		"  postgres:\n    dsn_env: OVDB_TEST_CRM_DSN\nschemas:\n  collections:\n    contacts:\n      fields:\n        name: {type: string}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := e.run("databases", "connect", "--manifest", manifestPath, "--json")
	e2 := decodeError(t, missing, envelope.StorageUnavailable)
	if !strings.Contains(e2.Reason, "OVDB_TEST_CRM_DSN") || e2.Next[0].Label != "Set OVDB_TEST_CRM_DSN and run `ovdb server restart` from that shell" {
		t.Errorf("missing variable = %+v", e2)
	}
	body, _ := json.Marshal(setup.ConnectRequest{Manifest: manifestPath})
	if want := e.apiBody(http.MethodPost, "/api/local/v1/databases/connect", string(body)); missing.stdout != want {
		t.Errorf("--json\n got %s\nwant %s", missing.stdout, want)
	}
	if _, err := os.Stat(setup.ManifestPath(e.dirs.Home, "crm")); !os.IsNotExist(err) {
		t.Errorf("crm registered: %v", err)
	}
}

// tree lists every file under root with its size and modification time.
func tree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		data := []byte{}
		if !info.IsDir() {
			if data, err = os.ReadFile(path); err != nil {
				return err
			}
		}
		rel, _ := filepath.Rel(root, path)
		b.WriteString(rel + " " + info.ModTime().String() + " " + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// F1 through the CLI: a manifest naming a variable with a non-URL value
// never shows that value in connect --json, databases --json or server.log.
func TestConnectNeverShowsANamedVariablesValue(t *testing.T) {
	const secret = "s3cretjunk-Tok"
	e := previewEnv(t)
	e.app.ChildEnv = append(e.app.ChildEnv, "OVDB_TEST_JUNK_DSN="+secret)
	manifestPath := filepath.Join(t.TempDir(), "junk.yaml")
	if err := os.WriteFile(manifestPath, []byte("database:\n  id: junk\n  schema_mode: strict\nstorage:\n  engine: postgres\n"+
		"  postgres:\n    dsn_env: OVDB_TEST_JUNK_DSN\nschemas:\n  collections:\n    contacts:\n      fields:\n        name: {type: string}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	connected := e.run("databases", "connect", "--manifest", manifestPath, "--json")
	_ = decodeError(t, connected, envelope.StorageUnavailable)
	status := e.run("databases", "--json")
	logText, _ := os.ReadFile(filepath.Join(e.dirs.Runtime, "server.log"))
	for _, where := range []struct{ name, text string }{
		{"connect --json", connected.stdout + connected.stderr}, {"databases --json", status.stdout}, {"server.log", string(logText)},
	} {
		if strings.Contains(where.text, "s3cretjunk") {
			t.Errorf("%s shows the value: %s", where.name, where.text)
		}
	}
	if !strings.Contains(string(logText), "connecting database junk failed") {
		t.Errorf("server.log lacks the failure: %s", logText)
	}
}
