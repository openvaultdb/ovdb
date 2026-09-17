package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
	"gopkg.in/yaml.v3"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// Mounter is the part of the openvaultdb-go server the registry drives.
type Mounter interface {
	Mount(db *core.Database) error
	UnmountContext(ctx context.Context, id string) error
}

// Registry is the running server's view of <home>/databases: it mounts
// every manifest at start, keeps each one's mount state, and creates and
// removes registrations while serving. Only the server holding home.lock
// owns one (local-server-and-web-console#REQ:single-server-home-lock).
type Registry struct {
	dirs   paths.Dirs
	server Mounter
	logf   func(format string, args ...any)

	mu     sync.Mutex
	mounts []MountRecord // by manifest path, sorted
}

// UnmountTimeout bounds the wait for in-flight requests when a database is
// removed or the server stops.
const UnmountTimeout = 10 * time.Second

// OpenRegistry mounts every manifest in <home>/databases on server. A
// manifest that fails to mount is recorded as needing attention with a
// redacted reason, logged, and never stops the others
// (local-server-and-web-console#REQ:registry-serving). Mounting never writes
// into user storage: catalogues live in <home>/catalogues and no git
// identity is stamped.
func OpenRegistry(dirs paths.Dirs, srv Mounter, logf func(format string, args ...any)) (*Registry, error) {
	r := &Registry{dirs: dirs, server: srv, logf: logf}
	if r.logf == nil {
		r.logf = func(string, ...any) {}
	}
	if _, err := os.Stat(RegistryDir(dirs.Home)); errors.Is(err, fs.ErrNotExist) {
		return r, r.writeMounts()
	}
	if err := paths.EnsurePrivateDir(CatalogueDir(dirs.Home)); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
		return nil, err
	}
	dbs, failures, err := mount.DirReportWithOptions(RegistryDir(dirs.Home), mount.Options{
		CatalogueDir: CatalogueDir(dirs.Home), SkipGitIdentity: true,
	})
	if err != nil {
		return nil, err
	}
	registrations, err := ReadRegistry(dirs.Home)
	if err != nil {
		closeDatabases(dbs)
		return nil, err
	}
	for _, registration := range registrations {
		record := MountRecord{ID: registration.ID, Manifest: registration.Manifest}
		if failure, failed := failures[registration.Manifest]; failed {
			record.State, record.Reason = MountNeedsAttention, mountReason(failure, registration.Manifest)
			r.logf("database %s needs attention: %s", registration.ID, record.Reason)
		} else if db, ok := dbs[registration.ID]; ok && registration.Err == nil {
			if err := srv.Mount(db); err != nil {
				_ = db.Close()
				record.State, record.Reason = MountNeedsAttention, mountReason(err, registration.Manifest)
			} else {
				record.State = MountMounted
				delete(dbs, registration.ID)
			}
		} else {
			// DirReport skips nothing it can read; a registration without a
			// mount or a failure appeared between the two reads.
			record.State, record.Reason = MountNeedsAttention, uicopy.T("database.reason.not_loaded", nil)
		}
		r.mounts = append(r.mounts, record)
	}
	closeDatabases(dbs)
	return r, r.writeMounts()
}

func closeDatabases(dbs map[string]*core.Database) {
	for _, db := range dbs {
		_ = db.Close()
	}
}

// mountReason is a mount error as people read it: redacted, without the
// manifest path the error starts with.
func mountReason(err error, manifestPath string) string {
	text := err.Error()
	for {
		trimmed := strings.TrimPrefix(text, manifestPath+": ")
		if trimmed == text {
			break
		}
		text = trimmed
	}
	return redact.String(strings.Join(strings.Fields(text), " "))
}

func (r *Registry) writeMounts() error {
	records := slices.Clone(r.mounts)
	if records == nil {
		records = []MountRecord{}
	}
	return WriteMounts(r.dirs.Runtime, Mounts{Databases: records})
}

