package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dal-go/dalgo/dal"
	"github.com/dal-go/dalgo2http"
	"github.com/dal-go/record"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	"github.com/openvaultdb/openvaultdb-go/pkg/schema"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

// These bytes are invented in RAM. They are never stored in the metadata Git
// fixture, a snapshot, or a report. The marker is lexical and deliberately odd.
func syntheticXML() string {
	return `<g:Envelope xmlns:g="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube><Cube time="2039-04-05"><Cube currency="AAA" rate="001.234567890123"/><Cube currency="ZZZ" rate="0.00009"/></Cube></Cube></g:Envelope>`
}

type syntheticFixture struct {
	proposal   Proposal
	baseline   *pinchain.Receipt // Explicitly synthetic: no fixed-chain closure claim.
	definition []byte
	descriptor pinchain.Artifact
	git        *pinchain.GitRuntime
}

func syntheticClone[T any](t *testing.T, v T) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(encoded(t, v), &out); err != nil {
		t.Fatal("fixture clone failed")
	}
	return out
}

func syntheticPublisher(t *testing.T) syntheticFixture {
	t.Helper()
	m, p, b, d := authoredFixture(t)
	dir := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("Git unavailable")
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		data, err := cmd.Output()
		if err != nil {
			t.Fatal("synthetic metadata Git operation failed")
		}
		return strings.TrimSpace(string(data))
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://github.com/openvaultdb/ovdb")
	for path, n := range m.Nodes {
		target := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal("metadata directory failed")
		}
		if err := os.WriteFile(target, n.Content, 0600); err != nil {
			t.Fatal("metadata write failed")
		}
	}
	run("add", ".")
	run("-c", "user.name=Synthetic metadata", "-c", "user.email=synthetic@example.invalid", "commit", "-qm", "Invented metadata objects only")
	p.Publisher.Directory = dir
	p.Publisher.Artifact.Commit = run("rev-parse", "HEAD")
	p.Publisher.Artifact.Blob = run("rev-parse", "HEAD:"+authoredManifest)
	p.Expected.Right.Pins[0].Revision = p.Publisher.Artifact.Commit
	p.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(p.Expected.Right)
	if err != nil {
		t.Fatal("synthetic rights digest failed")
	}
	desc := m.Nodes[authoredDescriptor].Content
	a := pinchain.Artifact{Role: "synthetic-paired-descriptor", Repository: p.Publisher.Artifact.Repository, Commit: p.Publisher.Artifact.Commit, Path: authoredDescriptor, Blob: run("rev-parse", "HEAD:"+authoredDescriptor), SHA256: hash(desc), Bytes: len(desc)}
	g, err := pinchain.AdmitGit(context.Background())
	if err != nil {
		t.Fatal("Git admission failed")
	}
	return syntheticFixture{syntheticClone(t, p), syntheticClone(t, b), bytes.Clone(d), a, g}
}

type syntheticCounters struct {
	driver, core, server          int
	calls, closes                 atomic.Int32
	logSink                       syntheticSink
	eofs, readErrors              atomic.Int32
	logMu                         sync.Mutex
	logEvents, writeFailureEvents int
}

// A selected sink spy records only byte counts and marker detection, never data.
type syntheticSink struct {
	bytes  int
	marker bool
}

func (s *syntheticSink) Write(data []byte) (int, error) {
	s.bytes += len(data)
	s.marker = s.marker || bytes.Contains(data, []byte("001.234567890123"))
	return len(data), nil
}

// Retain only counters and marker flags, never messages, attributes, or records.
type syntheticLogHandler struct {
	counts *syntheticCounters
	attrs  int
}

