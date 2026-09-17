package setup

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// Engine ids. They are the storage.engine values the manifest parser and
// openvaultdb-go's mount accept; engines_test.go fails when the two drift.
const (
	EngineInGitDB   = "ingitdb"
	EngineSQLite    = "sqlite"
	EngineFirestore = "firestore"
	EngineMySQL     = "mysql"
	EnginePostgres  = "postgres"
)

// Setup kinds: an engine is created and connected in guided steps, or set
// up with a manifest file only (database-setup-and-providers#REQ:manifest-only-engines-are-honest).
const (
	SetupGuided   = "guided"
	SetupManifest = "manifest"
)

// ManifestDocsURL explains manifests, engines and schema modes.
const ManifestDocsURL = "https://github.com/openvaultdb/openvaultdb-go#manifest-examples"

// SchemaDocsURL explains schema modes and how to declare collections.
const SchemaDocsURL = "https://github.com/openvaultdb/openvaultdb-go#schema-modes"

// Engine is one storage choice in the catalogue.
type Engine struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	SchemaModes []string `json:"schema_modes"`
	// Pinned engines come first, in catalogue order, and presentations
	// separate them from the rest.
	Pinned bool   `json:"pinned"`
	Setup  string `json:"setup"` // guided, manifest
	// Note adds a detail under the description, e.g. GitHub-backed inGitDB.
	Note string `json:"note,omitempty"`
	// ManifestSteps are the steps for a manifest-only engine, in order; the
	// last one points to the documentation.
	ManifestSteps []envelope.Next `json:"manifest_steps,omitempty"`
}

// EnginesDocument is the body of GET /api/local/v1/engines and the --json
// output of `ovdb engines`.
type EnginesDocument struct {
	Schema  int             `json:"schema"`
	Engines []Engine        `json:"engines"`
	Next    []envelope.Next `json:"next"`
}

// Engines is the storage catalogue in display order: inGitDB and SQLite
// pinned, then the rest by name (database-setup-and-providers#REQ:catalogue-order-and-filter).
// This order does not change the backend build order.
func Engines() []Engine {
	return []Engine{
		{ID: EngineInGitDB, Name: uicopy.T("engine.ingitdb.name", nil), Description: uicopy.T("engine.ingitdb.description", nil),
			SchemaModes: []string{"strict", "partial", "schemaless"}, Pinned: true, Setup: SetupGuided,
			Note: uicopy.T("engine.ingitdb.github_note", nil)},
		{ID: EngineSQLite, Name: uicopy.T("engine.sqlite.name", nil), Description: uicopy.T("engine.sqlite.description", nil),
			SchemaModes: []string{"strict"}, Pinned: true, Setup: SetupGuided},
		manifestEngine(EngineFirestore, uicopy.T("engine.firestore.name", nil), uicopy.T("engine.firestore.description", nil),
			[]string{"strict", "partial", "schemaless"}),
		manifestEngine(EngineMySQL, uicopy.T("engine.mysql.name", nil), uicopy.T("engine.mysql.description", nil),
			[]string{"strict"}),
		manifestEngine(EnginePostgres, uicopy.T("engine.postgres.name", nil), uicopy.T("engine.postgres.description", nil),
			[]string{"strict"}),
	}
}

func manifestEngine(id, name, description string, modes []string) Engine {
	return Engine{ID: id, Name: name, Description: description, SchemaModes: modes, Setup: SetupManifest,
		ManifestSteps: ManifestSteps(id)}
}

// ManifestSteps is how to set up a manifest-only engine: write a manifest,
// edit it and connect it (database-setup-and-providers#REQ:manifest-only-engines-are-honest).
func ManifestSteps(engine string) []envelope.Next {
	return []envelope.Next{
		{Label: uicopy.T("engine.manifest.step_init", nil), Command: "ovdb init --engine " + engine + " --id <name>"},
		{Label: uicopy.T("engine.manifest.step_connect", nil), Command: "ovdb databases connect --manifest <absolute path>", Action: ActionEditManifest},
		{Label: uicopy.T("engine.manifest.step_docs", map[string]string{"url": ManifestDocsURL})},
	}
}

// NewEnginesDocument wraps the catalogue.
func NewEnginesDocument() EnginesDocument {
	return EnginesDocument{Schema: envelope.Schema, Engines: Engines(), Next: []envelope.Next{
		{Label: uicopy.T("home.menu.create_database", nil), Command: "ovdb databases create <name>"},
		{Label: uicopy.T("home.menu.connect_database", nil), Command: "ovdb databases connect <name> --engine <ingitdb or sqlite> --path <absolute path>"},
	}}
}

// FilterEngines keeps the engines whose id, name or description contains
// filter, ignoring case, in their order (REQ:catalogue-order-and-filter).
// The web console applies the same rule in web/src/engines.ts.
func FilterEngines(engines []Engine, filter string) []Engine {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var out []Engine
	for _, engine := range engines {
		if strings.Contains(strings.ToLower(engine.ID+"\n"+engine.Name+"\n"+engine.Description), filter) {
			out = append(out, engine)
		}
	}
	return out
}

// FindEngine returns the catalogue entry for id.
func FindEngine(id string) (Engine, bool) {
	for _, engine := range Engines() {
		if engine.ID == id {
			return engine, true
		}
	}
	return Engine{}, false
}
