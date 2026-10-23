package dbcontext

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
)

// Rungs of the precedence ladder, as Context.Scope names them.
const (
	ScopeFlag        = "flag"        // --db
	ScopeEnvironment = "environment" // OVDB_DATABASE (and OVDB_PATH)
	ScopeProject     = "project"     // ovdb use, found by walk-up
	ScopeGlobal      = "global"      // ovdb use --global
	ScopeOnly        = "only"        // the only registered database
)

// Environment variables of the environment rung.
const (
	EnvDatabase = "OVDB_DATABASE"
	EnvPath     = "OVDB_PATH"
)

// Locations under OVDB home. Nothing is ever written into a project.
const (
	Dir         = "contexts"
	projectsDir = "projects"
	globalFile  = "global.json"
)

// Context is the database and path one command acts on, and where that came
// from: the rung, and for a project context the directory that supplied it.
type Context struct {
	Database string `json:"database"`
	Path     string `json:"path"`
	Scope    string `json:"scope"`
	Dir      string `json:"dir,omitempty"`
}

// Document is the body of GET and PUT /api/local/v1/context and the --json
// output of `ovdb use` and `ovdb pwd`.
type Document struct {
	Schema int `json:"schema"`
	// Context is what applies here; null when nothing does.
	Context *Context `json:"context"`
	// Global is the default for all projects, whatever applies here; the web
	// console shows and changes only this one (parity E3).
	Global *Context `json:"global"`
	// Databases are the registered ids, for choosing one.
	Databases []string `json:"databases"`
	// Message says what a change did, naming its scope.
	Message string `json:"message,omitempty"`
	// Notices are one-line warnings, such as a project context skipped
	// because its database is no longer registered.
	Notices []string        `json:"notices,omitempty"`
	Next    []envelope.Next `json:"next"`
}

// Request is what a client sends to resolve its context.
type Request struct {
	// Dirs are Lookup.Dirs, nearest first; empty for the web console.
	Dirs []string
	// Database and Path come from --db or OVDB_DATABASE/OVDB_PATH, with
	// Scope ScopeFlag or ScopeEnvironment; empty otherwise.
	Database, Path, Scope string
	// Root is Lookup.Root, where `ovdb use` stores; never sent.
	Root string
}

// Change is the body of PUT /api/local/v1/context.
type Change struct {
	Scope    string `json:"scope"` // project or global
	Dir      string `json:"dir,omitempty"`
	Database string `json:"database,omitempty"`
	Path     string `json:"path,omitempty"`
	Clear    bool   `json:"clear,omitempty"`
}

type stored struct {
	Schema   int    `json:"schema"`
	Database string `json:"database"`
	Path     string `json:"path"`
	Dir      string `json:"dir,omitempty"`
}

func projectFile(home, dir string) string {
	return filepath.Join(home, Dir, projectsDir, Key(dir)+".json")
}

func globalPath(home string) string { return filepath.Join(home, Dir, globalFile) }

func read(file string) *stored {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var s stored
	if json.Unmarshal(data, &s) != nil || s.Database == "" {
		return nil
	}
	return &s
}

// Registered is the registered id matching id exactly, or the only one that
// matches ignoring case (ids are unique ignoring case).
func Registered(ids []string, id string) (string, bool) {
	match := ""
	folded := 0
	for _, candidate := range ids {
		if candidate == id {
			return id, true
		}
		if strings.EqualFold(candidate, id) {
			match = candidate
			folded++
		}
	}
	return match, folded == 1
}

func cleanPath(path string) string {
	parsed, err := datapath.Parse(path)
	if err != nil {
		return "/"
	}
	return parsed.String()
}