func (*syntheticLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *syntheticLogHandler) inspectAttr(a slog.Attr) {
	_, _ = h.counts.logSink.Write([]byte(a.Key))
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		for _, child := range v.Group() {
			h.inspectAttr(child)
		}
		return
	}
	_, _ = h.counts.logSink.Write([]byte(v.String()))
}
func (h *syntheticLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.counts.logMu.Lock()
	defer h.counts.logMu.Unlock()
	h.counts.logEvents++
	_, _ = h.counts.logSink.Write([]byte(r.Message))
	r.Attrs(func(a slog.Attr) bool { h.inspectAttr(a); return true })
	if r.Level == slog.LevelError && r.Message == "JSON response write failed" && r.NumAttrs()+h.attrs == 0 {
		h.counts.writeFailureEvents++
	}
	return nil
}
func (h *syntheticLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.counts.logMu.Lock()
	defer h.counts.logMu.Unlock()
	for _, a := range attrs {
		h.inspectAttr(a)
	}
	return &syntheticLogHandler{counts: h.counts, attrs: h.attrs + len(attrs)}
}
func (h *syntheticLogHandler) WithGroup(name string) slog.Handler {
	h.counts.logMu.Lock()
	defer h.counts.logMu.Unlock()
	_, _ = h.counts.logSink.Write([]byte(name))
	return h
}

type syntheticBody struct {
	io.Reader
	counts *syntheticCounters
}

func (b *syntheticBody) Read(data []byte) (int, error) {
	n, err := b.Reader.Read(data)
	if err == io.EOF {
		b.counts.eofs.Add(1)
	} else if err != nil {
		b.counts.readErrors.Add(1)
	}
	return n, err
}

func (b *syntheticBody) Close() error { b.counts.closes.Add(1); return nil }

type syntheticReadError struct{}

func (syntheticReadError) Read([]byte) (int, error) { return 0, errors.New("invented body read error") }

// No dialer, default transport, cache, fallback, or redirect path exists here.
type syntheticTransport struct {
	counts  *syntheticCounters
	body    string
	status  int
	header  http.Header
	respond func(*http.Request) (*http.Response, error)
}

func (s *syntheticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if s.counts.calls.Add(1) != 1 || r.Method != "GET" || r.URL.String() != manifest.ECBDailyURL || r.Body != nil || r.Header.Get("Cache-Control") != "no-store, no-cache" || r.Header.Get("Pragma") != "no-cache" {
		return nil, errors.New("synthetic exact request refused")
	}
	for _, name := range []string{"Authorization", "Cookie", server.ProviderExecutionIDHeader} {
		if r.Header.Get(name) != "" {
			return nil, errors.New("synthetic credential forwarding refused")
		}
	}
	if s.respond != nil {
		return s.respond(r)
	}
	h := s.header.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Set("Content-Type", "text/xml")
	status := s.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Header: h, Body: &syntheticBody{strings.NewReader(s.body), s.counts}, Request: r}, nil
}

type syntheticRuntime struct {
	handler  http.Handler
	db       *core.Database
	expected Expected
	executor string
}

