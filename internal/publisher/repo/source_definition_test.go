package repo

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/source"
	"gopkg.in/yaml.v3"
)

func httpDefinitionRepository(t *testing.T) *Memory {
	t.Helper()
	b, err := os.ReadFile("../../../publisher/source/ecb-daily.example.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	r := d.Recordsets["FxReferenceQuote"]
	d.Recordsets = map[string]source.Recordset{}
	for _, name := range []string{"Album", "Artist"} {
		x := r
		x.Entity = name
		x.ProviderSourceID = "provider:ecb/" + name
		d.Recordsets[name] = x
	}
	m := withDescriptor(goodDescriptor, mdManifestFirst)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(ownManifest), &doc); err != nil {
		t.Fatal(err)
	}
	doc["source_definition"] = d
	doc["licences"].(map[string]any)["data"] = d.Rights.Declaration
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: rightsEncoded(doc)}
	var desc map[string]any
	_ = json.Unmarshal([]byte(goodDescriptor), &desc)
	desc["source_definition"] = d
	desc["licences"] = map[string]any{"data": d.Rights.Declaration}
	desc["recordsets"] = []any{map[string]any{"name": "Album", "licences": map[string]any{"data": d.Rights.Declaration}}, map[string]any{"name": "Artist", "licences": map[string]any{"data": d.Rights.Declaration}}}
	m.Nodes["ovdb-database.json"] = Node{Kind: File, Content: rightsEncoded(desc)}
	return m
}

func TestHTTPDefinitionImmutableMetadataOnly(t *testing.T) {
	m := httpDefinitionRepository(t)
	for _, profile := range []manifest.Profile{manifest.Publisher, manifest.Directory} {
		result := Check(m, Options{Profile: profile})
		if !result.OK() {
			t.Fatal(result.Findings)
		}
		e := result.Manifest.SourceDefinitionEvidence
		if e == nil || e.Origin != "publisher-definition-verified" || e.InputVerification != "dynamic-unpinned" || e.Manifest.Revision != strings.Repeat("a", 40) {
			t.Fatal("definition evidence missing or overstated")
		}
		if len(result.Manifest.SourceRights) != 0 {
			t.Fatal("mutable source gained pinned input evidence")
		}
		if result.Manifest.SourceDefinition.Value.RequireExecution() == nil {
			t.Fatal("metadata admission enabled execution")
		}
	}
}

func TestHTTPDefinitionCannotDowngradeOrEnable(t *testing.T) {
	for _, mutate := range []func(*Memory){
		func(m *Memory) {
			n := m.Nodes["ovdb.yaml"]
			n.Content = []byte(strings.Replace(string(n.Content), `"executionEnabled":false`, `"executionEnabled":true`, 1))
			m.Nodes["ovdb.yaml"] = n
		},
		func(m *Memory) {
			n := m.Nodes["ovdb.yaml"]
			n.Content = []byte(strings.Replace(string(n.Content), `"retention":"none"`, `"retention":"snapshot"`, 1))
			m.Nodes["ovdb.yaml"] = n
		},
		func(m *Memory) {
			n := m.Nodes["ovdb-database.json"]
			var d map[string]any
			_ = json.Unmarshal(n.Content, &d)
			delete(d, "source_definition")
			n.Content = rightsEncoded(d)
			m.Nodes["ovdb-database.json"] = n
		},
		func(m *Memory) {
			n := m.Nodes["ovdb-database.json"]
			n.Content = []byte(strings.Replace(string(n.Content), `"name":"European Central Bank"`, `"name":"Gateway"`, 1))
			m.Nodes["ovdb-database.json"] = n
		},
		func(m *Memory) {
			n := m.Nodes["ovdb.yaml"]
			var d map[string]any
			_ = json.Unmarshal(n.Content, &d)
			d["data_rights"] = nil
			n.Content = rightsEncoded(d)
			m.Nodes["ovdb.yaml"] = n
		},
		func(m *Memory) {
			n := m.Nodes["ovdb.yaml"]
			var d map[string]any
			_ = json.Unmarshal(n.Content, &d)
			d["representation_contract"] = nil
			n.Content = rightsEncoded(d)
			m.Nodes["ovdb.yaml"] = n
		},
	} {
		m := httpDefinitionRepository(t)
		mutate(m)
		r := Check(m, publisher())
		if r.OK() || r.Manifest.SourceDefinitionEvidence != nil || len(r.Manifest.SourceRights) > 0 {
			t.Fatal("unsafe definition admitted", r.Findings)
		}
	}
}

func TestHTTPDefinitionUsesOriginalGitObjects(t *testing.T) {
	m := httpDefinitionRepository(t)
	valid := m.Nodes["ovdb.yaml"].Content
	m.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Replace(string(valid), `"executionEnabled":false`, `"executionEnabled":true`, 1))}
	dir := committedFixture(t, m.Nodes)
	old := git(t, dir, nil, "rev-parse", "HEAD:ovdb.yaml")
	replacement := git(t, dir, valid, "hash-object", "-w", "--stdin")
	git(t, dir, nil, "replace", old, replacement)
	r := NewGit(ExecRunner{Dir: dir})
	result := Check(r, publisher())
	if result.OK() || result.Manifest.SourceDefinitionEvidence != nil {
		t.Fatal("replacement admitted blocked original", result.Findings)
	}
}

func TestHTTPDefinitionRequiresImmutablePublisherIdentity(t *testing.T) {
	m := httpDefinitionRepository(t)
	parsed, f := manifest.CheckManifest(m.Nodes["ovdb.yaml"].Content, "ovdb.yaml", manifest.Publisher)
	if len(f) != 0 {
		t.Fatal(f)
	}
	j, _ := manifest.NewJudge(manifest.Publisher)
	m.Err = errors.New("no selected commit")
	c := checker{r: m, j: j}
	c.sourceDefinition(&parsed, "ovdb.yaml", m.Nodes["ovdb.yaml"].Content)
	if len(c.res.Findings) != 1 || parsed.SourceDefinitionEvidence != nil {
		t.Fatal("missing immutable identity accepted")
	}
	if fullSHA(strings.Repeat("A", 40)) || fullSHA("short") || !fullSHA(strings.Repeat("a", 40)) {
		t.Fatal("revision validation wrong")
	}
}

func TestHTTPRepositoryClearsEvidenceForContradictoryRights(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(d map[string]any) { d["licences"] = map[string]any{"data": "MIT"} },
		func(d map[string]any) {
			d["recordsets"].([]any)[0].(map[string]any)["licences"] = map[string]any{"data": "MIT"}
		},
		func(d map[string]any) {
			d["licences"].(map[string]any)["data"].(map[string]any)["text"] = "Contradictory replacement terms"
		},
		func(d map[string]any) { d["sourceRights"] = []any{} },
	} {
		m := httpDefinitionRepository(t)
		n := m.Nodes["ovdb-database.json"]
		var d map[string]any
		_ = json.Unmarshal(n.Content, &d)
		mutate(d)
		n.Content = rightsEncoded(d)
		m.Nodes["ovdb-database.json"] = n
		r := Check(m, publisher())
		if r.OK() || r.Manifest.SourceDefinitionEvidence != nil || len(r.Manifest.SourceRights) > 0 {
			t.Fatal("contradictory rights/evidence admitted", r.Findings)
		}
	}
}
