package preflight

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
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
	for i, role := range []string{"publisher-index", "publisher-manifest", "paired-descriptor"} {
		a := inventory.Artifacts[i]
		if a.Role != role || a.Commit != p.Publisher.Artifact.Commit || a.Repository != "openvaultdb/ovdb" {
			t.Fatal("metadata pairing changed")
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
