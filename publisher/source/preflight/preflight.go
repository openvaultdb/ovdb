// Package preflight checks proposed ECB runtime metadata offline. Successful
// preparation never admits source execution, a public route or retained copies.
package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
	pubmanifest "github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/publisher/source"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

// Proposal is independently reviewed trusted configuration, not authority
// conferred by a command flag or user request. Expected facts must be frozen
// before running the verifier and never copied from its candidate output.
type Proposal struct {
	Executor  license.Identity `json:"executor"`
	Policy    Policy           `json:"policy"`
	Publisher *Publisher       `json:"publisher,omitempty"`
	Expected  *Expected        `json:"expected,omitempty"`
}

type Policy struct {
	ZeroFee   bool     `json:"zeroFee"`
	Anonymous []string `json:"anonymous"`
	Paying    []string `json:"paying"`
	Fields    []string `json:"fields"`
	MaxRows   int      `json:"maxRows"`
}

type Publisher struct {
	Directory string            `json:"directory"`
	Artifact  pinchain.Artifact `json:"artifact"`
}

type Expected struct {
	Binding providerreads.Binding `json:"binding"`
	Right   license.SourceRight   `json:"right"`
}

// Receipt deliberately has no SourceRight or provider profile. It cannot be
// loaded by --provider-read-profiles or used as an execution receipt.
type Receipt struct {
	Format             string            `json:"format"`
	Classification     string            `json:"classification"`
	ExecutionEnabled   bool              `json:"executionEnabled"`
	DirectoryStatus    string            `json:"directoryStatus"`
	Blockers           []string          `json:"blockers"`
	GitVersion         string            `json:"gitVersion"`
	Baseline           *pinchain.Receipt `json:"baseline"`
	PublisherCheck     string            `json:"publisherCheck"`
	PolicyCheck        string            `json:"policyCheck"`
	RuntimeEnforcement string            `json:"runtimeEnforcement"`
}

// Parse accepts bounded strict JSON metadata only, including duplicate-key and
// unknown/case-folded-field refusal through the existing contract decoder.
func Parse(data []byte) (Proposal, error) {
	var p Proposal
	if len(data) > providerreads.MaxMetadataBytes {
		return p, fmt.Errorf("preflight proposal exceeds metadata bound")
	}
	err := datarights.Decode(data, &p)
	return p, err
}

// Run admits the executable before object operations, verifies the fixed chain,
// then checks proposed metadata. There is no transport, mount or read executor.
func Run(ctx context.Context, repositories map[string]string, proposal Proposal) (*Receipt, error) {
	git, err := pinchain.AdmitGit(ctx)
	if err != nil {
		return nil, err
	}
	return runWithGit(ctx, repositories, proposal, git)
}

type gitRuntime interface {
	Validate(context.Context, map[string]string) (*pinchain.Receipt, error)
	ReadArtifact(context.Context, string, pinchain.Artifact) ([]byte, error)
	Executable() string
	Version() string
}

func runWithGit(ctx context.Context, repositories map[string]string, proposal Proposal, git gitRuntime) (*Receipt, error) {
	baseline, err := git.Validate(ctx, repositories)
	if err != nil {
		return nil, err
	}
	var definition []byte
	for _, pin := range baseline.Artifacts {
		if pin.Role == "descriptor" {
			definition, err = git.ReadArtifact(ctx, repositories[pin.Repository], pin)
			if err != nil {
				return nil, err
			}
		}
	}
	return verify(proposal, baseline, definition, git.Version(), func(p Publisher) (repo.Reader, error) {
		if _, err := git.ReadArtifact(ctx, p.Directory, p.Artifact); err != nil {
			return nil, err
		}
		return repo.AtCommit(repo.NewGit(repo.ExecRunner{Git: git.Executable(), Dir: p.Directory}), p.Artifact.Commit), nil
	})
}

