package localserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// sameOrigin is what a browser sends with a form or fetch from the page itself.
func sameOrigin(host string) map[string]string {
	return map[string]string{"Origin": "http://" + host, "Sec-Fetch-Site": "same-origin"}
}

var crossSite = map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}

// newCode asks for a login link with the instance secret and returns its code.
func (f *fixture) newCode(t *testing.T, next string) string {
	t.Helper()
	body := `{}`
	if next != "" {
		body = `{"next":"` + next + `"}`
	}
	rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/login-links", bearer: testSecret, body: body})
	var link LoginLink
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatalf("login link = %d %s", rec.Code, rec.Body)
	}
	parsed, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get("code")
}

// postLogin submits the sign-in form the way the login page does.
func (f *fixture) postLogin(t *testing.T, host, code, next string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"code": {code}}
	if next != "" {
		form.Set("next", next)
	}
	if header == nil {
		header = sameOrigin(host)
	}
	return f.do(t, request{method: http.MethodPost, path: "/login", host: host, body: form.Encode(),
		contentType: "application/x-www-form-urlencoded", header: header})
}

// signIn returns a session cookie value.
func (f *fixture) signIn(t *testing.T) string {
	t.Helper()
	rec := f.postLogin(t, testHost, f.newCode(t, ""), "", nil)
	cookie := sessionCookieOf(rec)
	if rec.Code != http.StatusSeeOther || cookie == nil {
		t.Fatalf("sign in = %d %s", rec.Code, rec.Body)
	}
	return cookie.Value
}

func sessionCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == SessionCookieName(testPort) {
			return cookie
		}
	}
	return nil
}

func isLanding(rec *httptest.ResponseRecorder) bool {
	return strings.Contains(rec.Body.String(), "Open the console from OVDB")
}

