package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
)

const ownerToken = "owner"

type registryFixture struct {
	dirs     paths.Dirs
	server   *server.Server
	mounter  Mounter // server unless a test swaps it
	registry *Registry
	opts     RegistryOptions
	mu       sync.Mutex
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

// open starts a new data server over the fixture's registry and mounts it,
// as a server start does.
func (f *registryFixture) open(t *testing.T) {
	t.Helper()
	f.server = server.New("test", nil, server.WithAuth(&auth.Config{OwnerToken: ownerToken}))
	mounter := f.mounter
	if mounter == nil {
		mounter = f.server
	}
	registry, err := OpenRegistry(f.dirs, mounter, func(format string, args ...any) {
		f.mu.Lock()
		defer f.mu.Unlock()
		fmt.Fprintf(&f.log, format+"\n", args...)
	}, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.registry = registry
	t.Cleanup(registry.Close)
	registry.MountAll(context.Background())
}

func (f *registryFixture) logText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.log.String()
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

func (f *registryFixture) state(t *testing.T, id string) Database {
	t.Helper()
	list, err := f.registry.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, db := range list {
		if db.ID == id {
			return db
		}
	}
	t.Fatalf("%s not listed: %+v", id, list)
	return Database{}
}

func (f *registryFixture) writeManifest(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(RegistryDir(f.dirs.Home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(RegistryDir(f.dirs.Home), name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
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

func commands(next []envelope.Next) []string {
	var out []string
	for _, n := range next {
		out = append(out, n.Command)
	}
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
		{CreateRequest{ID: "notes", Path: filepath.Join("relative", "notes")}, envelope.InvalidArgument},
		{CreateRequest{ID: "notes"}, envelope.InvalidArgument},
	} {
		_, err := f.registry.Create(tc.request)
		if e := envelope.As(err); e == nil || e.Code != tc.code || len(e.Next) == 0 {
			t.Errorf("Create(%+v) = %v, want %s with next", tc.request, err, tc.code)
		}
	}
	// PostgreSQL is honest about needing a manifest, with steps that work today.
	_, err := f.registry.Create(CreateRequest{ID: "crm", Engine: EnginePostgres, Path: abs})
	if e := envelope.As(err); e == nil || e.Next[0].Command != "ovdb init --engine postgres --id <name>" || e.Next[1].Command != "ovdb databases reload <name>" {
		t.Errorf("postgres = %+v", e)
	}
	for _, id := range []string{"notes", "a", "A_b-9", "0x"} {
		request := CreateRequest{ID: id, Path: abs}
		if err := ValidateCreate(&request); err != nil || request.Engine != EngineInGitDB {
			t.Errorf("ValidateCreate(%q) = %v, engine %q", id, err, request.Engine)
		}
	}
	// A trailing separator and dot segments are normalized, not refused.
	request := CreateRequest{ID: "notes", Path: abs + string(filepath.Separator) + "." + string(filepath.Separator)}
	if err := ValidateCreate(&request); err != nil || request.Path != abs {
		t.Errorf("trailing separator = %v, %q", err, request.Path)
	}
	if goruntime.GOOS == "windows" {
		request := CreateRequest{ID: "notes", Path: `C:/Users/someone/ovdb/notes/`}
		if err := ValidateCreate(&request); err != nil || request.Path != `C:\Users\someone\ovdb\notes` {
			t.Errorf("windows path = %v, %q", err, request.Path)
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
	if got := commands(result.Next); !slices.Equal(got, []string{"ovdb databases", ""}) {
		t.Errorf("next = %+v", result.Next)
	}
	mounts, _ := ReadMounts(f.dirs.Runtime)
	if mounts == nil || len(mounts.Databases) != 1 || mounts.Databases[0].State != MountMounted {
		t.Errorf("mounts.json = %+v", mounts)
	}
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

// AC:sqlite-points-to-schema: describing the data comes first, nothing
// suggests adding a record, and editing the manifest plus reload works.
func TestCreateSQLiteSchemaThenReload(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	result, err := f.create("shop", EngineSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if first := result.Next[0]; !strings.Contains(first.Label, "`example` collection is a placeholder") || first.Command != "ovdb databases reload shop" ||
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
	if rec := f.data(t, http.MethodPut, "/v1/databases/shop/records/items/a", `{"data":{"title":"A"}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("undescribed write = %d %s", rec.Code, rec.Body)
	}

	// Describe "items", reload, and the write works.
	manifestPath := ManifestPath(f.dirs.Home, "shop")
	text, _ := os.ReadFile(manifestPath)
	edited := strings.Replace(string(text), "    example:", "    items:\n      fields:\n        title: {type: string}\n    example:", 1)
	if err := os.WriteFile(manifestPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := f.registry.Reload(context.Background(), "SHOP") // any casing names one database
	if err != nil || reloaded.Database.State != MountMounted {
		t.Fatalf("reload = %+v, %v", reloaded, err)
	}
	if rec := f.data(t, http.MethodPut, "/v1/databases/shop/records/items/a", `{"data":{"title":"A"}}`); rec.Code != http.StatusNoContent {
		t.Errorf("described write after reload = %d %s", rec.Code, rec.Body)
	}
	if _, err := f.registry.Reload(context.Background(), "nope"); envelope.As(err) == nil || envelope.As(err).Code != envelope.NotFound {
		t.Errorf("reload unknown = %v", err)
	}
}

// AC:create-refuses-overwrite (service half) and the overlap rules: nothing
// is written, and next offers another location.
func TestCreateNeverOverwrites(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	if _, err := f.create("notes", EngineInGitDB); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, f.dirs.Home)
	_, err := f.registry.Create(CreateRequest{ID: "Notes", Path: filepath.Join(f.dirs.Data, "elsewhere")})
	e := envelope.As(err)
	if e == nil || e.Code != envelope.AlreadyExists || !strings.Contains(e.Reason, "named notes ") ||
		e.Next[0].Command != "ovdb databases create Notes-2 --path "+paths.QuoteArg(goruntime.GOOS, filepath.Join(f.dirs.Data, "Notes-2")) {
		t.Errorf("same id, other casing = %+v", e)
	} else if SuggestedName(e.Next[0]) != "Notes-2" {
		t.Errorf("suggested name = %q", SuggestedName(e.Next[0]))
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
	if err := os.WriteFile(filepath.Join(f.dirs.Data, "cart.sqlite-wal"), []byte("someone's wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		request CreateRequest
		reason  string
	}{
		{CreateRequest{ID: "diary", Path: folder}, "already has files"},
		{CreateRequest{ID: "shop", Engine: EngineSQLite, Path: sqliteFile}, "already exists"},
		{CreateRequest{ID: "cart", Engine: EngineSQLite, Path: filepath.Join(f.dirs.Data, "cart.sqlite")}, "cart.sqlite-wal already exists"},
		{CreateRequest{ID: "inner", Path: filepath.Join(f.dirs.Data, "notes", "inner")}, "overlaps the storage of the database notes"},
		{CreateRequest{ID: "outer", Path: f.dirs.Data + string(filepath.Separator)}, "overlaps the storage of the database notes"},
		{CreateRequest{ID: "inhome", Path: filepath.Join(f.dirs.Home, "databases", "sub")}, "OVDB's own settings folders"},
		{CreateRequest{ID: "inrun", Path: filepath.Join(f.dirs.Runtime, "x.sqlite"), Engine: EngineSQLite}, "OVDB's own settings folders"},
	} {
		_, err := f.registry.Create(tc.request)
		e := envelope.As(err)
		if e == nil || e.Code != envelope.LocationNotEmpty || !strings.Contains(e.Reason, tc.reason) ||
			e.Next[0].Action != ActionEditLocation || e.Next[len(e.Next)-1].Action != ActionDone {
			t.Errorf("%s: %+v", tc.request.ID, e)
		}
		for _, n := range e.Next {
			if strings.Contains(n.Command, tc.request.ID+"-2") {
				t.Errorf("%s: suggests a new empty database next to the data: %+v", tc.request.ID, n)
			}
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

	empty := filepath.Join(f.dirs.Data, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.Create(CreateRequest{ID: "empty", Path: empty}); err != nil {
		t.Errorf("empty folder: %v", err)
	}
}

// failingMounter serves nothing: every Mount fails.
type failingMounter struct{ Mounter }

func (failingMounter) Mount(*core.Database) error { return errors.New("mount refused") }

// F4: a failure to serve the new database rolls back exactly what the create
// made, with a truthful error.
func TestCreateRollsBackWhenServingFails(t *testing.T) {
	t.Parallel()
	f := &registryFixture{dirs: testDirs(t)}
	for _, dir := range []string{f.dirs.Home, f.dirs.Runtime, f.dirs.Data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.mounter = failingMounter{}
	f.open(t)
	homeBefore := treeOf(t, f.dirs.Home)
	for _, engine := range []string{EngineInGitDB, EngineSQLite} {
		_, err := f.create("db-"+engine, engine)
		e := envelope.As(err)
		if e == nil || e.Code != envelope.Internal || !strings.Contains(e.Reason, "couldn't start serving it") {
			t.Errorf("%s: %+v", engine, e)
		}
		if _, err := os.Stat(DefaultPath(f.dirs.Data, engine, "db-"+engine)); !os.IsNotExist(err) {
			t.Errorf("%s: storage left behind: %v", engine, err)
		}
	}
	if entries, _ := os.ReadDir(f.dirs.Data); len(entries) != 0 {
		t.Errorf("data home not clean: %v", entries)
	}
	for _, dir := range []string{RegistryDir(f.dirs.Home), CatalogueDir(f.dirs.Home)} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s not clean: %v (home before %v)", dir, entries, homeBefore)
		}
	}

	// A storage failure is plain words with another location to try.
	blocker := filepath.Join(f.dirs.Data, "blocked")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.registry.Create(CreateRequest{ID: "shop", Engine: EngineSQLite, Path: filepath.Join(blocker, "sub", "shop.sqlite")})
	if e := envelope.As(err); e == nil || e.Code != envelope.StorageUnavailable || !strings.Contains(e.Reason, "is a file, not a folder") ||
		len(e.Next) != 1 || e.Next[0].Action != ActionEditLocation {
		t.Errorf("blocked = %+v", e)
	}
}

// AC:remove-keeps-data (service half), REQ:list-and-remove and case rules.
func TestRemoveKeepsData(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	for _, engine := range []string{EngineInGitDB, EngineSQLite} {
		id := "Db-" + engine
		if _, err := f.create(id, engine); err != nil {
			t.Fatal(err)
		}
		result, err := f.registry.Remove(context.Background(), strings.ToLower(id))
		if err != nil {
			t.Fatal(err)
		}
		location := DefaultPath(f.dirs.Data, engine, id)
		if result.Database.ID != id || result.Database.Location != location || result.Database.State != "" {
			t.Errorf("result = %+v", result.Database)
		}
		if strings.Contains(fmt.Sprint(result.Next), "-2") || result.Next[len(result.Next)-1].Action != ActionDone {
			t.Errorf("removed next = %+v", result.Next)
		}
		if _, err := os.Stat(location); err != nil {
			t.Errorf("data removed: %v", err)
		}
		if rec := f.data(t, http.MethodGet, "/v1/databases/"+id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("still served: %d", rec.Code)
		}
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

// F6: a manifest that cannot be deleted leaves the database registered and
// served, never "Ready but not served".
func TestRemoveFailureKeepsServing(t *testing.T) {
	if goruntime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs Unix directory permissions")
	}
	t.Parallel()
	f := newRegistry(t)
	if _, err := f.create("notes", EngineInGitDB); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(RegistryDir(f.dirs.Home), 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(RegistryDir(f.dirs.Home), 0o700) }()
	_, err := f.registry.Remove(context.Background(), "notes")
	if e := envelope.As(err); e == nil || e.Code != envelope.StorageUnavailable || !strings.Contains(e.Reason, "nothing changed") {
		t.Errorf("remove = %+v", err)
	}
	if db := f.state(t, "notes"); db.State != MountMounted {
		t.Errorf("state = %+v", db)
	}
	if rec := f.data(t, http.MethodGet, "/v1/databases/notes", ""); rec.Code != http.StatusOK {
		t.Errorf("not served: %d", rec.Code)
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
	f.writeManifest(t, "broken.yaml", "database: [\n")
	f.writeManifest(t, "crm.yaml", "database:\n  id: crm\n  schema_mode: strict\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: CRM_DSN\n"+
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
	for name, text := range map[string]string{"mounts.json": string(mounts), "list": fmt.Sprint(list), "log": f.logText()} {
		if strings.Contains(text, "s3cret") || strings.Contains(text, dsn) {
			t.Errorf("%s leaks the connection string: %s", name, text)
		}
	}
	if !strings.Contains(f.logText(), "crm needs attention") {
		t.Errorf("log = %s", f.logText())
	}
	if rec := f.data(t, http.MethodGet, "/v1/databases/todo", ""); rec.Code != http.StatusOK {
		t.Errorf("todo not served: %d", rec.Code)
	}
	unknown, _ := ListDatabases(f.dirs.Home, nil)
	for _, db := range unknown {
		if db.State != MountUnknown || db.Reason != "" {
			t.Errorf("without server = %+v", db)
		}
	}
	if line := DatabasesStatus(list); line.Key != "home.status.databases_attention" || line.Params["count"] != "3" || line.Params["attention"] != "2" {
		t.Errorf("status line = %+v", line)
	}
	if line := DatabasesStatus(list[:1]); line.Key != "home.status.database_one_attention" {
		t.Errorf("one needing attention = %+v", line)
	}

	// A manifest added by hand loads with reload --all; a removed one goes.
	if err := os.Remove(filepath.Join(RegistryDir(f.dirs.Home), "broken.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.dirs.Data, "manual"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.writeManifest(t, "manual.yaml", "database:\n  id: manual\n  schema_mode: schemaless\nstorage:\n  engine: ingitdb\n  path: "+
		yamlScalar(filepath.Join(f.dirs.Data, "manual"))+"\n")
	if db := f.state(t, "manual"); db.State != MountNeedsAttention || !strings.Contains(db.Reason, "reload --all") {
		t.Errorf("hand-added before reload = %+v", db)
	}
	document, err := f.registry.ReloadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, db := range document.Databases {
		ids = append(ids, db.ID+"="+db.State)
	}
	if !slices.Equal(ids, []string{"crm=needs_attention", "manual=mounted", "todo=mounted"}) {
		t.Errorf("after reload --all = %v", ids)
	}
}

// F1: an unreachable server engine never holds up the other databases, and
// a stopping server does not wait for it.
func TestUnreachableStorageDoesNotBlock(t *testing.T) {
	t.Setenv("SLOW_DSN", "postgres://u:s3cret@10.255.255.1:5432/db?connect_timeout=30")
	f := &registryFixture{dirs: testDirs(t), opts: RegistryOptions{MountTimeout: 300 * time.Millisecond}}
	for _, dir := range []string{f.dirs.Home, f.dirs.Runtime} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.open(t)
	if _, err := f.create("todo", EngineInGitDB); err != nil {
		t.Fatal(err)
	}
	f.registry.Close()
	f.writeManifest(t, "slow.yaml", "database:\n  id: slow\n  schema_mode: strict\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: SLOW_DSN\n"+
		"schemas:\n  collections:\n    c:\n      fields:\n        t: {type: string}\n")

	started := time.Now()
	f.open(t)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("mounting took %v", elapsed)
	}
	if db := f.state(t, "slow"); db.State != MountNeedsAttention || strings.Contains(db.Reason, "s3cret") {
		t.Errorf("slow = %+v", db)
	}
	if db := f.state(t, "todo"); db.State != MountMounted {
		t.Errorf("todo = %+v", db)
	}
	// It stays removable.
	if _, err := f.registry.Remove(context.Background(), "slow"); err != nil {
		t.Errorf("remove slow: %v", err)
	}

	// Stopping while a mount hangs returns at once.
	f.writeManifest(t, "slow.yaml", "database:\n  id: slow\n  schema_mode: strict\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: SLOW_DSN\n"+
		"schemas:\n  collections:\n    c:\n      fields:\n        t: {type: string}\n")
	registry, err := OpenRegistry(f.dirs, server.New("test", nil), nil, RegistryOptions{MountTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { registry.MountAll(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("MountAll did not return when the server stopped")
	}
	registry.Close()
}

// F2: missing storage is reported, never recreated empty, and stays removable.
func TestMissingStorageNeedsAttention(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	for _, engine := range []string{EngineInGitDB, EngineSQLite} {
		if _, err := f.create("gone-"+engine, engine); err != nil {
			t.Fatal(err)
		}
	}
	f.registry.Close()
	for _, engine := range []string{EngineInGitDB, EngineSQLite} {
		if err := os.RemoveAll(DefaultPath(f.dirs.Data, engine, "gone-"+engine)); err != nil {
			t.Fatal(err)
		}
	}
	f.open(t)
	for _, engine := range []string{EngineInGitDB, EngineSQLite} {
		id := "gone-" + engine
		path := DefaultPath(f.dirs.Data, engine, id)
		if db := f.state(t, id); db.State != MountNeedsAttention || !strings.Contains(db.Reason, "The data at "+path+" is missing") {
			t.Errorf("%s = %+v", id, db)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: storage recreated: %v", id, err)
		}
		if _, err := f.registry.Remove(context.Background(), id); err != nil {
			t.Errorf("remove %s: %v", id, err)
		}
	}
}
