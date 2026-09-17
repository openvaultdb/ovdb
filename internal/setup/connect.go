package setup

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // read-only look at an existing SQLite file's tables

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// ConnectRequest is the body of POST /api/local/v1/databases/connect: an
// existing inGitDB folder or SQLite file (ID, Engine and Path), or a
// manifest file (Manifest alone) that names its database and storage
// (database-setup-and-providers#REQ:connect-existing-storage,
// REQ:connect-with-manifest). Paths are absolute: the client resolves them.
type ConnectRequest struct {
	ID       string `json:"id,omitempty"`
	Engine   string `json:"engine,omitempty"`
	Path     string `json:"path,omitempty"`
	Manifest string `json:"manifest,omitempty"`
}

// ActionEditManifest is the next action that returns to the manifest path.
const ActionEditManifest = "edit_manifest"

func connectFailed() string { return uicopy.T("database.connect.failed", nil) }

// connectCommand is the command that connects request, with placeholders
// for what the person has not given.
func connectCommand(request ConnectRequest) string {
	if request.Manifest != "" {
		return "ovdb databases connect --manifest " + paths.QuoteArg(goruntime.GOOS, request.Manifest)
	}
	id, path := request.ID, request.Path
	if id == "" {
		id = "<name>"
	}
	if path == "" {
		path = "<absolute path>"
	} else {
		path = paths.QuoteArg(goruntime.GOOS, path)
	}
	engine := request.Engine
	if engine == "" {
		engine = EngineInGitDB
	}
	return "ovdb databases connect " + id + " --engine " + engine + " --path " + path
}

func connectChooseName(request ConnectRequest) envelope.Next {
	request.ID = "<another name>"
	return envelope.Next{Label: uicopy.T("next.choose_name", nil), Command: connectCommand(request), Action: ActionEditName}
}

func connectChooseLocation(request ConnectRequest) envelope.Next {
	request.Path = ""
	command := connectCommand(request)
	command = strings.Replace(command, "<absolute path>", "<another absolute path>", 1)
	return envelope.Next{Label: uicopy.T("next.choose_location", nil), Command: command, Action: ActionEditLocation}
}

func connectChooseManifest() envelope.Next {
	return envelope.Next{Label: uicopy.T("next.choose_manifest", nil), Command: "ovdb databases connect --manifest <absolute path>", Action: ActionEditManifest}
}

// ValidateConnect checks a request without touching anything, fills the
// default engine and cleans its paths with this OS's rules.
func ValidateConnect(request *ConnectRequest) *envelope.Error {
	if request.Manifest != "" {
		if request.ID != "" || request.Engine != "" || request.Path != "" {
			return envelope.New(envelope.InvalidArgument, connectFailed()).
				WithReason(uicopy.T("database.connect.manifest_or_path", nil)).
				WithNext(connectChooseManifest())
		}
		if !filepath.IsAbs(request.Manifest) {
			return envelope.New(envelope.InvalidArgument, connectFailed()).
				WithReason(uicopy.T("database.create.bad_path", map[string]string{"path": request.Manifest})).
				WithNext(connectChooseManifest())
		}
		request.Manifest = filepath.Clean(request.Manifest)
		return nil
	}
	if request.Engine == "" {
		request.Engine = EngineInGitDB
	}
	if !idPattern.MatchString(request.ID) {
		return envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.create.bad_name", map[string]string{"name": request.ID})).
			WithNext(connectChooseName(*request))
	}
	engine, known := FindEngine(request.Engine)
	switch {
	case !known:
		return envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.connect.unknown_engine", map[string]string{"engine": request.Engine})).
			WithNext(envelope.Next{Label: uicopy.T("next.list_engines", nil), Command: "ovdb engines"}, connectChooseManifest())
	case engine.Setup == SetupManifest:
		return envelope.New(envelope.Unsupported, connectFailed()).
			WithReason(uicopy.T("database.create.manifest_only", map[string]string{"engine": engine.Name})).
			WithNext(ManifestSteps(engine.ID)...)
	case request.Path == "":
		return envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.connect.no_path", nil)).WithNext(connectChooseLocation(*request))
	case !filepath.IsAbs(request.Path):
		return envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.create.bad_path", map[string]string{"path": request.Path})).WithNext(connectChooseLocation(*request))
	}
	request.Path = filepath.Clean(request.Path)
	return nil
}

