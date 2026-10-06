package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dal-go/dalgo/dal"
	"github.com/dal-go/dalgo2http"
	"github.com/dal-go/record"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/schema"
	"github.com/spf13/cobra"
)

const serveHTTPManifest = `database: {id: ecb, schema_mode: strict, retention: none}
storage:
  engine: http
  http: {profile: ecb-daily/1, collection: daily}
schemas:
  collections:
    daily:
      fields:
        time: {type: string}
        currency: {type: string}
        rate: {type: string}
`

// Fabricated data only; this test never contacts ECB or records source bytes.
const serveHTTPXML = `<g:Envelope xmlns:g="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><g:subject>Synthetic</g:subject><g:Sender><g:name>Test</g:name></g:Sender><Cube><Cube time="2037-02-03"><Cube currency="AAA" rate="001.23000"/></Cube></Cube></g:Envelope>`

type serveHTTPTransport func(*http.Request) (*http.Response, error)

func (f serveHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func serveHTTPRequest(h http.Handler, method, path, body string, headers http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for name, values := range headers {
		for _, value := range values {
			r.Header.Add(name, value)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func snapshotFiles(t *testing.T, temp string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(temp, fmt.Sprintf("ovdb-query-snapshots-%d", os.Getuid())))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func isolatedServeTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	if filepath.Clean(os.TempDir()) != filepath.Clean(dir) {
		t.Fatalf("temporary directory not isolated: got %q, want %q", os.TempDir(), dir)
	}
	return dir
}

func TestServeHTTPProductionMountIsFixedAndReadOnlyWithoutFetching(t *testing.T) {
	isolatedServeTemp(t)
	dir := t.TempDir()
	path := writeManifest(t, dir, "ecb.yaml", serveHTTPManifest)
	_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", path}, func(h http.Handler) {
		w := serveHTTPRequest(h, "GET", "/v1/databases/ecb", "", nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"retention":"none"`) || !strings.Contains(w.Body.String(), `"query":true`) || !strings.Contains(w.Body.String(), `"write":false`) {
			t.Fatalf("HTTP discovery: %d %s", w.Code, w.Body)
		}
		for _, r := range []struct{ method, path, body string }{
			{"GET", "/v1/databases/ecb/records/daily/AAA", ""},
			{"PUT", "/v1/databases/ecb/records/daily/AAA", `{"data":{"rate":"1"}}`},
		} {
			w = serveHTTPRequest(h, r.method, r.path, r.body, nil)
			if w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unsupported request: %d %s", w.Code, w.Body)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("mount created retained files: %v %v", entries, err)
	}
	for name, bad := range map[string]string{
		"arbitrary URL":     strings.Replace(serveHTTPManifest, "profile: ecb-daily/1", "url: https://example.org/private", 1),
		"arbitrary profile": strings.Replace(serveHTTPManifest, "ecb-daily/1", "arbitrary", 1),
		"retained cache":    strings.Replace(serveHTTPManifest, "retention: none", "retention: none, cache_ttl: 1h", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", writeManifest(t, t.TempDir(), "bad.yaml", bad)}, func(http.Handler) { t.Fatal("invalid profile started serving") })
			if err == nil {
				t.Fatal("invalid HTTP configuration accepted")
			}
		})
	}
}

func TestServeHTTPNoRetentionAndRightsThroughCLI(t *testing.T) {
	temp := isolatedServeTemp(t)
	path := writeManifest(t, t.TempDir(), "ecb.yaml", strings.Replace(serveHTTPManifest, "retention: none", `retention: none, license: {url: "https://example.org/synthetic-terms"}`, 1))
	calls, closed := 0, false
	deps := defaultServeDeps()
	deps.mountFile = func(path string) (*core.Database, error) {
		m, err := manifest.Load(path)
		if err != nil {
			return nil, err
		}
		driver, err := dalgo2http.NewDB(dalgo2http.Config{Mode: dalgo2http.ModeLive,
			Collections: []dalgo2http.Collection{{Name: m.Storage.HTTP.Collection, URLTemplate: manifest.ECBDailyURL, Decoder: dalgo2http.DecoderECBEuroFXRef, KeyField: "currency", Timeout: 10 * time.Second, ClientSideFilter: true}},
			Client: &http.Client{Transport: serveHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != manifest.ECBDailyURL || r.Header.Get("Cache-Control") != "no-store, no-cache" {
					t.Fatal("request escaped the fixed no-store source")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/xml"}}, Body: io.NopCloser(strings.NewReader(serveHTTPXML))}, nil
			})}})
		if err != nil {
			return nil, err
		}
		db, err := core.Open(m, driver, []schema.Mode{schema.ModeStrict}, "")
		if err == nil {
			db.OnClose(func() error { closed = true; return nil })
		}
		return db, err
	}
	_, err := serveRun(t, deps, []string{"--manifest", path, "--read-only", "--server-id", "synthetic-cli"}, func(h http.Handler) {
		for _, r := range []struct{ method, path, body string }{
			{"POST", "/v1/databases/ecb/query", `{"collection":"daily"}`},
			{"POST", "/v1/databases/ecb/dtql", "from: {name: daily}\n"},
		} {
			w := serveHTTPRequest(h, r.method, r.path, r.body, nil)
			var answer struct {
				Records []struct {
					Data map[string]any `json:"data"`
				} `json:"records"`
				Rights []license.SourceRight `json:"sourceRights"`
				Used   []string              `json:"usedSourceIds"`
			}
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &answer) != nil {
				t.Fatalf("query: %d %s", w.Code, w.Body)
			}
			if len(answer.Records) != 1 || answer.Records[0].Data["rate"] != "001.23000" || answer.Records[0].Data["time"] != "2037-02-03" || len(answer.Rights) != 1 || len(answer.Used) != 1 || answer.Used[0] != "ovdb:synthetic-cli/ecb/daily" || answer.Rights[0].Source.ServerID != "synthetic-cli" || answer.Rights[0].Declaration.URL != "https://example.org/synthetic-terms" {
				t.Fatalf("native rows or declared executor rights changed: %+v", answer)
			}
		}
		before := calls
		for _, headers := range []http.Header{{"OVDB-Page-Size": {"1"}}, {"OVDB-Page-Token": {""}}, {"OVDB-Page-Close": {"true"}}, {"OVDB-Page-Future": {""}}} {
			for _, r := range []struct{ path, body string }{
				{"/v1/databases/ecb/query", `{"collection":"daily"}`},
				{"/v1/databases/ecb/dtql", "from: {name: daily}\n"},
				{"/v1/dtql", "from: {database: ecb, name: daily}\n"},
			} {
				w := serveHTTPRequest(h, "POST", r.path, r.body, headers)
				if w.Code != 422 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "retention_not_authorized") {
					t.Fatalf("continuation: %d %s", w.Code, w.Body)
				}
			}
		}
		w := serveHTTPRequest(h, "POST", "/v1/databases/ecb/query", `{"collection":"daily","snapshotToken":""}`, nil)
		if w.Code != 422 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("body continuation: %d %s", w.Code, w.Body)
		}
		for _, method := range []string{"PUT", "PATCH", "DELETE", "POST"} {
			w := serveHTTPRequest(h, method, "/v1/databases/ecb/records/daily/AAA", `{"data":{"rate":"1"}}`, nil)
			if w.Code != 403 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "read_only") {
				t.Fatalf("read-only: %d %s", w.Code, w.Body)
			}
		}
		if calls != before || before != 2 || len(snapshotFiles(t, temp)) != 0 {
			t.Fatal("refused request fetched or retained source data")
		}
	})
	if err != nil || !closed || len(snapshotFiles(t, temp)) != 0 {
		t.Fatalf("shutdown: %v closed=%v", err, closed)
	}
}

func TestServeTermsRequireExplicitIdentityBeforeServing(t *testing.T) {
	isolatedServeTemp(t)
	path := writeManifest(t, t.TempDir(), "ecb.yaml", strings.Replace(serveHTTPManifest, "retention: none", `retention: none, license: {text: "Synthetic terms"}`, 1))
	_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", path}, func(http.Handler) { t.Fatal("served terms without configured identity") })
	if err == nil || !strings.Contains(err.Error(), "stable server identity") {
		t.Fatalf("missing identity: %v", err)
	}
	for _, id := range []string{"", " ", "bad\nidentity", strings.Repeat("x", 257), string([]byte{0xff})} {
		_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", path, "--server-id", id}, func(http.Handler) { t.Fatal("served invalid identity") })
		if err == nil || !strings.Contains(err.Error(), "--server-id") {
			t.Fatalf("invalid identity accepted: %v", err)
		}
	}
}

func TestServeMountFailureClosesEarlierHTTPDatabase(t *testing.T) {
	closed := false
	deps := defaultServeDeps()
	deps.mountFile = func(path string) (*core.Database, error) {
		db, err := mount.File(path)
		if err == nil {
			db.OnClose(func() error { closed = true; return nil })
		}
		return db, err
	}
	path := writeManifest(t, t.TempDir(), "ecb.yaml", serveHTTPManifest)
	_, err := serveRun(t, deps, []string{"--manifest", path, "--manifest", filepath.Join(t.TempDir(), "missing.yaml")}, func(http.Handler) { t.Fatal("started after mount failure") })
	if err == nil || !closed {
		t.Fatalf("mount failure left HTTP resources open: %v closed=%v", err, closed)
	}
}

// A permitted synthetic SQLite snapshot proves serve closes library resources
// on return; the HTTP profile's refusals above prove it never creates one.
func TestServeShutdownRemovesAuthorizedSyntheticSnapshot(t *testing.T) {
	temp := isolatedServeTemp(t)
	_, err := serveRun(t, defaultServeDeps(), []string{"--manifest", writeShop(t)}, func(h http.Handler) {
		w := serveHTTPRequest(h, "POST", "/v1/databases/shop/dtql", "from: {name: customers}\n", http.Header{"OVDB-Page-Size": {"1"}})
		if w.Code != 200 || len(snapshotFiles(t, temp)) != 1 {
			t.Fatalf("synthetic snapshot not created: %d %s", w.Code, w.Body)
		}
	})
	if err != nil || len(snapshotFiles(t, temp)) != 0 {
		t.Fatalf("snapshot retained after server return: %v", err)
	}
}

type blockedServeReader struct {
	ctx         context.Context
	entered     chan struct{}
	release     chan struct{}
	cooperative bool
	closed      atomic.Bool
}

func (r *blockedServeReader) Cursor() (string, error) { return "", nil }
func (r *blockedServeReader) Close() error            { r.closed.Store(true); return nil }
func (r *blockedServeReader) Next() (record.Record, error) {
	close(r.entered)
	if r.cooperative {
		select {
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		case <-r.release:
		}
	} else {
		<-r.release
	}
	return nil, io.EOF
}

type blockedServeDB struct {
	dal.DB
	reader *blockedServeReader
}

func (d *blockedServeDB) ExecuteQueryToRecordsReader(ctx context.Context, _ dal.Query) (dal.RecordsReader, error) {
	d.reader.ctx = ctx
	return d.reader, nil
}

// The real permitted snapshot handler has already created an unregistered
// synthetic spool when Next blocks. Failed graceful shutdown must cancel it,
// force-close connections, and defer resource closure until the reader exits.
func TestServeShutdownOwnsActiveSnapshotUntilHandlerSettles(t *testing.T) {
	for _, mode := range []string{"graceful", "cancelled", "stubborn"} {
		t.Run(mode, func(t *testing.T) {
			temp := isolatedServeTemp(t)
			reader := &blockedServeReader{entered: make(chan struct{}), release: make(chan struct{}), cooperative: mode == "cancelled"}
			closed := make(chan struct{})
			closedEarly := atomic.Bool{}
			deps := defaultServeDeps()
			deps.drainTimeout = 20 * time.Millisecond
			deps.mountFile = func(path string) (*core.Database, error) {
				m, err := manifest.Load(path)
				if err != nil {
					return nil, err
				}
				db, err := core.Open(m, &blockedServeDB{reader: reader}, []schema.Mode{schema.ModeStrict}, "")
				if err == nil {
					db.OnClose(func() error {
						closedEarly.Store(!reader.closed.Load())
						close(closed)
						return nil
					})
				}
				return db, err
			}
			requestDone := make(chan *httptest.ResponseRecorder, 1)
			forced := false
			deps.run = func(_ *cobra.Command, srv *http.Server) error {
				go func() {
					requestDone <- serveHTTPRequest(srv.Handler, "POST", "/v1/databases/shop/dtql", "from: {name: customers}\n", http.Header{"OVDB-Page-Size": {"1"}})
				}()
				select {
				case <-reader.entered:
				case <-time.After(5 * time.Second):
					return errors.New("synthetic snapshot reader never started")
				}
				if len(snapshotFiles(t, temp)) != 1 {
					return errors.New("active synthetic spool is missing")
				}
				listener := newFakeListener()
				if mode == "graceful" {
					close(reader.release)
					<-requestDone
				} else {
					listener.shutdownErr = context.DeadlineExceeded
				}
				stop := make(chan os.Signal, 1)
				stop <- os.Interrupt
				err := serveUntil(io.Discard, "synthetic-no-listener", listener, stop)
				forced = listener.forced
				return err
			}
			cmd := newServeCmdWith(deps)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--manifest", writeShop(t)})
			err := cmd.Execute()
			if mode == "graceful" {
				if err != nil || forced {
					t.Fatalf("graceful shutdown: %v forced=%v", err, forced)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || !forced {
				t.Fatalf("failed shutdown lost error or force-close: %v forced=%v", err, forced)
			}
			if mode == "stubborn" {
				if !errors.Is(err, errServeHandlersActive) {
					t.Fatalf("active handler falsely reported settled: %v", err)
				}
				select {
				case <-closed:
					t.Fatal("database closed beneath active snapshot capture")
				default:
				}
				if len(snapshotFiles(t, temp)) != 1 {
					t.Fatal("active capture lost its spool ownership")
				}
				close(reader.release)
			}
			if mode != "graceful" {
				select {
				case <-requestDone:
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled snapshot handler did not settle")
				}
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("settled handler leaked deferred cleanup")
			}
			if closedEarly.Load() || !reader.closed.Load() || len(snapshotFiles(t, temp)) != 0 {
				t.Fatal("shutdown closed active resources or retained the synthetic spool/snapshot")
			}
		})
	}
}