// The sole execution exception is private test assembly over invented bytes.
// The blocked receipt is never itself executable or serialized as admission.
func syntheticAssemble(t *testing.T, f syntheticFixture, tr *syntheticTransport, startup func(*Expected), startupManifest ...func(*manifest.Manifest)) (*syntheticRuntime, error) {
	t.Helper()
	if tr == nil || tr.counts == nil {
		return nil, errors.New("mandatory invented transport missing")
	}
	p, b, d, desc := syntheticClone(t, f.proposal), syntheticClone(t, f.baseline), bytes.Clone(f.definition), f.descriptor
	r, err := verify(p, b, d, f.git.Version(), func(pub Publisher) (repo.Reader, error) {
		if _, err := f.git.ReadArtifact(context.Background(), pub.Directory, pub.Artifact); err != nil {
			return nil, err
		}
		if _, err := f.git.ReadArtifact(context.Background(), pub.Directory, desc); err != nil {
			return nil, err
		}
		return repo.AtCommit(repo.NewGit(repo.ExecRunner{Git: f.git.Executable(), Dir: pub.Directory}), pub.Artifact.Commit), nil
	})
	if err != nil {
		return nil, err
	}
	if r.Classification != "blocked-candidate-metadata-verified" || r.ExecutionEnabled || r.DirectoryStatus != "inactive" || !slices.Equal(r.Blockers, []string{"B1", "B2", "B3", "B4"}) {
		return nil, errors.New("blocked metadata disposition required")
	}
	// Keep consumer expectations separate from the mutable startup candidate.
	want := syntheticClone(t, *p.Expected)
	if startup != nil {
		startup(p.Expected)
	}
	tr.counts.driver++
	driver, err := dalgo2http.NewDB(dalgo2http.Config{Mode: dalgo2http.ModeLive, Collections: []dalgo2http.Collection{{Name: p.Executor.Recordset, URLTemplate: manifest.ECBDailyURL, Decoder: dalgo2http.DecoderECBEuroFXRef, KeyField: "currency", Timeout: time.Second, ClientSideFilter: true}}, Client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return dalgo2http.ErrRedirectNotAllowed }}})
	if err != nil {
		return nil, err
	}
	tr.counts.core++
	meta := &manifest.Manifest{Database: manifest.Database{ID: p.Executor.DatabaseID, SchemaMode: schema.ModeStrict, License: &p.Expected.Right.Declaration}, Storage: manifest.Storage{Engine: "http", HTTP: &manifest.HTTPOptions{Profile: manifest.HTTPProfileECBDaily, Collection: p.Executor.Recordset}}, Schemas: &schema.Schemas{Collections: map[string]schema.Collection{p.Executor.Recordset: {Fields: map[string]schema.Field{"time": {Type: schema.TypeString}, "currency": {Type: schema.TypeString}, "rate": {Type: schema.TypeString}}}}}}
	for _, mutate := range startupManifest {
		mutate(meta)
	}
	db, err := core.Open(meta, driver, []schema.Mode{schema.ModeStrict}, "")
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error("owned database close failed")
		}
	})
	tr.counts.server++
	s, err := server.NewChecked("synthetic-only", map[string]*core.Database{p.Executor.DatabaseID: db}, server.WithLogger(slog.New(&syntheticLogHandler{counts: tr.counts})), server.WithSourceRights(p.Executor.ServerID, nil), server.WithProviderReadProfiles(map[string]server.ProviderReadProfile{p.Executor.DatabaseID: {Collection: p.Executor.Recordset, Binding: p.Expected.Binding, SourceRight: &p.Expected.Right}}))
	if err != nil {
		return nil, err
	}
	t.Cleanup(s.CloseSnapshots)
	return &syntheticRuntime{s.Handler(), db, want, p.Executor.ServerID}, nil
}

func syntheticRequest(ctx context.Context, nonce, doc string) *http.Request {
	r := httptest.NewRequest("POST", "http://synthetic.invalid/v1/databases/ecb/dtql", strings.NewReader(doc)).WithContext(ctx)
	r.Header.Set(server.ProviderExecutionIDHeader, nonce)
	return r
}

func TestSyntheticPreparationRefusesBeforeFactories(t *testing.T) {
	f := syntheticPublisher(t)
	cases := map[string]func(*syntheticFixture){
		"publisher missing":   func(f *syntheticFixture) { f.proposal.Publisher = nil },
		"expectation missing": func(f *syntheticFixture) { f.proposal.Expected = nil },
		"policy":              func(f *syntheticFixture) { f.proposal.Policy.ZeroFee = false },
		"repository":          func(f *syntheticFixture) { f.proposal.Publisher.Artifact.Repository = "other/ovdb" },
		"commit":              func(f *syntheticFixture) { f.proposal.Publisher.Artifact.Commit = strings.Repeat("c", 40) },
		"path":                func(f *syntheticFixture) { f.proposal.Publisher.Artifact.Path = "missing.yaml" },
		"blob":                func(f *syntheticFixture) { f.proposal.Publisher.Artifact.Blob = strings.Repeat("c", 40) },
		"hash":                func(f *syntheticFixture) { f.proposal.Publisher.Artifact.SHA256 = strings.Repeat("c", 64) },
		"size":                func(f *syntheticFixture) { f.proposal.Publisher.Artifact.Bytes++ },
		"descriptor blob":     func(f *syntheticFixture) { f.descriptor.Blob = strings.Repeat("c", 40) },
		"descriptor hash":     func(f *syntheticFixture) { f.descriptor.SHA256 = strings.Repeat("c", 64) },
		"descriptor size":     func(f *syntheticFixture) { f.descriptor.Bytes++ },
		"full right":          func(f *syntheticFixture) { f.proposal.Expected.Right.FreeSource = nil },
		"declaration":         func(f *syntheticFixture) { f.proposal.Expected.Right.Declaration.Text = "altered" },
		"attribution":         func(f *syntheticFixture) { f.proposal.Expected.Right.Attribution = nil },
		"provider":            func(f *syntheticFixture) { f.proposal.Expected.Binding.ProviderSourceID = "other" },
		"rights source":       func(f *syntheticFixture) { f.proposal.Expected.Binding.RightsSourceID = "other" },
		"definition digest":   func(f *syntheticFixture) { f.proposal.Expected.Binding.DefinitionDigest = strings.Repeat("c", 64) },
		"decoder digest":      func(f *syntheticFixture) { f.proposal.Expected.Binding.DecoderDigest = strings.Repeat("c", 64) },
		"rights digest":       func(f *syntheticFixture) { f.proposal.Expected.Binding.RightsDigest = strings.Repeat("c", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := f
			bad.proposal = syntheticClone(t, f.proposal)
			mutate(&bad)
			c := &syntheticCounters{}
			if rt, err := syntheticAssemble(t, bad, &syntheticTransport{counts: c, body: syntheticXML()}, nil); err == nil || rt != nil {
				t.Fatal("invalid preparation accepted")
			}
			if c.driver+c.core+c.server != 0 || c.calls.Load() != 0 {
				t.Fatal("refusal constructed runtime or read")
			}
		})
	}
}

