package setup

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
)

// The built-in TODO demo's database id and folder (spec/features/todo-demo).
// The demo service lives in internal/setup/demo; the status document only
// needs to find an installed demo, so that part is here.
const (
	DemoDefaultID = "todo"
	DemosDir      = "demos"
	// DemosFile, in OVDB home, records every demo install wrote: the demo is
	// the database registered with that id at that place, never whatever
	// happens to live in a folder named demos.
	DemosFile = "demos.json"
)

// DemoLocation is where database id keeps the demo under dataHome:
// <data home>/demos/<id>.
func DemoLocation(dataHome, id string) string {
	return filepath.Join(dataHome, DemosDir, id)
}

// DemoRecord is one demo install: its app, database id and folder.
type DemoRecord struct {
	App      string `json:"app"`
	Database string `json:"database"`
	Location string `json:"location"`
}

type demosDocument struct {
	Schema int          `json:"schema"`
	Demos  []DemoRecord `json:"demos"`
}

// ReadDemos lists the recorded demo installs; a missing or unreadable file
// is none.
func ReadDemos(home string) []DemoRecord {
	data, err := os.ReadFile(filepath.Join(home, DemosFile))
	if err != nil {
		return nil
	}
	var document demosDocument
	if json.Unmarshal(data, &document) != nil {
		return nil
	}
	return document.Demos
}

// RecordDemo adds or replaces the record for record.Database. Only the
// server holding home.lock calls it.
func RecordDemo(home string, record DemoRecord) error {
	demos := slices.DeleteFunc(ReadDemos(home), func(r DemoRecord) bool { return strings.EqualFold(r.Database, record.Database) })
	demos = append(demos, record)
	if err := paths.EnsurePrivateDir(home); err != nil && !errors.Is(err, paths.ErrNotPrivate) {
		return err
	}
	return paths.WriteFilePrivate(filepath.Join(home, DemosFile), envelope.Marshal(demosDocument{Schema: envelope.Schema, Demos: demos}))
}

// ForgetDemo removes the record for database id, if any.
func ForgetDemo(home, id string) error {
	demos := ReadDemos(home)
	kept := slices.DeleteFunc(slices.Clone(demos), func(r DemoRecord) bool { return strings.EqualFold(r.Database, id) })
	if len(kept) == len(demos) {
		return nil
	}
	err := paths.WriteFilePrivate(filepath.Join(home, DemosFile), envelope.Marshal(demosDocument{Schema: envelope.Schema, Demos: kept}))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// SameLocation reports whether two absolute locations name the same place,
// ignoring case where the file system usually does.
func SameLocation(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if goruntime.GOOS == "windows" || goruntime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// DemoRecordFor is the recorded install at location, if any.
func DemoRecordFor(home, location string) (DemoRecord, bool) {
	for _, record := range ReadDemos(home) {
		if SameLocation(record.Location, location) {
			return record, true
		}
	}
	return DemoRecord{}, false
}

// FindDemo is the installed demo among databases: a registered inGitDB
// database whose id and location match a recorded install. The default id
// wins over others.
func FindDemo(home string, databases []Database) (Database, bool) {
	records := ReadDemos(home)
	var found []Database
	for _, db := range databases {
		if db.Engine != EngineInGitDB {
			continue
		}
		if slices.ContainsFunc(records, func(r DemoRecord) bool {
			return strings.EqualFold(r.Database, db.ID) && SameLocation(r.Location, db.Location)
		}) {
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
func NewDemoStatus(dirs paths.Dirs, databases []Database) DemoStatus {
	if db, ok := FindDemo(dirs.Home, databases); ok {
		return DemoStatus{Installed: true, Database: db.ID, Location: db.Location}
	}
	return DemoStatus{Location: DemoLocation(dirs.Data, DemoDefaultID)}
}
