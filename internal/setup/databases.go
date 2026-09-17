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
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
	"gopkg.in/yaml.v3"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
)

// Mounter is the part of the openvaultdb-go server the registry drives.
type Mounter interface {
	Mount(db *core.Database) error
	UnmountContext(ctx context.Context, id string) error
}

// Registry is the running server's view of <home>/databases: it mounts
// every manifest, keeps each one's mount state, and creates, reloads and
// removes registrations while serving. Only the server holding home.lock
// owns one (local-server-and-web-console#REQ:single-server-home-lock).
//
// Database ids are unique ignoring case (registry files live on file
// systems that may ignore case); commands name a database by its exact id,
// or by any casing when exactly one registered id matches.
type Registry struct {
	dirs   paths.Dirs
	server Mounter
	logf   func(format string, args ...any)
	// MountTimeout bounds each database's mount, so one unreachable
	// storage never holds up the others or the server.
	mountTimeout time.Duration

	// getenv reads the server's environment (connection variables).
	getenv func(string) string
	// beforeCommit, when set, runs after a connect mounted its storage and
	// before it takes the lock to register it (tests).
	beforeCommit func()

	mu     sync.Mutex
	mounts []MountRecord  // sorted by id
	gen    map[string]int // by manifest: bumps whenever a record is replaced
	closed bool
}

// Timeouts.
const (
	// UnmountTimeout bounds the wait for in-flight requests when a database
	// is removed, reloaded or the server stops.
	UnmountTimeout = 10 * time.Second
	// DefaultMountTimeout bounds one database's mount.
	DefaultMountTimeout = 5 * time.Second
)

// RegistryOptions tune a registry; the zero value is the default.
type RegistryOptions struct {
	MountTimeout time.Duration
	// Getenv reads the server's environment; os.Getenv when nil.
	Getenv func(string) string
}

// OpenRegistry reads <home>/databases and records every registration as
// mounting, without mounting anything: call MountAll once the server
// listens. It never blocks on storage.
func OpenRegistry(dirs paths.Dirs, srv Mounter, logf func(format string, args ...any), opts RegistryOptions) (*Registry, error) {
	r := &Registry{dirs: dirs, server: srv, logf: logf, mountTimeout: opts.MountTimeout, getenv: opts.Getenv, gen: map[string]int{}}
	if r.getenv == nil {
		r.getenv = os.Getenv
	}
	if r.logf == nil {
		r.logf = func(string, ...any) {}
	}
	if r.mountTimeout <= 0 {
		r.mountTimeout = DefaultMountTimeout
	}
	sweepStaging(dirs.Home)
	registrations, err := ReadRegistry(dirs.Home)
	if err != nil {
		return nil, err
	}
	for _, registration := range registrations {
		r.setMount(MountRecord{ID: registration.ID, Manifest: registration.Manifest, State: MountMounting})
	}
	return r, r.writeMounts()
}

// sweepStaging removes manifests a crashed create left half-written.
func sweepStaging(home string) {
	entries, _ := os.ReadDir(RegistryDir(home))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".creating") {
			_ = os.Remove(filepath.Join(RegistryDir(home), entry.Name()))
		}
	}
}

// MountAll mounts every registration still mounting, each with its own
// deadline and in parallel, and returns when all are settled or ctx ends
// (a server stopping while storage is unreachable). A database that fails,
// times out or whose storage is missing needs attention, with a redacted
// reason in status, mounts.json and server.log
// (local-server-and-web-console#REQ:registry-serving).
func (r *Registry) MountAll(ctx context.Context) {
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		r.logf("reading the registry: %s", redact.String(err.Error()))
		return
	}
	if len(registrations) > 0 {
		if err := paths.EnsurePrivateDir(CatalogueDir(r.dirs.Home)); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
			r.logf("creating %s: %s", CatalogueDir(r.dirs.Home), redact.String(err.Error()))
		}
	}
	r.mu.Lock()
	var pending []Registration
	for _, registration := range registrations {
		if record, ok := r.record(registration.Manifest); ok && record.State == MountMounting {
			pending = append(pending, registration)
		}
	}
	r.mu.Unlock()
	r.mountEach(ctx, pending)
}