func TestSyntheticStartupRefusalsWithoutReads(t *testing.T) {
	f := syntheticPublisher(t)
	for name, mutate := range map[string]func(*Expected){
		"identity": func(e *Expected) { e.Right.Source.ServerID = "other" },
		"notices":  func(e *Expected) { e.Right.Attribution = nil },
		"digest":   func(e *Expected) { e.Binding.RightsDigest = strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			c := &syntheticCounters{}
			if _, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, mutate); err == nil {
				t.Fatal("invalid startup accepted")
			}
			if c.calls.Load() != 0 {
				t.Fatal("startup read provider")
			}
		})
	}
	t.Run("retention", func(t *testing.T) {
		c := &syntheticCounters{}
		if _, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, nil, func(m *manifest.Manifest) { m.Database.Retention = "snapshot" }); err == nil || c.calls.Load() != 0 {
			t.Fatal("retention startup drift admitted or read")
		}
	})
}

func TestSyntheticGeneratedOriginalsIgnoreDirtyMetadata(t *testing.T) {
	f := syntheticPublisher(t)
	if err := os.WriteFile(filepath.Join(f.proposal.Publisher.Directory, authoredDescriptor), []byte("damaged staged metadata"), 0600); err != nil {
		t.Fatal("dirty fixture failed")
	}
	cmd := exec.Command(f.git.Executable(), "-C", f.proposal.Publisher.Directory, "add", authoredDescriptor)
	if cmd.Run() != nil {
		t.Fatal("stage failed")
	}
	c := &syntheticCounters{}
	if _, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, nil); err != nil || c.calls.Load() != 0 {
		t.Fatal("dirty metadata replaced selected original")
	}
	bad := f
	bad.proposal = syntheticClone(t, f.proposal)
	bad.proposal.Publisher.Artifact.SHA256 = hash([]byte("dirty bytes"))
	c = &syntheticCounters{}
	if _, err := syntheticAssemble(t, bad, &syntheticTransport{counts: c, body: syntheticXML()}, nil); err == nil || c.driver != 0 {
		t.Fatal("dirty pin accepted")
	}
	cmd = exec.Command(f.git.Executable(), "-C", f.proposal.Publisher.Directory, "-c", "user.name=Synthetic metadata", "-c", "user.email=synthetic@example.invalid", "commit", "-qm", "Synthetic damaged descriptor")
	if cmd.Run() != nil {
		t.Fatal("damage commit failed")
	}
	data, err := exec.Command(f.git.Executable(), "-C", f.proposal.Publisher.Directory, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal("damage identity failed")
	}
	bad.proposal = syntheticClone(t, f.proposal)
	bad.proposal.Publisher.Artifact.Commit = strings.TrimSpace(string(data))
	bad.proposal.Expected.Right.Pins[0].Revision = bad.proposal.Publisher.Artifact.Commit
	bad.proposal.Expected.Binding.RightsDigest, err = providerreads.RightsDigest(bad.proposal.Expected.Right)
	if err != nil {
		t.Fatal("damage digest failed")
	}
	c = &syntheticCounters{}
	if _, err := syntheticAssemble(t, bad, &syntheticTransport{counts: c, body: syntheticXML()}, nil); err == nil || c.driver != 0 {
		t.Fatal("damaged selected commit admitted")
	}
}

