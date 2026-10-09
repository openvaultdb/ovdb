package repo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

// providerOf is the repository whose metadata a fixture holds; the fixture in the current vocabulary is the same provider's.
func providerOf(name string) string {
	return "https://github.com/ingitdb/" + map[string]string{"real-ror": "ror-ingitdb", "native-geonames": "geo-ingitdb"}[strings.TrimSuffix(name, "-current")]
}

// Build two independent native scopes from captured metadata, with synthetic raw
// input bytes. These prove structural/data behavior, never production admission.
func defaultNativeFixture(t *testing.T, name string, body []byte) (*Memory, DependencyReaders, representation.Reference) {
	t.Helper()
	dir := "../../../publisher/representation/testdata/" + name + "/"
	data, err := os.ReadFile(dir + "contract.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := representation.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	doc.Format = representation.Format3
	ref := representation.Reference{Repository: "https://github.com/example/input", Revision: strings.Repeat("d", 40), Path: "input/$records/rows.json", SHA256: representation.Hash(body)}
	doc.Contracts[0].Source.Data = &ref
	var refs []struct {
		Reference representation.Reference `json:"reference"`
		File      string                   `json:"file"`
	}
	raw, _ := os.ReadFile(dir + "references.json")
	if err = json.Unmarshal(raw, &refs); err != nil {
		t.Fatal(err)
	}
	p := &Memory{Nodes: map[string]Node{}}
	deps := DependencyReaders{}
	for _, entry := range refs {
		data, err := os.ReadFile(dir + entry.File)
		if err != nil {
			t.Fatal(err)
		}
		reader := p
		if entry.Reference.Repository != "" {
			key := DependencyKey{entry.Reference.Repository, entry.Reference.Revision}
			if deps[key] == nil {
				deps[key] = pinnedFixtureReader{Memory: &Memory{Nodes: map[string]Node{}}, revision: key.Revision}
			}
			reader = deps[key].(pinnedFixtureReader).Memory
		}
		reader.Nodes[entry.Reference.Path] = Node{Kind: File, Content: data}
	}
	_, r := sourceProofFixture(body, ref.Revision)
	r.Nodes = map[string]Node{ref.Path: {Kind: File, Content: body}}
	deps[DependencyKey{ref.Repository, ref.Revision}] = r
	c := doc.Contracts[0]
	model, err := readModel(p.Nodes[c.Target.Model.Path].Content)
	if err != nil {
		t.Fatal(err)
	}
	graph := c.Target.Module
	p.Nodes["model/"+graph+".modelspec.hcl"] = Node{Kind: File}
	m := map[string]any{"format": manifest.ManifestFormat, "id": graph, "title": "Native fixture", "description": "Synthetic offline proof", "url": "https://ovdb.example.com/" + graph, "deployment": map[string]any{"url": "https://ovdb.example.com/" + graph, "engine": "sqlite", "discovery": "https://ovdb.example.com/.well-known/openvaultdb"}, "model": map[string]any{"modelspec": c.Target.Model.Path, "hcl": "model/" + graph + ".modelspec.hcl"}, "meaning": map[string]any{"file": c.Target.Binding.Document.Path, "graph": map[string]any{"id": graph, "address": "meaning://" + strings.TrimPrefix(providerOf(name), "https://")}}, "publisher": map[string]any{"name": "Fixture", "url": "https://github.com/ingitdb", "repository": providerOf(name)}, "licences": map[string]any{"data": "CC0-1.0 AND CC-BY-4.0", "model": "MIT", "meaning": "CC0-1.0"}, "recordsets": model.entities}
	attachment, _ := json.Marshal(doc)
	p.Nodes["contract.json"] = Node{Kind: File, Content: attachment}
	m["representation_contract"] = map[string]string{"path": "contract.json", "sha256": representation.Hash(attachment)}
	raw, _ = json.Marshal(m)
	p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
	p.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [./ovdb.yaml]\n---\n")}
	return p, deps, ref
}

func TestDefaultAttachedCheckRequiresByteProof(t *testing.T) {
	for _, name := range []string{"real-ror", "native-geonames"} {
		p, deps, ref := defaultNativeFixture(t, name, []byte("\xff\xef\xbb\xbf raw exact bytes"))
		r := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps})
		if !r.OK() {
			t.Fatalf("%s: %v", name, r.Findings)
		}
		dataReader := deps[DependencyKey{ref.Repository, ref.Revision}].(*sourceReader)
		if dataReader.blobCalls != 1 {
			t.Fatal("data proof not required")
		}
		delete(deps, DependencyKey{ref.Repository, ref.Revision})
		data, _ := p.Blob("ovdb.yaml", MaxFileBytes)
		m, _ := manifest.CheckManifest(data, "ovdb.yaml", manifest.Publisher)
		a, _ := representation.ParseAttachment(data)
		if findings := CheckRepresentation(p, m, *a, deps); len(findings) != 0 {
			t.Fatalf("metadata helper read raw data: %v", findings)
		}
		if Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}).OK() {
			t.Fatal("missing data reader produced default success")
		}
	}
}