// mountEach mounts registrations in parallel and records each outcome.
func (r *Registry) mountEach(ctx context.Context, registrations []Registration) {
	var wg sync.WaitGroup
	for _, registration := range registrations {
		r.mu.Lock()
		gen := r.gen[registration.Manifest]
		r.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.mountOne(ctx, registration, gen)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

type mountOutcome struct {
	db  *core.Database
	err error
}

// mountOne mounts one registration within the mount deadline and records
// the result for generation gen; a record replaced or removed meanwhile
// wins, and a late database is closed.
func (r *Registry) mountOne(ctx context.Context, registration Registration, gen int) {
	record := MountRecord{ID: registration.ID, Manifest: registration.Manifest, State: MountNeedsAttention}
	if reason := r.checkRegistration(registration); reason != "" {
		record.Reason = reason
		r.settle(record, gen, nil)
		return
	}
	outcome := make(chan mountOutcome, 1)
	go func() {
		db, err := mount.FileWithOptions(registration.Manifest, mount.Options{CatalogueDir: CatalogueDir(r.dirs.Home), SkipGitIdentity: true})
		outcome <- mountOutcome{db, err}
	}()
	timer := time.NewTimer(r.mountTimeout)
	defer timer.Stop()
	select {
	case result := <-outcome:
		if result.err != nil {
			record.Reason = mountReason(result.err, registration.Manifest, EnvironmentValues(registration.Parsed, r.getenv)...)
			r.settle(record, gen, nil)
			return
		}
		record.State = MountMounted
		r.settle(record, gen, result.db)
	case <-timer.C:
		record.Reason = uicopy.T("database.reason.timeout", map[string]string{"seconds": strconv.Itoa(int(r.mountTimeout.Seconds()))})
		if r.mountTimeout < time.Second {
			record.Reason = uicopy.T("database.reason.timeout", map[string]string{"seconds": "1"})
		}
		r.settle(record, gen, nil)
		go closeWhenDone(outcome)
	case <-ctx.Done():
		go closeWhenDone(outcome)
	}
}

func closeWhenDone(outcome <-chan mountOutcome) {
	if result := <-outcome; result.db != nil {
		_ = result.db.Close()
	}
}

// checkRegistration is why a registration cannot be mounted before trying:
// a manifest that does not parse, an id used twice, or local storage that is
// missing. Mounting would silently create missing storage empty.
func (r *Registry) checkRegistration(registration Registration) string {
	if registration.Parsed == nil {
		return mountReason(registration.Err, registration.Manifest)
	}
	registrations, _ := ReadRegistry(r.dirs.Home)
	for _, other := range registrations {
		if other.Manifest != registration.Manifest && strings.EqualFold(other.ID, registration.ID) && other.Manifest < registration.Manifest {
			return uicopy.T("database.reason.duplicate", map[string]string{"name": registration.ID, "manifest": other.Manifest})
		}
	}
	if path, ok := localStorage(registration.Parsed, filepath.Dir(registration.Manifest)); ok {
		if _, err := os.Stat(path); err != nil {
			return uicopy.T("database.reason.storage_missing", map[string]string{"path": path, "name": registration.ID})
		}
	}
	return ""
}

// localStorage is the file or folder a local engine keeps its data in.
func localStorage(m *manifest.Manifest, baseDir string) (string, bool) {
	switch {
	case m.Storage.Engine == EngineSQLite:
	case m.Storage.Engine == EngineInGitDB && (m.Storage.InGitDB == nil || m.Storage.InGitDB.GitHub == nil):
	default:
		return "", false
	}
	return Location(m, baseDir), true
}

// settle records a mount outcome and serves db, unless the record changed
// since generation gen or the registry closed.
func (r *Registry) settle(record MountRecord, gen int, db *core.Database) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.record(record.Manifest); !ok || r.closed || r.gen[record.Manifest] != gen {
		if db != nil {
			_ = db.Close()
		}
		return
	}
	if db != nil {
		if err := r.server.Mount(db); err != nil {
			_ = db.Close()
			record.State, record.Reason = MountNeedsAttention, mountReason(err, record.Manifest)
		}
	}
	if record.State == MountNeedsAttention {
		r.logf("database %s needs attention: %s", record.ID, record.Reason)
	}
	r.setMountSameGen(record)
	if err := r.writeMounts(); err != nil {
		r.logf("writing mounts.json: %s", redact.String(err.Error()))
	}
}

// mountReason is a mount error as people read it: the values of the
// manifest's variables (secrets) masked, redacted, on one line, without the
// manifest path the error starts with.
func mountReason(err error, manifestPath string, secrets ...string) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for {
		trimmed := strings.TrimPrefix(text, manifestPath+": ")
		if trimmed == text {
			break
		}
		text = trimmed
	}
	return redact.String(maskValues(strings.Join(strings.Fields(text), " "), secrets))
}

