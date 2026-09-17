package setup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
)

// snapshot is every file under root with its content hash, .git included.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			out[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for path, sum := range after {
		if before[path] != sum {
			t.Errorf("%s: %s added or changed", what, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("%s: %s removed", what, path)
		}
	}
}

// withGlobalGitIdentity gives git a name and email only through a global
// config file, the way a person's own setup does.
func withGlobalGitIdentity(t *testing.T) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Person\n\temail = person@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// ingitdbRepo is an existing inGitDB Git repository with one committed
// record, made outside OVDB: the records are written through a mount of a
// separate seed folder and copied in, so nothing has ever mounted the
// repository itself before a test snapshots it.
func ingitdbRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	base := t.TempDir()
	seed := filepath.Join(base, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(base, "notes.yaml")
	text := "database: {id: notes, schema_mode: schemaless}\nstorage: {engine: ingitdb, path: " + yamlScalar(seed) + "}\n"
	if err := os.WriteFile(manifestPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := mount.FileWithOptions(manifestPath, mount.Options{CatalogueDir: filepath.Join(base, "catalogue"), SkipGitIdentity: true})
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New("test", map[string]*core.Database{"notes": db}, server.WithAuth(&auth.Config{OwnerToken: ownerToken}))
	request := httptest.NewRequest(http.MethodPut, "/v1/databases/notes/records/items/milk", strings.NewReader(`{"data":{"title":"Milk"}}`))
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, request)
	_ = db.Close()
	if recorder.Code >= 300 {
		t.Fatalf("seeding: %d %s", recorder.Code, recorder.Body)
	}

	repo := filepath.Join(base, "notes-repo")
	err = filepath.WalkDir(seed, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(seed, path)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(repo, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(repo, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	// A repository as a person clones it: everything committed.
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "Seed notes"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "dalgo2ingitdb")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the fixture repository was mounted before the test: %v", err)
	}
	return repo
}

// gitState is what a person's repository must keep across a connect: its
// working tree and every .git file (index, HEAD, config and objects
// included), except what the inGitDB driver keeps under .git/dalgo2ingitdb.
type gitState struct {
	files map[string]string
	head  string
}

func repoState(t *testing.T, repo string) gitState {
	t.Helper()
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return gitState{files: snapshot(t, repo), head: string(head)}
}

// sameRepository fails unless after equals before, allowing only additions
// under .git/dalgo2ingitdb (the driver's transaction lock).
func sameRepository(t *testing.T, what string, before, after gitState) {
	t.Helper()
	driver := filepath.Join(".git", "dalgo2ingitdb")
	for path, sum := range after.files {
		if old, ok := before.files[path]; ok {
			if old != sum && path != ".git" {
				t.Errorf("%s: %s changed", what, path)
			}
			continue
		}
		if path != driver && !strings.HasPrefix(path, driver+string(filepath.Separator)) {
			t.Errorf("%s: %s added", what, path)
		}
	}
	for path := range before.files {
		if _, ok := after.files[path]; !ok {
			t.Errorf("%s: %s removed", what, path)
		}
	}
	if before.head != after.head {
		t.Errorf("%s: HEAD moved from %s to %s", what, before.head, after.head)
	}
}

// AC:connect-leaves-folder-untouched, the inGitDB half: an existing Git
// repository with records and only a global git identity registers with its
// working tree, index, HEAD and .git/config unchanged (the only addition is
// the driver's lock under .git/dalgo2ingitdb), and serves its records.
func TestConnectInGitDBRepositoryLeavesItUntouched(t *testing.T) {
	withGlobalGitIdentity(t)
	repo := ingitdbRepo(t)
	before := repoState(t, repo)
	f := newRegistry(t)

	result, err := f.registry.Connect(ConnectRequest{ID: "notes", Engine: EngineInGitDB, Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	sameRepository(t, "connect", before, repoState(t, repo))
	if out, _ := exec.Command("git", "-C", repo, "status", "--porcelain", "--ignored").Output(); len(out) != 0 {
		t.Errorf("git status after connect: %s", out)
	}
	if db := result.Database; db.ID != "notes" || db.Engine != EngineInGitDB || db.Location != repo || db.State != MountMounted || db.Manifest != ManifestPath(f.dirs.Home, "notes") {
		t.Errorf("result database = %+v", db)
	}
	if got := commands(result.Next); !slices.Equal(got, []string{"ovdb list / --db notes", "ovdb use notes", "ovdb databases", ""}) {
		t.Errorf("next = %q", got)
	}
	if recorder := f.data(t, http.MethodGet, "/v1/databases/notes/records/items/milk", ""); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Milk") {
		t.Errorf("reading the connected record: %d %s", recorder.Code, recorder.Body)
	}
	if state := f.state(t, "notes"); state.State != MountMounted {
		t.Errorf("listed = %+v", state)
	}

	// Removing and restarting keeps it untouched too: every later mount skips
	// the git identity and keeps the catalogue in OVDB home.
	f.registry.Close()
	f.open(t)
	if state := f.state(t, "notes"); state.State != MountMounted {
		t.Errorf("after a restart = %+v", state)
	}
	sameRepository(t, "remount", before, repoState(t, repo))
}

// AC:connect-leaves-folder-untouched, the SQLite half: a text file named
// x.sqlite is refused with storage_unavailable and nothing registered.
func TestConnectRefusesATextFileNamedSQLite(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	path := filepath.Join(t.TempDir(), "x.sqlite")
	if err := os.WriteFile(path, []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.registry.Connect(ConnectRequest{ID: "x", Engine: EngineSQLite, Path: path})
	e := envelope.As(err)
	if e == nil || e.Code != envelope.StorageUnavailable || !strings.Contains(e.Reason, "isn't a SQLite database file") || len(e.Next) == 0 {
		t.Fatalf("Connect = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "not a database\n" {
		t.Errorf("file changed: %q", data)
	}
	if list, _ := f.registry.List(); len(list) != 0 {
		t.Errorf("registered: %+v", list)
	}
	if entries, _ := os.ReadDir(RegistryDir(f.dirs.Home)); len(entries) != 0 {
		t.Errorf("registry folder not empty: %v", entries)
	}
}

func sqliteFile(t *testing.T, statements ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shop.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// An existing SQLite file is described from its own tables, so mounting it
// creates no table and the file stays byte for byte the same.
func TestConnectSQLiteFileDescribesItsTables(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	path := sqliteFile(t,
		`CREATE TABLE products (id TEXT PRIMARY KEY, title TEXT NOT NULL, price REAL, stock INTEGER DEFAULT 0, active BOOLEAN)`,
		`INSERT INTO products (id, title, price) VALUES ('tea', 'Green tea', 3.5)`,
		`CREATE TABLE audit (at TEXT)`)
	before := snapshot(t, filepath.Dir(path))

	result, err := f.registry.Connect(ConnectRequest{ID: "shop", Engine: EngineSQLite, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	sameSnapshot(t, "connect", before, snapshot(t, filepath.Dir(path)))
	manifestText, _ := os.ReadFile(result.Database.Manifest)
	for _, want := range []string{"products:", "title: {type: string, required: true}", "price: {type: number}", "stock: {type: integer}", "active: {type: boolean}"} {
		if !strings.Contains(string(manifestText), want) {
			t.Errorf("manifest lacks %q:\n%s", want, manifestText)
		}
	}
	if strings.Contains(string(manifestText), "audit") || strings.Contains(string(manifestText), "example:") {
		t.Errorf("manifest declares a table without id or a placeholder:\n%s", manifestText)
	}
	if recorder := f.data(t, http.MethodGet, "/v1/databases/shop/records/products/tea", ""); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Green tea") {
		t.Errorf("reading the connected record: %d %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(strings.Join(commands(result.Next), " "), "ovdb add") {
		t.Errorf("next suggests adding: %+v", result.Next)
	}

	empty := sqliteFile(t, `CREATE TABLE audit (at TEXT)`)
	_, err = f.registry.Connect(ConnectRequest{ID: "empty", Engine: EngineSQLite, Path: empty})
	if e := envelope.As(err); e == nil || e.Code != envelope.SchemaRequired || !strings.Contains(strings.Join(commands(e.Next), " "), "ovdb databases connect --manifest") {
		t.Errorf("a file without usable tables = %v", err)
	}
}

func TestConnectValidatesAndRefusesUnsafeLocations(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	if _, err := f.create("notes", EngineInGitDB); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	for _, tc := range []struct {
		name    string
		request ConnectRequest
		code    envelope.Code
		reason  string
	}{
		{"bad name", ConnectRequest{ID: "my notes", Path: folder}, envelope.InvalidArgument, "can't be a database name"},
		{"relative path", ConnectRequest{ID: "a", Path: "relative"}, envelope.InvalidArgument, "full path"},
		{"no path", ConnectRequest{ID: "a"}, envelope.InvalidArgument, "Choose the folder"},
		{"server engine", ConnectRequest{ID: "a", Engine: EnginePostgres, Path: folder}, envelope.Unsupported, "manifest"},
		{"both", ConnectRequest{ID: "a", Manifest: filepath.Join(folder, "m.yaml")}, envelope.InvalidArgument, "not both"},
		{"relative manifest", ConnectRequest{Manifest: "m.yaml"}, envelope.InvalidArgument, "full path"},
		{"taken name", ConnectRequest{ID: "NOTES", Path: folder}, envelope.AlreadyExists, "already registered"},
		{"inside OVDB home", ConnectRequest{ID: "a", Path: f.dirs.Home}, envelope.InvalidArgument, "OVDB's own"},
		{"inside another database", ConnectRequest{ID: "a", Path: filepath.Join(f.dirs.Data, "notes", ".git")}, envelope.InvalidArgument, "overlaps the storage of the database notes"},
		{"around another database", ConnectRequest{ID: "a", Path: f.dirs.Data}, envelope.InvalidArgument, "overlaps"},
		{"missing folder", ConnectRequest{ID: "a", Path: filepath.Join(folder, "gone")}, envelope.StorageUnavailable, "never creates storage"},
		{"a file as inGitDB", ConnectRequest{ID: "a", Path: sqliteFile(t, `CREATE TABLE t (id TEXT)`)}, envelope.StorageUnavailable, "not an inGitDB folder"},
		{"missing manifest", ConnectRequest{Manifest: filepath.Join(folder, "none.yaml")}, envelope.InvalidArgument, "no file"},
	} {
		_, err := f.registry.Connect(tc.request)
		if e := envelope.As(err); e == nil || e.Code != tc.code || !strings.Contains(e.Reason, tc.reason) || len(e.Next) == 0 {
			t.Errorf("%s: Connect = %v, want %s with %q and next", tc.name, err, tc.code, tc.reason)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing folder was created")
	}
	if list, _ := f.registry.List(); len(list) != 1 {
		t.Errorf("registered = %+v", list)
	}
}

// AC:connect-postgres-manifest and AC:dsn-never-leaks: a missing dsn_env is
// storage_unavailable naming the variable and nothing registered; a set one
// that cannot connect fails without its value anywhere.
func TestConnectManifestConnectionVariable(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	env := map[string]string{}
	f.registry.getenv = func(name string) string { return env[name] }
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "crm.yaml")
	text := "database:\n  id: crm\n  schema_mode: strict\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: CRM_DSN\nschemas:\n  collections:\n    contacts:\n      fields:\n        name: {type: string}\n"
	if err := os.WriteFile(manifestPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := f.registry.Connect(ConnectRequest{Manifest: manifestPath})
	e := envelope.As(err)
	if e == nil || e.Code != envelope.StorageUnavailable || !strings.Contains(e.Reason, "CRM_DSN") || len(e.Next) == 0 ||
		e.Next[0].Label != "Set CRM_DSN and run `ovdb server restart` from that shell" || e.Next[0].Command != "ovdb server restart" {
		t.Fatalf("without CRM_DSN: %v %+v", err, e)
	}
	if list, _ := f.registry.List(); len(list) != 0 {
		t.Errorf("registered without the variable: %+v", list)
	}

	for _, dsn := range []string{"postgres://u:s3cret@127.0.0.1:1/db?connect_timeout=2", "host=127.0.0.1 port=1 user=u password=s3cret dbname=db connect_timeout=2"} {
		env["CRM_DSN"] = dsn
		_, err = f.registry.Connect(ConnectRequest{Manifest: manifestPath})
		e = envelope.As(err)
		if e == nil || e.Code != envelope.StorageUnavailable {
			t.Fatalf("unreachable server: %v", err)
		}
		body := string(envelope.MarshalError(e))
		for _, where := range []struct{ name, text string }{{"error", body}, {"log", f.logText()}} {
			if strings.Contains(where.text, "s3cret") {
				t.Errorf("%s shows the password: %s", where.name, where.text)
			}
		}
		if list, _ := f.registry.List(); len(list) != 0 {
			t.Errorf("registered an unreachable database: %+v", list)
		}
		if entries, _ := os.ReadDir(RegistryDir(f.dirs.Home)); len(entries) != 0 {
			t.Errorf("registry folder keeps %v", entries)
		}
	}
	if mounts, _ := os.ReadFile(MountsPath(f.dirs.Runtime)); strings.Contains(string(mounts), "s3cret") {
		t.Errorf("mounts.json shows the password: %s", mounts)
	}
}

// A manifest's relative storage path is made absolute against its own
// folder in the copy OVDB keeps; the original file is left as it is.
func TestConnectManifestCopiesWithAbsolutePaths(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data", "journal", InGitDBDir), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "journal.yaml")
	text := "# My journal\ndatabase:\n  id: journal\n  schema_mode: schemaless\nstorage:\n  engine: ingitdb\n  path: ./data/journal # kept next to this file\n"
	if err := os.WriteFile(manifestPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.registry.Connect(ConnectRequest{Manifest: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "data", "journal")
	if result.Database.ID != "journal" || result.Database.Location != want {
		t.Errorf("database = %+v, want location %s", result.Database, want)
	}
	copied, _ := os.ReadFile(ManifestPath(f.dirs.Home, "journal"))
	// The copy is re-encoded from the typed manifest (anchors and merge keys
	// resolved), so the original's comments are not kept.
	if !strings.Contains(string(copied), yamlScalar(want)) || strings.Contains(string(copied), "./data/journal") {
		t.Errorf("copy path is not absolute:\n%s", copied)
	}
	if original, _ := os.ReadFile(manifestPath); string(original) != text {
		t.Errorf("original changed:\n%s", original)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "data", "journal")); len(entries) != 1 {
		t.Errorf("connect wrote into the folder: %v", entries)
	}

	// Connecting it again is already_exists; so is a manifest naming a
	// registered id in other case.
	_, err = f.registry.Connect(ConnectRequest{Manifest: manifestPath})
	if e := envelope.As(err); e == nil || e.Code != envelope.AlreadyExists {
		t.Errorf("again = %v", err)
	}

	invalid := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(invalid, []byte("database: {id: bad}\nstorage: {engine: oracle}\n"), 0o600)
	if _, err := f.registry.Connect(ConnectRequest{Manifest: invalid}); envelope.As(err) == nil || envelope.As(err).Code != envelope.InvalidArgument {
		t.Errorf("invalid manifest = %v", err)
	}
}

// With no git identity at all, connecting still registers the folder but
// says first how to let Git save changes there.
func TestConnectWithoutGitIdentitySaysHowToSetIt(t *testing.T) {
	withGlobalGitIdentity(t)
	repo := ingitdbRepo(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	if exec.Command("git", "-C", repo, "var", "GIT_AUTHOR_IDENT").Run() == nil {
		t.Skip("git works out an identity on this machine without configuration")
	}
	if !GitIdentityMissing(repo) {
		t.Fatal("GitIdentityMissing = false")
	}
	f := newRegistry(t)
	result, err := f.registry.Connect(ConnectRequest{ID: "notes", Engine: EngineInGitDB, Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	if got := commands(result.Next); len(got) < 2 || got[0] != `git config --global user.name "<your name>"` || got[1] != `git config --global user.email "<your email>"` {
		t.Errorf("next = %q", got)
	}
	if out, _ := exec.Command("git", "-C", repo, "config", "--local", "--get", "user.name").Output(); len(out) != 0 {
		t.Errorf("connect set a local identity: %s", out)
	}
	dir, ok := f.registry.GitStorage("notes")
	if !ok || dir != repo {
		t.Errorf("GitStorage = %q %v", dir, ok)
	}
}

func TestEnginesPointManifestSetupAtConnect(t *testing.T) {
	t.Parallel()
	document := NewEnginesDocument()
	data, _ := json.Marshal(document)
	if strings.Contains(string(data), "Guided connect is coming") || strings.Contains(string(data), "reload <name>") {
		t.Errorf("engines still describe the placeholder path: %s", data)
	}
	for _, engine := range document.Engines {
		if engine.Setup != SetupManifest {
			continue
		}
		if got := commands(engine.ManifestSteps); !slices.Equal(got, []string{"ovdb init --engine " + engine.ID + " --id <name>", "ovdb databases connect --manifest <absolute path>", ""}) {
			t.Errorf("%s steps = %q", engine.ID, got)
		}
	}
}

// F1: the value of any variable a manifest names never appears in an error,
// log line, list reason or mounts.json, whatever its shape (not only URLs
// with a password): connect, startup mounting and reload alike.
func TestNamedEnvironmentValuesNeverLeak(t *testing.T) {
	// openvaultdb-go reads the variable from the process environment.
	t.Setenv("JUNK_DSN", "s3cretjunk-Tok")
	f := &registryFixture{dirs: testDirs(t)}
	for _, dir := range []string{f.dirs.Home, f.dirs.Runtime} {
		if err := paths.EnsurePrivateDir(dir); err != nil {
			t.Fatal(err)
		}
	}
	text := "database:\n  id: crm\n  schema_mode: strict\nstorage:\n  engine: postgres\n  postgres:\n    dsn_env: JUNK_DSN\nschemas:\n  collections:\n    contacts:\n      fields:\n        name: {type: string}\n"
	// Mounted at startup and on reload from the registry.
	f.writeManifest(t, "crm.yaml", text)
	f.open(t)
	if _, err := f.registry.Reload(context.Background(), "crm"); err != nil {
		t.Fatal(err)
	}
	list, _ := json.Marshal(f.state(t, "crm"))
	mounts, _ := os.ReadFile(MountsPath(f.dirs.Runtime))

	// Connected from a manifest file.
	manifestPath := filepath.Join(t.TempDir(), "crm2.yaml")
	if err := os.WriteFile(manifestPath, []byte(strings.Replace(text, "id: crm", "id: crm2", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.registry.Connect(ConnectRequest{Manifest: manifestPath})
	e := envelope.As(err)
	if e == nil || e.Code != envelope.StorageUnavailable {
		t.Fatalf("connect = %v", err)
	}
	for _, where := range []struct{ name, text string }{
		{"connect error", string(envelope.MarshalError(e))}, {"list", string(list)}, {"mounts.json", string(mounts)}, {"log", f.logText()},
	} {
		if strings.Contains(where.text, "s3cretjunk") {
			t.Errorf("%s shows the variable's value: %s", where.name, where.text)
		}
	}
	if !strings.Contains(string(list), "JUNK_DSN") && !strings.Contains(string(envelope.MarshalError(e)), "JUNK_DSN") {
		t.Errorf("the variable's name is gone too: %s", list)
	}
}

func TestMaskEnvironmentValues(t *testing.T) {
	t.Parallel()
	got := maskValues(`parse "a\"b s3cret" failed: a"b s3cret; short ab`, []string{`a"b s3cret`, "ab", ""})
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "short ab") {
		t.Errorf("masked = %q", got)
	}
}

// F2: a folder that is not an inGitDB database (a code project's Git
// repository, an empty folder) is refused with invalid_argument and next
// steps, and nothing in it changes.
func TestConnectRefusesAFolderThatIsNotInGitDB(t *testing.T) {
	withGlobalGitIdentity(t)
	requireGit(t)
	f := newRegistry(t)
	project := filepath.Join(t.TempDir(), "proj")
	for name, text := range map[string]string{"src/main.go": "package main\n", "README.md": "# proj\n", "docs/a.yaml": "a: 1\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(project, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "Project"}} {
		if out, err := exec.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	empty := t.TempDir()
	for _, folder := range []string{project, empty} {
		before := snapshot(t, folder)
		_, err := f.registry.Connect(ConnectRequest{ID: "proj", Engine: EngineInGitDB, Path: folder})
		e := envelope.As(err)
		if e == nil || e.Code != envelope.InvalidArgument || !strings.Contains(e.Reason, "isn't an inGitDB database") ||
			!slices.Contains(commands(e.Next), "ovdb databases create proj") || !slices.ContainsFunc(e.Next, func(n envelope.Next) bool { return n.Action == ActionEditLocation }) {
			t.Errorf("%s: Connect = %v %+v", folder, err, e)
		}
		sameSnapshot(t, folder, before, snapshot(t, folder))
	}
	if list, _ := f.registry.List(); len(list) != 0 {
		t.Errorf("registered: %+v", list)
	}
}

// F4: YAML merge keys and anchors are resolved before paths are made
// absolute and checked, so the checked location is the mounted one and a
// relative path never lands in OVDB home.
func TestConnectManifestResolvesMergeKeys(t *testing.T) {
	t.Parallel()
	f := newRegistry(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	shop := filepath.Join(dir, "data", "shop.sqlite")
	source := sqliteFile(t, `CREATE TABLE things (id TEXT PRIMARY KEY, title TEXT)`)
	data, _ := os.ReadFile(source)
	if err := os.WriteFile(shop, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{
		"database: {id: m1, schema_mode: strict}\nstorage:\n  <<: {engine: sqlite, path: data/shop.sqlite}\nschemas:\n  collections:\n    things:\n      fields:\n        title: {type: string}\n",
		"database: {id: m2, schema_mode: strict}\nstorage:\n  <<: &engine {engine: sqlite}\n  path: &path data/shop.sqlite\nschemas:\n  collections:\n    things:\n      fields:\n        title: {type: string}\n",
	} {
		manifestPath := filepath.Join(dir, "m"+string(rune('1'+i))+".yaml")
		if err := os.WriteFile(manifestPath, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			// The same storage again, through an anchor: refused as overlapping.
			_, err := f.registry.Connect(ConnectRequest{Manifest: manifestPath})
			if e := envelope.As(err); e == nil || e.Code != envelope.InvalidArgument || !strings.Contains(e.Reason, "overlaps the storage of the database m1") {
				t.Errorf("anchor manifest = %v", err)
			}
			continue
		}
		result, err := f.registry.Connect(ConnectRequest{Manifest: manifestPath})
		if err != nil {
			t.Fatalf("merge-key manifest = %v", err)
		}
		if result.Database.Location != shop {
			t.Errorf("location = %s, want %s", result.Database.Location, shop)
		}
		copied, _ := os.ReadFile(result.Database.Manifest)
		if !strings.Contains(string(copied), yamlScalar(shop)) {
			t.Errorf("copy lacks the absolute path:\n%s", copied)
		}
	}
	found := []string{}
	_ = filepath.WalkDir(f.dirs.Home, func(path string, _ fs.DirEntry, _ error) error {
		if strings.Contains(path, ".sqlite") {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("storage created in OVDB home: %v", found)
	}
}