func TestDefaultSourceDataBoundsDedupAndRefusals(t *testing.T) {
	for _, size := range []int{(4 << 20) + 1, MaxSourceDataBytes, MaxSourceDataBytes + 1} {
		p, deps, _ := defaultNativeFixture(t, "real-ror", bytes.Repeat([]byte("x"), size))
		if got := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}); got.OK() != (size <= MaxSourceDataBytes) {
			t.Fatalf("size %d: %v", size, got.Findings)
		}
	}
	p, deps, ref := defaultNativeFixture(t, "real-ror", []byte("original"))
	reader := deps[DependencyKey{ref.Repository, ref.Revision}].(*sourceReader)
	p.Nodes["other.yaml"] = p.Nodes["ovdb.yaml"]
	p.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [./ovdb.yaml, ./other.yaml]\n---\n")}
	if got := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}); !got.OK() || reader.blobCalls != 1 {
		t.Fatalf("dedupe %v calls=%d", got.Findings, reader.blobCalls)
	}
	reader.Nodes[ref.Path] = Node{Kind: File, Content: []byte("changed")}
	if Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}).OK() || reader.blobCalls != 2 {
		t.Fatal("cross-check stale data proof")
	}
	for _, kind := range []Kind{Missing, Symlink, Submodule} {
		reader.Nodes[ref.Path] = Node{Kind: kind}
		if Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}).OK() {
			t.Fatal("nonregular input accepted")
		}
	}
}

// stageGoldenFile is the Directory's verdicts on the representation stages (testdata/reference/representation-stages.mjs): the data-stage mutations of the
// fixtures, and the edits to the target model and the source schema of the fixtures in the current vocabulary.
type stageGoldenFile struct {
	Format     string `json:"format"`
	Strength   string `json:"strength"`
	Cases      []stageGoldenCase
	Vocabulary []vocabularyCase
}
type stageGoldenCase struct {
	Fixture  string          `json:"fixture"`
	Mutation string          `json:"mutation"`
	Size     int             `json:"size"`
	Metadata bool            `json:"metadata"`
	Data     SourceDataStage `json:"data"`
}
type vocabularyCase struct {
	Fixture  string `json:"fixture"`
	Position string `json:"position"`
	Edit     string `json:"edit"`
	From     string `json:"from"`
	Old      string `json:"old"`
	New      string `json:"new"`
	Bytes    string `json:"bytes"`
	Metadata bool   `json:"metadata"`
}

func readStages(t *testing.T) stageGoldenFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/reference/representation-stages.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden stageGoldenFile
	if err = json.Unmarshal(raw, &golden); err != nil || golden.Format != "ovdb-representation-stage-reference/1" || golden.Strength != "structural-metadata-and-offline-raw-bytes-only" {
		t.Fatalf("stage golden: %v", err)
	}
	return golden
}

// attachedStage is what Go says of the attached contract of a provider: the findings of the metadata stage and, when it holds, the stage of the data.
func attachedStage(p *Memory, deps DependencyReaders) (metadata bool, data SourceDataStage, findings []manifest.Finding) {
	manifestBytes := p.Nodes["ovdb.yaml"].Content
	m, _ := manifest.CheckManifest(manifestBytes, "ovdb.yaml", manifest.Publisher)
	attachment, _ := representation.ParseAttachment(manifestBytes)
	c := &checker{r: p, dirs: map[string]dirResult{}, files: map[string]fileRead{}}
	findings, doc := c.attachedMetadata(m, *attachment, deps)
	metadata, data = len(findings) == 0, SourceDataOutsideScope
	if doc != nil && metadata {
		data = VerifySourceData(doc, deps).Stage
	}
	return metadata, data, findings
}