func (r *Registry) writeMounts() error {
	records := slices.Clone(r.mounts)
	if records == nil {
		records = []MountRecord{}
	}
	return WriteMounts(r.dirs.Runtime, Mounts{Databases: records})
}

func (r *Registry) record(manifestPath string) (MountRecord, bool) {
	for _, record := range r.mounts {
		if record.Manifest == manifestPath {
			return record, true
		}
	}
	return MountRecord{}, false
}

// setMount replaces the record for its manifest as a new generation.
func (r *Registry) setMount(record MountRecord) {
	r.gen[record.Manifest]++
	r.setMountSameGen(record)
}

func (r *Registry) setMountSameGen(record MountRecord) {
	r.mounts = slices.DeleteFunc(r.mounts, func(m MountRecord) bool { return m.Manifest == record.Manifest })
	r.mounts = append(r.mounts, record)
	slices.SortStableFunc(r.mounts, func(a, b MountRecord) int { return strings.Compare(a.ID, b.ID) })
}

func (r *Registry) dropMount(manifestPath string) {
	r.gen[manifestPath]++
	r.mounts = slices.DeleteFunc(r.mounts, func(m MountRecord) bool { return m.Manifest == manifestPath })
}

// AwaitMount waits while database id is still mounting after a start or
// reload, up to its mount deadline, so a data request that arrives right after
// a command auto-started the server finds the database served rather than
// not found (database-context-navigation#REQ:data-commands-use-server). It
// returns at once for a database in any other state, or none.
func (r *Registry) AwaitMount(ctx context.Context, id string) {
	deadline := time.Now().Add(r.mountTimeout + time.Second)
	for {
		r.mu.Lock()
		mounting := false
		for _, record := range r.mounts {
			if strings.EqualFold(record.ID, id) && record.State == MountMounting {
				mounting = true
			}
		}
		r.mu.Unlock()
		if !mounting || !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
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
// unknown. Mounts still in flight are closed when they finish.
func (r *Registry) Close() {
	r.mu.Lock()
	records := r.mounts
	r.mounts, r.closed = nil, true
	r.mu.Unlock()
	for _, record := range records {
		if record.State == MountMounted {
			r.unmount(record.ID)
		}
	}
	_ = os.Remove(MountsPath(r.dirs.Runtime))
}

func (r *Registry) unmount(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), UnmountTimeout)
	defer cancel()
	if err := r.server.UnmountContext(ctx, id); err != nil && !errors.Is(err, server.ErrDatabaseNotMounted) {
		// No longer served either way; it closes once its last request ends.
		r.logf("closing database %s: %s", id, redact.String(err.Error()))
	}
}

// CreateRequest is the body of POST /api/local/v1/databases. Path is the
// absolute location the client resolved (by default under its data home).
type CreateRequest struct {
	ID     string `json:"id"`
	Engine string `json:"engine"`
	Path   string `json:"path"`
}

// DatabaseResult is the body of a successful create, reload or remove, and
// the --json output of `ovdb databases create|reload|remove`.
type DatabaseResult struct {
	Schema   int             `json:"schema"`
	Database Database        `json:"database"`
	Next     []envelope.Next `json:"next"`
}

// Next actions presentations can act on in place (envelope.Next.Action).
// For edit_name, a command naming a database (`ovdb databases create
// notes-2`) carries the suggested name: see SuggestedName.
const (
	ActionEditName     = "edit_name"
	ActionEditLocation = "edit_location"
	ActionDatabases    = "databases"
	ActionDone         = "done"
	// ActionUse makes the database current: for this project in the TUI,
	// as the default for all projects in the web console (parity E3).
	ActionUse = "use"
	// ActionBrowse opens Browse data for the database.
	ActionBrowse = "browse"
)

// SuggestedName is the name an edit_name next action suggests, or "".
func SuggestedName(next envelope.Next) string {
	fields := strings.Fields(next.Command)
	if next.Action != ActionEditName || len(fields) < 4 || fields[1] != "databases" || fields[2] != "create" || strings.HasPrefix(fields[3], "<") {
		return ""
	}
	return fields[3]
}

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