// connectPlan is a validated connect: the manifest to register and what it
// points at.
type connectPlan struct {
	id, engine string
	manifest   []byte
	parsed     *manifest.Manifest
	location   string // local storage, or "" for a server engine
	// gitIdentityMissing: commits to this inGitDB folder would fail.
	gitIdentityMissing bool
}

// Connect registers existing storage and serves it without a restart. It
// never writes into the storage: the manifest lives in <home>/databases, the
// inferred-schema catalogue in <home>/catalogues, Git configuration is left
// alone, and a SQLite file is described from its own tables so mounting
// creates none. The storage is mounted once to validate it, and nothing is
// kept unless that succeeds.
func (r *Registry) Connect(request ConnectRequest) (DatabaseResult, error) {
	if err := ValidateConnect(&request); err != nil {
		return DatabaseResult{}, err
	}
	r.mu.Lock()
	plan, err := r.planConnect(request)
	r.mu.Unlock()
	if err != nil {
		return DatabaseResult{}, err
	}

	for _, dir := range []string{RegistryDir(r.dirs.Home), CatalogueDir(r.dirs.Home)} {
		if err := paths.EnsurePrivateDir(dir); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
			return DatabaseResult{}, r.connectUnavailable(request, plan, err.Error())
		}
	}
	staging := filepath.Join(RegistryDir(r.dirs.Home), "."+plan.id+"."+strconv.FormatInt(time.Now().UnixNano(), 36)+".creating")
	catalogue := filepath.Join(CatalogueDir(r.dirs.Home), plan.id+".inferred.json")
	_, catalogueErr := os.Lstat(catalogue)
	catalogueExisted := catalogueErr == nil
	cleanup := func() {
		_ = os.Remove(staging)
		if !catalogueExisted {
			_ = os.Remove(catalogue)
		}
	}
	if err := paths.WriteFilePrivate(staging, plan.manifest); err != nil {
		return DatabaseResult{}, r.connectUnavailable(request, plan, err.Error())
	}
	db, reason := r.mountStaging(staging, EnvironmentValues(plan.parsed, r.getenv))
	if db == nil {
		cleanup()
		return DatabaseResult{}, r.connectUnavailable(request, plan, reason)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	registrations, err := ReadRegistry(r.dirs.Home)
	if err == nil {
		if existing := registeredAs(registrations, plan.id); existing != "" {
			err = alreadyConnected(request, existing)
		}
	}
	if err != nil {
		_ = db.Close()
		cleanup()
		return DatabaseResult{}, err
	}
	manifestPath := ManifestPath(r.dirs.Home, plan.id)
	if err := os.Rename(staging, manifestPath); err != nil {
		_ = db.Close()
		cleanup()
		return DatabaseResult{}, r.connectUnavailable(request, plan, err.Error())
	}
	if err := r.server.Mount(db); err != nil {
		_ = db.Close()
		_ = os.Remove(manifestPath)
		cleanup()
		return DatabaseResult{}, envelope.New(envelope.Internal, connectFailed()).
			WithReason(uicopy.T("database.create.serve_failed", map[string]string{"name": plan.id, "error": redact.String(err.Error())})).
			WithNext(envelope.Next{Label: uicopy.T("next.restart_server", nil), Command: "ovdb server restart"})
	}
	r.setMount(MountRecord{ID: plan.id, Manifest: manifestPath, State: MountMounted})
	if err := r.writeMounts(); err != nil {
		r.logf("writing mounts.json: %s", redact.String(err.Error()))
	}
	r.logf("connected database %s (%s)", plan.id, plan.engine)

	database := Database{ID: plan.id, Engine: plan.engine, Location: Location(plan.parsed, RegistryDir(r.dirs.Home)), State: MountMounted, Manifest: manifestPath}
	return DatabaseResult{Schema: envelope.Schema, Database: database, Next: ConnectedNext(database, plan.gitIdentityMissing)}, nil
}