// attach writes the contract document to the provider and points the manifest at its bytes.
func attach(p *Memory, doc representation.Document) {
	raw, _ := json.Marshal(doc)
	p.Nodes["contract.json"] = Node{Kind: File, Content: raw}
	var m map[string]any
	_ = json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m)
	m["representation_contract"] = map[string]string{"path": "contract.json", "sha256": representation.Hash(raw)}
	raw, _ = json.Marshal(m)
	p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
}

func TestRepresentationStagesAgreeWithLandedDirectory(t *testing.T) {
	golden := readStages(t)
	if len(golden.Cases) != 52 {
		t.Fatalf("%d cases", len(golden.Cases))
	}
	for _, tc := range golden.Cases {
		t.Run(tc.Fixture+"/"+tc.Mutation, func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), tc.Size)
			if tc.Mutation == "raw-bom" {
				body = []byte{239, 187, 191}
			}
			if tc.Mutation == "raw-invalid-utf8" {
				body = []byte{255}
			}
			p, deps, ref := defaultNativeFixture(t, tc.Fixture, body)
			key := DependencyKey{ref.Repository, ref.Revision}
			input := deps[key].(*sourceReader)
			switch tc.Mutation {
			case "missing-data-reader":
				delete(deps, key)
			case "wrong-data-head":
				input.revision = strings.Repeat("e", 40)
			case "data-symlink":
				input.Nodes[ref.Path] = Node{Kind: Symlink}
			case "data-submodule":
				input.Nodes[ref.Path] = Node{Kind: Submodule}
			case "data-missing":
				delete(input.Nodes, ref.Path)
			case "metadata-symlink":
				var doc representation.Document
				_ = json.Unmarshal(p.Nodes["contract.json"].Content, &doc)
				p.Nodes[doc.Contracts[0].Target.Binding.Document.Path] = Node{Kind: Symlink}
			case "wrong-data-hash":
				var doc representation.Document
				_ = json.Unmarshal(p.Nodes["contract.json"].Content, &doc)
				doc.Contracts[0].Source.Data.SHA256 = strings.Repeat("b", 64)
				attach(p, doc)
			}
			metadata, data, findings := attachedStage(p, deps)
			if metadata != tc.Metadata || data != tc.Data {
				t.Fatalf("like-strength Go=%v/%s JS=%v/%s, findings=%v", metadata, data, tc.Metadata, tc.Data, findings)
			}
			if got := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}); got.OK() != (tc.Metadata && tc.Data == SourceDataChecked) {
				t.Fatalf("default result lost a required stage: %v", got.Findings)
			}
		})
	}
}