func verify(p Proposal, baseline *pinchain.Receipt, definition []byte, version string, publisher func(Publisher) (repo.Reader, error)) (*Receipt, error) {
	if baseline == nil || baseline.Classification != "blocked-baseline-verified" || baseline.ExecutionEnabled || baseline.DirectoryStatus != "inactive" || !slices.Equal(baseline.Blockers, []string{"B1", "B2", "B3", "B4"}) {
		return nil, fmt.Errorf("inactive baseline required")
	}
	for _, value := range []string{p.Executor.ServerID, p.Executor.DatabaseID, p.Executor.Recordset} {
		if !identity(value) {
			return nil, fmt.Errorf("explicit stable executor/database/collection required")
		}
	}
	capabilities := []string{"filter", "projection", "limit", "transient-display"}
	if !p.Policy.ZeroFee || !slices.Equal(p.Policy.Anonymous, capabilities) || !slices.Equal(p.Policy.Paying, capabilities) || !slices.Equal(p.Policy.Fields, []string{"time", "currency", "rate"}) || p.Policy.MaxRows < 1 || p.Policy.MaxRows > 100 {
		return nil, fmt.Errorf("policy-preflight refusal: require identical bounded public-free native capabilities")
	}
	r := &Receipt{Format: "ovdb-ecb-offline-preflight/1", Classification: "blocked-publisher-artifact-missing", DirectoryStatus: "inactive", Blockers: []string{"B1", "B2", "B3", "B4"}, GitVersion: version, Baseline: baseline, PublisherCheck: "publisher artifact missing", PolicyCheck: "policy-preflight-only", RuntimeEnforcement: "unproved"}
	if p.Publisher == nil {
		return r, nil
	}
	if p.Expected == nil {
		return nil, fmt.Errorf("independently frozen binding and rights expectation required")
	}
	reader, err := publisher(*p.Publisher)
	if err != nil {
		return nil, fmt.Errorf("publisher original metadata unavailable")
	}
	if err := checkPublisher(reader, *p.Publisher, baseline, definition, p.Executor, *p.Expected); err != nil {
		return nil, err
	}
	r.Classification = "blocked-candidate-metadata-verified"
	r.PublisherCheck = "original publisher metadata verified; execution blocked"
	return r, nil
}

func identity(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || value == "" || len(value) > 256 {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func checkPublisher(reader repo.Reader, p Publisher, baseline *pinchain.Receipt, reviewedDefinition []byte, executor license.Identity, expected Expected) error {
	original, _ := repo.OriginalObjects(reader)
	url := "https://github.com/" + p.Artifact.Repository
	options := repo.Options{Repository: &url, Profile: pubmanifest.Publisher}
	checked := repo.Check(original, options)
	if !checked.OK() || checked.Descriptors != 1 || !checked.Manifest.SourceDefinition.Usable() || checked.Manifest.SourceDefinitionEvidence == nil {
		return fmt.Errorf("publisher requires clean original manifest and paired descriptor")
	}
	m := checked.Manifest
	e := m.SourceDefinitionEvidence
	a := p.Artifact
	if a.Role != "publisher-manifest" || e.Manifest.Revision != a.Commit || e.Manifest.Path != a.Path || e.Manifest.SHA256 != a.SHA256 || e.Manifest.Bytes != int64(a.Bytes) {
		return fmt.Errorf("publisher manifest pin mismatch")
	}
	// A definition at a new publisher commit must contain exactly the reviewed
	// authored metadata; its manifest digest remains a separate new byte digest.
	paths := map[string]string{"model-hcl": m.ModelHCL.Value, "model-json": m.ModelSpec.Value, "meaning": m.MeaningFile.Value}
	var decoderDigest string
	for _, pin := range baseline.Artifacts {
		if pin.Role == "decoder" {
			decoderDigest = pin.SHA256
		}
		path, needed := paths[pin.Role]
		if !needed {
			continue
		}
		data, err := original.Blob(path, repo.MaxFileBytes)
		if err != nil || path != pin.Path || len(data) != pin.Bytes || fmt.Sprintf("%x", sha256.Sum256(data)) != pin.SHA256 {
			return fmt.Errorf("publisher model/meaning artifact drift")
		}
	}
	definition, _ := json.Marshal(m.SourceDefinition.Value)
	want, err := source.Parse(reviewedDefinition)
	if err != nil {
		return fmt.Errorf("reviewed source definition unavailable")
	}
	wantBytes, _ := json.Marshal(want)
	if !slices.Equal(definition, wantBytes) {
		return fmt.Errorf("publisher source definition differs from reviewed native contract")
	}
	right, err := repo.PrepareDynamicSourceRight(original, options, "FxReferenceQuote", executor)
	if err != nil {
		return err
	}
	digest, err := providerreads.RightsDigest(expected.Right)
	if err != nil {
		return err
	}
	binding := expected.Binding
	if !reflect.DeepEqual(right, expected.Right) || binding.ProviderSourceID != "provider:ecb/FxReferenceQuote" || binding.RightsSourceID != executor.SourceID() || binding.DefinitionDigest != a.SHA256 || binding.DecoderDigest != decoderDigest || binding.RightsDigest != digest {
		return fmt.Errorf("independently frozen rights/binding mismatch")
	}
	_, err = providerreads.NewCollector(providerreads.Plan{Execution: providerreads.Execution{ID: "offline-preflight", Mode: "proxy", ExecutorID: executor.ServerID}, Bindings: []providerreads.Binding{binding}, Requests: []providerreads.Request{{ResourceID: binding.ResourceID, Method: "GET", UpstreamURL: manifest.ECBDailyURL, Params: map[string]any{}}}, SourceRights: []license.SourceRight{right}, MaxReads: new(1), MaxMetadataBytes: providerreads.MaxMetadataBytes})
	return err
}
