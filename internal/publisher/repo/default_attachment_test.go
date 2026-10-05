package repo

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

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
	m := map[string]any{"format": manifest.ManifestFormat, "id": graph, "title": "Native fixture", "description": "Synthetic offline proof", "url": "https://ovdb.example.com/" + graph, "deployment": map[string]any{"url": "https://ovdb.example.com/" + graph, "engine": "sqlite", "discovery": "https://ovdb.example.com/.well-known/openvaultdb"}, "model": map[string]any{"modelspec": c.Target.Model.Path, "hcl": "model/" + graph + ".modelspec.hcl"}, "meaning": map[string]any{"file": c.Target.Binding.Document.Path, "graph": map[string]any{"id": graph, "address": "meaning://github.com/ingitdb/" + map[string]string{"real-ror": "ror-ingitdb", "native-geonames": "geo-ingitdb"}[name]}}, "publisher": map[string]any{"name": "Fixture", "url": "https://github.com/ingitdb", "repository": "https://github.com/ingitdb/" + map[string]string{"real-ror": "ror-ingitdb", "native-geonames": "geo-ingitdb"}[name]}, "licences": map[string]any{"data": "CC0-1.0 AND CC-BY-4.0", "model": "MIT", "meaning": "CC0-1.0"}, "recordsets": model.entities}
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

func TestRepresentationStagesAgreeWithLandedDirectory(t *testing.T) {
	raw, err := os.ReadFile("testdata/reference/representation-stages.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Format   string `json:"format"`
		Strength string `json:"strength"`
		Cases    []struct {
			Fixture  string          `json:"fixture"`
			Mutation string          `json:"mutation"`
			Size     int             `json:"size"`
			Metadata bool            `json:"metadata"`
			Data     SourceDataStage `json:"data"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &golden); err != nil || golden.Format != "ovdb-representation-stage-reference/1" || golden.Strength != "structural-metadata-and-offline-raw-bytes-only" || len(golden.Cases) != 26 {
		t.Fatalf("stage golden: %v", err)
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
				raw, _ := json.Marshal(doc)
				p.Nodes["contract.json"] = Node{Kind: File, Content: raw}
				var m map[string]any
				_ = json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m)
				m["representation_contract"] = map[string]string{"path": "contract.json", "sha256": representation.Hash(raw)}
				raw, _ = json.Marshal(m)
				p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
			}
			manifestBytes := p.Nodes["ovdb.yaml"].Content
			m, _ := manifest.CheckManifest(manifestBytes, "ovdb.yaml", manifest.Publisher)
			attachment, _ := representation.ParseAttachment(manifestBytes)
			c := &checker{r: p, dirs: map[string]dirResult{}, files: map[string]fileRead{}}
			problems, doc := c.attachedMetadata(m, *attachment, deps)
			metadata := len(problems) == 0
			data := SourceDataOutsideScope
			if doc != nil && metadata {
				data = VerifySourceData(doc, deps).Stage
			}
			if metadata != tc.Metadata || data != tc.Data {
				t.Fatalf("like-strength Go=%v/%s JS=%v/%s, findings=%v", metadata, data, tc.Metadata, tc.Data, problems)
			}
			if got := Check(p, Options{Profile: manifest.Publisher, Dependencies: deps}); got.OK() != (tc.Metadata && tc.Data == SourceDataChecked) {
				t.Fatalf("default result lost a required stage: %v", got.Findings)
			}
		})
	}
}
