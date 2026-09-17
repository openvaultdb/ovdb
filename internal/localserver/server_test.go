package localserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

const (
	testPort   = 6832
	testSecret = "instance-secret"
	testHost   = "127.0.0.1:6832"
)

type fixture struct {
	handler   *Handler
	dirs      paths.Dirs
	token     string // a valid scoped token from auth.json
	shutdowns int
	now       time.Time
	options   func(*Options) // adjusts Options before each (re)start
}

// consoleStub stands in for the embedded console so tests see which
// requests reached it.
var consoleStub = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "console:"+r.URL.Path)
})

func newFixture(t *testing.T, options ...func(*Options)) *fixture {
	t.Helper()
	base := t.TempDir()
	dirs := paths.Dirs{Home: filepath.Join(base, "home"), Runtime: filepath.Join(base, "run"), Data: filepath.Join(base, "data")}
	for _, dir := range []string{dirs.Home, dirs.Runtime} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := auth.OpenStore(filepath.Join(dirs.Home, AuthStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dirs: dirs, token: "ovdb_scoped_token"}
	if err := store.CreateGrant(&auth.Grant{DatabaseID: "todo", Capabilities: []auth.Capability{{Action: "read"}}}, f.token); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	f.options = func(o *Options) {
		for _, option := range options {
			option(o)
		}
	}
	f.restart(t)
	return f
}

// restart builds a new handler over the same directories, as a server
// restart does.
func (f *fixture) restart(t *testing.T) {
	t.Helper()
	started := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	opts := Options{
		Dirs: f.dirs,
		Record: runtime.Record{
			Schema: 1, InstanceID: "instance-1", Home: f.dirs.Home, PID: 42, ProcessIdentity: "id",
			Port: testPort, Version: "1.2.3", StartedAt: started,
		},
		Secret:          testSecret,
		RequestShutdown: func() { f.shutdowns++ },
		Now:             func() time.Time { return f.now },
		Console:         consoleStub,
	}
	f.options(&opts)
	handler, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = handler
}

type request struct {
	method, path, host, bearer, contentType, body string
	cookie                                        string            // session cookie value
	header                                        map[string]string // extra headers
}

func (f *fixture) do(t *testing.T, r request) *httptest.ResponseRecorder {
	t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	req := httptest.NewRequest(r.method, r.path, body)
	req.Host = testHost
	if r.host != "" {
		req.Host = r.host
	}
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.cookie != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName(testPort), Value: r.cookie})
	}
	for name, value := range r.header {
		req.Header.Set(name, value)
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	} else if r.method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for header, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
		"X-Frame-Options":         "DENY",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func assertEnvelope(t *testing.T, rec *httptest.ResponseRecorder, status int, code envelope.Code) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	e := envelope.Decode(rec.Body.Bytes())
	if e == nil || e.Code != code {
		t.Errorf("body = %s, want envelope %s", rec.Body, code)
	}
}

// The credential table of REQ:credentials, for the 1a routes, and
// AC:endpoints-authenticated.
func TestCredentialTable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	type cell struct {
		status int
		code   envelope.Code
	}
	ok := cell{status: http.StatusOK}
	unauthorized := cell{http.StatusUnauthorized, envelope.Unauthorized}
	forbidden := cell{http.StatusForbidden, envelope.Forbidden}

	for _, endpoint := range endpoints {
		path, body := endpointRequest(f, endpoint)
		for _, tc := range []struct {
			name, bearer string
			want         cell
		}{
			{"no credential", "", unauthorized},
			{"invalid bearer", "wrong", unauthorized},
			{"scoped token", f.token, forbidden},
			{"instance secret", testSecret, ok},
		} {
			t.Run(endpoint.method+" "+endpoint.path+"/"+tc.name, func(t *testing.T) {
				rec := f.do(t, request{method: endpoint.method, path: path, bearer: tc.bearer, body: body})
				assertSecurityHeaders(t, rec)
				if tc.want.code == "" {
					created := tc.want.status == http.StatusOK && rec.Code == http.StatusCreated
					if rec.Code != tc.want.status && !created {
						t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want.status, rec.Body)
					}
					return
				}
				assertEnvelope(t, rec, tc.want.status, tc.want.code)
				if tc.want.status == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
					t.Error("401 without WWW-Authenticate")
				}
			})
		}
	}

	// The /v1 data API is openvaultdb-go's, with the secret as owner token.
	for _, tc := range []struct {
		bearer string
		status int
	}{{"", 401}, {"wrong", 401}, {testSecret, 200}} {
		rec := f.do(t, request{path: "/v1/databases", bearer: tc.bearer})
		if rec.Code != tc.status {
			t.Errorf("GET /v1/databases bearer %q = %d, want %d", tc.bearer, rec.Code, tc.status)
		}
		assertSecurityHeaders(t, rec)
	}
	// Unknown local API paths and methods reveal nothing without credentials.
	assertEnvelope(t, f.do(t, request{path: "/api/local/v1/nope"}), http.StatusUnauthorized, envelope.Unauthorized)
	assertEnvelope(t, f.do(t, request{path: "/api/local/v1/nope", bearer: testSecret}), http.StatusNotFound, envelope.NotFound)
	assertEnvelope(t, f.do(t, request{method: http.MethodDelete, path: "/api/local/v1/status", bearer: testSecret}),
		http.StatusMethodNotAllowed, envelope.InvalidArgument)
}

