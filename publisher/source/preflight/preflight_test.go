package preflight

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	pubmanifest "github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/publisher/source"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

func hash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func encoded(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func bytesAt(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func baseline(t *testing.T) (*pinchain.Receipt, []byte) {
	t.Helper()
	var p struct {
		DecoderVersion, DecoderModuleVersion string
		Artifacts                            []pinchain.Artifact
	}
	if err := json.Unmarshal(pinchain.Manifest(), &p); err != nil {
		t.Fatal(err)
	}
	return &pinchain.Receipt{Format: "ovdb-ecb-metadata-receipt/1", Classification: "blocked-baseline-verified", ManifestSHA256: hash(pinchain.Manifest()), DirectoryStatus: "inactive", Blockers: []string{"B1", "B2", "B3", "B4"}, DecoderVersion: p.DecoderVersion, DecoderModuleVersion: p.DecoderModuleVersion, Artifacts: p.Artifacts}, bytesAt(t, "../pinchain/testdata/descriptor")
}

func proposed() Proposal {
	return Proposal{Executor: license.Identity{ServerID: "synthetic-proxy", DatabaseID: "ecb", Recordset: "daily"}, Policy: Policy{ZeroFee: true, Anonymous: []string{"filter", "projection", "limit", "transient-display"}, Paying: []string{"filter", "projection", "limit", "transient-display"}, Fields: []string{"time", "currency", "rate"}, MaxRows: 50}}
}

func publisherFixture(t *testing.T) (*repo.Memory, Proposal, *pinchain.Receipt, []byte) {
	t.Helper()
	b, definition := baseline(t)
	d, err := source.Parse(definition)
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"format": "ovdb-manifest/draft-1", "id": "ecb", "title": "Synthetic ECB publisher metadata", "description": "Authored metadata only.", "url": "https://example.org/ovdb/dbs/ecb",
		"deployment": map[string]any{"url": "https://example.org/ovdb/dbs/ecb", "engine": "http", "discovery": "https://example.org/.well-known/openvaultdb"},
		"model":      map[string]any{"address": "modelspec://github.com/openvaultdb/ovdb/ecb", "modelspec": "publisher/source/model/ecb-daily.modelspec.json", "hcl": "publisher/source/model/ecb-daily.modelspec.hcl"},
		"meaning":    map[string]any{"file": "publisher/source/model/ecb-daily.meaning.yaml", "graph": map[string]any{"id": "ecb-daily", "address": "meaning://github.com/openvaultdb/ovdb"}},
		"publisher":  map[string]any{"name": "Synthetic publisher", "url": "https://github.com/openvaultdb", "repository": "https://github.com/openvaultdb/ovdb"},
		"licences":   map[string]any{"data": d.Rights.Declaration, "model": "CC0-1.0", "meaning": "CC0-1.0"}, "recordsets": []string{"FxReferenceQuote"}, "source_definition": d,
	}
	desc := map[string]any{"format": "ovdb-database/draft-1", "id": "https://example.org/ovdb/dbs/ecb", "localId": "ecb", "serverId": "https://example.org/ovdb", "serverDbBaseUrl": "https://example.org/ovdb/dbs/ecb", "apiUrl": "https://example.org/ovdb/api", "deployment": map[string]any{"discovery": "https://example.org/.well-known/openvaultdb"}, "source_definition": d, "licences": map[string]any{"data": d.Rights.Declaration}, "recordsets": []any{map[string]any{"name": "FxReferenceQuote", "licences": map[string]any{"data": d.Rights.Declaration}}}}
	manifest := encoded(t, document)
	m := &repo.Memory{Nodes: map[string]repo.Node{"OVDB.md": {Kind: repo.File, Content: []byte("---\novdb: 1\npublish: [./ovdb.yaml, ./ovdb-database.json]\n---\n")}, "ovdb.yaml": {Kind: repo.File, Content: manifest}, "ovdb-database.json": {Kind: repo.File, Content: encoded(t, desc)}}}
	for _, pin := range b.Artifacts {
		if pin.Role == "model-hcl" || pin.Role == "model-json" || pin.Role == "meaning" {
			m.Nodes[pin.Path] = repo.Node{Kind: repo.File, Content: bytesAt(t, "../pinchain/testdata/"+pin.Role)}
		}
	}
	checked := repo.Check(m, repo.Options{Profile: pubmanifest.Publisher})
	if !checked.OK() {
		t.Fatal(checked.Findings)
	}
	p := proposed()
	p.Publisher = &Publisher{Directory: "synthetic-only", Artifact: pinchain.Artifact{Role: "publisher-manifest", Repository: "openvaultdb/ovdb", Commit: strings.Repeat("a", 40), Path: "ovdb.yaml", Blob: strings.Repeat("b", 40), Bytes: len(manifest), SHA256: hash(manifest)}}
	// Independently author the expected inventory from frozen fixture metadata;
	// do not call PrepareDynamicSourceRight or copy the verifier's output.
	right := license.SourceRight{SourceID: p.Executor.SourceID(), Source: p.Executor, Declaration: d.Rights.Declaration.Normalized(), DeclarationScope: license.DatabaseScope, DeclaredAt: license.Identity{ServerID: p.Executor.ServerID, DatabaseID: p.Executor.DatabaseID}, EvidenceOrigin: "publisher-definition-verified", Pins: []license.Pin{{Role: "provider", Repository: "https://github.com/openvaultdb/ovdb", Revision: p.Publisher.Artifact.Commit, Path: "ovdb.yaml", SHA256: hash(manifest), Bytes: int64(len(manifest))}}, Attribution: &d.Rights.Attribution, FreeSource: &d.Rights.FreeSource, Transformations: d.Rights.Transformations}
	digest, err := providerreads.RightsDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	var decoder string
	for _, pin := range b.Artifacts {
		if pin.Role == "decoder" {
			decoder = pin.SHA256
		}
	}
	p.Expected = &Expected{Right: right, Binding: providerreads.Binding{ProviderSourceID: "provider:ecb/FxReferenceQuote", RightsSourceID: p.Executor.SourceID(), ResourceID: "ecb-daily", DefinitionDigest: hash(manifest), DecoderDigest: decoder, RightsDigest: digest}}
	return m, p, b, definition
}

func TestBlockedMissingPublisherAndPolicyRefusals(t *testing.T) {
	b, d := baseline(t)
	reader := func(Publisher) (repo.Reader, error) { t.Fatal("missing publisher opened repository"); return nil, nil }
	r, err := verify(proposed(), b, d, "git version 2.54.0", reader)
	if err != nil || r.ExecutionEnabled || r.DirectoryStatus != "inactive" || r.Classification != "blocked-publisher-artifact-missing" || r.RuntimeEnforcement != "unproved" {
		t.Fatal(err, r)
	}
	data := encoded(t, r)
	for _, forbidden := range []string{"sourceRight", "providerReads", "definitionDigest", `"records"`, "fetchedAt"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("output leaked loadable/evidence field %s", forbidden)
		}
	}
	for _, mutate := range []func(*Proposal){
		func(p *Proposal) { p.Executor.ServerID = "" }, func(p *Proposal) { p.Executor.DatabaseID = "bad\nidentity" }, func(p *Proposal) { p.Executor.Recordset = " daily" }, func(p *Proposal) { p.Executor.Recordset = strings.Repeat("a", 257) }, func(p *Proposal) { p.Executor.Recordset = string([]byte{255}) },
		func(p *Proposal) { p.Policy.ZeroFee = false }, func(p *Proposal) { p.Policy.Anonymous = append(p.Policy.Anonymous, "export") }, func(p *Proposal) { p.Policy.Paying = append(p.Policy.Paying, "paid-ai") }, func(p *Proposal) { p.Policy.Fields = []string{"time", "rate"} }, func(p *Proposal) { p.Policy.MaxRows = 0 }, func(p *Proposal) { p.Policy.MaxRows = 101 },
	} {
		p := proposed()
		mutate(&p)
		if r, err := verify(p, b, d, "Git", reader); err == nil || r != nil {
			t.Fatal("unsafe proposal accepted")
		}
	}
	for _, mutate := range []func(*pinchain.Receipt){func(b *pinchain.Receipt) { b.ExecutionEnabled = true }, func(b *pinchain.Receipt) { b.DirectoryStatus = "active" }, func(b *pinchain.Receipt) { b.Classification = "admitted" }, func(b *pinchain.Receipt) { b.Blockers = nil }} {
		clone := *b
		mutate(&clone)
		if r, err := verify(proposed(), &clone, d, "Git", reader); err == nil || r != nil {
			t.Fatal("unsafe baseline accepted")
		}
	}
	if _, err := verify(proposed(), nil, d, "Git", reader); err == nil {
		t.Fatal("missing baseline accepted")
	}
	if _, err := Parse(make([]byte, providerreads.MaxMetadataBytes+1)); err == nil {
		t.Fatal("overbound proposal accepted")
	}
	for _, raw := range []string{`{}`, `{"executor":{},"executor":{}}`, `{"Policy":{}}`, `{"unexpected":true}`, `{} {}`, `{"policy":{"zeroFee":true,"paidAi":true}}`} {
		p, err := Parse([]byte(raw))
		if err == nil {
			if _, err = verify(p, b, d, "Git", reader); err == nil {
				t.Fatal("malformed/incomplete proposal accepted")
			}
		}
	}
	if _, err := Parse(encoded(t, proposed())); err != nil {
		t.Fatal(err)
	}
}

