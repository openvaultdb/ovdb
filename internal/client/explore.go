package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
)

// ExploreDataTugPath prepares a DataTug CLI connection (capability row 22,
// explore-data-handoff#REQ:prepare-datatug-cli-connection).
const ExploreDataTugPath = "/api/local/v1/explore/datatug"

// ExploreMenu is Explore data's intent-first menu for db
// (REQ:intent-first-menu): the current-state description of each tool,
// before any file is written. It never starts the server: whether db is the
// installed TODO demo is read from the registry the same way Demo() is.
func (l *Local) ExploreMenu(_ context.Context, db string) ([]byte, error) {
	databases, err := setup.ListDatabases(l.Dirs.Home, nil)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(setup.DatabaseIDs(databases), db) {
		// A clean not_found, in --json too, exit 1 — never a menu for a
		// database that does not exist (review-inc-7.md F8).
		return nil, explore.DatabaseNotFound(db)
	}
	menu := explore.NewMenu(db, explore.IsDemo(l.Dirs.Home, db, databases))
	return envelope.Marshal(menu), nil
}

// ExploreDataTugApp is Choosing DataTug.app (REQ:honest-datatug-app-state): a
// pure local document, never a network call — DataTug.app's limitation is
// fixed copy, not server state.
func (l *Local) ExploreDataTugApp(db string) []byte {
	return envelope.Marshal(explore.NewDataTugApp(db))
}

// PrepareDataTugCLI chooses DataTug CLI (REQ:prepare-datatug-cli-connection):
// starts the server unless noStart (the descriptor's baseUrl needs its real
// port), writes the four-key descriptor, and reports whether datatug is on
// PATH.
//
// The PATH check happens twice: the server, writing the descriptor,
// necessarily checks its own; the CLI and TUI run in a different process
// (a shell, an agent harness) that commonly has a different PATH from
// whatever started the detached server (go install into a fresh shell,
// brew's prefix missing from the server's launch environment, …), so this
// client overrides on_path, install_commands and next with its own
// process's answer (review-inc-7.md F2) — the person is told about the
// PATH they can actually fix. l.DataTugLookPath stands in for exec.LookPath
// in tests; the web console has no client process, so it keeps the
// server's own check (worded accordingly in its copy).
func (l *Local) PrepareDataTugCLI(ctx context.Context, db, collection string, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	query := url.Values{"db": {db}}
	if collection != "" {
		query.Set("collection", collection)
	}
	response, err := c.Do(ctx, http.MethodGet, ExploreDataTugPath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var document explore.DataTugCLI
	if err := json.Unmarshal(response.Body, &document); err != nil {
		// Not a schema-1 document (a V1Error already returned above as err
		// would have short-circuited); pass the body through unchanged.
		return response.Body, nil
	}
	explore.ApplyOnPath(&document, explore.OnPath(l.DataTugLookPath))
	return envelope.Marshal(document), nil
}