func chooseLocation(request CreateRequest) envelope.Next {
	return envelope.Next{Label: uicopy.T("next.choose_location", nil),
		Command: "ovdb databases create " + request.ID + engineFlag(request.Engine) + " --path <another absolute path>", Action: ActionEditLocation}
}

// ValidateCreate checks a request without touching anything. It fills the
// default engine and normalizes the location with this OS's path rules
// (so C:/Users/… is absolute on Windows, and a trailing separator is fine).
func ValidateCreate(request *CreateRequest) *envelope.Error {
	if request.Engine == "" {
		request.Engine = EngineInGitDB
	}
	if !idPattern.MatchString(request.ID) {
		return envelope.New(envelope.InvalidArgument, createFailed()).
			WithReason(uicopy.T("database.create.bad_name", map[string]string{"name": request.ID})).
			WithNext(envelope.Next{Label: uicopy.T("next.choose_name", nil), Command: "ovdb databases create <name>", Action: ActionEditName})
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
	switch {
	case request.Path == "":
		return envelope.New(envelope.InvalidArgument, createFailed()).
			WithReason(uicopy.T("database.create.no_path", nil)).WithNext(chooseLocation(*request))
	case !filepath.IsAbs(request.Path):
		return envelope.New(envelope.InvalidArgument, createFailed()).
			WithReason(uicopy.T("database.create.bad_path", map[string]string{"path": request.Path})).WithNext(chooseLocation(*request))
	}
	request.Path = filepath.Clean(request.Path)
	return nil
}

// Create registers, creates and mounts a new database without a restart.
// It never overwrites: a registered id is already_exists and a location in
// use is location_not_empty, both with nothing changed
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
	registeredAs := func(id string) string {
		for _, reg := range registrations {
			if strings.EqualFold(reg.ID, id) || strings.EqualFold(filepath.Base(reg.Manifest), id+".yaml") {
				return reg.ID
			}
		}
		return ""
	}
	if existing := registeredAs(request.ID); existing != "" {
		return DatabaseResult{}, envelope.New(envelope.AlreadyExists, createFailed()).
			WithReason(uicopy.T("database.create.already_exists", map[string]string{"name": existing})).
			WithNext(r.anotherName(request, registeredAs), envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases})
	}
	if reason := r.locationInUse(request, registrations); reason != "" {
		return DatabaseResult{}, envelope.New(envelope.LocationNotEmpty, createFailed()).
			WithReason(reason).
			WithNext(chooseLocation(request),
				envelope.Next{Label: uicopy.T("next.done", nil), Action: ActionDone})
	}

	manifestPath, err := r.provision(request)
	if err != nil {
		return DatabaseResult{}, err
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
// the data comes before anything that writes
// (database-setup-and-providers#REQ:sqlite-next-step-is-schema).
func CreatedNext(database Database) []envelope.Next {
	return resultNext(database, database.Engine == EngineSQLite)
}

// resultNext is the Result of creating or connecting database
// (database-setup-and-providers#REQ:create-result-next-actions). Only
// implemented commands are offered; Explore data and AI agent skills join
// as their increments land.
func resultNext(database Database, describeSchema bool) []envelope.Next {
	var next []envelope.Next
	if describeSchema {
		next = append(next,
			envelope.Next{Label: uicopy.T("next.describe_schema", map[string]string{"manifest": database.Manifest}), Command: "ovdb databases reload " + database.ID},
			envelope.Next{Label: uicopy.T("next.schema_docs", map[string]string{"url": SchemaDocsURL})})
	}
	return append(next,
		envelope.Next{Label: uicopy.T("home.menu.browse", nil), Command: "ovdb list / --db " + database.ID, Action: ActionBrowse},
		envelope.Next{Label: uicopy.T("next.use_in_project", nil), Command: "ovdb use " + database.ID, Action: ActionUse},
		envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases},
		envelope.Next{Label: uicopy.T("next.done", nil), Action: ActionDone})
}

func engineFlag(engine string) string {
	if engine == EngineInGitDB || engine == "" {
		return ""
	}
	return " --engine " + engine
}

