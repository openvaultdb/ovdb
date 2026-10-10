package repo

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

func contractFixture(t *testing.T) (*Memory, *Memory, DependencyReaders) {
	t.Helper()
	provider := &Memory{Nodes: map[string]Node{"OVDB.md": {Kind: File, Content: []byte("---\novdb: 1\npublish: [./ovdb.yaml]\n---\n")}, "model/chinook.modelspec.json": {Kind: File, Content: []byte(`{"module":{"name":"chinook"},"entities":{"Album":{},"Artist":{}}}`)}, "model/chinook.modelspec.hcl": {Kind: File}, "model/chinook.meaning.yaml": {Kind: File, Content: []byte("id: chinook\nlicense: CC0-1.0\nmodels: {chinook: chinook.modelspec.hcl}\nconcepts: []\n")}}}
	dependency := &Memory{Nodes: map[string]Node{}}
	for _, name := range []string{"contract.json", "source.modelspec.json", "target.modelspec.json", "core.meaning.json", "target.meaning.json", "snapshot.json", "keys.json", "bridge.json", "decision.md"} {
		data, err := os.ReadFile("../../../publisher/representation/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "contract.json" || name == "target.meaning.json" {
			data = []byte(strings.ReplaceAll(string(data), strings.Repeat("b", 40), strings.Repeat("a", 40)))
		}
		provider.Nodes[name] = Node{Kind: File, Content: data}
		dependency.Nodes[name] = Node{Kind: File, Content: data}
	}
	var doc representation.Document
	_ = json.Unmarshal(provider.Nodes["contract.json"].Content, &doc)
	doc.Contracts[0].Target.Binding.Document.SHA256 = representation.Hash(provider.Nodes["target.meaning.json"].Content)
	doc.Contracts[0].Target.Model.Path = "model/chinook.modelspec.json"
	doc.Contracts[0].Target.Binding.Document.Path = "model/chinook.meaning.yaml"
	provider.Nodes["model/chinook.modelspec.json"] = provider.Nodes["target.modelspec.json"]
	n := provider.Nodes["target.meaning.json"]
	n.Content = []byte(strings.TrimSuffix(string(n.Content), "}\n") + `,"id":"chinook","license":"CC0-1.0","models":{"geo":"chinook.modelspec.hcl"}}`)
	provider.Nodes["model/chinook.meaning.yaml"] = n
	doc.Contracts[0].Target.Binding.Document.SHA256 = representation.Hash(n.Content)
	data, _ := json.Marshal(doc)
	provider.Nodes["contract.json"] = Node{Kind: File, Content: data}
	provider.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(ownManifest, "/chinook\n", "/geo\n"), "  - Album\n  - Artist\n", "  - Countries\n  - CustomerCountries\n"), "chinook.modelspec.hcl", "chinook.modelspec.hcl") + "representation_contract: {path: contract.json, sha256: " + representation.Hash(data) + "}\n")}
	return provider, dependency, DependencyReaders{{Repository: "https://github.com/example/source", Revision: strings.Repeat("a", 40)}: dependency}
}
func TestRepresentationRepository(t *testing.T) {
	p, _, o := contractFixture(t)
	findings := checkFixtureRepresentation(p, o)
	if len(findings) > 0 {
		t.Fatal(findings)
	}
}
func TestRepresentationRepositoryNegatives(t *testing.T) {
	for _, name := range []string{"missing", "unreadable", "hash", "head", "external unavailable", "external revision", "external missing", "external tree", "external blob", "invalid schema", "manifest link"} {
		t.Run(name, func(t *testing.T) {
			p, d, o := contractFixture(t)
			switch name {
			case "missing":
				delete(p.Nodes, "contract.json")
			case "unreadable":
				p.BrokenBlobs = map[string]error{"contract.json": errors.New("broken")}
			case "hash":
				p.Nodes["contract.json"] = Node{Kind: File, Content: []byte("{}")}
			case "head": // The second HEAD is deliberately made unavailable, after the outer check succeeds.
				r := &headFailsAfterFirst{Memory: p}
				r.calls = 1
				findings := checkFixtureRepresentation(r, o)
				if len(findings) == 0 {
					t.Fatal("second HEAD error accepted")
				}
				return
			case "external unavailable":
				o = nil
			case "external revision":
				d.Err = errors.New("no revision")
			case "external missing":
				delete(d.Nodes, "source.modelspec.json")
			case "external tree":
				d.BrokenDirs = map[string]error{"": errors.New("broken tree")}
			case "external blob":
				d.BrokenBlobs = map[string]error{"source.modelspec.json": errors.New("broken blob")}
			case "manifest link":
				n := p.Nodes["ovdb.yaml"]
				n.Content = []byte(strings.ReplaceAll(string(n.Content), "  - CustomerCountries\n", ""))
				p.Nodes["ovdb.yaml"] = n
			case "invalid schema":
				n := p.Nodes["contract.json"]
				n.Content = []byte(strings.ReplaceAll(string(n.Content), representation.Format, "unsupported"))
				p.Nodes["contract.json"] = n
				p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(ownManifest + "representation_contract: {path: contract.json, sha256: " + representation.Hash(n.Content) + "}\n")}
			}
			if len(checkFixtureRepresentation(p, o)) == 0 {
				t.Fatal("bad contract accepted")
			}
		})
	}
}

type headFailsAfterFirst struct {
	*Memory
	calls int
}

func (r *headFailsAfterFirst) Head() (string, error) {
	r.calls++
	if r.calls > 1 {
		return "", errors.New("gone")
	}
	return r.Memory.Head()
}

