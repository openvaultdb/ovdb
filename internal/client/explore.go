package client

import (
	"context"
	"net/http"
	"net/url"

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
// port), checks datatug on PATH, and writes the four-key descriptor.
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
	return response.Body, nil
}