// Mounts is the current mount state.
func (r *Registry) Mounts() *Mounts {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Mounts{Schema: envelope.Schema, Databases: slices.Clone(r.mounts)}
}

// List is the databases list with this server's mount state.
func (r *Registry) List() ([]Database, error) {
	return ListDatabases(r.dirs.Home, r.Mounts())
}

// Close unmounts every database, releasing engine resources such as SQLite
// file handles, and removes mounts.json: without a server, mount state is
// unknown.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.mounts {
		if record.State != MountMounted {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), UnmountTimeout)
		if err := r.server.UnmountContext(ctx, record.ID); err != nil && !errors.Is(err, server.ErrDatabaseNotMounted) {
			r.logf("closing database %s: %s", record.ID, redact.String(err.Error()))
		}
		cancel()
	}
	r.mounts = nil
	_ = os.Remove(MountsPath(r.dirs.Runtime))
}

// CreateRequest is the body of POST /api/local/v1/databases. Path is the
// absolute location the client resolved (by default under its data home).
type CreateRequest struct {
	ID     string `json:"id"`
	Engine string `json:"engine"`
	Path   string `json:"path"`
}

// DatabaseResult is the body of a successful create or remove, and the
// --json output of `ovdb databases create|remove`.
type DatabaseResult struct {
	Schema   int             `json:"schema"`
	Database Database        `json:"database"`
	Next     []envelope.Next `json:"next"`
}

// Next actions presentations can act on in place (envelope.Next.Action).
const (
	ActionEditName     = "edit_name"
	ActionEditLocation = "edit_location"
	ActionDatabases    = "databases"
	ActionDone         = "done"
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// DefaultPath is where a new database lives unless the person picks another
// place: <data home>/<id>/ for inGitDB, <data home>/<id>.sqlite for SQLite
// (database-setup-and-providers#REQ:create-new-database). Clients call it
// with the data home they resolved.
func DefaultPath(dataHome, engine, id string) string {
	if engine == EngineSQLite {
		return filepath.Join(dataHome, id+".sqlite")
	}
	return filepath.Join(dataHome, id)
}

func createFailed() string { return uicopy.T("database.create.failed", nil) }

// ValidateCreate checks a request without touching anything. It fills the
// default engine.
func ValidateCreate(request *CreateRequest) *envelope.Error {
	if request.Engine == "" {
		request.Engine = EngineInGitDB
	}
	if !idPattern.MatchString(request.ID) {
		return envelope.New(envelope.InvalidArgument, createFailed()).
			WithReason(uicopy.T("database.create.bad_name", map[string]string{"name": request.ID})).
			WithNext(envelope.Next{Label: uicopy.T("next.choose_name", nil), Command: "ovdb databases create notes", Action: ActionEditName})
	}
	engine, known := FindEngine(request.Engine)
	switch {
	case !known:
		return envelope.New(envelope.InvalidArgument, createFailed()).
			WithReason(uicopy.T("database.create.unknown_engine", map[string]string{"engine": request.Engine})).
			WithNext(envelope.Next{Label: uicopy.T("next.list_engines", nil), Command: "ovdb engines"})
	case engine.Setup == SetupManifest:
		return envelope.New(envelope.Unsupported, createFailed()).
			WithReason(uicopy.T("database.create.manifest_only", map[string]string{"engine": engine.Name})).
			WithNext(ManifestSteps(engine.ID)...)
	}
	if request.Path == "" || !filepath.IsAbs(request.Path) || filepath.Clean(request.Path) != request.Path {
		return envelope.New(envelope.InvalidArgument, createFailed()).
			WithReason(uicopy.T("database.create.bad_path", map[string]string{"path": request.Path})).
			WithNext(envelope.Next{Label: uicopy.T("next.choose_location", nil),
				Command: "ovdb databases create " + request.ID + " --engine " + request.Engine + " --path <absolute path>", Action: ActionEditLocation})
	}
	return nil
}

// Create registers, creates and mounts a new database without a restart.
// It never overwrites: a registered id is already_exists and an existing
// location is location_not_empty, both with nothing changed
// (database-setup-and-providers#REQ:create-never-overwrites).
func (r *Registry) Create(request CreateRequest) (DatabaseResult, error) {
	if err := ValidateCreate(&request); err != nil {
		return DatabaseResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return DatabaseResult{}, err
	}
	taken := func(id string) bool {
		return slices.ContainsFunc(registrations, func(reg Registration) bool {
			return strings.EqualFold(reg.ID, id) || strings.EqualFold(filepath.Base(reg.Manifest), id+".yaml")
		})
	}
	if taken(request.ID) {
		return DatabaseResult{}, envelope.New(envelope.AlreadyExists, createFailed()).
			WithReason(uicopy.T("database.create.already_exists", map[string]string{"name": request.ID})).
			WithNext(r.anotherName(request, taken, false), envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases})
	}
	if reason := locationInUse(request); reason != "" {
		return DatabaseResult{}, envelope.New(envelope.LocationNotEmpty, createFailed()).
			WithReason(reason).
			WithNext(r.anotherName(request, taken, true), envelope.Next{
				Label:   uicopy.T("next.choose_location", nil),
				Command: "ovdb databases create " + request.ID + engineFlag(request.Engine) + " --path <another absolute path>",
				Action:  ActionEditLocation,
			})
	}

	db, manifestPath, err := r.provision(request)
	if err != nil {
		return DatabaseResult{}, err
	}
	if err := r.server.Mount(db); err != nil {
		_ = db.Close()
		_ = os.Remove(manifestPath)
		return DatabaseResult{}, envelope.New(envelope.AlreadyExists, createFailed()).
			WithReason(uicopy.T("database.create.already_exists", map[string]string{"name": request.ID}))
	}
	r.setMount(MountRecord{ID: request.ID, Manifest: manifestPath, State: MountMounted})
	if err := r.writeMounts(); err != nil {
		r.logf("writing mounts.json: %s", redact.String(err.Error()))
	}
	r.logf("created database %s (%s)", request.ID, request.Engine)

	database := Database{ID: request.ID, Engine: request.Engine, Location: request.Path, State: MountMounted, Manifest: manifestPath}
	return DatabaseResult{Schema: envelope.Schema, Database: database, Next: CreatedNext(database)}, nil
}

