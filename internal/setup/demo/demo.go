// Package demo is the built-in TODO demo service (spec/features/todo-demo,
// decision 0010): two lists, To buy and To watch, in a schemaless inGitDB
// database registered as `todo` at <data home>/demos/todo, and the documents
// `ovdb demo install|open|status`, the TUI, the web console and the TODO app
// render.
//
// Installing goes through the registry like any create and seeds the lists
// through the data API, so the demo's files are exactly what an app writing
// the same records would produce. It never overwrites: a demo already
// installed is reported as such, and a conflicting `todo` database or a
// folder with other files is refused with a --id to use instead.
package demo

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// The TODO demo's fixed names.
const (
	// App is the demo name commands default to, leaving room for
	// `--app <name>` (REQ:demo-command-shape).
	App = "todo"
	// DefaultID is the database id the demo installs as.
	DefaultID = setup.DemoDefaultID
	// AppPath is the TODO app on the local server (REQ:todo-app-same-origin).
	AppPath = "/apps/todo/"
)

// Next actions presentations act on in place (envelope.Next.Action).
const (
	// ActionOpenApp opens the TODO app signed in.
	ActionOpenApp = "open_app"
	// ActionInstall installs the demo.
	ActionInstall = "install_demo"
)

// Lists are the demo's list paths, in the order apps show them.
var Lists = []string{"/lists/to-buy", "/lists/to-watch"}

// Location is where database id keeps the demo under dataHome.
func Location(dataHome, id string) string { return setup.DemoLocation(dataHome, id) }

// Document is the body of GET /api/local/v1/demo, of POST
// /api/local/v1/demo/install, and the --json output of `ovdb demo status`
// and `ovdb demo install`.
type Document struct {
	Schema int    `json:"schema"`
	App    string `json:"app"`
	// Installed is true once the demo database is registered.
	Installed bool `json:"installed"`
	// AlreadyInstalled is set on an install that changed nothing.
	AlreadyInstalled bool `json:"already_installed,omitempty"`
	// Database is the demo database id; empty until installed.
	Database string `json:"database,omitempty"`
	// Location is where the demo's files are, or will be once installed.
	Location string `json:"location"`
	// State is the database's mount state once installed.
	State string `json:"state,omitempty"`
	// AppPath is the TODO app's path on the local server.
	AppPath string          `json:"app_path"`
	Lists   []string        `json:"lists"`
	Next    []envelope.Next `json:"next"`
}

// Find is the installed demo database among databases.
func Find(databases []setup.Database) (setup.Database, bool) { return setup.FindDemo(databases) }

func isDemo(db setup.Database) bool { return setup.IsDemo(db) }

// Inspect is the demo document for the registered databases, with the
// default location under dataHome when the demo is not installed.
func Inspect(dataHome string, databases []setup.Database) Document {
	doc := Document{Schema: envelope.Schema, App: App, AppPath: AppPath, Lists: Lists, Location: Location(dataHome, DefaultID)}
	if db, ok := Find(databases); ok {
		doc.Installed, doc.Database, doc.Location, doc.State = true, db.ID, db.Location, db.State
	}
	doc.Next = next(doc)
	return doc
}

// next is what to do with the demo: install it, or open its app. Installing
// the TODO AI skill and Explore data join as increments 7 and 8 land
// (REQ:demo-next-actions); only implemented actions are offered.
func next(doc Document) []envelope.Next {
	if !doc.Installed {
		return []envelope.Next{{Label: uicopy.T("demo.next.install", nil), Command: "ovdb demo install --yes", Action: ActionInstall}}
	}
	return []envelope.Next{
		{Label: uicopy.T("demo.next.open_app", nil), Command: "ovdb demo open", Action: ActionOpenApp},
		{Label: uicopy.T("next.done", nil), Action: setup.ActionDone},
	}
}

// InstallRequest is the body of POST /api/local/v1/demo/install. Both
// fields are optional: ID defaults to todo, Path to <data home>/demos/<id>
// under the data home the client resolved (the server's own when the web
// console asks).
type InstallRequest struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