// endpointRequest is a valid request path and body for endpoint: a new
// database to create, then the same one to remove.
func endpointRequest(f *fixture, e endpoint) (path, body string) {
	path = strings.ReplaceAll(e.path, "{id}", "credentials")
	switch {
	case e.path == "/api/local/v1/config":
		body = `{"key":"server.port","value":"7000"}`
	case e.method == http.MethodPut && e.path == "/api/local/v1/context":
		body = `{"scope":"global","clear":true}`
	case e.method == http.MethodPost && e.path == "/api/local/v1/databases":
		data, _ := json.Marshal(setup.CreateRequest{ID: "credentials", Path: filepath.Join(f.dirs.Data, "credentials")})
		body = string(data)
	}
	return path, body
}

func TestConnectFlowIsNotSupported(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, r := range []request{
		{path: "/authorize"}, {method: http.MethodPost, path: "/authorize", contentType: "application/x-www-form-urlencoded"},
		{method: http.MethodPost, path: "/token", bearer: testSecret},
	} {
		rec := f.do(t, r)
		assertSecurityHeaders(t, rec)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_supported"`) {
			t.Errorf("%s %s = %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
}

// AC:dns-rebinding-blocked.
func TestHostAllowlist(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, host := range []string{"ovdb.localhost:6832", "OVDB.LOCALHOST:6832", "localhost:6832", "127.0.0.1:6832", "[::1]:6832"} {
		if rec := f.do(t, request{path: "/", host: host}); rec.Code != http.StatusOK {
			t.Errorf("Host %s = %d", host, rec.Code)
		}
	}
	for _, host := range []string{
		"attacker.example:6832", "localhost.:6832", "ovdb.localhost.:6832", "localhost", "127.0.0.1:6833",
		"127.0.0.1", "evil.localhost:6832", "127.0.0.1:6832.evil.example",
	} {
		for _, path := range []string{"/", "/api/local/v1/whoami", "/v1/databases"} {
			rec := f.do(t, request{path: path, host: host, bearer: testSecret})
			assertEnvelope(t, rec, http.StatusForbidden, envelope.Forbidden)
			assertSecurityHeaders(t, rec)
		}
	}
}

// REQ:landing-page: no session means the landing page, with no data.
func TestLandingPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := f.do(t, request{path: "/settings", host: "ovdb.localhost:6832"})
	assertSecurityHeaders(t, rec)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("landing = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"Open the console from OVDB", "ovdb open", "AI assistant"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing lacks %q", want)
		}
	}
	if strings.Contains(body, "127.0.0.1 link") {
		t.Error("fallback hint shown on ovdb.localhost")
	}
	if fallback := f.do(t, request{path: "/"}).Body.String(); !strings.Contains(fallback, "If ovdb.localhost didn&#39;t open") {
		t.Errorf("fallback host lacks the hint: %s", fallback)
	}
	if strings.Contains(body, "instance-1") || strings.Contains(body, f.dirs.Home) {
		t.Error("landing exposes server data")
	}
	if rec := f.do(t, request{method: http.MethodPost, path: "/"}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d", rec.Code)
	}
}

