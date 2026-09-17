package setup

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// Registry locations inside OVDB home and the runtime directory.
const (
	// DatabasesDir holds one manifest per registered database, <id>.yaml.
	DatabasesDir = "databases"
	// CataloguesDir holds the inferred-schema catalogues OVDB keeps for
	// registered databases, so mounting never writes into user storage.
	CataloguesDir = "catalogues"
	// MountsFile is the running server's per-database mount state.
	MountsFile = "mounts.json"
)

// Mount states.
const (
	MountMounted        = "mounted"
	MountNeedsAttention = "needs_attention"
	MountUnknown        = "unknown" // no server running
)

// RegistryDir is <home>/databases.
func RegistryDir(home string) string { return filepath.Join(home, DatabasesDir) }

// CatalogueDir is <home>/catalogues.
func CatalogueDir(home string) string { return filepath.Join(home, CataloguesDir) }

// ManifestPath is where database id is registered.
func ManifestPath(home, id string) string { return filepath.Join(RegistryDir(home), id+".yaml") }

// MountsPath is <runtime>/mounts.json.
func MountsPath(runtimeDir string) string { return filepath.Join(runtimeDir, MountsFile) }

// Database is one registered database as every interface lists it.
type Database struct {
	ID       string `json:"id"`
	Engine   string `json:"engine,omitempty"`
	Location string `json:"location,omitempty"`
	State    string `json:"state,omitempty"` // mounted, needs_attention, unknown
	// Reason says, redacted, why a database needs attention.
	Reason string `json:"reason,omitempty"`
	// Manifest is the registry file, so a person can inspect or fix it.
	Manifest string `json:"manifest"`
}

// DatabasesDocument is the body of GET /api/local/v1/databases and the
// --json output of `ovdb databases`.
type DatabasesDocument struct {
	Schema    int             `json:"schema"`
	Databases []Database      `json:"databases"`
	Next      []envelope.Next `json:"next"`
}

// Registration is one manifest file in the registry, read without mounting.
type Registration struct {
	ID       string
	Manifest string
	Parsed   *manifest.Manifest // nil when the file cannot be parsed
	Err      error              // why it cannot be parsed
}

// ReadRegistry lists the manifests in <home>/databases, sorted by file
// name. A manifest that cannot be read or parsed is still listed, under its
// file name, with its error: one broken file never hides the others
// (local-server-and-web-console#REQ:registry-serving).
func ReadRegistry(home string) ([]Registration, error) {
	entries, err := os.ReadDir(RegistryDir(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Registration
	for _, entry := range entries {
		name := entry.Name()
		manifestFile := strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
		if entry.IsDir() || strings.HasPrefix(name, ".") || !manifestFile {
			continue
		}
		path := filepath.Join(RegistryDir(home), name)
		registration := Registration{ID: strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml"), Manifest: path}
		parsed, err := manifest.Load(path)
		if err != nil {
			registration.Err = err
		} else {
			registration.Parsed = parsed
			registration.ID = parsed.Database.ID
		}
		out = append(out, registration)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Describe is registration as listed, with state and reason left to the
// caller.
func (r Registration) Describe() Database {
	db := Database{ID: r.ID, Manifest: r.Manifest}
	if r.Parsed != nil {
		db.Engine = r.Parsed.Storage.Engine
		db.Location = Location(r.Parsed, filepath.Dir(r.Manifest))
	}
	return db
}

// Location describes where m keeps its data without any credential: the
// absolute storage path, or for server engines where the connection comes
// from. baseDir resolves relative paths.
func Location(m *manifest.Manifest, baseDir string) string {
	storage := m.Storage
	switch {
	case storage.Engine == EngineInGitDB && storage.InGitDB != nil && storage.InGitDB.GitHub != nil:
		gh := storage.InGitDB.GitHub
		return "github.com/" + gh.Owner + "/" + gh.Repo + "@" + gh.GitRef()
	case storage.Engine == EnginePostgres:
		return uicopy.T("database.location.env", map[string]string{"name": storage.Postgres.DSNEnvVar()})
	case storage.Engine == EngineMySQL:
		return uicopy.T("database.location.env", map[string]string{"name": storage.MySQL.DSNEnvVar()})
	case storage.Engine == EngineFirestore && storage.Firestore != nil:
		database := storage.Firestore.Database
		if database == "" {
			database = "(default)"
		}
		return "firestore://" + storage.Firestore.Project + "/" + database
	case storage.Path == "":
		return ""
	case filepath.IsAbs(storage.Path):
		return filepath.Clean(storage.Path)
	default:
		return filepath.Join(baseDir, storage.Path)
	}
}

// MountRecord is one database's entry in mounts.json.
type MountRecord struct {
	ID       string `json:"id"`
	Manifest string `json:"manifest"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
}

// Mounts is mounts.json: what the running server mounted and what needs
// attention, with reasons already redacted.
type Mounts struct {
	Schema    int           `json:"schema"`
	Databases []MountRecord `json:"databases"`
}

// WriteMounts atomically writes mounts.json owner-only.
func WriteMounts(runtimeDir string, mounts Mounts) error {
	mounts.Schema = envelope.Schema
	for i := range mounts.Databases {
		mounts.Databases[i].Reason = redact.String(mounts.Databases[i].Reason)
	}
	data, err := json.MarshalIndent(mounts, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFilePrivate(MountsPath(runtimeDir), append(data, '\n'))
}

// ReadMounts reads mounts.json; nil when there is none.
func ReadMounts(runtimeDir string) (*Mounts, error) {
	data, err := os.ReadFile(MountsPath(runtimeDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var mounts Mounts
	if err := json.Unmarshal(data, &mounts); err != nil {
		return nil, err
	}
	return &mounts, nil
}

// ListDatabases builds the databases list from the registry. mounts is the
// running server's state; nil means no server runs, and every database is
// "unknown (server not running)".
func ListDatabases(home string, mounts *Mounts) ([]Database, error) {
	registrations, err := ReadRegistry(home)
	if err != nil {
		return nil, err
	}
	byManifest := map[string]MountRecord{}
	if mounts != nil {
		for _, record := range mounts.Databases {
			byManifest[record.Manifest] = record
		}
	}
	out := []Database{}
	for _, registration := range registrations {
		db := registration.Describe()
		switch record, ok := byManifest[registration.Manifest]; {
		case mounts == nil:
			db.State = MountUnknown
		case ok:
			db.State, db.Reason = record.State, redact.String(record.Reason)
		default:
			// Registered after the server started, outside the local API:
			// the server picks it up on its next start.
			db.State = MountNeedsAttention
			db.Reason = uicopy.T("database.reason.not_loaded", nil)
		}
		out = append(out, db)
	}
	return out, nil
}

// NewDatabasesDocument wraps databases with the next actions that apply.
func NewDatabasesDocument(databases []Database) DatabasesDocument {
	if databases == nil {
		databases = []Database{}
	}
	next := []envelope.Next{{Label: uicopy.T("home.menu.create_database", nil), Command: "ovdb databases create <name>"}}
	unknown := false
	for _, db := range databases {
		unknown = unknown || db.State == MountUnknown
	}
	if unknown {
		next = append(next, envelope.Next{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"})
	}
	if len(databases) > 0 {
		next = append(next, envelope.Next{Label: uicopy.T("next.remove_database", nil), Command: "ovdb databases remove <name>"})
	}
	return DatabasesDocument{Schema: envelope.Schema, Databases: databases, Next: next}
}