// CreatedNext is what to do after creating database: for SQLite, describing
// a first collection's schema comes before anything that writes
// (database-setup-and-providers#REQ:sqlite-next-step-is-schema). Only
// implemented commands are offered; Browse data, Explore data and AI agent
// skills join as their increments land.
func CreatedNext(database Database) []envelope.Next {
	var next []envelope.Next
	if database.Engine == EngineSQLite {
		next = append(next,
			envelope.Next{Label: uicopy.T("next.describe_schema", map[string]string{"manifest": database.Manifest}), Command: "ovdb server restart"},
			envelope.Next{Label: uicopy.T("next.schema_docs", map[string]string{"url": SchemaDocsURL})})
	}
	return append(next,
		envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases},
		envelope.Next{Label: uicopy.T("next.done", nil), Action: ActionDone})
}

func engineFlag(engine string) string {
	if engine == EngineInGitDB {
		return ""
	}
	return " --engine " + engine
}

// anotherName suggests the first free <id>-N; atDefault drops a custom path,
// because the conflict was the location.
func (r *Registry) anotherName(request CreateRequest, taken func(string) bool, atDefault bool) envelope.Next {
	var suggestion string
	for n := 2; ; n++ {
		suggestion = request.ID + "-" + strconv.Itoa(n)
		candidate := request
		candidate.ID = suggestion
		candidate.Path = DefaultPath(r.dirs.Data, request.Engine, suggestion)
		if !taken(suggestion) && (!atDefault || locationInUse(candidate) == "") {
			break
		}
	}
	command := "ovdb databases create " + suggestion + engineFlag(request.Engine)
	if !atDefault && request.Path != DefaultPath(r.dirs.Data, request.Engine, request.ID) {
		command += " --path " + paths.QuoteArg(goruntime.GOOS, filepath.Join(filepath.Dir(request.Path), suggestion+filepath.Ext(request.Path)))
	}
	return envelope.Next{Label: uicopy.T("next.use_name", map[string]string{"name": suggestion}), Command: command, Action: ActionEditName}
}

