package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/publisher/source"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

const authoredManifest = "publisher/source/ecb-daily/ovdb.yaml"
const authoredDescriptor = "publisher/source/ecb-daily/ovdb-database.json"

// Historical authored metadata is checked with independently authored synthetic
// expectations. Current declarations are checked separately; this fixture does
// not accept a real operator identity or advance the original acceptance.
func authoredFixture(t *testing.T) (*repo.Memory, Proposal, *pinchain.Receipt, []byte) {
	t.Helper()
	_, p, b, d := publisherFixture(t)
	m := &repo.Memory{Nodes: map[string]repo.Node{}}
	// The model files of the working tree must be in an accepted state (model_guard_test.go): the pinned bytes, or exactly their rename. The fixture
	// is built from the pinned bytes either way, which are the bytes that the chain accepts.
	model := requirePinnedModel(t)
	for _, path := range []string{"OVDB.md", authoredManifest, authoredDescriptor, "publisher/source/model/ecb-daily.modelspec.hcl", "publisher/source/model/ecb-daily.modelspec.json", "publisher/source/model/ecb-daily.meaning.yaml"} {
		fixture := filepath.Join("../../..", path)
		if path == authoredManifest || path == authoredDescriptor {
			fixture = filepath.Join("metadata/historical", filepath.Base(path))
		}
		var content []byte
		switch path {
		case "publisher/source/model/ecb-daily.modelspec.hcl":
			content = model.hcl
		case "publisher/source/model/ecb-daily.modelspec.json":
			content = model.json
		default:
			content = bytesAt(t, fixture)
		}
		m.Nodes[path] = repo.Node{Kind: repo.File, Content: content}
	}
	data := m.Nodes[authoredManifest].Content
	p.Publisher.Artifact.Path = authoredManifest
	p.Publisher.Artifact.Bytes = len(data)
	p.Publisher.Artifact.SHA256 = hash(data)
	p.Expected.Binding.DefinitionDigest = hash(data)
	p.Expected.Right.Pins[0].Path = authoredManifest
	p.Expected.Right.Pins[0].Bytes = int64(len(data))
	p.Expected.Right.Pins[0].SHA256 = hash(data)
	var err error
	p.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(p.Expected.Right)
	if err != nil {
		t.Fatal(err)
	}
	return m, p, b, d
}

func authoredVerify(m *repo.Memory, p Proposal, b *pinchain.Receipt, d []byte) (*Receipt, error) {
	return verify(p, b, d, "git version 2.54.0", func(Publisher) (repo.Reader, error) { return m, nil })
}

