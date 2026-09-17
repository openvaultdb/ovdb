package setup

import (
	"path/filepath"
	"strings"
)

// The built-in TODO demo's database id and folder (spec/features/todo-demo).
// The demo service lives in internal/setup/demo; the status document only
// needs to find an installed demo, so that part is here.
const (
	DemoDefaultID = "todo"
	DemosDir      = "demos"
)

// DemoLocation is where database id keeps the demo under dataHome:
// <data home>/demos/<id>.
func DemoLocation(dataHome, id string) string {
	return filepath.Join(dataHome, DemosDir, id)
}

// IsDemo reports whether db is a TODO demo database: inGitDB, kept in a
// demos folder under its own id.
func IsDemo(db Database) bool {
	location := filepath.Clean(db.Location)
	return db.Engine == EngineInGitDB && filepath.IsAbs(location) &&
		filepath.Base(filepath.Dir(location)) == DemosDir && strings.EqualFold(filepath.Base(location), db.ID)
}

// FindDemo is the installed demo database among databases; the default id
// wins over others.
func FindDemo(databases []Database) (Database, bool) {
	var found []Database
	for _, db := range databases {
		if IsDemo(db) {
			found = append(found, db)
		}
	}
	for _, db := range found {
		if db.ID == DemoDefaultID {
			return db, true
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return Database{}, false
}

// DemoStatus is the demo field group of the status document
// (first-run-onboarding#REQ:status-command).
type DemoStatus struct {
	Installed bool   `json:"installed"`
	Database  string `json:"database,omitempty"`
	Location  string `json:"location"`
}

// NewDemoStatus describes the demo among databases, or where it would go.
func NewDemoStatus(dataHome string, databases []Database) DemoStatus {
	if db, ok := FindDemo(databases); ok {
		return DemoStatus{Installed: true, Database: db.ID, Location: db.Location}
	}
	return DemoStatus{Location: DemoLocation(dataHome, DemoDefaultID)}
}