func TestSyntheticUnsupportedOperationsBeforeRead(t *testing.T) {
	f := syntheticPublisher(t)
	for _, tc := range []struct{ name, method, path, doc, header string }{
		{"paging", "POST", "/v1/databases/ecb/dtql", "from: {name: daily}\n", "OVDB-Page-Size"},
		{"continuation", "POST", "/v1/databases/ecb/dtql", "from: {name: daily}\n", "OVDB-Page-Token"},
		{"point get", "GET", "/v1/databases/ecb/records/daily/AAA", "", ""},
		{"write", "POST", "/v1/databases/ecb/records/daily", "{}", ""},
		{"offset", "POST", "/v1/databases/ecb/dtql", "from: {name: daily}\noffset: 1\n", ""},
		{"order", "POST", "/v1/databases/ecb/dtql", "from: {name: daily}\norderBy: [{field: currency}]\n", ""},
		{"aggregate", "POST", "/v1/databases/ecb/dtql", "from: {name: daily}\ncolumns: [{aggregate: {function: count, args: []}, as: n}]\n", ""},
		{"join", "POST", "/v1/databases/ecb/dtql", "from: {name: daily, as: a}\njoins: [{from: {name: daily, as: b}, on: {left: a.currency, right: b.currency}}]\n", ""},
		{"federation", "POST", "/v1/dtql", "from: {database: ecb, name: daily}\n", ""},
		// These absent endpoints prove route refusal, not retention sink hardening.
		{"history absent route", "GET", "/v1/databases/ecb/history", "", ""},
		{"export absent route", "GET", "/v1/databases/ecb/export", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &syntheticCounters{}
			rt, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, nil)
			if err != nil {
				t.Fatal("assembly failed")
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, "http://synthetic.invalid"+tc.path, strings.NewReader(tc.doc))
			r.Header.Set(server.ProviderExecutionIDHeader, strings.Repeat("a", 32))
			if tc.header != "" {
				r.Header.Set(tc.header, "1")
			}
			rt.handler.ServeHTTP(w, r)
			if w.Code < 400 || c.calls.Load() != 0 {
				t.Fatalf("unsupported route status=%d calls=%d", w.Code, c.calls.Load())
			}
		})
	}
	t.Run("driver exists", func(t *testing.T) {
		c := &syntheticCounters{}
		rt, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, nil)
		if err != nil {
			t.Fatal("assembly failed")
		}
		if _, err = rt.db.DB().Exists(context.Background(), record.NewKeyWithID("daily", "AAA")); !errors.Is(err, dal.ErrNotSupported) || c.calls.Load() != 0 {
			t.Fatal("point Exists did not refuse before read")
		}
	})
}

type syntheticBlockedBody struct {
	ctx                                              context.Context
	started, terminated, closed, release             chan struct{}
	startOnce, terminateOnce, closeOnce, releaseOnce sync.Once
	ignoreContext                                    bool
}

func newSyntheticBlockedBody(ignore bool) *syntheticBlockedBody {
	return &syntheticBlockedBody{started: make(chan struct{}), terminated: make(chan struct{}), closed: make(chan struct{}), release: make(chan struct{}), ignoreContext: ignore}
}
func (b *syntheticBlockedBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	if b.ignoreContext {
		<-b.release
	} else {
		select {
		case <-b.ctx.Done():
		case <-b.release:
		}
	}
	b.terminateOnce.Do(func() { close(b.terminated) })
	return 0, context.Canceled
}
func (b *syntheticBlockedBody) Close() error { b.closeOnce.Do(func() { close(b.closed) }); return nil }
func (b *syntheticBlockedBody) unblock()     { b.releaseOnce.Do(func() { close(b.release) }) }
func syntheticAwait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("owned acknowledgement timeout: %s", what)
	}
}