// anotherName suggests the first free <id>-N at its default location (or
// next to the custom one).
func (r *Registry) anotherName(request CreateRequest, registeredAs func(string) string) envelope.Next {
	custom := request.Path != DefaultPath(r.dirs.Data, request.Engine, request.ID)
	var suggestion, path string
	for n := 2; ; n++ {
		suggestion = request.ID + "-" + strconv.Itoa(n)
		path = DefaultPath(r.dirs.Data, request.Engine, suggestion)
		if custom {
			path = filepath.Join(filepath.Dir(request.Path), suggestion+filepath.Ext(request.Path))
		}
		if _, err := os.Stat(path); registeredAs(suggestion) == "" && errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	command := "ovdb databases create " + suggestion + engineFlag(request.Engine)
	if custom {
		command += " --path " + paths.QuoteArg(goruntime.GOOS, path)
	}
	return envelope.Next{Label: uicopy.T("next.use_name", map[string]string{"name": suggestion}), Command: command, Action: ActionEditName}
}

// resolved is path with symlinks in its deepest existing ancestor resolved,
// so two spellings of one place compare equal.
func resolved(path string) string {
	path = filepath.Clean(path)
	var rest []string
	for current := path; ; {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(append([]string{real}, rest...)...)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		rest = append([]string{filepath.Base(current)}, rest...)
		current = parent
	}
}

// within reports whether path is dir or inside it, ignoring case where the
// usual file systems do (Windows and macOS), so two spellings of one place
// overlap.
func within(path, dir string) bool {
	if goruntime.GOOS == "windows" || goruntime.GOOS == "darwin" {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// locationInUse is why request's location cannot hold a new database, or "":
// it overlaps OVDB's own folders or another database's storage, or it holds
// data (a folder with files, an existing file or SQLite sidecar).
func (r *Registry) locationInUse(request CreateRequest, registrations []Registration) string {
	if reason := r.locationOverlaps(request, registrations); reason != "" {
		return reason
	}
	return locationHoldsData(request)
}

// locationOverlaps is why request's location cannot hold a database because
// it overlaps OVDB's own folders or another database's storage, or "".
func (r *Registry) locationOverlaps(request CreateRequest, registrations []Registration) string {
	target := resolved(request.Path)
	for _, own := range []string{r.dirs.Home, r.dirs.Runtime} {
		if dir := resolved(own); within(target, dir) || within(dir, target) {
			return uicopy.T("database.create.location_ovdb", map[string]string{"path": request.Path})
		}
	}
	for _, reg := range registrations {
		if reg.Parsed == nil {
			continue
		}
		if storage, ok := localStorage(reg.Parsed, filepath.Dir(reg.Manifest)); ok {
			if dir := resolved(storage); within(target, dir) || within(dir, target) {
				return uicopy.T("database.create.location_overlaps", map[string]string{"path": request.Path, "name": reg.ID})
			}
		}
	}
	return ""
}

// locationHoldsData is why request's location already holds data (a folder
// with files, an existing file or SQLite sidecar), or "".
func locationHoldsData(request CreateRequest) string {
	params := map[string]string{"path": request.Path}
	info, err := os.Stat(request.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		// A file in the way or no permission fails while creating, with
		// plain words (storageUnavailable).
	case request.Engine == EngineInGitDB && info.IsDir():
		if entries, err := os.ReadDir(request.Path); err != nil || len(entries) > 0 {
			return uicopy.T("database.create.folder_not_empty", params)
		}
	default:
		return uicopy.T("database.create.file_exists", params)
	}
	if request.Engine == EngineSQLite {
		for _, suffix := range sqliteSidecars {
			if _, err := os.Lstat(request.Path + suffix); err == nil {
				return uicopy.T("database.create.file_exists", map[string]string{"path": request.Path + suffix})
			}
		}
	}
	return ""
}

var sqliteSidecars = []string{"-journal", "-wal", "-shm"}

// storageUnavailable is a create failure in plain words, always with the
// remedy of another location.
func storageUnavailable(request CreateRequest, err error) *envelope.Error {
	params := map[string]string{"path": request.Path, "error": redact.String(err.Error())}
	reason := uicopy.T("database.create.storage_failed", params)
	switch {
	case errors.Is(err, fs.ErrPermission):
		reason = uicopy.T("database.create.storage_permission", params)
	case errors.Is(err, fs.ErrExist), strings.Contains(err.Error(), "not a directory"):
		reason = uicopy.T("database.create.storage_in_the_way", params)
	}
	return envelope.New(envelope.StorageUnavailable, createFailed()).WithReason(reason).WithNext(chooseLocation(request))
}

// provision creates the storage, writes and mounts the manifest, moves it
// into the registry and serves the database. On any failure it removes
// exactly what it created.
func (r *Registry) provision(request CreateRequest) (string, error) {
	var created []string // removed in reverse on failure
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.RemoveAll(created[i])
		}
	}
	fail := func(err *envelope.Error, cause error) error {
		rollback()
		r.logf("creating database %s failed: %s", request.ID, redact.String(cause.Error()))
		return err
	}
	unavailable := func(err error) error { return fail(storageUnavailable(request, err), err) }
	createdIfMissing := func(path string) {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			created = append(created, path)
		}
	}

	for _, dir := range []string{RegistryDir(r.dirs.Home), CatalogueDir(r.dirs.Home)} {
		if err := paths.EnsurePrivateDir(dir); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
			return "", unavailable(err)
		}
	}
	storageDir := request.Path
	if request.Engine == EngineSQLite {
		storageDir = filepath.Dir(request.Path)
	}
	missing, err := missingDirs(storageDir)
	if err != nil {
		return "", unavailable(err)
	}
	if len(missing) > 0 {
		created = append(created, missing[len(missing)-1]) // the outermost new directory
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return "", unavailable(err)
	}
	switch request.Engine {
	case EngineInGitDB:
		if len(missing) == 0 {
			// An empty folder that already existed: remove only what OVDB adds.
			for _, name := range []string{".git", ".ingitdb"} {
				createdIfMissing(filepath.Join(request.Path, name))
			}
		}
		// A Git history per write batch; inGitDB also works without git.
		_ = exec.Command("git", "-C", request.Path, "init", "-q").Run()
	case EngineSQLite:
		createdIfMissing(request.Path)
		for _, suffix := range sqliteSidecars {
			createdIfMissing(request.Path + suffix)
		}
	}

	staging := filepath.Join(RegistryDir(r.dirs.Home), "."+request.ID+".creating")
	catalogue := filepath.Join(CatalogueDir(r.dirs.Home), request.ID+".inferred.json")
	createdIfMissing(catalogue)
	created = append(created, staging)
	if err := paths.WriteFilePrivate(staging, []byte(newManifest(request))); err != nil {
		return "", unavailable(err)
	}
	// OVDB created this storage, so the first mount may give its new Git
	// repository the identity commits need; later mounts never touch it.
	db, err := mount.FileWithOptions(staging, mount.Options{CatalogueDir: CatalogueDir(r.dirs.Home)})
	if err != nil {
		return "", unavailable(errors.New(mountReason(err, staging)))
	}
	manifestPath := ManifestPath(r.dirs.Home, request.ID)
	if err := os.Rename(staging, manifestPath); err != nil {
		_ = db.Close()
		return "", unavailable(err)
	}
	created = append(created, manifestPath)
	if err := r.server.Mount(db); err != nil {
		_ = db.Close()
		return "", fail(envelope.New(envelope.Internal, createFailed()).
			WithReason(uicopy.T("database.create.serve_failed", map[string]string{"name": request.ID, "error": redact.String(err.Error())})).
			WithNext(envelope.Next{Label: uicopy.T("next.restart_server", nil), Command: "ovdb server restart"}), err)
	}
	return manifestPath, nil
}