// mountStaging mounts a manifest once within the mount deadline, returning
// the database or a redacted reason.
func (r *Registry) mountStaging(staging string, secrets []string) (*core.Database, string) {
	outcome := make(chan mountOutcome, 1)
	go func() {
		db, err := mount.FileWithOptions(staging, mount.Options{CatalogueDir: CatalogueDir(r.dirs.Home), SkipGitIdentity: true})
		outcome <- mountOutcome{db, err}
	}()
	timer := time.NewTimer(r.mountTimeout)
	defer timer.Stop()
	select {
	case result := <-outcome:
		if result.err != nil {
			return nil, mountReason(result.err, staging, secrets...)
		}
		return result.db, ""
	case <-timer.C:
		go closeWhenDone(outcome)
		seconds := max(int(r.mountTimeout.Seconds()), 1)
		return nil, uicopy.T("database.reason.timeout", map[string]string{"seconds": strconv.Itoa(seconds)})
	}
}

// connectUnavailable is storage_unavailable for a storage that did not
// mount, with the reason redacted and logged.
func (r *Registry) connectUnavailable(request ConnectRequest, plan connectPlan, reason string) *envelope.Error {
	reason = redact.String(maskValues(reason, EnvironmentValues(plan.parsed, r.getenv)))
	r.logf("connecting database %s failed: %s", plan.id, reason)
	next := connectChooseLocation(request)
	if request.Manifest != "" {
		next = connectChooseManifest()
	}
	return envelope.New(envelope.StorageUnavailable, connectFailed()).
		WithReason(uicopy.T("database.connect.mount_failed", map[string]string{"error": reason})).
		WithNext(next)
}

func registeredAs(registrations []Registration, id string) string {
	for _, reg := range registrations {
		if strings.EqualFold(reg.ID, id) || strings.EqualFold(filepath.Base(reg.Manifest), id+".yaml") {
			return reg.ID
		}
	}
	return ""
}

func alreadyConnected(request ConnectRequest, existing string) *envelope.Error {
	e := envelope.New(envelope.AlreadyExists, connectFailed()).
		WithReason(uicopy.T("database.create.already_exists", map[string]string{"name": existing}))
	if request.Manifest != "" {
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.change_manifest_id", nil)}, connectChooseManifest())
	} else {
		e = e.WithNext(connectChooseName(request))
	}
	return e.WithNext(envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: ActionDatabases})
}

// planConnect reads and checks what request names; the caller holds r.mu.
func (r *Registry) planConnect(request ConnectRequest) (connectPlan, error) {
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return connectPlan{}, err
	}
	var plan connectPlan
	if request.Manifest != "" {
		plan, err = planManifest(request)
	} else {
		plan = connectPlan{id: request.ID, engine: request.Engine, location: request.Path}
	}
	if err != nil {
		return connectPlan{}, err
	}
	if existing := registeredAs(registrations, plan.id); existing != "" {
		return connectPlan{}, alreadyConnected(request, existing)
	}
	if plan.location != "" {
		if reason := r.locationOverlaps(CreateRequest{ID: plan.id, Path: plan.location}, registrations); reason != "" {
			next := connectChooseLocation(request)
			if request.Manifest != "" {
				next = connectChooseManifest()
			}
			return connectPlan{}, envelope.New(envelope.InvalidArgument, connectFailed()).WithReason(reason).WithNext(next)
		}
		if reason := checkExistingStorage(plan.engine, plan.location); reason != "" {
			r.logf("connecting database %s failed: %s", plan.id, reason)
			next := connectChooseLocation(request)
			if request.Manifest != "" {
				next = connectChooseManifest()
			}
			return connectPlan{}, envelope.New(envelope.StorageUnavailable, connectFailed()).WithReason(reason).WithNext(next)
		}
	}
	if request.Manifest == "" {
		if err := plan.describe(request); err != nil {
			return connectPlan{}, err
		}
	}
	if name := missingEnvironment(plan.parsed, r.getenv); name != "" {
		// The variable's name only: its value is a secret.
		return connectPlan{}, envelope.New(envelope.StorageUnavailable, connectFailed()).
			WithReason(uicopy.T("database.connect.env_missing", map[string]string{"name": name})).
			WithNext(envelope.Next{Label: uicopy.T("next.set_env_restart", map[string]string{"name": name}), Command: "ovdb server restart"},
				connectChooseManifest())
	}
	if plan.engine == EngineInGitDB && plan.location != "" {
		plan.gitIdentityMissing = GitIdentityMissing(plan.location)
	}
	return plan, nil
}