// AC:login-link-both-hosts: a previewer's GET does not spend the code, the
// POST signs in on either host, and a reused code shows the landing page.
func TestLoginExchangeOnPost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	code := f.newCode(t, "/settings")

	// A link previewer fetches the fallback link, twice.
	for range 2 {
		rec := f.do(t, request{path: "/login?code=" + url.QueryEscape(code) + "&next=%2Fsettings"})
		assertSecurityHeaders(t, rec)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || sessionCookieOf(rec) != nil ||
			!strings.Contains(body, `method="post" action="/login"`) || !strings.Contains(body, `value="`+code+`"`) ||
			!strings.Contains(body, `<noscript>`) || !strings.Contains(body, `src="/login/submit.js"`) || !strings.Contains(body, `value="/settings"`) {
			t.Fatalf("GET /login = %d %s", rec.Code, body)
		}
	}

	// The browser posts it on 127.0.0.1.
	rec := f.postLogin(t, testHost, code, "/settings", nil)
	cookie := sessionCookieOf(rec)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings" || cookie == nil {
		t.Fatalf("POST /login = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	// REQ:sessions: HttpOnly, SameSite=Lax, host-only, named with the port.
	raw := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"ovdb_session_6832=", "Path=/", "HttpOnly", "SameSite=Lax", "Max-Age=2592000"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q lacks %q", raw, want)
		}
	}
	if strings.Contains(strings.ToLower(raw), "domain=") {
		t.Errorf("Set-Cookie %q is not host-only", raw)
	}
	if rec := f.do(t, request{path: "/settings", cookie: cookie.Value}); rec.Body.String() != "console:/settings" {
		t.Errorf("signed-in /settings = %s", rec.Body)
	}

	// Another browser reuses the same code on ovdb.localhost.
	reused := f.postLogin(t, "ovdb.localhost:6832", code, "", nil)
	if reused.Code != http.StatusOK || sessionCookieOf(reused) != nil || !isLanding(reused) {
		t.Errorf("reused code = %d %s", reused.Code, reused.Body)
	}

	// A fresh code works on the primary host; next outside the server is ignored.
	primary := f.postLogin(t, "ovdb.localhost:6832", f.newCode(t, ""), "//evil.example/", nil)
	if primary.Code != http.StatusSeeOther || primary.Header().Get("Location") != "/" || sessionCookieOf(primary) == nil {
		t.Errorf("primary host login = %d %v", primary.Code, primary.Header())
	}

	// Expired and unknown codes show the landing page.
	expiring := f.newCode(t, "")
	f.now = f.now.Add(LoginLinkTTL)
	for _, bad := range []string{expiring, "not-a-code", ""} {
		if rec := f.postLogin(t, testHost, bad, "", nil); sessionCookieOf(rec) != nil || !isLanding(rec) {
			t.Errorf("code %q = %d %s", bad, rec.Code, rec.Body)
		}
	}
	if rec := f.do(t, request{path: "/login"}); !isLanding(rec) {
		t.Errorf("GET /login without a code = %s", rec.Body)
	}
	if rec := f.do(t, request{method: http.MethodPut, path: "/login"}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /login = %d", rec.Code)
	}
	for path, contentType := range map[string]string{"/login/page.css": "text/css; charset=utf-8", "/login/submit.js": "text/javascript; charset=utf-8"} {
		if rec := f.do(t, request{path: path}); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != contentType || rec.Body.Len() == 0 {
			t.Errorf("%s = %d %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// The session rows of the credential table (REQ:credentials) and
// AC:unauthenticated-rejected.
func TestSessionCredentialTable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	session := f.signIn(t)
	browser := func(r request) *httptest.ResponseRecorder {
		r.cookie = session
		if r.method != "" && r.method != http.MethodGet {
			r.header = sameOrigin(testHost)
		}
		return f.do(t, r)
	}

	for _, endpoint := range endpoints {
		body := ""
		if endpoint.path == "/api/local/v1/config" {
			body = `{"key":"server.port","value":"7000"}`
		}
		rec := browser(request{method: endpoint.method, path: endpoint.path, body: body})
		assertSecurityHeaders(t, rec)
		if endpoint.access == accessInstanceSecret {
			assertEnvelope(t, rec, http.StatusForbidden, envelope.Forbidden)
		} else if rec.Code != http.StatusOK {
			t.Errorf("session %s %s = %d %s", endpoint.method, endpoint.path, rec.Code, rec.Body)
		}
	}
	if f.shutdowns != 0 {
		t.Errorf("a session shut the server down")
	}

	// Pages: the console for a session, the landing page for everyone else.
	if rec := browser(request{path: "/"}); rec.Body.String() != "console:/" {
		t.Errorf("session / = %s", rec.Body)
	}
	for _, r := range []request{{path: "/"}, {path: "/apps/todo/"}, {path: "/", bearer: testSecret}, {path: "/", bearer: f.token},
		{path: "/", cookie: "forged"}, {path: "/assets/console.js"}} {
		rec := f.do(t, r)
		assertSecurityHeaders(t, rec)
		if !isLanding(rec) || strings.Contains(rec.Body.String(), "console:") {
			t.Errorf("%+v = %s", r, rec.Body)
		}
	}
	assertEnvelope(t, f.do(t, request{path: "/api/local/v1/status", cookie: "forged"}), http.StatusUnauthorized, envelope.Unauthorized)

	// /v1 with the cookie is forwarded as the owner.
	if rec := browser(request{path: "/v1/databases"}); rec.Code != http.StatusOK {
		t.Errorf("session GET /v1/databases = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, request{path: "/v1/databases"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /v1/databases = %d", rec.Code)
	}
	// An explicit Authorization header wins over the cookie.
	for _, tc := range []struct {
		bearer string
		status int
	}{{"wrong", http.StatusUnauthorized}, {f.token, http.StatusForbidden}} {
		assertEnvelope(t, f.do(t, request{path: "/api/local/v1/status", cookie: session, bearer: tc.bearer}), tc.status,
			map[int]envelope.Code{401: envelope.Unauthorized, 403: envelope.Forbidden}[tc.status])
		if rec := f.do(t, request{path: "/", cookie: session, bearer: tc.bearer}); !isLanding(rec) {
			t.Errorf("bearer %q with a cookie reached the console", tc.bearer)
		}
	}
	if rec := f.do(t, request{path: "/v1/databases", cookie: session, bearer: "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("invalid bearer with a cookie on /v1 = %d", rec.Code)
	}
	// /authorize and /token stay unsupported for sessions too.
	for _, path := range []string{"/authorize", "/token"} {
		if rec := browser(request{method: http.MethodPost, path: path}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not_supported") {
			t.Errorf("session POST %s = %d %s", path, rec.Code, rec.Body)
		}
	}
}

// REQ:sessions: hashed on disk, survive a restart, 30-day sliding expiry,
// at most one renewal write a minute.
func TestSessionsPersistHashedAndSlide(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	session := f.signIn(t)
	file := filepath.Join(f.dirs.Runtime, SessionsFile)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), session) || !strings.Contains(string(data), hashCode(session)) {
		t.Errorf("sessions.json = %s", data)
	}
	if info, _ := os.Stat(file); goruntime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("sessions.json mode = %v", info.Mode().Perm())
	}
	signedIn := func() bool { return f.do(t, request{path: "/", cookie: session}).Body.String() == "console:/" }

	f.restart(t)
	if !signedIn() {
		t.Fatal("session lost across a restart")
	}

	// Uses inside a minute do not rewrite the file.
	f.now = f.now.Add(30 * time.Second)
	if !signedIn() {
		t.Fatal("session invalid after 30 s")
	}
	if again, _ := os.ReadFile(file); string(again) != string(data) {
		t.Errorf("sessions.json rewritten within a minute:\n%s\n%s", data, again)
	}
	// A use after a minute writes the slid expiry and refreshes the cookie.
	f.now = f.now.Add(sessionWriteInterval)
	rec := f.do(t, request{path: "/", cookie: session})
	if again, _ := os.ReadFile(file); string(again) == string(data) || sessionCookieOf(rec) == nil {
		t.Errorf("renewal not written (cookie %v)", sessionCookieOf(rec))
	}

	// 29 days later it still works, and that use slides it again.
	f.now = f.now.Add(29 * 24 * time.Hour)
	if !signedIn() {
		t.Fatal("session expired before 30 days")
	}
	f.now = f.now.Add(29 * 24 * time.Hour)
	if !signedIn() {
		t.Fatal("expiry did not slide")
	}
	// Pending renewals are flushed on stop and read back after restart.
	f.now = f.now.Add(10 * time.Second)
	signedIn()
	if err := f.handler.Flush(); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	f.now = f.now.Add(SessionTTL - time.Minute)
	if !signedIn() {
		t.Fatal("flushed renewal lost")
	}
	// Unused for 30 days: gone.
	f.now = f.now.Add(SessionTTL)
	if signedIn() {
		t.Fatal("session outlived 30 idle days")
	}

	// A session removed from sessions.json ends on the next request.
	other := f.signIn(t)
	if f.do(t, request{path: "/", cookie: other}).Body.String() != "console:/" {
		t.Fatal("new session invalid")
	}
	if err := os.WriteFile(file, []byte(`{"schema":1,"sessions":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, f.do(t, request{path: "/api/local/v1/status", cookie: other}), http.StatusUnauthorized, envelope.Unauthorized)
}

// AC:cross-site-cookie-post-blocked, the cookie half; and POST /login.
func TestCrossOriginProtection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	session := f.signIn(t)
	put := func(header map[string]string, cookie, bearer string) *httptest.ResponseRecorder {
		return f.do(t, request{method: http.MethodPut, path: "/api/local/v1/config", body: `{"key":"server.port","value":"7100"}`,
			cookie: cookie, bearer: bearer, header: header})
	}
	for name, header := range map[string]map[string]string{
		"cross-site":              crossSite,
		"same-site other port":    {"Origin": "http://127.0.0.1:9999", "Sec-Fetch-Site": "same-site"},
		"origin only":             {"Origin": "https://evil.example"},
		"other allowed host":      {"Origin": "http://ovdb.localhost:6832"},
		"null origin (sandboxed)": {"Origin": "null"},
	} {
		rec := put(header, session, "")
		assertEnvelope(t, rec, http.StatusForbidden, envelope.Forbidden)
		assertSecurityHeaders(t, rec)
		if name == "cross-site" && !strings.Contains(rec.Body.String(), "another website") {
			t.Errorf("cross-site message = %s", rec.Body)
		}
	}
	if config, _ := setup.LoadConfig(f.dirs.Home); config.Server.Port != 0 {
		t.Fatalf("a cross-origin request changed the port to %d", config.Server.Port)
	}
	// The console's own fetch, and a non-browser client with a cookie, pass.
	if rec := put(sameOrigin(testHost), session, ""); rec.Code != http.StatusOK {
		t.Errorf("same-origin PUT = %d %s", rec.Code, rec.Body)
	}
	if rec := put(nil, session, ""); rec.Code != http.StatusOK {
		t.Errorf("header-less PUT = %d %s", rec.Code, rec.Body)
	}
	// Bearer requests are exempt: a browser never adds that header by itself.
	if rec := put(crossSite, "", testSecret); rec.Code != http.StatusOK {
		t.Errorf("cross-site bearer PUT = %d %s", rec.Code, rec.Body)
	}
	// Safe methods pass even cross-site (links from chats arrive signed in).
	if rec := f.do(t, request{path: "/", cookie: session, header: crossSite}); rec.Body.String() != "console:/" {
		t.Errorf("cross-site GET / = %s", rec.Body)
	}

	// A cross-site POST /login neither signs in nor spends the code.
	code := f.newCode(t, "")
	rec := f.postLogin(t, testHost, code, "", crossSite)
	assertEnvelope(t, rec, http.StatusForbidden, envelope.Forbidden)
	if sessionCookieOf(rec) != nil {
		t.Error("cross-site login set a cookie")
	}
	if rec := f.postLogin(t, testHost, code, "", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("code spent by the rejected cross-site login: %d", rec.Code)
	}
}

// AC:cross-site-cookie-post-blocked, the bearer half: server.cors origins get
// CORS headers on /v1/… and /token, for bearer requests only.
func TestCORSForBearerRequestsOnly(t *testing.T) {
	t.Parallel()
	const app = "http://localhost:5173"
	f := newFixture(t, func(o *Options) {
		if _, err := setup.ApplyConfigChange(o.Dirs, setup.ConfigChange{Key: setup.KeyServerCORS, Value: app}, false); err != nil {
			t.Fatal(err)
		}
		o.Databases = todoDatabase(t)
	})
	session := f.signIn(t)
	fromApp := map[string]string{"Origin": app, "Sec-Fetch-Site": "cross-site"}

	// Preflight, then the bearer PUT itself.
	preflight := f.do(t, request{method: http.MethodOptions, path: "/v1/databases/todo/records/lists/x", header: map[string]string{
		"Origin": app, "Access-Control-Request-Method": "PUT", "Access-Control-Request-Headers": "authorization, content-type",
	}})
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != app ||
		!strings.Contains(preflight.Header().Get("Access-Control-Allow-Headers"), "Authorization") ||
		!strings.Contains(preflight.Header().Get("Access-Control-Allow-Methods"), "PUT") {
		t.Errorf("preflight = %d %v", preflight.Code, preflight.Header())
	}
	rec := f.do(t, request{method: http.MethodPut, path: "/v1/databases/todo/records/lists/x", bearer: testSecret,
		body: `{"data":{"title":"From the app"}}`, header: fromApp})
	if rec.Code >= 300 || rec.Header().Get("Access-Control-Allow-Origin") != app || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("bearer PUT from %s = %d %v %s", app, rec.Code, rec.Header(), rec.Body)
	}
	if get := f.do(t, request{path: "/v1/databases/todo/records/lists/x", bearer: testSecret}); !strings.Contains(get.Body.String(), "From the app") {
		t.Errorf("record not written: %s", get.Body)
	}
	if rec := f.do(t, request{method: http.MethodPost, path: "/token", bearer: testSecret, header: fromApp}); rec.Header().Get("Access-Control-Allow-Origin") != app {
		t.Errorf("/token lacks CORS headers: %v", rec.Header())
	}

	for name, r := range map[string]request{
		"other origin":      {path: "/v1/databases", bearer: testSecret, header: map[string]string{"Origin": "https://evil.example"}},
		"other preflight":   {method: http.MethodOptions, path: "/v1/databases", header: map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"}},
		"cookie request":    {path: "/v1/databases", cookie: session, header: fromApp},
		"local API":         {path: "/api/local/v1/status", bearer: testSecret, header: fromApp},
		"local preflight":   {method: http.MethodOptions, path: "/api/local/v1/status", header: map[string]string{"Origin": app, "Access-Control-Request-Method": "GET"}},
		"console page":      {path: "/", cookie: session, header: fromApp},
		"no origin, bearer": {path: "/v1/databases", bearer: testSecret},
	} {
		if got := f.do(t, r).Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q", name, got)
		}
	}
}