func TestSyntheticOwnedCancellationAndUncooperativeBody(t *testing.T) {
	f := syntheticPublisher(t)
	for _, phase := range []string{"before headers", "blocked body", "context ignoring body", "timeout before headers"} {
		t.Run(phase, func(t *testing.T) {
			c := &syntheticCounters{}
			started := make(chan struct{})
			contextAck := make(chan struct{})
			done := make(chan struct{})
			body := newSyntheticBlockedBody(phase == "context ignoring body")
			tr := &syntheticTransport{counts: c, respond: func(r *http.Request) (*http.Response, error) {
				close(started)
				if phase == "before headers" || phase == "timeout before headers" {
					<-r.Context().Done()
					close(contextAck)
					return nil, r.Context().Err()
				}
				body.ctx = r.Context()
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: body, Request: r}, nil
			}}
			rt, err := syntheticAssemble(t, f, tr, nil)
			if err != nil {
				t.Fatal("assembly failed")
			}
			consumer := syntheticNewConsumer(t, rt)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := httptest.NewRecorder()
			go func() {
				defer close(done)
				rt.handler.ServeHTTP(w, syntheticRequest(ctx, consumer.plan.Execution.ID, "from: {name: daily}\n"))
			}()
			// Every failure path releases and joins the deliberately blocked fake.
			t.Cleanup(func() { cancel(); body.unblock(); syntheticAwait(t, done, "final handler join") })
			syntheticAwait(t, started, "request entered")
			if phase == "blocked body" || phase == "context ignoring body" {
				syntheticAwait(t, body.started, "body read entered")
			}
			if phase != "timeout before headers" {
				cancel()
			}
			if phase == "context ignoring body" {
				select {
				case <-done:
					t.Fatal("context ignoring body unexpectedly terminated without release")
				case <-time.After(25 * time.Millisecond):
				}
				body.unblock()
			} else if phase == "before headers" || phase == "timeout before headers" {
				syntheticAwait(t, contextAck, "transport context observed")
			}
			syntheticAwait(t, done, "handler returned")
			if phase == "blocked body" || phase == "context ignoring body" {
				syntheticAwait(t, body.terminated, "body read termination")
				syntheticAwait(t, body.closed, "body close")
				_ = body.Close()
			}
			if w.Code == 200 {
				t.Fatal("cancelled request emitted success")
			}
			if _, err := consumer.accept(ctx, w.Body.Bytes()); err == nil || consumer.accesses != 0 {
				t.Fatal("cancelled request published")
			}
			consumer.dispose()
			if c.calls.Load() != 1 {
				t.Fatal("cancellation read count mismatch")
			}
		})
	}
}

type syntheticWriteProbe struct {
	header         http.Header
	status, writes int
	fail           bool
	marker         bool
}

func (w *syntheticWriteProbe) Header() http.Header    { return w.header }
func (w *syntheticWriteProbe) WriteHeader(status int) { w.status = status }
func (w *syntheticWriteProbe) Write(b []byte) (int, error) {
	w.writes++
	w.marker = w.marker || bytes.Contains(b, []byte("001.234567890123"))
	if w.fail {
		return 0, errors.New("invented response writer failure")
	}
	return len(b), nil
}

func TestSyntheticResponseWriterFailureBoundary(t *testing.T) {
	f := syntheticPublisher(t)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive successful writer", true: "required producer acknowledgement"}[fail], func(t *testing.T) {
			c := &syntheticCounters{}
			rt, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, nil)
			if err != nil {
				t.Fatal("assembly failed")
			}
			consumer := syntheticNewConsumer(t, rt)
			w := &syntheticWriteProbe{header: http.Header{}, fail: fail}
			r := syntheticRequest(context.Background(), consumer.plan.Execution.ID, "from: {name: daily}\n")
			rt.handler.ServeHTTP(w, r)
			if w.status != http.StatusOK || w.writes != 1 || !w.marker || c.calls.Load() != 1 || c.closes.Load() != 1 {
				t.Fatal("writer positive control or owned upstream close failed")
			}
			wantEvents := 0
			if fail {
				wantEvents = 1
			}
			// Released openvaultdb-go v0.20.2 emits one fixed ERROR event with
			// no attributes on write failure, and none for a successful writer.
			// Caller-owned context cancellation is not inferred; upstream has
			// already closed before either write. No harness cancel is installed.
			if c.writeFailureEvents != wantEvents || c.logEvents != wantEvents || c.logSink.marker {
				t.Fatal("exact source-free producer acknowledgement mismatch")
			}
		})
	}
}

