package setup

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
)

const ownerToken = "owner"

type registryFixture struct {
	dirs     paths.Dirs
	server   *server.Server
	registry *Registry
	log      bytes.Buffer
}

func newRegistry(t *testing.T) *registryFixture {
	t.Helper()
	f := &registryFixture{dirs: testDirs(t)}
	for _, dir := range []string{f.dirs.Home, f.dirs.Runtime} {
		if err := paths.EnsurePrivateDir(dir); err != nil {
			t.Fatal(err)
		}
	}
	f.open(t)
	return f
}

// open starts a new data server over the fixture's registry, as a server
// start does.
func (f *registryFixture) open(t *testing.T) {
	t.Helper()
	f.server = server.New("test", nil, server.WithAuth(&auth.Config{OwnerToken: ownerToken}))
	registry, err := OpenRegistry(f.dirs, f.server, func(format string, args ...any) {
		fmt.Fprintf(&f.log, format+"\n", args...)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registry = registry
	t.Cleanup(registry.Close)
}

func (f *registryFixture) data(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func (f *registryFixture) create(id, engine string) (DatabaseResult, error) {
	return f.registry.Create(CreateRequest{ID: id, Engine: engine, Path: DefaultPath(f.dirs.Data, engine, id)})
}

func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(root, path)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func TestCreateValidatesInput(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	abs := filepath.Join(f.dirs.Data, "x")
	for _, tc := range []struct {
		request CreateRequest
		code    envelope.Code
	}{
		{CreateRequest{ID: "", Path: abs}, envelope.InvalidArgument},
		{CreateRequest{ID: "-notes", Path: abs}, envelope.InvalidArgument},
		{CreateRequest{ID: "my notes", Path: abs}, envelope.InvalidArgument},
		{CreateRequest{ID: "notes/..", Path: abs}, envelope.InvalidArgument},
		{CreateRequest{ID: "notes", Engine: "oracle", Path: abs}, envelope.InvalidArgument},
		{CreateRequest{ID: "notes", Engine: EnginePostgres, Path: abs}, envelope.Unsupported},
		{CreateRequest{ID: "notes", Path: "relative/notes"}, envelope.InvalidArgument},
		{CreateRequest{ID: "notes", Path: abs + string(filepath.Separator) + ".." + string(filepath.Separator) + "y"}, envelope.InvalidArgument},
		{CreateRequest{ID: "notes"}, envelope.InvalidArgument},
	} {
		_, err := f.registry.Create(tc.request)
		if e := envelope.As(err); e == nil || e.Code != tc.code || len(e.Next) == 0 {
			t.Errorf("Create(%+v) = %v, want %s with next", tc.request, err, tc.code)
		}
	}
	// PostgreSQL is honest about needing a manifest.
	_, err := f.registry.Create(CreateRequest{ID: "crm", Engine: EnginePostgres, Path: abs})
	if e := envelope.As(err); e == nil || e.Next[0].Command != "ovdb init --engine postgres --id <name>" {
		t.Errorf("postgres = %+v", e)
	}
	for _, id := range []string{"notes", "a", "A_b-9", "0x"} {
		request := CreateRequest{ID: id, Path: abs}
		if err := ValidateCreate(&request); err != nil || request.Engine != EngineInGitDB {
			t.Errorf("ValidateCreate(%q) = %v, engine %q", id, err, request.Engine)
		}
	}
	if entries, _ := os.ReadDir(f.dirs.Data); len(entries) != 0 {
		t.Errorf("a refused create wrote %v", entries)
	}
}

// AC:create-ingitdb-default (service half): the folder exists, the manifest
// declares inGitDB schemaless, and records can be written at once.
func TestCreateInGitDBServesWithoutRestart(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	result, err := f.create("notes", EngineInGitDB)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.dirs.Data, "notes")
	if db := result.Database; db.ID != "notes" || db.Engine != EngineInGitDB || db.Location != want || db.State != MountMounted ||
		db.Manifest != ManifestPath(f.dirs.Home, "notes") {
		t.Errorf("result = %+v", db)
	}
	manifestText, _ := os.ReadFile(ManifestPath(f.dirs.Home, "notes"))
	if !strings.Contains(string(manifestText), "schema_mode: schemaless") || !strings.Contains(string(manifestText), "engine: ingitdb") {
		t.Errorf("manifest = %s", manifestText)
	}
	if rec := f.data(t, http.MethodPut, "/v1/databases/notes/records/items/hello", `{"data":{"title":"Hello"}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("write = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(want, "items")); err != nil {
		t.Errorf("record not stored as files: %v", err)
	}
	if ids := commands(result.Next); !slices.Equal(ids, []string{"ovdb databases", ""}) {
		t.Errorf("next = %+v", result.Next)
	}
	mounts, _ := ReadMounts(f.dirs.Runtime)
	if mounts == nil || len(mounts.Databases) != 1 || mounts.Databases[0].State != MountMounted {
		t.Errorf("mounts.json = %+v", mounts)
	}
	// The catalogue lives in OVDB home, never in the person's folder.
	if _, err := os.Stat(filepath.Join(want, ".ovdb")); !os.IsNotExist(err) {
		t.Errorf("mount wrote .ovdb into the storage: %v", err)
	}

	// A restart remounts it from the registry.
	f.registry.Close()
	f.open(t)
	if rec := f.data(t, http.MethodGet, "/v1/databases/notes/records/items/hello", ""); rec.Code != http.StatusOK {
		t.Errorf("read after restart = %d %s", rec.Code, rec.Body)
	}
}

func commands(next []envelope.Next) []string {
	var out []string
	for _, n := range next {
		out = append(out, n.Command)
	}
	return out
}

// AC:sqlite-points-to-schema: describing a schema comes first and nothing
// suggests adding a record.
func TestCreateSQLitePointsToSchema(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	result, err := f.create("shop", EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if result.Database.Location != filepath.Join(f.dirs.Data, "shop.sqlite") {
		t.Errorf("location = %s", result.Database.Location)
	}
	if first := result.Next[0]; !strings.Contains(first.Label, "schema") || first.Command != "ovdb server restart" ||
		!strings.Contains(result.Next[1].Label, SchemaDocsURL) {
		t.Errorf("next = %+v", result.Next)
	}
	for _, n := range result.Next {
		for _, word := range []string{"add", "set", "records"} {
			if strings.Contains(n.Command, word) {
				t.Errorf("next suggests a write: %+v", n)
			}
		}
	}
	// Collections without a schema are refused; the described one works.
	if rec := f.data(t, http.MethodPut, "/v1/databases/shop/records/items/a", `{"data":{"title":"A"}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("undescribed write = %d %s", rec.Code, rec.Body)
	}
	if rec := f.data(t, http.MethodPut, "/v1/databases/shop/records/example/a", `{"data":{"title":"A"}}`); rec.Code != http.StatusNoContent {
		t.Errorf("described write = %d %s", rec.Code, rec.Body)
	}
}

// AC:create-refuses-overwrite (service half): nothing is written, and next
// offers another name and another location.
func TestCreateNeverOverwrites(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	if _, err := f.create("notes", EngineInGitDB); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, f.dirs.Home)
	_, err := f.registry.Create(CreateRequest{ID: "Notes", Path: filepath.Join(f.dirs.Data, "elsewhere")})
	if e := envelope.As(err); e == nil || e.Code != envelope.AlreadyExists || e.Next[0].Command != "ovdb databases create Notes-2 --path "+paths.QuoteArg(goruntime.GOOS, filepath.Join(f.dirs.Data, "Notes-2")) {
		t.Errorf("same id = %+v", e)
	}

	folder := filepath.Join(f.dirs.Data, "diary")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "mine.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	sqliteFile := filepath.Join(f.dirs.Data, "shop.sqlite")
	if err := os.WriteFile(sqliteFile, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, request := range []CreateRequest{
		{ID: "diary", Path: folder},
		{ID: "shop", Engine: EngineSQLite, Path: sqliteFile},
	} {
		_, err := f.registry.Create(request)
		e := envelope.As(err)
		if e == nil || e.Code != envelope.LocationNotEmpty || len(e.Next) != 2 ||
			e.Next[0].Action != ActionEditName || e.Next[1].Action != ActionEditLocation {
			t.Errorf("%s: %+v", request.ID, e)
			continue
		}
		if !strings.HasPrefix(e.Next[0].Command, "ovdb databases create "+request.ID+"-2") {
			t.Errorf("%s another name = %+v", request.ID, e.Next[0])
		}
	}
	if data, _ := os.ReadFile(sqliteFile); string(data) != "not a database" {
		t.Error("the existing SQLite file changed")
	}
	if got := treeOf(t, folder); !slices.Equal(got, []string{".", "mine.txt"}) {
		t.Errorf("the existing folder changed: %v", got)
	}
	if after := treeOf(t, f.dirs.Home); !slices.Equal(before, after) {
		t.Errorf("OVDB home changed: %v → %v", before, after)
	}

	// An existing empty folder is fine.
	empty := filepath.Join(f.dirs.Data, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.Create(CreateRequest{ID: "empty", Path: empty}); err != nil {
		t.Errorf("empty folder: %v", err)
	}
}

// A mount failure rolls back everything the create made.
func TestCreateRollsBackOnMountFailure(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	// A file where the SQLite file's folder should be.
	blocker := filepath.Join(f.dirs.Data, "blocked")
	if err := os.MkdirAll(f.dirs.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.registry.Create(CreateRequest{ID: "shop", Engine: EngineSQLite, Path: filepath.Join(blocker, "sub", "shop.sqlite")})
	if e := envelope.As(err); e == nil || e.Code != envelope.StorageUnavailable {
		t.Errorf("blocked = %v", err)
	}
	if registrations, _ := ReadRegistry(f.dirs.Home); len(registrations) != 0 {
		t.Errorf("registry = %+v", registrations)
	}
	if entries, _ := os.ReadDir(RegistryDir(f.dirs.Home)); len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

// AC:remove-keeps-data (service half) and REQ:list-and-remove.
func TestRemoveKeepsData(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	for _, engine := range []string{EngineInGitDB, EngineSQLite} {
		id := "db-" + engine
		if _, err := f.create(id, engine); err != nil {
			t.Fatal(err)
		}
		result, err := f.registry.Remove(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		location := DefaultPath(f.dirs.Data, engine, id)
		if result.Database.Location != location || result.Database.State != "" {
			t.Errorf("result = %+v", result.Database)
		}
		if _, err := os.Stat(location); err != nil {
			t.Errorf("data removed: %v", err)
		}
		if _, err := os.Stat(ManifestPath(f.dirs.Home, id)); !os.IsNotExist(err) {
			t.Errorf("manifest kept: %v", err)
		}
		if rec := f.data(t, http.MethodGet, "/v1/databases/"+id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("still served: %d", rec.Code)
		}
		// The storage is released and still refuses a new database.
		_, err = f.create(id, engine)
		if e := envelope.As(err); e == nil || e.Code != envelope.LocationNotEmpty {
			t.Errorf("re-create over kept data = %v", err)
		}
	}
	_, err := f.registry.Remove(context.Background(), "nope")
	if e := envelope.As(err); e == nil || e.Code != envelope.NotFound {
		t.Errorf("remove unknown = %v", err)
	}
	if list, _ := f.registry.List(); len(list) != 0 {
		t.Errorf("list = %+v", list)
	}
}

// AC:tolerant-registry and AC:dsn-never-leaks (service half): one valid,
// one broken and one unreachable PostgreSQL manifest.
func TestTolerantRegistryRedactsReasons(t *testing.T) {
	const dsn = "postgres://u:s3cret@nohost.invalid/db"
	t.Setenv("CRM_DSN", dsn)
	f := newRegistry(t)
	if _, err := f.create("todo", EngineInGitDB); err != nil {
		t.Fatal(err)
	}
	f.registry.Close()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(RegistryDir(f.dirs.Home), name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("broken.yaml", "database: [\n")
	write("crm.yaml", "database:\n  id: crm\n  schema_mode: strict\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: CRM_DSN\n"+
		"schemas:\n  collections:\n    contacts:\n      fields:\n        name: {type: string}\n")
	f.open(t)

	list, err := f.registry.List()
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]Database{}
	for _, db := range list {
		states[db.ID] = db
	}
	if states["todo"].State != MountMounted || states["broken"].State != MountNeedsAttention || states["crm"].State != MountNeedsAttention {
		t.Fatalf("states = %+v", list)
	}
	if states["crm"].Location != "connection from $CRM_DSN" || states["crm"].Reason == "" {
		t.Errorf("crm = %+v", states["crm"])
	}
	mounts, _ := os.ReadFile(MountsPath(f.dirs.Runtime))
	listed := fmt.Sprint(list)
	for name, text := range map[string]string{"mounts.json": string(mounts), "list": listed, "log": f.log.String()} {
		if strings.Contains(text, "s3cret") || strings.Contains(text, dsn) {
			t.Errorf("%s leaks the connection string: %s", name, text)
		}
	}
	if !strings.Contains(f.log.String(), "crm needs attention") {
		t.Errorf("log = %s", f.log.String())
	}
	if rec := f.data(t, http.MethodGet, "/v1/databases/todo", ""); rec.Code != http.StatusOK {
		t.Errorf("todo not served: %d", rec.Code)
	}

	// Without a server every state is unknown.
	unknown, _ := ListDatabases(f.dirs.Home, nil)
	for _, db := range unknown {
		if db.State != MountUnknown || db.Reason != "" {
			t.Errorf("without server = %+v", db)
		}
	}
	// Home counts them, with attention.
	if line := DatabasesStatus(list); line.Key != "home.status.databases_attention" || line.Params["count"] != "3" || line.Params["attention"] != "2" {
		t.Errorf("status line = %+v", line)
	}
}
