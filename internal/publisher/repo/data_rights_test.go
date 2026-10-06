package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
	"gopkg.in/yaml.v3"
)

func rightsEncoded(v any) []byte { b, _ := json.Marshal(v); return b }
func rightsRef(path string, data []byte) datarights.Reference {
	sum := sha256.Sum256(data)
	return datarights.Reference{Path: path, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}
}
func rightsRepository(t *testing.T) *Memory {
	t.Helper()
	m := withDescriptor(goodDescriptor, mdManifestFirst)
	declaration := license.Declaration{URL: "https://example.org/terms#reuse", Name: "Source terms"}
	server := rightsEncoded(map[string]any{"format": "ovdb-server/draft-1", "id": "https://chinookdb.com/ovdb", "data_rights": map[string]any{"format": datarights.Format}, "licences": map[string]any{"data": declaration}})
	provenance := rightsEncoded(datarights.Provenance{Format: datarights.ProvenanceFormat, Attribution: license.Notice{Text: "Source credit"}, Transformations: []string{"Restructured"}, Inputs: []datarights.Reference{}, Terms: []datarights.Reference{}})
	s := rightsRef("metadata/server.json", server)
	profile := datarights.Profile{Format: datarights.Format, Server: &s, Recordsets: map[string]license.Declaration{"Album": {Text: "Album-only terms"}}, Provenance: rightsRef("metadata/provenance.json", provenance)}
	var manifestDoc map[string]any
	if err := yaml.Unmarshal([]byte(ownManifest), &manifestDoc); err != nil {
		t.Fatal(err)
	}
	manifestDoc["data_rights"] = profile
	manifestDoc["licences"].(map[string]any)["data"] = declaration
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: rightsEncoded(manifestDoc)}
	var descriptor map[string]any
	_ = json.Unmarshal([]byte(goodDescriptor), &descriptor)
	descriptor["data_rights"] = profile
	descriptor["licences"] = map[string]any{"data": declaration}
	descriptor["recordsets"] = []any{map[string]any{"name": "Album", "licences": map[string]any{"data": profile.Recordsets["Album"]}}, map[string]any{"name": "Artist", "licences": map[string]any{"data": declaration}}}
	m.Nodes["ovdb-database.json"] = Node{Kind: File, Content: rightsEncoded(descriptor)}
	m.Nodes["metadata/server.json"] = Node{Kind: File, Content: server}
	m.Nodes["metadata/provenance.json"] = Node{Kind: File, Content: provenance}
	return m
}
func TestPublisherRepositoryVerifiesServerOnlyInheritance(t *testing.T) {
	m := rightsRepository(t)
	result := Check(m, publisher())
	if !result.OK() {
		t.Fatal(result.Findings)
	}
	if len(result.Manifest.SourceRights) != 3 {
		t.Fatal("verified source inventory missing")
	}
	for _, right := range result.Manifest.SourceRights {
		if right.Source.ServerID != "https://chinookdb.com/ovdb" || right.Source.DatabaseID != "https://chinookdb.com/ovdb/dbs/chinook" {
			t.Fatal("source identity lost")
		}
		if right.Source.Recordset != "Album" && right.DeclarationScope != license.ServerScope {
			t.Fatal("server-only declaration became database override")
		}
	}
	// A database-only raw declaration is valid with a paired serving identity.
	var doc map[string]any
	_ = json.Unmarshal(m.Nodes["ovdb.yaml"].Content, &doc)
	raw := doc["data_rights"].(map[string]any)
	delete(raw, "server")
	raw["database"] = doc["licences"].(map[string]any)["data"]
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: rightsEncoded(doc)}
	var descriptor map[string]any
	_ = json.Unmarshal(m.Nodes["ovdb-database.json"].Content, &descriptor)
	descriptor["data_rights"] = raw
	m.Nodes["ovdb-database.json"] = Node{Kind: File, Content: rightsEncoded(descriptor)}
	result = Check(m, publisher())
	if !result.OK() {
		t.Fatal(result.Findings)
	}
	if result.Manifest.SourceRights[0].DeclarationScope == license.ServerScope {
		t.Fatal("database override scope lost")
	}
}
func TestPublisherRepositoryRefusesRightsBypass(t *testing.T) {
	for _, mutate := range []func(*Memory){
		func(m *Memory) { m.Nodes["metadata/server.json"] = Node{Kind: Symlink} },
		func(m *Memory) { m.Nodes["metadata/provenance.json"] = Node{Kind: Submodule} },
		func(m *Memory) { m.Nodes["metadata/server.json"] = Node{Kind: File, Content: []byte("stale")} },
		func(m *Memory) {
			n := m.Nodes["ovdb-database.json"]
			n.Content = []byte(strings.Replace(string(n.Content), `https://chinookdb.com/ovdb"`, `https://other.example/ovdb"`, 1))
			m.Nodes["ovdb-database.json"] = n
		},
		func(m *Memory) { delete(m.Nodes, "metadata/provenance.json") },
		func(m *Memory) {
			n := m.Nodes["ovdb-database.json"]
			n.Content = []byte(strings.Replace(string(n.Content), `"Album-only terms"`, `"stale terms"`, 1))
			m.Nodes["ovdb-database.json"] = n
		},
	} {
		m := rightsRepository(t)
		mutate(m)
		result := Check(m, publisher())
		if result.OK() {
			t.Fatal("invalid rights publication accepted")
		}
		if len(result.Manifest.SourceRights) > 0 {
			t.Fatal("refused publication exposed verified inventory")
		}
	}
}
func TestRightsCannotUseReplacementObjects(t *testing.T) {
	m := rightsRepository(t)
	valid := m.Nodes["metadata/server.json"].Content
	m.Nodes["metadata/server.json"] = Node{Kind: File, Content: []byte("corrupt committed server")}
	dir := committedFixture(t, m.Nodes)
	old := git(t, dir, nil, "rev-parse", "HEAD:metadata/server.json")
	replacement := git(t, dir, valid, "hash-object", "-w", "--stdin")
	git(t, dir, nil, "replace", old, replacement)
	if result := Check(NewGit(ExecRunner{Dir: dir}), publisher()); result.OK() {
		t.Fatal("replacement object bypassed rights hash")
	}
	// A replacement manifest cannot hide opt-in raw authorship.
	original := git(t, dir, nil, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "ovdb.yaml"), []byte(ownManifest), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "add", "ovdb.yaml")
	git(t, dir, nil, "commit", "--quiet", "-m", "hide rights")
	head := git(t, dir, nil, "rev-parse", "HEAD")
	git(t, dir, nil, "update-ref", "refs/heads/main", original)
	git(t, dir, nil, "replace", original, head)
	if result := Check(NewGit(ExecRunner{Dir: dir}), publisher()); result.OK() {
		t.Fatal("replacement commit hid original data_rights")
	}
}