// locationInUse is why request's location cannot hold a new database, or "".
func locationInUse(request CreateRequest) string {
	info, err := os.Stat(request.Path)
	if err != nil {
		// Missing is the normal case; anything else (a file in the way, no
		// permission) fails while creating, as storage_unavailable.
		return ""
	}
	params := map[string]string{"path": request.Path}
	if request.Engine == EngineInGitDB && info.IsDir() {
		entries, err := os.ReadDir(request.Path)
		if err == nil && len(entries) == 0 {
			return ""
		}
		return uicopy.T("database.create.folder_not_empty", params)
	}
	return uicopy.T("database.create.file_exists", params)
}

// provision creates the storage, writes and mounts the manifest, and moves
// it into the registry. On any failure it removes what it created.
func (r *Registry) provision(request CreateRequest) (*core.Database, string, error) {
	var created []string // removed in reverse on failure
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.RemoveAll(created[i])
		}
	}
	unavailable := func(err error) error {
		rollback()
		r.logf("creating database %s failed: %s", request.ID, redact.String(err.Error()))
		return envelope.New(envelope.StorageUnavailable, createFailed()).WithReason(redact.String(err.Error()))
	}

	for _, dir := range []string{RegistryDir(r.dirs.Home), CatalogueDir(r.dirs.Home)} {
		if err := paths.EnsurePrivateDir(dir); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
			return nil, "", unavailable(err)
		}
	}
	storageDir := request.Path
	if request.Engine == EngineSQLite {
		storageDir = filepath.Dir(request.Path)
	}
	missing, err := missingDirs(storageDir)
	if err != nil {
		return nil, "", unavailable(err)
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return nil, "", unavailable(err)
	}
	if len(missing) > 0 {
		created = append(created, missing[len(missing)-1]) // the outermost new directory
	}
	switch request.Engine {
	case EngineInGitDB:
		if len(missing) == 0 {
			// An empty folder that already existed: remove only what OVDB adds.
			created = append(created, filepath.Join(request.Path, ".git"))
		}
		// A Git history per write batch; inGitDB also works without git.
		_ = exec.Command("git", "-C", request.Path, "init", "-q").Run()
	case EngineSQLite:
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			created = append(created, request.Path+suffix)
		}
	}

	staging := filepath.Join(RegistryDir(r.dirs.Home), "."+request.ID+".creating")
	created = append(created, staging, filepath.Join(CatalogueDir(r.dirs.Home), request.ID+".inferred.json"))
	if err := paths.WriteFilePrivate(staging, []byte(newManifest(request))); err != nil {
		return nil, "", unavailable(err)
	}
	// OVDB created this storage, so the first mount may give its new Git
	// repository the identity commits need; later mounts never touch it.
	db, err := mount.FileWithOptions(staging, mount.Options{CatalogueDir: CatalogueDir(r.dirs.Home)})
	if err != nil {
		return nil, "", unavailable(errors.New(mountReason(err, staging)))
	}
	manifestPath := ManifestPath(r.dirs.Home, request.ID)
	if err := os.Rename(staging, manifestPath); err != nil {
		_ = db.Close()
		return nil, "", unavailable(err)
	}
	return db, manifestPath, nil
}

