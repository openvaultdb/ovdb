package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	pubmanifest "github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

// These expectations were authored from independently reviewed original metadata,
// before running PrepareDynamicSourceRight or preflight. They describe metadata
// acceptance only, and cannot serve as a runtime profile or execution admission.
func acceptedProposal(t *testing.T) Proposal {
	t.Helper()
	p, err := Parse(bytesAt(t, "metadata/ecb-daily.proposal.json"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAcceptedOriginalMetadataInventory(t *testing.T) {
	ctx := context.Background()
	g, err := pinchain.AdmitGit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Format    string              `json:"format"`
		Artifacts []pinchain.Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(bytesAt(t, "metadata/ecb-daily.original-artifacts.json"), &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Format != "ovdb-ecb-original-metadata-inventory/1" || len(inventory.Artifacts) != 3 {
		t.Fatal("original metadata inventory changed")
	}
	p := acceptedProposal(t)
	if !reflect.DeepEqual(inventory.Artifacts[1], p.Publisher.Artifact) {
		t.Fatal("whole manifest pin diverges from independent proposal")
	}
	runner := repo.ExecRunner{Git: g.Executable(), Dir: "../../.."}
	shallow, err := runner.Run([]string{"--no-replace-objects", "rev-parse", "--is-shallow-repository"}, 32)
	if err != nil || (strings.TrimSpace(string(shallow)) != "true" && strings.TrimSpace(string(shallow)) != "false") {
		t.Fatal("cannot establish checkout history boundary", err)
	}
	isShallow := strings.TrimSpace(string(shallow)) == "true"
	current := currentMetadataInventory(t)
	for i, role := range []string{"publisher-index", "publisher-manifest", "paired-descriptor"} {
		a := inventory.Artifacts[i]
		if a.Role != role || a.Commit != p.Publisher.Artifact.Commit || a.Repository != "openvaultdb/ovdb" {
			t.Fatal("metadata pairing changed")
		}
		if isShallow {
			if current[i].Path != a.Path || current[i].Role != a.Role {
				t.Fatal("current inventory changed historical artifact locations")
			}
			if err := checkCurrentMetadata(ctx, g, "../../..", current[i]); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := g.ReadArtifact(ctx, "../../..", a); err != nil {
			t.Fatal(err)
		}
		// Each original-object identity component independently refuses drift.
		for _, fault := range []string{"commit", "path", "blob", "digest", "size"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				bad := a
				switch fault {
				case "commit":
					bad.Commit = "0000000000000000000000000000000000000000"
				case "path":
					bad.Path = "missing-original-metadata"
				case "blob":
					bad.Blob = "0000000000000000000000000000000000000000"
				case "digest":
					bad.SHA256 = hash([]byte("altered metadata"))
				case "size":
					bad.Bytes++
				}
				if _, err := g.ReadArtifact(ctx, "../../..", bad); err == nil {
					t.Fatal("altered original-object pin accepted")
				}
			})
		}
	}
	if isShallow {
		t.Log("shallow checkout: tracked current artifact bytes/hash/blob/size match separate current inventory; historical commit and original publisher/preflight proof not performed")
		return
	}
	reader := repo.AtCommit(repo.NewGit(repo.ExecRunner{Git: g.Executable(), Dir: "../../.."}), p.Publisher.Artifact.Commit)
	url := "https://github.com/openvaultdb/ovdb"
	checked := repo.Check(reader, repo.Options{Repository: &url, Profile: pubmanifest.Publisher})
	if !checked.OK() || checked.Descriptors != 1 {
		t.Fatal("original publisher/paired descriptor refused", checked.Findings)
	}
	d := checked.Manifest.SourceDefinition.Value
	if d.RequireExecution() == nil {
		t.Fatal("original source execution admitted")
	}
	b, definition := baseline(t)
	p.Publisher.Directory, err = filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	r, err := verify(p, b, definition, g.Version(), func(pub Publisher) (repo.Reader, error) {
		if _, err := g.ReadArtifact(ctx, pub.Directory, pub.Artifact); err != nil {
			return nil, err
		}
		return reader, nil
	})
	if err != nil || r == nil || r.Classification != "blocked-candidate-metadata-verified" || r.ExecutionEnabled || r.DirectoryStatus != "inactive" || !slices.Equal(r.Blockers, []string{"B1", "B2", "B3", "B4"}) || r.RuntimeEnforcement != "unproved" {
		t.Fatal("accepted metadata cleared runtime gates", r, err)
	}
	t.Logf("original publisher check: commit=%s manifest=1 paired-descriptor=1; preflight=%s; execution=false; Directory=inactive; blockers=B1,B2,B3,B4", p.Publisher.Artifact.Commit, r.Classification)
}

func TestAcceptedDetachedRightAndBinding(t *testing.T) {
	p := acceptedProposal(t)
	if p.Executor.ServerID != "openvaultdb-cloud" || p.Executor.DatabaseID != "ecb" || p.Executor.Recordset != "daily" || p.Executor.SourceID() != "ovdb:openvaultdb-cloud/ecb/daily" || p.Expected.Binding.ProviderSourceID != "provider:ecb/FxReferenceQuote" || p.Expected.Binding.ResourceID != "ecb-daily" {
		t.Fatal("explicit native/executor/resource decision changed")
	}
	digest, err := providerreads.RightsDigest(p.Expected.Right)
	if err != nil || digest != p.Expected.Binding.RightsDigest {
		t.Fatal("independent canonical rights digest differs", digest, err)
	}
	// Use the existing bounded original-metadata memory seam, without provider I/O.
	m, _, b, d := authoredFixture(t)
	control := acceptedProposal(t)
	control.Publisher.Artifact.Commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	control.Expected.Right.Pins[0].Revision = control.Publisher.Artifact.Commit
	control.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(control.Expected.Right)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := authoredVerify(m, control, b, d); err != nil || r == nil || r.Classification != "blocked-candidate-metadata-verified" {
		t.Fatal("positive memory control refused", r, err)
	}
	for _, fault := range []string{"native alias", "local alias", "right identity", "declaration", "scope", "declared at", "origin", "missing pin", "pin digest", "attribution", "free source", "transformation", "definition digest", "decoder digest", "rights digest"} {
		t.Run(fault, func(t *testing.T) {
			bad := acceptedProposal(t)
			switch fault {
			case "native alias":
				bad.Expected.Binding.ProviderSourceID = "provider:ecb/daily"
			case "local alias":
				bad.Executor.Recordset = "FxReferenceQuote"
			case "right identity":
				bad.Expected.Right.SourceID = "ovdb:other/ecb/daily"
			case "declaration":
				bad.Expected.Right.Declaration.Name = "Altered conditions"
			case "scope":
				bad.Expected.Right.DeclarationScope = "recordset"
			case "declared at":
				bad.Expected.Right.DeclaredAt.Recordset = "daily"
			case "origin":
				bad.Expected.Right.EvidenceOrigin = "server-declared"
			case "missing pin":
				bad.Expected.Right.Pins = nil
			case "pin digest":
				bad.Expected.Right.Pins[0].SHA256 = hash([]byte("wrong"))
			case "attribution":
				bad.Expected.Right.Attribution = nil
			case "free source":
				bad.Expected.Right.FreeSource = nil
			case "transformation":
				bad.Expected.Right.Transformations = nil
			case "definition digest":
				bad.Expected.Binding.DefinitionDigest = hash([]byte("wrong"))
			case "decoder digest":
				bad.Expected.Binding.DecoderDigest = hash([]byte("wrong"))
			case "rights digest":
				bad.Expected.Binding.RightsDigest = hash([]byte("wrong"))
			}
			// Memory has a synthetic revision; align only that fixture revision.
			bad.Publisher.Artifact.Commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			if len(bad.Expected.Right.Pins) > 0 {
				bad.Expected.Right.Pins[0].Revision = bad.Publisher.Artifact.Commit
			}
			if fault != "rights digest" {
				bad.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(bad.Expected.Right)
				if err != nil {
					t.Fatal(err)
				}
			}
			if r, err := authoredVerify(m, bad, b, d); err == nil || r != nil {
				t.Fatal("altered independent expectation accepted")
			}
		})
	}
}

// checkCurrentMetadata proves only current tracked bytes and blob identity. It
// deliberately substitutes HEAD's revision and does not certify the frozen
// historical commit, which a depth-1 CI checkout need not contain.
func checkCurrentMetadata(ctx context.Context, g *pinchain.GitRuntime, dir string, a pinchain.Artifact) error {
	runner := repo.ExecRunner{Git: g.Executable(), Dir: dir}
	head, err := runner.Run([]string{"--no-replace-objects", "rev-parse", "HEAD"}, 128)
	if err != nil {
		return err
	}
	a.Commit = strings.TrimSpace(string(head))
	if _, err := g.ReadArtifact(ctx, dir, a); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(a.Path)))
	if err != nil {
		return err
	}
	if len(data) != a.Bytes || hash(data) != a.SHA256 {
		return fmt.Errorf("current metadata checkout bytes drift")
	}
	return nil
}

func TestAcceptedShallowCurrentMetadata(t *testing.T) {
	ctx := context.Background()
	g, err := pinchain.AdmitGit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "depth-one")
	runner := repo.ExecRunner{Git: g.Executable(), Dir: t.TempDir()}
	if _, err := runner.Run([]string{"--no-replace-objects", "clone", "--quiet", "--depth=1", "--no-local", "file://" + filepath.ToSlash(source), dir}, 4096); err != nil {
		t.Fatal(err)
	}
	runner.Dir = dir
	// Restore only the publisher origin identity after the local file transport;
	// setting this metadata performs no network request.
	if _, err := runner.Run([]string{"remote", "set-url", "origin", "https://github.com/openvaultdb/ovdb"}, 4096); err != nil {
		t.Fatal(err)
	}
	shallow, err := runner.Run([]string{"rev-parse", "--is-shallow-repository"}, 32)
	if err != nil || strings.TrimSpace(string(shallow)) != "true" {
		t.Fatal("regression fixture is not depth one", err)
	}
	original := acceptedProposal(t).Publisher.Artifact
	if _, err := g.ReadArtifact(ctx, dir, original); err == nil {
		t.Fatal("depth-one fixture unexpectedly contains historical commit")
	}
	a := currentMetadataInventory(t)[1]
	if err := checkCurrentMetadata(ctx, g, dir, a); err != nil {
		t.Fatal("matching current tracked artifact refused", err)
	}
	for _, fault := range []string{"path", "blob", "digest", "size"} {
		t.Run(fault, func(t *testing.T) {
			bad := a
			switch fault {
			case "path":
				bad.Path = "missing-current-metadata"
			case "blob":
				bad.Blob = "0000000000000000000000000000000000000000"
			case "digest":
				bad.SHA256 = hash([]byte("altered metadata"))
			case "size":
				bad.Bytes++
			}
			if err := checkCurrentMetadata(ctx, g, dir, bad); err == nil {
				t.Fatal("current tracked metadata drift accepted")
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, a.Path), []byte("changed checkout metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkCurrentMetadata(ctx, g, dir, a); err == nil {
		t.Fatal("dirty current metadata accepted")
	}
}