func checkFixtureRepresentation(r Reader, dependencies DependencyReaders) []manifest.Finding {
	data, _ := r.Blob("ovdb.yaml", MaxFileBytes)
	m, _ := manifest.CheckManifest(data, "ovdb.yaml", manifest.Directory)
	attachment, err := representation.ParseAttachment(data)
	if err != nil {
		panic(err)
	}
	return CheckRepresentation(r, m, *attachment, dependencies)
}
func TestDefaultAttachedLabelRequiresMetadataReaders(t *testing.T) {
	p, _, _ := contractFixture(t)
	if Check(p, Options{Profile: manifest.Publisher}).OK() {
		t.Fatal("default attached check accepted absent metadata readers")
	}
}
func TestRepresentationAttachmentMustBeLocal(t *testing.T) {
	p, _, deps := contractFixture(t)
	if len(CheckRepresentation(p, manifest.Manifest{}, representation.Reference{Repository: "https://github.com/example/source"}, deps)) == 0 {
		t.Fatal("external attachment admitted")
	}
}

type pinnedFixtureReader struct {
	*Memory
	revision string
}

func (r pinnedFixtureReader) Head() (string, error) { return r.revision, nil }

// nativeFixture is the repository of a contract that is read by the native identifier (real ROR files), the manifest that goes with it as the facts of its
// own-form manifest, the attachment and the dependencies.
func nativeFixture(t *testing.T) (Reader, manifest.Manifest, representation.Reference, DependencyReaders) {
	t.Helper()
	dir := "../../../publisher/representation/testdata/real-ror/"
	data, err := os.ReadFile(dir + "contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Reference representation.Reference `json:"reference"`
		File      string                   `json:"file"`
	}
	refs, err := os.ReadFile(dir + "references.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(refs, &entries); err != nil {
		t.Fatal(err)
	}
	provider := &Memory{Nodes: map[string]Node{"contract.json": {Kind: File, Content: data}}}
	deps := DependencyReaders{}
	for _, entry := range entries {
		bytes, err := os.ReadFile(dir + entry.File)
		if err != nil {
			t.Fatal(err)
		}
		reader := provider
		if entry.Reference.Repository != "" {
			key := DependencyKey{Repository: entry.Reference.Repository, Revision: entry.Reference.Revision}
			existing, ok := deps[key]
			if !ok {
				existing = pinnedFixtureReader{Memory: &Memory{Nodes: map[string]Node{}}, revision: entry.Reference.Revision}
				deps[key] = existing
			}
			reader = existing.(pinnedFixtureReader).Memory
		}
		reader.Nodes[entry.Reference.Path] = Node{Kind: File, Content: bytes}
	}
	m := manifest.Manifest{Form: manifest.FormOwn, PublisherRepository: manifest.Fact[string]{Present: true, Valid: true, Value: "https://github.com/ingitdb/ror-ingitdb"}, ModelSpec: manifest.Fact[string]{Present: true, Valid: true, Value: "model/ror.modelspec.json"}, MeaningFile: manifest.Fact[string]{Present: true, Valid: true, Value: "model/ror.meaning.yaml"}, Recordsets: manifest.Fact[[]string]{Present: true, Valid: true, Value: []string{"organizations"}}}
	r := pinnedFixtureReader{Memory: provider, revision: "bbbec903248680caea04e68f94b9a957b6efc55b"}
	return r, m, representation.Reference{Path: "contract.json", SHA256: representation.Hash(data)}, deps
}

func TestNativeRepresentationRepository(t *testing.T) {
	r, m, a, deps := nativeFixture(t)
	if problems := CheckRepresentation(r, m, a, deps); len(problems) > 0 {
		t.Fatal(problems)
	}
	// There is deliberately no native data file or key corpus in this reader.
	m.Recordsets.Value = nil
	if len(CheckRepresentation(r, m, a, deps)) == 0 {
		t.Fatal("undeclared native target recordset accepted")
	}
}

func TestRepresentationUsesMappedTargetAndNativeBridgeRecordsets(t *testing.T) {
	for _, name := range []string{"mapped target", "missing mapping", "incorrect mapping", "missing target", "mapped bridge only"} {
		t.Run(name, func(t *testing.T) {
			p, _, deps := contractFixture(t)
			n := p.Nodes["ovdb.yaml"]
			text := strings.ReplaceAll(string(n.Content), "  - Countries\n", "  - dbo.Countries\n")
			mapping := "recordset_entities: {dbo.Countries: Countries}\n"
			switch name {
			case "missing mapping":
				mapping = ""
			case "incorrect mapping":
				mapping = "recordset_entities: {dbo.Countries: CustomerCountries}\n"
			case "missing target":
				text = strings.ReplaceAll(text, "  - dbo.Countries\n", "")
				mapping = ""
			case "mapped bridge only":
				text = strings.ReplaceAll(text, "  - CustomerCountries\n", "  - dbo.CustomerCountries\n")
				mapping = "recordset_entities: {dbo.Countries: Countries, dbo.CustomerCountries: CustomerCountries}\n"
			}
			n.Content = []byte(text + mapping)
			p.Nodes["ovdb.yaml"] = n
			findings := checkFixtureRepresentation(p, deps)
			result := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps})
			if name == "mapped target" {
				if len(findings) != 0 || !result.OK() {
					t.Fatalf("valid mapped target refused: metadata %v; default %v", findings, result.Findings)
				}
				return
			}
			linked := false
			for _, finding := range findings {
				linked = linked || finding.Rule == "representation-manifest-link"
			}
			if !linked || result.OK() {
				t.Fatalf("invalid association accepted: metadata %v; default %v", findings, result.Findings)
			}
		})
	}
}