func TestSyntheticInventedExplicitJourneys(t *testing.T) {
	f := syntheticPublisher(t)
	for _, tc := range []struct {
		name, doc string
		rows      int
		rate      bool
	}{
		{"populated", "from: {name: daily}\n", 2, true},
		{"projection", "from: {name: daily}\ncolumns: [{field: time}, {field: currency}]\n", 2, false},
		{"limit", "from: {name: daily}\nlimit: 1\n", 1, true},
		{"filtered zero", "from: {name: daily}\nwhere: {op: '==', left: {field: currency}, right: {value: BBB}}\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &syntheticCounters{}
			rt, err := syntheticAssemble(t, f, &syntheticTransport{counts: c, body: syntheticXML()}, nil)
			if err != nil {
				t.Fatal("synthetic assembly failed")
			}
			if c.calls.Load() != 0 || c.driver != 1 || c.core != 1 || c.server != 1 {
				t.Fatal("startup isolation failed")
			}
			consumer := syntheticNewConsumer(t, rt)
			w := httptest.NewRecorder()
			rt.handler.ServeHTTP(w, syntheticRequest(context.Background(), consumer.plan.Execution.ID, tc.doc))
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("query status=%d or missing no-store", w.Code)
			}
			rows, err := consumer.accept(context.Background(), w.Body.Bytes())
			if err != nil || len(rows) != tc.rows || consumer.accesses != 1 {
				t.Fatal("consumer gate or count failed")
			}
			expectedCurrencies := []string{"AAA", "ZZZ"}
			if tc.name == "limit" {
				expectedCurrencies = expectedCurrencies[:1]
			} else if tc.rows == 0 {
				expectedCurrencies = nil
			}
			for i, row := range rows {
				currency := expectedCurrencies[i]
				if row.Key != "daily/"+currency || row.Data["currency"] != currency || row.Data["time"] != "2039-04-05" {
					t.Fatal("expected distinct native row identity or reference date mismatch")
				}
				if _, ok := row.Data["rate"]; ok != tc.rate {
					t.Fatal("projection mismatch")
				}
				if tc.rate && row.Data["rate"] != map[string]string{"AAA": "001.234567890123", "ZZZ": "0.00009"}[currency] {
					t.Fatal("native lexical representation changed")
				}
			}
			if c.calls.Load() != 1 || c.closes.Load() != 1 {
				t.Fatal("explicit query read/close count failed")
			}
			if c.eofs.Load() != 1 {
				t.Fatal("successful body did not acknowledge EOF")
			}
			if c.logSink.marker {
				t.Fatal("invented marker reached configured logger sink")
			}
			consumer.dispose()
			rt.db.Close()
			if c.calls.Load() != 1 {
				t.Fatal("cleanup read provider")
			}
		})
	}
}

func TestSyntheticConfiguredSinkPositiveControl(t *testing.T) {
	var sink syntheticSink
	_, _ = sink.Write([]byte(syntheticXML()))
	if !sink.marker || sink.bytes == 0 {
		t.Fatal("sink spy missed positive RAM control")
	}
	c := &syntheticCounters{}
	logger := slog.New(&syntheticLogHandler{counts: c})
	logger.Error("unrelated synthetic event")
	if c.logEvents != 1 || c.writeFailureEvents != 0 || c.logSink.marker {
		t.Fatal("unrelated event confused acknowledgement spy")
	}
	logger.Error("JSON response write failed")
	if c.logEvents != 2 || c.writeFailureEvents != 1 || c.logSink.marker {
		t.Fatal("exact acknowledgement positive control failed")
	}
	logger.Error("JSON response write failed", "invented payload", syntheticXML())
	if c.logEvents != 3 || c.writeFailureEvents != 1 || !c.logSink.marker {
		t.Fatal("attribute-bearing event or marker positive control failed")
	}
	// Selected live-only NewDB has no snapshot store. The logger sink used by
	// the real checked server is separately checked in success/error journeys.
	// This proves configured harness sinks only, not deployed or OS storage.
}

