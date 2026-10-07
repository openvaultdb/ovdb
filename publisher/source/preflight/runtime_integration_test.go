package preflight

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/dal-go/dalgo2http"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/schema"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

type fakeGit struct {
	baseline   *pinchain.Receipt
	definition []byte
	fail       string
	path       string
}

func (g fakeGit) Validate(context.Context, map[string]string) (*pinchain.Receipt, error) {
	if g.fail == "baseline" {
		return nil, errors.New("missing baseline")
	}
	return g.baseline, nil
}
func (g fakeGit) ReadArtifact(_ context.Context, _ string, a pinchain.Artifact) ([]byte, error) {
	if g.fail == a.Role {
		return nil, errors.New("missing object")
	}
	return g.definition, nil
}
func (g fakeGit) Executable() string { return g.path }
func (g fakeGit) Version() string    { return "git version 2.54.0" }

func TestPipelineObjectRefusalsAndBlockedReceipt(t *testing.T) {
	b, d := baseline(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	g := fakeGit{baseline: b, definition: d, path: real}
	r, err := runWithGit(context.Background(), nil, proposed(), g)
	if err != nil || r.Classification != "blocked-publisher-artifact-missing" {
		t.Fatal(err)
	}
	for _, failure := range []string{"baseline", "descriptor"} {
		bad := g
		bad.fail = failure
		if r, err := runWithGit(context.Background(), nil, proposed(), bad); err == nil || r != nil {
			t.Fatal("object failure emitted receipt")
		}
	}
	_, p, _, _ := publisherFixture(t)
	g.fail = "publisher-manifest"
	if _, err := runWithGit(context.Background(), nil, p, g); err == nil {
		t.Fatal("missing publisher object accepted")
	}
	g.fail = ""
	p.Publisher.Directory = t.TempDir()
	if _, err := runWithGit(context.Background(), nil, p, g); err == nil {
		t.Fatal("nonrepository accepted")
	}
}

type forbiddenTransport struct{ calls *int }

func (f forbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*f.calls++
	return nil, errors.New("synthetic network trap")
}

func TestPreparedMetadataAtRealCheckedStartupWithoutReads(t *testing.T) {
	m, p, b, d := publisherFixture(t)
	if err := checkPublisher(m, *p.Publisher, b, d, p.Executor, *p.Expected); err != nil {
		t.Fatal(err)
	}
	calls := 0
	driver, err := dalgo2http.NewDB(dalgo2http.Config{Mode: dalgo2http.ModeLive, Collections: []dalgo2http.Collection{{Name: p.Executor.Recordset, URLTemplate: manifest.ECBDailyURL, Decoder: dalgo2http.DecoderECBEuroFXRef, KeyField: "currency", Timeout: time.Second, ClientSideFilter: true}}, Client: &http.Client{Transport: forbiddenTransport{&calls}}})
	if err != nil {
		t.Fatal(err)
	}
	meta := &manifest.Manifest{Database: manifest.Database{ID: p.Executor.DatabaseID, SchemaMode: schema.ModeStrict, License: &p.Expected.Right.Declaration}, Storage: manifest.Storage{Engine: "http", HTTP: &manifest.HTTPOptions{Profile: manifest.HTTPProfileECBDaily, Collection: p.Executor.Recordset}}, Schemas: &schema.Schemas{Collections: map[string]schema.Collection{p.Executor.Recordset: {Fields: map[string]schema.Field{"time": {Type: schema.TypeString}, "currency": {Type: schema.TypeString}, "rate": {Type: schema.TypeString}}}}}}
	db, err := core.Open(meta, driver, []schema.Mode{schema.ModeStrict}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	profile := server.ProviderReadProfile{Collection: p.Executor.Recordset, Binding: p.Expected.Binding, SourceRight: &p.Expected.Right}
	s, err := server.NewChecked("synthetic", map[string]*core.Database{p.Executor.DatabaseID: db}, server.WithSourceRights(p.Executor.ServerID, nil), server.WithProviderReadProfiles(map[string]server.ProviderReadProfile{p.Executor.DatabaseID: profile}))
	if err != nil || calls != 0 {
		t.Fatal("startup read or metadata mismatch", err, calls)
	}
	s.CloseSnapshots()
	bad := *profile.SourceRight
	bad.Attribution = nil
	profile.SourceRight = &bad
	if _, err := server.NewChecked("synthetic", map[string]*core.Database{p.Executor.DatabaseID: db}, server.WithSourceRights(p.Executor.ServerID, nil), server.WithProviderReadProfiles(map[string]server.ProviderReadProfile{p.Executor.DatabaseID: profile})); err == nil || calls != 0 {
		t.Fatal("unsafe startup accepted or read upstream")
	}
	// Trap itself is synthetic and demonstrably discriminating, never HTTP I/O.
	if _, err := (forbiddenTransport{&calls}).RoundTrip(nil); err == nil || calls != 1 {
		t.Fatal("broken transport trap")
	}
}