// planManifest reads, parses and copies a manifest file with its relative
// paths made absolute against the manifest's folder.
func planManifest(request ConnectRequest) (connectPlan, error) {
	params := map[string]string{"path": request.Manifest}
	data, err := os.ReadFile(request.Manifest)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return connectPlan{}, envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.connect.manifest_missing", params)).WithNext(connectChooseManifest())
	case err != nil:
		params["error"] = redact.String(err.Error())
		return connectPlan{}, envelope.New(envelope.StorageUnavailable, connectFailed()).
			WithReason(uicopy.T("database.connect.manifest_unreadable", params)).WithNext(connectChooseManifest())
	}
	parsed, err := manifest.Parse(data)
	if err != nil {
		params["error"] = redact.String(err.Error())
		return connectPlan{}, envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.connect.manifest_invalid", params)).
			WithNext(connectChooseManifest(), envelope.Next{Label: uicopy.T("engine.manifest.step_docs", map[string]string{"url": ManifestDocsURL})})
	}
	if !idPattern.MatchString(parsed.Database.ID) {
		return connectPlan{}, envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.create.bad_name", map[string]string{"name": parsed.Database.ID})).
			WithNext(envelope.Next{Label: uicopy.T("next.change_manifest_id", nil)}, connectChooseManifest())
	}
	if parsed.ACL != nil && parsed.ACL.Enabled && parsed.ACLStore == nil {
		// Policy files are read from the manifest's own folder, which a copy
		// in OVDB home does not have.
		return connectPlan{}, envelope.New(envelope.Unsupported, connectFailed()).
			WithReason(uicopy.T("database.connect.policy_files", params)).
			WithNext(envelope.Next{Label: uicopy.T("next.policy_store", nil)}, connectChooseManifest())
	}
	baseDir := filepath.Dir(request.Manifest)
	copied, err := absoluteManifest(data, baseDir)
	if err != nil {
		params["error"] = redact.String(err.Error())
		return connectPlan{}, envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.connect.manifest_invalid", params)).WithNext(connectChooseManifest())
	}
	reparsed, err := manifest.Parse(copied)
	if err != nil {
		params["error"] = redact.String(err.Error())
		return connectPlan{}, envelope.New(envelope.InvalidArgument, connectFailed()).
			WithReason(uicopy.T("database.connect.manifest_invalid", params)).WithNext(connectChooseManifest())
	}
	plan := connectPlan{id: reparsed.Database.ID, engine: reparsed.Storage.Engine, manifest: copied, parsed: reparsed}
	if location, ok := localStorage(reparsed, baseDir); ok {
		plan.location = location
	}
	return plan, nil
}

// absoluteManifest is a manifest's text with storage.path and
// acl_store.path made absolute against baseDir, comments kept.
func absoluteManifest(data []byte, baseDir string) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return data, nil
	}
	changed := false
	for _, section := range []string{"storage", "acl_store"} {
		value := mapValue(mapValue(document.Content[0], section), "path")
		if value != nil && value.Kind == yaml.ScalarNode && value.Value != "" && !filepath.IsAbs(value.Value) {
			value.Value = filepath.Join(baseDir, value.Value)
			value.Style = yaml.DoubleQuotedStyle
			changed = true
		}
	}
	header := "# Connected by OVDB from " + filepath.ToSlash(filepath.Clean(filepath.Join(baseDir))) + ". See " + ManifestDocsURL + "\n"
	if !changed {
		return append([]byte(header), data...), nil
	}
	out, err := yaml.Marshal(&document)
	if err != nil {
		return nil, err
	}
	return append([]byte(header), out...), nil
}

func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// sqliteHeader starts every SQLite database file.
const sqliteHeader = "SQLite format 3\x00"