func TestSyntheticGenuineFixedObjectClosureOptIn(t *testing.T) {
	raw := os.Getenv("OVDB_SYNTHETIC_FIXED_REPOS")
	if raw == "" {
		t.Skip("genuine 12-artifact fixed closure not run: test-only repository inventory absent; ordinary baseline fixture is synthetic")
	}
	var repositories map[string]string
	if json.Unmarshal([]byte(raw), &repositories) != nil {
		t.Fatal("test-only fixed repository inventory invalid")
	}
	f := syntheticPublisher(t)
	r, err := Run(context.Background(), repositories, f.proposal)
	if err != nil {
		t.Fatalf("genuine fixed object closure refused; no acquisition or fixture substitution: %v", err)
	}
	if r.Classification != "blocked-candidate-metadata-verified" || r.ExecutionEnabled {
		t.Fatal("genuine closure disposition changed")
	}
}

func TestSyntheticProviderFailuresNeverPublish(t *testing.T) {
	f := syntheticPublisher(t)
	valid := syntheticXML()
	for name, body := range map[string]string{"empty": "", "malformed": "<", "namespace": strings.ReplaceAll(valid, "http://www.ecb.int/vocabulary/2002-08-01/eurofxref", "urn:bad"), "date": strings.Replace(valid, "2039-04-05", "2039-02-30", 1), "duplicate quote": strings.Replace(valid, "ZZZ", "AAA", 1), "DTD": "<!DOCTYPE x>" + valid, "entity": strings.Replace(valid, "001.234567890123", "1&#46;2", 1), "oversized": strings.Repeat("x", (2<<20)+1)} {
		t.Run(name, func(t *testing.T) { syntheticFailure(t, f, &syntheticTransport{body: body}, true) })
	}
	for _, status := range []int{301, 400, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			syntheticFailure(t, f, &syntheticTransport{body: valid, status: status, header: http.Header{"Location": []string{"https://forbidden.invalid/"}}}, true)
		})
	}
	t.Run("header bound", func(t *testing.T) {
		syntheticFailure(t, f, &syntheticTransport{body: valid, header: http.Header{"Etag": []string{strings.Repeat("x", 1025)}}}, true)
	})
	t.Run("transport error", func(t *testing.T) {
		syntheticFailure(t, f, &syntheticTransport{respond: func(*http.Request) (*http.Response, error) { return nil, errors.New("invented transport error") }}, false)
	})
	t.Run("body read error", func(t *testing.T) {
		c := &syntheticCounters{}
		tr := &syntheticTransport{counts: c, respond: func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/xml"}}, Body: &syntheticBody{syntheticReadError{}, c}, Request: r}, nil
		}}
		rt, err := syntheticAssemble(t, f, tr, nil)
		if err != nil {
			t.Fatal("assembly failed")
		}
		w := httptest.NewRecorder()
		rt.handler.ServeHTTP(w, syntheticRequest(context.Background(), strings.Repeat("a", 32), "from: {name: daily}\n"))
		if w.Code == 200 || c.readErrors.Load() != 1 || c.closes.Load() != 1 || c.logSink.marker {
			t.Fatal("body read error cleanup or sink refusal failed")
		}
	})
}

func syntheticFailure(t *testing.T, f syntheticFixture, tr *syntheticTransport, bodyAcquired bool) {
	t.Helper()
	c := &syntheticCounters{}
	tr.counts = c
	rt, err := syntheticAssemble(t, f, tr, nil)
	if err != nil {
		t.Fatal("synthetic assembly failed")
	}
	consumer := syntheticNewConsumer(t, rt)
	w := httptest.NewRecorder()
	rt.handler.ServeHTTP(w, syntheticRequest(context.Background(), consumer.plan.Execution.ID, "from: {name: daily}\n"))
	if w.Code == 200 {
		t.Fatal("provider failure emitted successful response")
	}
	if _, err = consumer.accept(context.Background(), w.Body.Bytes()); err == nil || consumer.accesses != 0 {
		t.Fatal("error response passed consumer gate")
	}
	if c.calls.Load() != 1 || (bodyAcquired && c.closes.Load() != 1) {
		t.Fatal("failure read/close count mismatch")
	}
	if c.logSink.marker {
		t.Fatal("provider failure disclosed synthetic marker to logger")
	}
}