// Resolve applies the ladder: --db or OVDB_DATABASE, the nearest project
// context, the global default, the only registered database. A stored
// context naming a database that is no longer registered is skipped.
func Resolve(home string, ids []string, request Request) Document {
	sort.Strings(ids)
	document := Document{Schema: envelope.Schema, Databases: append([]string{}, ids...), Next: []envelope.Next{}}
	if global := read(globalPath(home)); global != nil {
		if id, ok := Registered(ids, global.Database); ok {
			document.Global = &Context{Database: id, Path: cleanPath(global.Path), Scope: ScopeGlobal}
		}
	}
	switch {
	case request.Database != "" && (request.Scope == ScopeFlag || request.Scope == ScopeEnvironment):
		database := request.Database
		if id, ok := Registered(ids, database); ok {
			database = id
		}
		document.Context = &Context{Database: database, Path: cleanPath(request.Path), Scope: request.Scope}
	default:
		for _, dir := range request.Dirs {
			project := read(projectFile(home, dir))
			if project == nil {
				continue
			}
			if id, ok := Registered(ids, project.Database); ok {
				document.Context = &Context{Database: id, Path: cleanPath(project.Path), Scope: ScopeProject, Dir: dir}
				break
			}
			document.Notices = append(document.Notices, uicopy.T("context.skipped", map[string]string{"dir": dir, "name": project.Database}))
		}
		switch {
		case document.Context != nil:
		case document.Global != nil:
			global := *document.Global
			document.Context = &global
		case len(ids) == 1:
			document.Context = &Context{Database: ids[0], Path: "/", Scope: ScopeOnly}
		}
	}
	if document.Context == nil {
		document.Next = ChooseNext(ids)
	}
	return document
}

// ChooseNext is how to pick a database when none applies.
func ChooseNext(ids []string) []envelope.Next {
	if len(ids) == 0 {
		return []envelope.Next{{Label: uicopy.T("home.menu.create_database", nil), Command: "ovdb databases create <name>"}}
	}
	return []envelope.Next{
		{Label: uicopy.T("next.use_database", nil), Command: "ovdb use <database>"},
		{Label: uicopy.T("next.name_database", nil), Command: "ovdb list / --db <database>"},
	}
}

// NoContext is not_found for a data command with no database from any rung
// (AC:no-context-error), listing the registered databases.
func NoContext(ids []string) *envelope.Error {
	reason := uicopy.T("context.none.reason_empty", nil)
	if len(ids) > 0 {
		reason = uicopy.T("context.none.reason", map[string]string{"databases": strings.Join(ids, ", ")})
	}
	return envelope.New(envelope.NotFound, uicopy.T("context.none.message", nil)).WithReason(reason).WithNext(ChooseNext(ids)...)
}

// UnknownDatabase is not_found for a database name that is not registered,
// listing the ones that are.
func UnknownDatabase(message, id string, ids []string) *envelope.Error {
	reason := uicopy.T("context.unknown_database", map[string]string{"name": id})
	if len(ids) > 0 {
		reason += " " + uicopy.T("context.none.reason", map[string]string{"databases": strings.Join(ids, ", ")})
	}
	return envelope.New(envelope.NotFound, message).WithReason(reason).WithNext(ChooseNext(ids)...)
}

// Apply stores or clears a context for change and returns the stored
// context's document. ids are the registered databases; the caller holds
// the registry.
func Apply(home string, ids []string, change Change) (Document, error) {
	failed := uicopy.T("context.change_failed", nil)
	var file string
	switch change.Scope {
	case ScopeProject:
		if change.Dir == "" || !filepath.IsAbs(change.Dir) {
			return Document{}, envelope.New(envelope.InvalidArgument, failed).
				WithReason(uicopy.T("context.dir_required", nil))
		}
		if !change.Clear && tooBroad(change.Dir) {
			// Every directory below would share it (review F5).
			return Document{}, envelope.New(envelope.InvalidArgument, failed).
				WithReason(uicopy.T("context.dir_too_broad", map[string]string{"dir": change.Dir})).
				WithNext(envelope.Next{Label: uicopy.T("next.use_in_project_folder", nil)},
					envelope.Next{Label: uicopy.T("next.use_global", nil), Command: "ovdb use --global <database>"})
		}
		file = projectFile(home, change.Dir)
	case ScopeGlobal:
		file = globalPath(home)
	default:
		return Document{}, envelope.New(envelope.InvalidArgument, failed).
			WithReason(uicopy.T("context.bad_scope", map[string]string{"scope": change.Scope}))
	}
	sort.Strings(ids)
	document := Document{Schema: envelope.Schema, Databases: append([]string{}, ids...), Next: []envelope.Next{}}
	if change.Clear {
		err := os.Remove(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Document{}, envelope.New(envelope.StorageUnavailable, failed).WithReason(err.Error())
		}
		switch {
		case err != nil:
			document.Message = uicopy.T("context.nothing_to_clear", nil)
		case change.Scope == ScopeProject:
			document.Message = uicopy.T("context.cleared.project", map[string]string{"dir": change.Dir})
		default:
			document.Message = uicopy.T("context.cleared.global", nil)
		}
		document.Global = Resolve(home, ids, Request{}).Global
		return document, nil
	}
	id, ok := Registered(ids, change.Database)
	if !ok {
		return Document{}, UnknownDatabase(failed, change.Database, ids)
	}
	path := "/"
	if change.Path != "" {
		parsed, err := datapath.Parse(change.Path)
		if err != nil {
			return Document{}, err
		}
		path = parsed.String()
	}
	record := stored{Schema: envelope.Schema, Database: id, Path: path}
	context := Context{Database: id, Path: path, Scope: change.Scope}
	if change.Scope == ScopeProject {
		record.Dir, context.Dir = change.Dir, change.Dir
	}
	if err := write(file, record); err != nil {
		return Document{}, envelope.New(envelope.StorageUnavailable, failed).WithReason(err.Error())
	}
	document.Context = &context
	document.Global = Resolve(home, ids, Request{}).Global
	document.Message = UsingMessage(context)
	return document, nil
}