// checkExistingStorage is why location cannot be connected as engine's
// storage, or "": connecting never creates storage.
func checkExistingStorage(engine, location string) string {
	params := map[string]string{"path": location}
	info, err := os.Stat(location)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return uicopy.T("database.connect.nothing_there", params)
	case err != nil:
		params["error"] = redact.String(err.Error())
		return uicopy.T("database.connect.unreadable", params)
	case engine == EngineInGitDB && !info.IsDir():
		return uicopy.T("database.connect.not_a_folder", params)
	case engine == EngineSQLite && info.IsDir():
		return uicopy.T("database.connect.not_sqlite", params)
	case engine == EngineSQLite:
		file, err := os.Open(location)
		if err != nil {
			params["error"] = redact.String(err.Error())
			return uicopy.T("database.connect.unreadable", params)
		}
		defer func() { _ = file.Close() }()
		header := make([]byte, len(sqliteHeader))
		if _, err := io.ReadFull(file, header); err != nil || string(header) != sqliteHeader {
			return uicopy.T("database.connect.not_sqlite", params)
		}
	case engine == EngineInGitDB:
		if _, err := os.ReadDir(location); err != nil {
			params["error"] = redact.String(err.Error())
			return uicopy.T("database.connect.unreadable", params)
		}
	}
	return ""
}

// describe builds the registry manifest for a folder or file: inGitDB
// schemaless, as created; SQLite strict with its existing tables as
// collections, so mounting adds no table to the file.
func (plan *connectPlan) describe(request ConnectRequest) error {
	create := CreateRequest{ID: request.ID, Engine: request.Engine, Path: request.Path}
	text := newManifest(create)
	if request.Engine == EngineSQLite {
		collections, err := sqliteCollections(request.Path)
		if err != nil {
			return envelope.New(envelope.StorageUnavailable, connectFailed()).
				WithReason(uicopy.T("database.connect.not_sqlite", map[string]string{"path": request.Path})).
				WithNext(connectChooseLocation(request))
		}
		if len(collections) == 0 {
			return envelope.New(envelope.SchemaRequired, connectFailed()).
				WithReason(uicopy.T("database.connect.sqlite_no_tables", map[string]string{"path": request.Path})).
				WithNext(envelope.Next{Label: uicopy.T("next.describe_in_manifest", nil), Command: "ovdb init --engine sqlite --id " + request.ID + " --path " + paths.QuoteArg(goruntime.GOOS, request.Path)},
					envelope.Next{Label: uicopy.T("next.connect_manifest", nil), Command: "ovdb databases connect --manifest <absolute path>", Action: ActionEditManifest},
					envelope.Next{Label: uicopy.T("next.schema_docs", map[string]string{"url": SchemaDocsURL})})
		}
		text = sqliteManifest(create, collections)
	}
	parsed, err := manifest.Parse([]byte(text))
	if err != nil {
		return envelope.New(envelope.StorageUnavailable, connectFailed()).
			WithReason(uicopy.T("database.connect.mount_failed", map[string]string{"error": redact.String(err.Error())})).
			WithNext(connectChooseLocation(request))
	}
	plan.manifest, plan.parsed = []byte(text), parsed
	return nil
}

// sqliteColumn is one column of an existing table.
type sqliteColumn struct {
	name, fieldType string
	required        bool
}

type sqliteTable struct {
	name    string
	columns []sqliteColumn
}