func TestLocalAPIDocuments(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := func(r request) *httptest.ResponseRecorder {
		r.bearer = testSecret
		return f.do(t, r)
	}

	rec := owner(request{path: runtime.WhoamiPath})
	if want := `{"schema":1,"instance_id":"instance-1","version":"1.2.3"}` + "\n"; rec.Body.String() != want {
		t.Errorf("whoami = %s", rec.Body)
	}

	server := setup.RunningServer(&runtime.Record{Port: testPort, Version: "1.2.3", PID: 42,
		StartedAt: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}, f.dirs)
	if got, want := owner(request{path: "/api/local/v1/server"}).Body.String(), string(envelope.Marshal(setup.NewServerDocument(server))); got != want {
		t.Errorf("server = %s, want %s", got, want)
	}
	if got, want := owner(request{path: "/api/local/v1/status"}).Body.String(), string(envelope.Marshal(setup.NewStatus("1.2.3", f.dirs, server, nil, nil))); got != want {
		t.Errorf("status = %s, want %s", got, want)
	}

	// Config: read, reject bad input, write, read back.
	if got := owner(request{path: "/api/local/v1/config"}).Body.String(); got != `{"schema":1,"config":{"server":{}},"next":[]}`+"\n" {
		t.Errorf("empty config = %s", got)
	}
	assertEnvelope(t, owner(request{method: http.MethodPut, path: "/api/local/v1/config", body: `{"key":"server.port","value":"99999"}`}),
		http.StatusBadRequest, envelope.InvalidArgument)
	assertEnvelope(t, owner(request{method: http.MethodPut, path: "/api/local/v1/config", body: `{"key":"nope","value":"1"}`}),
		http.StatusBadRequest, envelope.InvalidArgument)
	assertEnvelope(t, owner(request{method: http.MethodPut, path: "/api/local/v1/config", body: `{`}),
		http.StatusBadRequest, envelope.InvalidArgument)
	assertEnvelope(t, owner(request{method: http.MethodPut, path: "/api/local/v1/config", body: `{"key":"server.port","value":"7000"}`, contentType: "text/plain"}),
		http.StatusUnsupportedMediaType, envelope.InvalidArgument)
	rec = owner(request{method: http.MethodPut, path: "/api/local/v1/config", body: `{"key":"server.port","value":"7000"}`})
	if want := `{"schema":1,"config":{"server":{"port":7000}},"changed":true,"next":[{"label":"Restart the OVDB server to use it","command":"ovdb server restart"}]}` + "\n"; rec.Body.String() != want {
		t.Errorf("PUT config = %s", rec.Body)
	}
	if config, _ := setup.LoadConfig(f.dirs.Home); config.Server.Port != 7000 {
		t.Errorf("config.yaml port = %d", config.Server.Port)
	}

	// Shutdown answers first, then asks the process to stop.
	rec = owner(request{method: http.MethodPost, path: "/api/local/v1/server/shutdown", body: `{}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"stopping"`) || f.shutdowns != 1 {
		t.Errorf("shutdown = %d %s, shutdowns %d", rec.Code, rec.Body, f.shutdowns)
	}
}

func TestLoginLinks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/login-links", bearer: testSecret})
	var link LoginLink
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("login link = %d %s", rec.Code, rec.Body)
	}
	primary, fallback := strings.TrimPrefix(link.URL, "http://ovdb.localhost:6832/login?code="),
		strings.TrimPrefix(link.FallbackURL, "http://127.0.0.1:6832/login?code=")
	if primary == link.URL || primary != fallback || len(primary) < 40 {
		t.Errorf("links %q and %q do not share one code", link.URL, link.FallbackURL)
	}
	if want := time.Date(2026, 9, 17, 10, 10, 0, 0, time.UTC); !link.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %s, want %s", link.ExpiresAt, want)
	}
	second := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/login-links", bearer: testSecret, body: `{"next":"/apps/todo/"}`})
	if !strings.Contains(second.Body.String(), "next=%2Fapps%2Ftodo%2F") || strings.Contains(second.Body.String(), primary) {
		t.Errorf("second link = %s", second.Body)
	}
	for _, next := range []string{"//evil.example", "https://evil.example/", `/\evil.example`, "relative"} {
		body, _ := json.Marshal(LoginLinkRequest{Next: next})
		assertEnvelope(t, f.do(t, request{method: http.MethodPost, path: "/api/local/v1/login-links", bearer: testSecret, body: string(body)}),
			http.StatusBadRequest, envelope.InvalidArgument)
	}
}

func TestPanicIsRecoveredRedacted(t *testing.T) {
	t.Parallel()
	var logged strings.Builder
	h := securityHeaders(recoverPanics(&logged)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("mount failed: postgres://u:s3cret@db/x")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/databases", nil))
	assertEnvelope(t, rec, http.StatusInternalServerError, envelope.Internal)
	assertSecurityHeaders(t, rec)
	if strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(logged.String(), "s3cret") || !strings.Contains(logged.String(), "panic serving GET /v1/databases") {
		t.Errorf("body %s, log %s", rec.Body, logged.String())
	}
}

func TestWellKnownOmitsConnectEndpoints(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := f.do(t, request{path: "/.well-known/openvaultdb"})
	if want := `{"authEnabled":true,"name":"OpenVaultDB","protocol":"openvaultdb/0.1","version":"1.2.3"}` + "\n"; rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("well-known = %d %s", rec.Code, rec.Body)
	}
}