// missingDirs lists dir and its missing parents, deepest first.
func missingDirs(dir string) ([]string, error) {
	var missing []string
	for current := dir; ; {
		_, err := os.Stat(current)
		if err == nil {
			return missing, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return missing, nil
		}
		current = parent
	}
}

// newManifest is the registry manifest for a new database. SQLite accepts
// only strict mode, which needs one described collection to start: the
// manifest declares an example one and says how to describe real ones.
func newManifest(request CreateRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Registered by OVDB. See %s\n", ManifestDocsURL)
	b.WriteString("database:\n")
	fmt.Fprintf(&b, "  id: %s\n", yamlScalar(request.ID))
	if request.Engine == EngineSQLite {
		b.WriteString("  schema_mode: strict\n")
	} else {
		b.WriteString("  schema_mode: schemaless\n")
	}
	b.WriteString("storage:\n")
	fmt.Fprintf(&b, "  engine: %s\n", request.Engine)
	fmt.Fprintf(&b, "  path: %s\n", yamlScalar(request.Path))
	if request.Engine == EngineSQLite {
		fmt.Fprintf(&b, `schemas:
  collections:
    # SQLite stores records only in collections described here. Describe
    # yours like this one (%s),
    # then run: ovdb server restart
    example:
      fields:
        title: {type: string, required: true}
`, SchemaDocsURL)
	}
	return b.String()
}

func yamlScalar(s string) string {
	data, _ := yaml.Marshal(s)
	return strings.TrimSuffix(string(data), "\n")
}

func (r *Registry) setMount(record MountRecord) {
	r.mounts = slices.DeleteFunc(r.mounts, func(m MountRecord) bool { return m.Manifest == record.Manifest })
	r.mounts = append(r.mounts, record)
	slices.SortStableFunc(r.mounts, func(a, b MountRecord) int { return strings.Compare(a.ID, b.ID) })
}

// Remove unregisters database id without deleting its data, and says where
// the data remains (database-setup-and-providers#REQ:list-and-remove).
func (r *Registry) Remove(ctx context.Context, id string) (DatabaseResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return DatabaseResult{}, err
	}
	index := slices.IndexFunc(registrations, func(reg Registration) bool { return reg.ID == id })
	if index < 0 {
		return DatabaseResult{}, envelope.New(envelope.NotFound, uicopy.T("database.remove.failed", nil)).
			WithReason(uicopy.T("database.remove.not_found", map[string]string{"name": id})).
			WithNext(envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases})
	}
	registration := registrations[index]
	database := registration.Describe()

	if slices.ContainsFunc(r.mounts, func(m MountRecord) bool { return m.Manifest == registration.Manifest && m.State == MountMounted }) {
		unmountCtx, cancel := context.WithTimeout(ctx, UnmountTimeout)
		err := r.server.UnmountContext(unmountCtx, id)
		cancel()
		if err != nil && !errors.Is(err, server.ErrDatabaseNotMounted) {
			// The database is no longer served either way; it closes once
			// its last request finishes.
			r.logf("closing database %s: %s", id, redact.String(err.Error()))
		}
	}
	if err := os.Remove(registration.Manifest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return DatabaseResult{}, err
	}
	_ = os.Remove(filepath.Join(CatalogueDir(r.dirs.Home), id+".inferred.json"))
	r.mounts = slices.DeleteFunc(r.mounts, func(m MountRecord) bool { return m.Manifest == registration.Manifest })
	if err := r.writeMounts(); err != nil {
		r.logf("writing mounts.json: %s", redact.String(err.Error()))
	}
	r.logf("removed database %s (data kept)", id)
	return NewRemovedResult(database), nil
}

// NewRemovedResult is the result of removing database.
func NewRemovedResult(database Database) DatabaseResult {
	database.State = ""
	return DatabaseResult{Schema: envelope.Schema, Database: database, Next: []envelope.Next{
		{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases},
		{Label: uicopy.T("home.menu.create_database", nil), Command: "ovdb databases create <name>"},
	}}
}