func TestPublisherMetadataAndIndependentExpectations(t *testing.T) {
	m, p, b, d := publisherFixture(t)
	read := func(Publisher) (repo.Reader, error) { return m, nil }
	r, err := verify(p, b, d, "Git", read)
	if err != nil || r.Classification != "blocked-candidate-metadata-verified" || r.ExecutionEnabled {
		t.Fatal(err, r)
	}
	p.Expected = nil
	if _, err := verify(p, b, d, "Git", read); err == nil {
		t.Fatal("missing independent expected facts accepted")
	}
	_, p, _, _ = publisherFixture(t)
	if _, err := verify(p, b, d, "Git", func(Publisher) (repo.Reader, error) { return nil, errors.New("missing") }); err == nil {
		t.Fatal("missing publisher read accepted")
	}
	for _, mutate := range []func(*Proposal){
		func(p *Proposal) { p.Publisher.Artifact.Role = "descriptor" }, func(p *Proposal) { p.Publisher.Artifact.Path = "wrong" }, func(p *Proposal) { p.Publisher.Artifact.SHA256 = hash([]byte("wrong")) },
		func(p *Proposal) { p.Expected.Right.Attribution = nil }, func(p *Proposal) { p.Expected.Right.FreeSource = nil }, func(p *Proposal) { p.Expected.Right.Source.ServerID = "other" }, func(p *Proposal) { p.Expected.Right.Declaration.Text = "changed terms" }, func(p *Proposal) { p.Expected.Right.Declaration.Text = string([]byte{255}) },
		func(p *Proposal) { p.Expected.Binding.ProviderSourceID = "provider:other/FxReferenceQuote" }, func(p *Proposal) { p.Expected.Binding.RightsSourceID = "ovdb:other/ecb/daily" }, func(p *Proposal) { p.Expected.Binding.DecoderDigest = strings.Repeat("c", 64) }, func(p *Proposal) { p.Expected.Binding.RightsDigest = strings.Repeat("d", 64) }, func(p *Proposal) { p.Expected.Binding.DefinitionDigest = strings.Repeat("e", 64) }, func(p *Proposal) { p.Expected.Binding.ResourceID = "" },
	} {
		m, p, b, d := publisherFixture(t)
		mutate(&p)
		if _, err := verify(p, b, d, "Git", func(Publisher) (repo.Reader, error) { return m, nil }); err == nil {
			t.Fatal("drifted expected metadata accepted")
		}
	}
	for _, fault := range []string{"OVDB.md", "ovdb-database.json", "publisher/source/model/ecb-daily.modelspec.hcl"} {
		m, p, b, d := publisherFixture(t)
		delete(m.Nodes, fault)
		if _, err := verify(p, b, d, "Git", func(Publisher) (repo.Reader, error) { return m, nil }); err == nil {
			t.Fatal("missing artifact accepted", fault)
		}
	}
	m, p, b, d = publisherFixture(t)
	n := m.Nodes["publisher/source/model/ecb-daily.modelspec.hcl"]
	n.Content = append(n.Content, ' ')
	m.Nodes["publisher/source/model/ecb-daily.modelspec.hcl"] = n
	if _, err := verify(p, b, d, "Git", func(Publisher) (repo.Reader, error) { return m, nil }); err == nil {
		t.Fatal("changed HCL bytes accepted")
	}
	m, p, b, _ = publisherFixture(t)
	if _, err := verify(p, b, []byte("invalid"), "Git", func(Publisher) (repo.Reader, error) { return m, nil }); err == nil {
		t.Fatal("invalid reviewed definition accepted")
	}
	changed := bytes.Replace(d, []byte("ECB reuse conditions"), []byte("Different conditions"), 1)
	if _, err := verify(p, b, changed, "Git", func(Publisher) (repo.Reader, error) { return m, nil }); err == nil {
		t.Fatal("changed reviewed definition accepted")
	}
	m, p, b, d = publisherFixture(t)
	if _, err := verify(p, b, d, "Git", func(Publisher) (repo.Reader, error) { return &lateHeadFailure{Reader: m}, nil }); err == nil {
		t.Fatal("publisher changed during rights preparation")
	}
}

type lateHeadFailure struct {
	repo.Reader
	heads int
}

func (r *lateHeadFailure) Head() (string, error) {
	r.heads++
	if r.heads > 2 {
		return "", errors.New("synthetic object disappeared")
	}
	return r.Reader.Head()
}

// All Run refusals leave stdout/transport absent. PATH probes do not execute
// an object operation on older/unknown Git; validation drift emits no receipt.
func TestRunRuntimeAndBaselineRefusal(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	if r, err := Run(context.Background(), nil, proposed()); err == nil || r != nil {
		t.Fatal("missing Git accepted")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\ncase \"$*\" in *version) echo 'git version 2.54.0';; *) exit 1;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if r, err := Run(context.Background(), nil, proposed()); err == nil || r != nil {
		t.Fatal("missing baseline accepted")
	}
}