// userHomeDir is os.UserHomeDir; tests replace it.
var userHomeDir = os.UserHomeDir

// tooBroad reports whether dir is the user's home directory or a file
// system root, where a project context would apply to everything below.
func tooBroad(dir string) bool {
	if filepath.Dir(dir) == dir {
		return true
	}
	home, err := userHomeDir()
	return err == nil && home != "" && Key(Canonical(home)) == Key(Canonical(dir))
}

func write(file string, record stored) error {
	if err := paths.EnsurePrivateDir(filepath.Dir(file)); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFilePrivate(file, append(data, '\n'))
}

// ClearDatabase removes every stored context naming id, so removing a
// database never leaves a project pointing at it.
func ClearDatabase(home, id string) error {
	files := []string{globalPath(home)}
	entries, err := os.ReadDir(filepath.Join(home, Dir, projectsDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		files = append(files, filepath.Join(home, Dir, projectsDir, entry.Name()))
	}
	var errs []error
	for _, file := range files {
		if s := read(file); s != nil && strings.EqualFold(s.Database, id) {
			if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// UsingMessage is the line saying what a change set, naming its scope
// (REQ:use-sets-scoped-context): `Now using todo for this project (/p/a)`.
func UsingMessage(c Context) string {
	params := map[string]string{"database": c.Database, "dir": c.Dir, "path": c.Path}
	switch {
	case c.Scope == ScopeProject && c.Path == "/":
		return uicopy.T("context.using.project", params)
	case c.Scope == ScopeProject:
		return uicopy.T("context.using.project_path", params)
	case c.Path == "/":
		return uicopy.T("context.using.global", params)
	default:
		return uicopy.T("context.using.global_path", params)
	}
}

// Source names where c came from, for `ovdb use` and `ovdb pwd`
// (REQ:context-lookup): "project context from /p/a".
func Source(c Context) string {
	params := map[string]string{"dir": c.Dir}
	switch c.Scope {
	case ScopeFlag:
		return uicopy.T("context.source.flag", nil)
	case ScopeEnvironment:
		return uicopy.T("context.source.environment", nil)
	case ScopeProject:
		return uicopy.T("context.source.project", params)
	case ScopeGlobal:
		return uicopy.T("context.source.global", nil)
	default:
		return uicopy.T("context.source.only", nil)
	}
}

// Query is r as the query string of GET /api/local/v1/context, home and
// status.
func (r Request) Query() url.Values {
	values := url.Values{}
	for _, dir := range r.Dirs {
		values.Add("dir", dir)
	}
	if r.Database != "" {
		values.Set("database", r.Database)
		values.Set("scope", r.Scope)
		if r.Path != "" {
			values.Set("path", r.Path)
		}
	}
	return values
}

// RequestFrom reads a Request from a query string. Only absolute directories
// count: the browser, which has no working directory, sends none.
func RequestFrom(values url.Values) Request {
	var request Request
	for _, dir := range values["dir"] {
		if filepath.IsAbs(dir) {
			request.Dirs = append(request.Dirs, filepath.Clean(dir))
		}
	}
	request.Database, request.Path, request.Scope = values.Get("database"), values.Get("path"), values.Get("scope")
	return request
}