// Reconnect registers and serves an existing inGitDB folder without
// touching its files: a database OVDB created and a person later removed
// from OVDB (its data kept), such as the TODO demo installed again into its
// own folder. Connecting any other storage is `ovdb databases connect`.
func (r *Registry) Reconnect(request CreateRequest) (DatabaseResult, error) {
	request.Engine = EngineInGitDB
	if err := ValidateCreate(&request); err != nil {
		return DatabaseResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return DatabaseResult{}, err
	}
	if _, ok := find(registrations, request.ID); ok || slices.ContainsFunc(registrations, func(reg Registration) bool {
		return strings.EqualFold(filepath.Base(reg.Manifest), request.ID+".yaml")
	}) {
		return DatabaseResult{}, envelope.New(envelope.AlreadyExists, createFailed()).
			WithReason(uicopy.T("database.create.already_exists", map[string]string{"name": request.ID}))
	}
	if reason := r.locationOverlaps(request, registrations); reason != "" {
		return DatabaseResult{}, envelope.New(envelope.LocationNotEmpty, createFailed()).WithReason(reason)
	}
	if info, err := os.Stat(request.Path); err != nil || !info.IsDir() {
		if err == nil {
			err = fs.ErrExist
		}
		return DatabaseResult{}, storageUnavailable(request, err)
	}
	for _, dir := range []string{RegistryDir(r.dirs.Home), CatalogueDir(r.dirs.Home)} {
		if err := paths.EnsurePrivateDir(dir); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
			return DatabaseResult{}, storageUnavailable(request, err)
		}
	}
	staging := filepath.Join(RegistryDir(r.dirs.Home), "."+request.ID+".creating")
	if err := paths.WriteFilePrivate(staging, []byte(newManifest(request))); err != nil {
		return DatabaseResult{}, storageUnavailable(request, err)
	}
	db, err := mount.FileWithOptions(staging, mount.Options{CatalogueDir: CatalogueDir(r.dirs.Home), SkipGitIdentity: true})
	if err != nil {
		_ = os.Remove(staging)
		return DatabaseResult{}, storageUnavailable(request, errors.New(mountReason(err, staging)))
	}
	manifestPath := ManifestPath(r.dirs.Home, request.ID)
	if err := os.Rename(staging, manifestPath); err != nil {
		_ = db.Close()
		_ = os.Remove(staging)
		return DatabaseResult{}, storageUnavailable(request, err)
	}
	if err := r.server.Mount(db); err != nil {
		_ = db.Close()
		_ = os.Remove(manifestPath)
		return DatabaseResult{}, envelope.New(envelope.Internal, createFailed()).
			WithReason(uicopy.T("database.create.serve_failed", map[string]string{"name": request.ID, "error": redact.String(err.Error())}))
	}
	r.setMount(MountRecord{ID: request.ID, Manifest: manifestPath, State: MountMounted})
	if err := r.writeMounts(); err != nil {
		r.logf("writing mounts.json: %s", redact.String(err.Error()))
	}
	r.logf("registered database %s again (%s)", request.ID, request.Engine)
	database := Database{ID: request.ID, Engine: request.Engine, Location: request.Path, State: MountMounted, Manifest: manifestPath}
	return DatabaseResult{Schema: envelope.Schema, Database: database, Next: CreatedNext(database)}, nil
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
// manifest declares a placeholder one and says how to describe real ones.
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
    # SQLite stores records only in collections described here. "example"
    # is a placeholder: rename it and describe your fields (%s),
    # then run: ovdb databases reload %s
    example:
      fields:
        title: {type: string, required: true}