func TestRightsExternalDependenciesRequireExactCommit(t *testing.T) {
	m := rightsRepository(t)
	var doc map[string]any
	_ = json.Unmarshal(m.Nodes["ovdb.yaml"].Content, &doc)
	profile := doc["data_rights"].(map[string]any)
	server := profile["server"].(map[string]any)
	server["repository"] = "https://github.com/owner/server"
	server["revision"] = strings.Repeat("b", 40)
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: rightsEncoded(doc)}
	var descriptor map[string]any
	_ = json.Unmarshal(m.Nodes["ovdb-database.json"].Content, &descriptor)
	descriptor["data_rights"] = profile
	m.Nodes["ovdb-database.json"] = Node{Kind: File, Content: rightsEncoded(descriptor)}
	if result := Check(m, publisher()); result.OK() {
		t.Fatal("unprovisioned external dependency accepted")
	}
	dependency := &sourceReader{Memory: &Memory{Nodes: map[string]Node{"metadata/server.json": m.Nodes["metadata/server.json"]}}, revision: strings.Repeat("c", 40)}
	o := publisher()
	o.Dependencies = DependencyReaders{{Repository: "https://github.com/owner/server", Revision: strings.Repeat("b", 40)}: dependency}
	if result := Check(m, o); result.OK() {
		t.Fatal("wrong dependency head accepted")
	}
	dependency.revision = strings.Repeat("b", 40)
	result := Check(m, o)
	if !result.OK() {
		t.Fatal(result.Findings)
	}
	for _, right := range result.Manifest.SourceRights {
		found := false
		for _, pin := range right.Pins {
			if pin.Repository == "https://github.com/owner/server" && pin.Revision == strings.Repeat("b", 40) {
				found = true
			}
		}
		if !found {
			t.Fatal("external declaration pin missing")
		}
	}
}