// fixtureDocument is the bytes of the document at path in a fixture of publisher/representation/testdata, as its references.json names them.
func fixtureDocument(t *testing.T, fixture, path string) []byte {
	t.Helper()
	dir := "../../../publisher/representation/testdata/" + fixture + "/"
	raw, err := os.ReadFile(dir + "references.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Reference representation.Reference `json:"reference"`
		File      string                   `json:"file"`
	}
	if err = json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.Reference.Path == path {
			data, err := os.ReadFile(dir + item.File)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("%s: no %s", fixture, path)
	return nil
}

// replaceModel puts another document in place of the target model or of the source schema of a fixture, with every hash that this changes put right in the files
// that hold it: the contract pins the model, the receipts that name it and the snapshot that lists it and them. Each file that holds a hash that changed changes
// too, and so on until none does. Nothing but a hash is edited (the Directory's stage does the same).
func replaceModel(t *testing.T, p *Memory, deps DependencyReaders, position string, document []byte) {
	t.Helper()
	var doc representation.Document
	if err := json.Unmarshal(p.Nodes["contract.json"].Content, &doc); err != nil {
		t.Fatal(err)
	}
	contract := &doc.Contracts[0]
	if position == "source" {
		ref := &contract.Source.Schema
		deps[DependencyKey{ref.Repository, ref.Revision}].(pinnedFixtureReader).Nodes[ref.Path] = Node{Kind: File, Content: document}
		ref.SHA256 = representation.Hash(document)
		attach(p, doc)
		return
	}
	ref := &contract.Target.Model
	renames := map[string]string{ref.SHA256: representation.Hash(document)}
	p.Nodes[ref.Path] = Node{Kind: File, Content: document}
	for again := true; again; {
		again = false
		for path, node := range p.Nodes {
			if path == ref.Path || path == "contract.json" || path == "ovdb.yaml" || path == "OVDB.md" || node.Kind != File {
				continue
			}
			text := string(node.Content)
			for from, to := range renames {
				text = strings.ReplaceAll(text, from, to)
			}
			if text != string(node.Content) {
				renames[representation.Hash(node.Content)] = representation.Hash([]byte(text))
				p.Nodes[path] = Node{Kind: File, Content: []byte(text)}
				again = true
			}
		}
	}
	raw, _ := json.Marshal(doc)
	text := string(raw)
	for from, to := range renames {
		text = strings.ReplaceAll(text, from, to)
	}
	doc = representation.Document{}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	attach(p, doc)
}

// vocabularyRefusals is why Go refuses each edit that it refuses, so that none is refused for a reason that is not the edit's (a wrong hash in the files that the
// edit changed would refuse them all): the Directory's golden says only that it refuses.
var vocabularyRefusals = map[string]string{
	"current-keys-under-the-earlier-identifier": "ModelSpec document says",
	"earlier-keys-under-the-current-identifier": "ModelSpec document says",
	"current-keys-under-an-unknown-identifier":  "does not resolve exactly",
	"entities-beside-records":                   "ModelSpec document says",
	"records-beside-entities":                   "ModelSpec document says",
	"properties-in-a-record-type":               "ModelSpec document says",
	"fields-in-an-entity":                       "ModelSpec document says",
	"entity-on-a-field":                         "ModelSpec document says",
	"record-on-a-property":                      "ModelSpec document says",
	"collections-under-the-current":             "removed or reserved",
	"projections-under-the-earlier":             "removed or reserved",
	"records-in-capitals":                       "non-exact JSON field",
	"module-is-another":                         "does not resolve exactly",
}

// The edits to a model or a schema in either vocabulary are the Directory's (each one string replacement; the golden carries it and the hash of the document it
// makes): Go and the Directory agree on which of them the contract can point at.
func TestRepresentationVocabularyAgreesWithLandedDirectory(t *testing.T) {
	golden := readStages(t)
	if len(golden.Vocabulary) != 56 {
		t.Fatalf("%d cases", len(golden.Vocabulary))
	}
	accepted := 0
	for _, tc := range golden.Vocabulary {
		t.Run(tc.Fixture+"/"+tc.Position+"/"+tc.Edit, func(t *testing.T) {
			p, deps, _ := defaultNativeFixture(t, tc.Fixture, []byte("xxx"))
			var doc representation.Document
			_ = json.Unmarshal(p.Nodes["contract.json"].Content, &doc)
			path := doc.Contracts[0].Target.Model.Path
			if tc.Position == "source" {
				path = doc.Contracts[0].Source.Schema.Path
			}
			from := tc.Fixture
			if tc.From == "earlier" {
				from = strings.TrimSuffix(tc.Fixture, "-current")
			}
			base := string(fixtureDocument(t, from, path))
			if !strings.Contains(base, tc.Old) {
				t.Fatalf("%q is not in the document", tc.Old)
			}
			edited := []byte(strings.Replace(base, tc.Old, tc.New, 1))
			if representation.Hash(edited) != tc.Bytes {
				t.Fatalf("this edit makes another document than the one the Directory was asked about")
			}
			replaceModel(t, p, deps, tc.Position, edited)
			metadata, data, findings := attachedStage(p, deps)
			if metadata != tc.Metadata || metadata && data != SourceDataChecked {
				t.Fatalf("Go=%v/%s JS=%v, findings=%v", metadata, data, tc.Metadata, findings)
			}
			if metadata {
				accepted++
			} else if want := vocabularyRefusals[tc.Edit]; want == "" || !strings.Contains(fmt.Sprint(findings), want) {
				t.Fatalf("want a refusal that says %q, got %v", want, findings)
			}
		})
	}
	if accepted != 4 {
		t.Fatalf("%d edits are accepted, want the 4 that leave the document in one vocabulary", accepted)
	}
}