`, SchemaDocsURL, request.ID)
	}
	return b.String()
}

func yamlScalar(s string) string {
	data, _ := yaml.Marshal(s)
	return strings.TrimSuffix(string(data), "\n")
}

// find is the registration named id: the exact id, or the only one that
// matches ignoring case.
func find(registrations []Registration, id string) (Registration, bool) {
	var folded []Registration
	for _, reg := range registrations {
		if reg.ID == id {
			return reg, true
		}
		if strings.EqualFold(reg.ID, id) {
			folded = append(folded, reg)
		}
	}
	if len(folded) == 1 {
		return folded[0], true
	}
	return Registration{}, false
}

func notFound(message, id string) *envelope.Error {
	return envelope.New(envelope.NotFound, message).
		WithReason(uicopy.T("database.remove.not_found", map[string]string{"name": id})).
		WithNext(envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases})
}

// Remove unregisters database id without deleting its data, and says where
// the data remains (database-setup-and-providers#REQ:list-and-remove). The
// manifest goes first, so a failure leaves the database registered and
// served; the drain of in-flight requests happens without the registry
// lock, so status and lists never wait on it.
func (r *Registry) Remove(ctx context.Context, id string) (DatabaseResult, error) {
	r.mu.Lock()
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		r.mu.Unlock()
		return DatabaseResult{}, err
	}
	registration, ok := find(registrations, id)
	if !ok {
		r.mu.Unlock()
		return DatabaseResult{}, notFound(uicopy.T("database.remove.failed", nil), id)
	}
	record, _ := r.record(registration.Manifest)
	if err := os.Remove(registration.Manifest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.mu.Unlock()
		return DatabaseResult{}, envelope.New(envelope.StorageUnavailable, uicopy.T("database.remove.failed", nil)).
			WithReason(uicopy.T("database.remove.manifest_failed", map[string]string{"manifest": registration.Manifest, "error": redact.String(err.Error())}))
	}
	r.dropMount(registration.Manifest)
	if err := r.writeMounts(); err != nil {
		r.logf("writing mounts.json: %s", redact.String(err.Error()))
	}
	r.mu.Unlock()

	if record.State == MountMounted {
		r.unmount(registration.ID)
	}
	_ = os.Remove(filepath.Join(CatalogueDir(r.dirs.Home), registration.ID+".inferred.json"))
	// No project or default keeps pointing at a database OVDB no longer serves.
	if err := dbcontext.ClearDatabase(r.dirs.Home, registration.ID); err != nil {
		r.logf("clearing contexts for %s: %s", registration.ID, redact.String(err.Error()))
	}
	r.logf("removed database %s (data kept)", registration.ID)
	return NewRemovedResult(registration.Describe()), nil
}

// NewRemovedResult is the result of removing database.
func NewRemovedResult(database Database) DatabaseResult {
	database.State = ""
	return DatabaseResult{Schema: envelope.Schema, Database: database, Next: []envelope.Next{
		{Label: uicopy.T("home.menu.create_database", nil), Command: "ovdb databases create <name>"},
		{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases},
		{Label: uicopy.T("next.done", nil), Action: ActionDone},
	}}
}

// Reload mounts database id again from its manifest, so an edited manifest
// (a SQLite schema, a fixed connection variable) or restored storage takes
// effect without restarting the server.
func (r *Registry) Reload(ctx context.Context, id string) (DatabaseResult, error) {
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return DatabaseResult{}, err
	}
	registration, ok := find(registrations, id)
	if !ok {
		return DatabaseResult{}, notFound(uicopy.T("database.reload.failed", nil), id)
	}
	r.reload(ctx, []Registration{registration})
	list, err := r.List()
	if err != nil {
		return DatabaseResult{}, err
	}
	for _, db := range list {
		if db.Manifest == registration.Manifest {
			return DatabaseResult{Schema: envelope.Schema, Database: db, Next: ReloadedNext(db)}, nil
		}
	}
	return DatabaseResult{}, notFound(uicopy.T("database.reload.failed", nil), id)
}

// ReloadAll reloads every registration, picks up manifests added to
// <home>/databases by hand and forgets ones deleted by hand.
func (r *Registry) ReloadAll(ctx context.Context) (DatabasesDocument, error) {
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return DatabasesDocument{}, err
	}
	r.mu.Lock()
	var gone []MountRecord
	for _, record := range r.mounts {
		if !slices.ContainsFunc(registrations, func(reg Registration) bool { return reg.Manifest == record.Manifest }) {
			gone = append(gone, record)
			r.dropMount(record.Manifest)
		}
	}
	r.mu.Unlock()
	for _, record := range gone {
		if record.State == MountMounted {
			r.unmount(record.ID)
		}
	}
	r.reload(ctx, registrations)
	list, err := r.List()
	return NewDatabasesDocument(list), err
}

// reload unmounts each registration's current database (without the lock)
// and mounts it again as a new generation.
func (r *Registry) reload(ctx context.Context, registrations []Registration) {
	r.mu.Lock()
	var mounted []string
	for _, registration := range registrations {
		if record, ok := r.record(registration.Manifest); ok && record.State == MountMounted {
			mounted = append(mounted, record.ID)
		}
		r.setMount(MountRecord{ID: registration.ID, Manifest: registration.Manifest, State: MountMounting})
	}
	r.mu.Unlock()
	for _, id := range mounted {
		r.unmount(id)
	}
	r.mountEach(ctx, registrations)
}

// ReloadedNext is what to do after a reload.
func ReloadedNext(database Database) []envelope.Next {
	next := []envelope.Next{}
	if database.State == MountNeedsAttention {
		next = append(next,
			envelope.Next{Label: uicopy.T("next.reload_again", nil), Command: "ovdb databases reload " + database.ID},
			envelope.Next{Label: uicopy.T("next.remove_database", nil), Command: "ovdb databases remove " + database.ID})
	}
	return append(next, envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases})
}