// Registry is the part of setup.Registry installing uses.
type Registry interface {
	Create(request setup.CreateRequest) (setup.DatabaseResult, error)
	Remove(ctx context.Context, id string) (setup.DatabaseResult, error)
	List() ([]setup.Database, error)
}

// Op is one record the seed writes: an absolute escaped path and its data.
type Op struct {
	Path string         `json:"key"`
	Data map[string]any `json:"data"`
}

// Seeder writes ops to database id in one batch through the data API.
type Seeder func(ctx context.Context, id string, ops []Op) error

// Service installs the demo on a running server.
type Service struct {
	Dirs     paths.Dirs
	Registry Registry
	Seed     Seeder
	Now      func() time.Time // time.Now when nil
	Logf     func(format string, args ...any)
}

func installFailed() string { return uicopy.T("demo.install.failed", nil) }

// Install registers the demo database, writes the seed lists, and returns the
// installed demo (REQ:demo-install-idempotent). An installed demo is reported
// with AlreadyInstalled and nothing written.
func (s *Service) Install(ctx context.Context, request InstallRequest) (Document, error) {
	explicitID := request.ID != ""
	if !explicitID {
		request.ID = DefaultID
	}
	if request.Path == "" {
		request.Path = Location(s.Dirs.Data, request.ID)
	}
	create := setup.CreateRequest{ID: request.ID, Engine: setup.EngineInGitDB, Path: request.Path}
	if err := setup.ValidateCreate(&create); err != nil {
		err.Message = installFailed()
		err.Next = []envelope.Next{{Label: uicopy.T("next.choose_name", nil), Command: "ovdb demo install --id <name>"}}
		return Document{}, err
	}
	databases, err := s.Registry.List()
	if err != nil {
		return Document{}, err
	}
	if done, ok := s.alreadyInstalled(databases, create, explicitID); ok {
		return done, nil
	}
	for _, db := range databases {
		if strings.EqualFold(db.ID, create.ID) {
			return Document{}, envelope.New(envelope.AlreadyExists, installFailed()).
				WithReason(uicopy.T("demo.install.id_taken", map[string]string{"name": db.ID, "location": db.Location})).
				WithNext(s.anotherID(databases), envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases", Action: setup.ActionDatabases})
		}
	}
	existed := true
	if entries, err := os.ReadDir(create.Path); errors.Is(err, fs.ErrNotExist) {
		existed = false
	} else if err == nil && len(entries) > 0 {
		return Document{}, envelope.New(envelope.LocationNotEmpty, installFailed()).
			WithReason(uicopy.T("demo.install.folder_not_empty", map[string]string{"path": create.Path})).
			WithNext(s.anotherID(databases))
	}

	result, err := s.Registry.Create(create)
	if err != nil {
		// Another install won the race: report what is there now.
		if e := envelope.As(err); e != nil && e.Code == envelope.AlreadyExists {
			if databases, listErr := s.Registry.List(); listErr == nil {
				if done, ok := s.alreadyInstalled(databases, create, true); ok {
					return done, nil
				}
			}
		}
		if e := envelope.As(err); e != nil {
			e.Message = installFailed()
			return Document{}, e
		}
		return Document{}, err
	}
	if err := s.Seed(ctx, create.ID, s.seed()); err != nil {
		s.undo(create, existed)
		s.logf("seeding the TODO demo failed: %s", redact.String(err.Error()))
		return Document{}, envelope.New(envelope.StorageUnavailable, installFailed()).
			WithReason(uicopy.T("demo.install.seed_failed", map[string]string{"path": create.Path, "error": redact.String(err.Error())})).
			WithNext(envelope.Next{Label: uicopy.T("demo.next.try_again", nil), Command: "ovdb demo install --yes"})
	}
	s.logf("installed the TODO demo as %s", create.ID)
	db := result.Database
	doc := Document{Schema: envelope.Schema, App: App, Installed: true, Database: db.ID, Location: db.Location, State: db.State, AppPath: AppPath, Lists: Lists}
	doc.Next = next(doc)
	return doc, nil
}

// alreadyInstalled reports an installed demo that matches the request: the
// same id at the same place, or, when no id was asked for, any installed demo.
func (s *Service) alreadyInstalled(databases []setup.Database, create setup.CreateRequest, explicitID bool) (Document, bool) {
	for _, db := range databases {
		sameID := strings.EqualFold(db.ID, create.ID)
		samePlace := db.Engine == setup.EngineInGitDB && sameLocation(db.Location, create.Path)
		if sameID && samePlace || !explicitID && isDemo(db) {
			doc := Inspect(s.Dirs.Data, []setup.Database{db})
			if !isDemo(db) {
				// The same id and place, registered by hand.
				doc = Document{Schema: envelope.Schema, App: App, Installed: true, Database: db.ID, Location: db.Location, State: db.State, AppPath: AppPath, Lists: Lists}
				doc.Next = next(doc)
			}
			doc.AlreadyInstalled = true
			return doc, true
		}
	}
	return Document{}, false
}

func sameLocation(a, b string) bool {
	return a != "" && b != "" && filepath.Clean(a) == filepath.Clean(b)
}

// anotherID suggests the first free todo-demo[-N] with a free folder
// (AC:conflicting-todo-refused suggests `ovdb demo install --id todo-demo`).
func (s *Service) anotherID(databases []setup.Database) envelope.Next {
	suggestion := DefaultID + "-demo"
	for n := 2; ; n++ {
		taken := false
		for _, db := range databases {
			taken = taken || strings.EqualFold(db.ID, suggestion)
		}
		entries, err := os.ReadDir(Location(s.Dirs.Data, suggestion))
		if !taken && (errors.Is(err, fs.ErrNotExist) || err == nil && len(entries) == 0) {
			break
		}
		suggestion = DefaultID + "-demo-" + strconv.Itoa(n)
	}
	return envelope.Next{Label: uicopy.T("demo.next.another_id", map[string]string{"name": suggestion}), Command: "ovdb demo install --id " + suggestion, Action: setup.ActionEditName}
}

// undo removes a demo whose seed failed, so installing again starts clean:
// the registration, and the files OVDB created.
func (s *Service) undo(create setup.CreateRequest, existed bool) {
	if _, err := s.Registry.Remove(context.Background(), create.ID); err != nil {
		s.logf("removing the half-installed TODO demo: %s", redact.String(err.Error()))
		return
	}
	if !existed {
		_ = os.RemoveAll(create.Path)
		return
	}
	entries, _ := os.ReadDir(create.Path)
	for _, entry := range entries {
		_ = os.RemoveAll(filepath.Join(create.Path, entry.Name()))
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// seed is the demo data: two lists and their items, added a second apart so
// every client shows them in the same order.
func (s *Service) seed() []Op {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	at := now().UTC().Truncate(time.Second)
	var ops []Op
	add := func(list, listTitle string, items ...[2]string) {
		ops = append(ops, Op{Path: "/lists/" + list, Data: map[string]any{"title": listTitle}})
		for _, item := range items {
			ops = append(ops, Op{Path: "/lists/" + list + "/items/" + item[0], Data: map[string]any{
				"title": item[1], "done": false, "added_at": at.Format(time.RFC3339),
			}})
			at = at.Add(time.Second)
		}
	}
	add("to-buy", uicopy.T("demo.seed.to_buy", nil),
		[2]string{"milk", uicopy.T("demo.seed.milk", nil)},
		[2]string{"bananas", uicopy.T("demo.seed.bananas", nil)},
		[2]string{"coffee", uicopy.T("demo.seed.coffee", nil)})
	add("to-watch", uicopy.T("demo.seed.to_watch", nil),
		[2]string{"the-matrix", uicopy.T("demo.seed.the_matrix", nil)},
		[2]string{"interstellar", uicopy.T("demo.seed.interstellar", nil)})
	return ops
}