// simpleName is a table or column name a manifest can declare as is.
var simpleName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// sqliteCollections lists the tables of an existing SQLite file that OVDB
// can serve (those with an id column), read-only.
func sqliteCollections(path string) ([]sqliteTable, error) {
	location := filepath.ToSlash(path)
	if !strings.HasPrefix(location, "/") {
		location = "/" + location // C:/… on Windows
	}
	dsn := (&url.URL{Scheme: "file", Path: location, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var tables []sqliteTable
	for _, name := range names {
		if !simpleName.MatchString(name) {
			continue
		}
		columns, hasID, err := sqliteColumns(db, name)
		if err != nil {
			return nil, err
		}
		if hasID {
			tables = append(tables, sqliteTable{name: name, columns: columns})
		}
	}
	return tables, nil
}

func sqliteColumns(db *sql.DB, table string) ([]sqliteColumn, bool, error) {
	rows, err := db.Query(`SELECT name, type, "notnull", dflt_value FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var columns []sqliteColumn
	hasID := false
	for rows.Next() {
		var name, declared string
		var notNull int
		var defaultValue sql.NullString
		if err := rows.Scan(&name, &declared, &notNull, &defaultValue); err != nil {
			return nil, false, err
		}
		if name == "id" {
			hasID = true
			continue
		}
		if !simpleName.MatchString(name) {
			continue
		}
		columns = append(columns, sqliteColumn{name: name, fieldType: fieldType(declared), required: notNull == 1 && !defaultValue.Valid})
	}
	return columns, hasID, rows.Err()
}

// fieldType maps a declared SQLite column type to a manifest field type by
// SQLite's type affinity rules.
func fieldType(declared string) string {
	upper := strings.ToUpper(declared)
	switch {
	case strings.Contains(upper, "BOOL"):
		return "boolean"
	case strings.Contains(upper, "INT"):
		return "integer"
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return "string"
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"),
		strings.Contains(upper, "NUMERIC"), strings.Contains(upper, "DECIMAL"):
		return "number"
	default:
		return "any"
	}
}

// sqliteManifest is the registry manifest for an existing SQLite file.
func sqliteManifest(request CreateRequest, tables []sqliteTable) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Connected by OVDB. See %s\n", ManifestDocsURL)
	b.WriteString("database:\n")
	fmt.Fprintf(&b, "  id: %s\n", yamlScalar(request.ID))
	b.WriteString("  schema_mode: strict\n")
	b.WriteString("storage:\n  engine: sqlite\n")
	fmt.Fprintf(&b, "  path: %s\n", yamlScalar(request.Path))
	fmt.Fprintf(&b, "schemas:\n  # The tables found in the file. Describe new collections here (%s),\n  # then run: ovdb databases reload %s\n  collections:\n", SchemaDocsURL, request.ID)
	for _, table := range tables {
		fmt.Fprintf(&b, "    %s:\n      fields:", table.name)
		if len(table.columns) == 0 {
			b.WriteString(" {}\n")
			continue
		}
		b.WriteString("\n")
		for _, column := range table.columns {
			fmt.Fprintf(&b, "        %s: {type: %s", column.name, column.fieldType)
			if column.required {
				b.WriteString(", required: true")
			}
			b.WriteString("}\n")
		}
	}
	return b.String()
}

// missingEnvironment is the connection variable m needs that the server's
// environment lacks, or "".
func missingEnvironment(m *manifest.Manifest, getenv func(string) string) string {
	if m == nil {
		return ""
	}
	var name string
	switch m.Storage.Engine {
	case EnginePostgres:
		name = m.Storage.Postgres.DSNEnvVar()
	case EngineMySQL:
		name = m.Storage.MySQL.DSNEnvVar()
	default:
		return ""
	}
	if getenv(name) == "" {
		return name
	}
	return ""
}

// GitIdentityMissing reports whether dir is a Git working tree where a
// commit would fail because Git has no name or email for this user, so
// inGitDB writes there would fail. OVDB never sets one in a person's
// folder; it says how to set their own.
func GitIdentityMissing(dir string) bool {
	if exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run() != nil {
		return false // not a Git working tree, or no git: inGitDB does not commit
	}
	for _, ident := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if exec.Command("git", "-C", dir, "var", ident).Run() != nil {
			return true
		}
	}
	return false
}

// GitIdentityMissingCode is the /v1 error code local mode adds for a write
// that Git could not commit for want of a name and email.
const GitIdentityMissingCode = "git_identity_missing"

// GitIdentityNext is how to give Git a name and email.
func GitIdentityNext() []envelope.Next {
	return []envelope.Next{
		{Label: uicopy.T("next.git_name", nil), Command: `git config --global user.name "<your name>"`},
		{Label: uicopy.T("next.git_email", nil), Command: `git config --global user.email "<your email>"`},
	}
}

// GitStorage is the local inGitDB folder database id keeps its data in.
func (r *Registry) GitStorage(id string) (string, bool) {
	registrations, err := ReadRegistry(r.dirs.Home)
	if err != nil {
		return "", false
	}
	registration, ok := find(registrations, id)
	if !ok || registration.Parsed == nil || registration.Parsed.Storage.Engine != EngineInGitDB {
		return "", false
	}
	return localStorage(registration.Parsed, filepath.Dir(registration.Manifest))
}

// ConnectedNext is what to do after connecting database: first how to let
// Git save changes when it cannot, then what a created database offers.
func ConnectedNext(database Database, gitIdentityMissing bool) []envelope.Next {
	var next []envelope.Next
	if gitIdentityMissing {
		next = append(next, GitIdentityNext()...)
	}
	return append(next, resultNext(database, false)...)
}