func TestAuthoredOriginalHTTPMetadataRemainsBlocked(t *testing.T) {
	m, p, b, d := authoredFixture(t)
	r, err := authoredVerify(m, p, b, d)
	if err != nil || r == nil || r.Classification != "blocked-candidate-metadata-verified" || r.ExecutionEnabled || r.DirectoryStatus != "inactive" || strings.Join(r.Blockers, ",") != "B1,B2,B3,B4" {
		t.Fatal(r, err)
	}
	var manifest struct {
		Publisher        struct{ Name, URL, Repository string }
		SourceDefinition source.Definition `json:"source_definition"`
	}
	if err := json.Unmarshal(m.Nodes[authoredManifest].Content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Publisher.Name != "OpenVaultDB metadata maintainers" || manifest.Publisher.URL != "https://github.com/openvaultdb" || manifest.Publisher.Repository != "https://github.com/openvaultdb/ovdb" || manifest.SourceDefinition.Provider.Name != "European Central Bank" {
		t.Fatal("metadata author/provider identity drift")
	}
	baselineDefinition, err := source.Parse(d)
	if err != nil || !bytes.Equal(encoded(t, manifest.SourceDefinition), encoded(t, baselineDefinition)) {
		t.Fatal("authored source definition changed from reviewed baseline", err)
	}
	if err := manifest.SourceDefinition.RequireExecution(); err == nil {
		t.Fatal("version-1 execution accepted")
	}
	manifest.SourceDefinition.Admission.ExecutionEnabled = true
	manifest.SourceDefinition.Admission.Status = "active"
	if err := manifest.SourceDefinition.RequireExecution(); err == nil {
		t.Fatal("mutated version-1 execution accepted")
	}
	// Missing independent authority must refuse before even opening the publisher.
	p.Expected = nil
	if _, err := verify(p, b, d, "Git", func(Publisher) (repo.Reader, error) { t.Fatal("missing expectation opened publisher"); return nil, nil }); err == nil {
		t.Fatal("missing expected identity accepted")
	}
}

func TestAuthoredMetadataRefusesPinDescriptorAndNoticeDrift(t *testing.T) {
	for _, fault := range []string{"repository", "commit", "path", "hash", "size", "missing descriptor", "extra descriptor", "descriptor id", "descriptor definition", "notice", "terms", "model", "expected identity", "expected notice"} {
		t.Run(fault, func(t *testing.T) {
			m, p, b, d := authoredFixture(t)
			replace := func(path, old, changed string) {
				n := m.Nodes[path]
				if !bytes.Contains(n.Content, []byte(old)) {
					t.Fatal("ineffective drift fixture")
				}
				n.Content = bytes.Replace(n.Content, []byte(old), []byte(changed), 1)
				m.Nodes[path] = n
			}
			switch fault {
			case "repository":
				p.Publisher.Artifact.Repository = "other/ovdb"
			case "commit":
				p.Publisher.Artifact.Commit = strings.Repeat("c", 40)
			case "path":
				p.Publisher.Artifact.Path = "other.yaml"
			case "hash":
				p.Publisher.Artifact.SHA256 = strings.Repeat("c", 64)
			case "size":
				p.Publisher.Artifact.Bytes++
			case "missing descriptor":
				delete(m.Nodes, authoredDescriptor)
			case "extra descriptor":
				m.Nodes["extra.json"] = m.Nodes[authoredDescriptor]
				replace("OVDB.md", "---\n\n", "  - ./extra.json\n---\n\n")
			case "descriptor id":
				replace(authoredDescriptor, `"localId": "ecb"`, `"localId": "other"`)
			case "descriptor definition":
				replace(authoredDescriptor, `"maxBytes": 2097152`, `"maxBytes": 2097151`)
			case "notice":
				replace(authoredManifest, "Source: European Central Bank (ECB).", "Changed attribution.")
			case "terms":
				replace(authoredManifest, "ECB reuse conditions", "Changed terms")
			case "model":
				n := m.Nodes["publisher/source/model/ecb-daily.modelspec.hcl"]
				n.Content = append(n.Content, ' ')
				m.Nodes["publisher/source/model/ecb-daily.modelspec.hcl"] = n
			case "expected identity":
				p.Expected.Right.Source.ServerID = "unaccepted-other-operator"
			case "expected notice":
				p.Expected.Right.FreeSource = nil
			}
			if r, err := authoredVerify(m, p, b, d); err == nil || r != nil {
				t.Fatal("drift accepted", r)
			}
		})
	}
}

func TestAuthoredCommittedObjectsExcludeDirtyMetadata(t *testing.T) {
	m, p, b, d := authoredFixture(t)
	dir := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("local synthetic git %v: %v %s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://github.com/openvaultdb/ovdb")
	for path, n := range m.Nodes {
		target := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, n.Content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=Offline metadata fixture", "-c", "user.email=fixture@example.org", "commit", "-qm", "Synthetic metadata only")
	p.Publisher.Directory = dir
	p.Publisher.Artifact.Commit = run("rev-parse", "HEAD")
	p.Publisher.Artifact.Blob = run("rev-parse", "HEAD:"+authoredManifest)
	p.Expected.Right.Pins[0].Revision = p.Publisher.Artifact.Commit
	p.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(p.Expected.Right)
	if err != nil {
		t.Fatal(err)
	}
	g, err := pinchain.AdmitGit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	read := func(p Publisher) (repo.Reader, error) {
		if _, err := g.ReadArtifact(context.Background(), dir, p.Artifact); err != nil {
			return nil, err
		}
		return repo.AtCommit(repo.NewGit(repo.ExecRunner{Git: g.Executable(), Dir: dir}), p.Artifact.Commit), nil
	}
	if r, err := verify(p, b, d, g.Version(), read); err != nil || r.Classification != "blocked-candidate-metadata-verified" {
		t.Fatal(r, err)
	}
	// A staged, dirty descriptor cannot repair or replace the selected commit.
	if err := os.WriteFile(filepath.Join(dir, authoredDescriptor), []byte("invalid dirty descriptor"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", authoredDescriptor)
	if r, err := verify(p, b, d, g.Version(), read); err != nil || r.ExecutionEnabled {
		t.Fatal("dirty bytes became metadata", r, err)
	}
	// Pinning the dirty body instead of the committed object must refuse.
	bad := p
	pub := *p.Publisher
	bad.Publisher = &pub
	bad.Publisher.Artifact.SHA256 = hash([]byte("dirty manifest"))
	if r, err := verify(bad, b, d, g.Version(), read); err == nil || r != nil {
		t.Fatal("dirty-byte pin accepted")
	}
	// Once descriptor damage is committed, that commit cannot be accepted either.
	run("-c", "user.name=Offline metadata fixture", "-c", "user.email=fixture@example.org", "commit", "-qm", "Synthetic descriptor damage")
	p.Publisher.Artifact.Commit = run("rev-parse", "HEAD")
	p.Expected.Right.Pins[0].Revision = p.Publisher.Artifact.Commit
	p.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(p.Expected.Right)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := verify(p, b, d, g.Version(), read); err == nil || r != nil {
		t.Fatal("committed descriptor damage accepted")
	}
}
