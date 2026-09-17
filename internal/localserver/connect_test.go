package localserver

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

const testApp = "http://localhost:5173"

// connectFixture is a server with database todo and testApp in server.cors.
func connectFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t, func(o *Options) {
		if _, err := setup.ApplyConfigChange(o.Dirs, setup.ConfigChange{Key: setup.KeyServerCORS, Value: testApp}, false); err != nil {
			t.Fatal(err)
		}
		o.Databases = todoDatabase(t)
	})
}

// connectQuery is a valid connect request from testApp for read access.
func connectQuery() url.Values {
	return url.Values{
		"client_id": {"todo-app"}, "redirect_uri": {testApp + "/callback"}, "db": {"todo"},
		"capabilities": {"records:read,collections:read"}, "state": {"xyz"},
	}
}

// approve submits the consent form the way the consent page does.
func (f *fixture) approve(t *testing.T, cookie, decision string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := connectQuery()
	form.Set("decision", decision)
	return f.do(t, request{method: http.MethodPost, path: "/authorize", cookie: cookie, body: form.Encode(),
		contentType: "application/x-www-form-urlencoded", header: header})
}

// returnLocation is where a return page sends the browser, or "".
func returnLocation(rec *httptest.ResponseRecorder) string {
	body := rec.Body.String()
	_, after, found := strings.Cut(body, `<meta http-equiv="refresh" content="0;url=`)
	if !found {
		return ""
	}
	location, _, _ := strings.Cut(after, `"`)
	return html.UnescapeString(location)
}

// exchange trades a code at /token the way a browser app on testApp does:
// form-encoded, no credential.
func (f *fixture) exchange(t *testing.T, code string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"todo-app"}}
	return f.do(t, request{method: http.MethodPost, path: "/token", body: form.Encode(),
		contentType: "application/x-www-form-urlencoded", header: map[string]string{"Origin": testApp}})
}

// AC:connect-flow-needs-session.
func TestConnectFlowNeedsSession(t *testing.T) {
	t.Parallel()
	f := connectFixture(t)
	authorizeURL := "/authorize?" + connectQuery().Encode()

	// Without a session: the landing page with a sign-in hint, approving
	// nothing — for a browser, a token and the instance secret alike.
	for _, bearer := range []string{"", f.token, testSecret} {
		rec := f.do(t, request{path: authorizeURL, bearer: bearer})
		assertSecurityHeaders(t, rec)
		if rec.Code != http.StatusOK || !isLanding(rec) || !strings.Contains(rec.Body.String(), "An app asked to connect to OVDB") ||
			strings.Contains(rec.Body.String(), `name="decision"`) {
			t.Errorf("GET /authorize with bearer %q = %d %s", bearer, rec.Code, rec.Body)
		}
		post := f.approve(t, "", "approve", nil)
		if bearer != "" {
			post = f.do(t, request{method: http.MethodPost, path: "/authorize", bearer: bearer,
				body:        url.Values{"decision": {"approve"}, "client_id": {"todo-app"}, "redirect_uri": {testApp + "/callback"}, "db": {"todo"}, "capabilities": {"records:read"}}.Encode(),
				contentType: "application/x-www-form-urlencoded"})
		}
		if post.Code != http.StatusForbidden || !isLanding(post) || returnLocation(post) != "" || post.Header().Get("Location") != "" {
			t.Errorf("POST /authorize with bearer %q = %d %v %s", bearer, post.Code, post.Header(), post.Body)
		}
	}

	// Signed in: the consent page, then an approval that sends the browser
	// back with a code.
	session := f.signIn(t, primaryHost)
	consent := f.do(t, request{path: authorizeURL, host: primaryHost, cookie: session})
	assertSecurityHeaders(t, consent)
	for _, want := range []string{"Allow todo-app to use todo?", "<code>records:read</code>", "<code>collections:read</code>",
		"<code>" + testApp + "/callback</code>", `action="/authorize"`, `name="state" value="xyz"`, `value="approve"`, `value="deny"`} {
		if consent.Code != http.StatusOK || !strings.Contains(consent.Body.String(), want) {
			t.Errorf("consent page lacks %q: %d %s", want, consent.Code, consent.Body)
		}
	}
	approved := f.approve(t, session, "approve", sameOrigin(primaryHost))
	assertSecurityHeaders(t, approved)
	location, err := url.Parse(returnLocation(approved))
	if approved.Code != http.StatusOK || err != nil || location.Host != "localhost:5173" || location.Path != "/callback" ||
		location.Query().Get("state") != "xyz" || location.Query().Get("code") == "" {
		t.Fatalf("approve = %d %v %s", approved.Code, err, approved.Body)
	}
	if !strings.Contains(approved.Body.String(), `<a class="button" href="`+html.EscapeString(location.String())+`"`) {
		t.Errorf("return page has no link back: %s", approved.Body)
	}

	// The app exchanges the code with CORS headers, and the token works on
	// /v1 within its capabilities.
	exchanged := f.exchange(t, location.Query().Get("code"))
	var token struct {
		AccessToken string `json:"access_token"`
		Database    string `json:"database"`
	}
	if exchanged.Code != http.StatusOK || json.Unmarshal(exchanged.Body.Bytes(), &token) != nil || token.AccessToken == "" || token.Database != "todo" {
		t.Fatalf("exchange = %d %s", exchanged.Code, exchanged.Body)
	}
	if got := exchanged.Header().Get("Access-Control-Allow-Origin"); got != testApp {
		t.Errorf("/token Access-Control-Allow-Origin = %q", got)
	}
	if rec := f.do(t, request{path: "/v1/databases/todo", bearer: token.AccessToken}); rec.Code != http.StatusOK {
		t.Errorf("connected token GET /v1/databases/todo = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, request{method: http.MethodPut, path: "/v1/databases/todo/records/lists/x", bearer: token.AccessToken, body: `{"data":{"title":"x"}}`}); rec.Code != http.StatusForbidden {
		t.Errorf("read-only connected token PUT = %d %s", rec.Code, rec.Body)
	}
	// A code works once.
	if rec := f.exchange(t, location.Query().Get("code")); rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "access_token") {
		t.Errorf("reused code = %d %s", rec.Code, rec.Body)
	}

	// Deny sends the browser back with the error and no code.
	denied, err := url.Parse(returnLocation(f.approve(t, session, "deny", sameOrigin(primaryHost))))
	if err != nil || denied.Query().Get("error") != "access_denied" || denied.Query().Get("code") != "" {
		t.Errorf("deny went to %v (%v)", denied, err)
	}
}

// AC:cross-site-cookie-post-blocked for the consent form: another site cannot
// approve with the browser's session.
func TestCrossSiteConsentIsRejected(t *testing.T) {
	t.Parallel()
	f := connectFixture(t)
	session := f.signIn(t, primaryHost)
	for _, header := range []map[string]string{crossSite, {"Origin": "https://evil.example"}} {
		rec := f.approve(t, session, "approve", header)
		assertEnvelope(t, rec, http.StatusForbidden, envelope.Forbidden)
		if returnLocation(rec) != "" || strings.Contains(rec.Body.String(), "code=") {
			t.Errorf("cross-site approve = %s", rec.Body)
		}
	}
	// A cookie request to /token gets no CORS headers either.
	rec := f.do(t, request{method: http.MethodPost, path: "/token", host: primaryHost, cookie: session,
		contentType: "application/x-www-form-urlencoded", header: map[string]string{"Origin": testApp, "Sec-Fetch-Site": "same-site"}})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("cookie /token Access-Control-Allow-Origin = %q", got)
	}
}

func TestInvalidConnectRequests(t *testing.T) {
	t.Parallel()
	f := connectFixture(t)
	session := f.signIn(t)
	for name, change := range map[string]func(url.Values){
		"unknown database":    func(v url.Values) { v.Set("db", "nope") },
		"no client":           func(v url.Values) { v.Del("client_id") },
		"relative redirect":   func(v url.Values) { v.Set("redirect_uri", "/callback") },
		"script redirect":     func(v url.Values) { v.Set("redirect_uri", "javascript://x/%0aalert(1)") },
		"remote http":         func(v url.Values) { v.Set("redirect_uri", "http://evil.example/cb") },
		"localhost lookalike": func(v url.Values) { v.Set("redirect_uri", "http://localhost.evil.example/cb") },
		"user info":           func(v url.Values) { v.Set("redirect_uri", "https://localhost:5173@evil.example/cb") },
		"password":            func(v url.Values) { v.Set("redirect_uri", "https://user:pw@evil.example/cb") },
		"fragment":            func(v url.Values) { v.Set("redirect_uri", "https://evil.example/cb#frag") },
		"empty fragment":      func(v url.Values) { v.Set("redirect_uri", "https://evil.example/cb#") },
		"unknown capability":  func(v url.Values) { v.Set("capabilities", "records:everything") },
	} {
		query := connectQuery()
		change(query)
		rec := f.do(t, request{path: "/authorize?" + query.Encode(), cookie: session})
		assertSecurityHeaders(t, rec)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "This connect request isn&#39;t valid") ||
			strings.Contains(rec.Body.String(), `name="decision"`) {
			t.Errorf("%s: GET = %d %s", name, rec.Code, rec.Body)
		}
		for _, decision := range []string{"approve", "deny"} {
			query.Set("decision", decision)
			post := f.do(t, request{method: http.MethodPost, path: "/authorize", cookie: session, body: query.Encode(),
				contentType: "application/x-www-form-urlencoded", header: sameOrigin(testHost)})
			if post.Code != http.StatusBadRequest || returnLocation(post) != "" {
				t.Errorf("%s: POST %s = %d %s", name, decision, post.Code, post.Body)
			}
		}
	}
}

// AC:token-against-local-server, the server half: a read-only token reads,
// cannot write and cannot use the local API; auth.json is in OVDB home,
// owner-only, and never holds or lists the secret.
func TestScopedTokenAgainstLocalServer(t *testing.T) {
	t.Parallel()
	f := connectFixture(t)
	created := f.do(t, request{method: http.MethodPost, path: "/v1/tokens", bearer: testSecret,
		body: `{"label":"reader","databaseId":"todo","capabilities":["records:read","collections:read","schema:read"]}`})
	var token struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &token) != nil || token.Token == "" {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	if rec := f.do(t, request{path: "/v1/databases/todo", bearer: token.Token}); rec.Code != http.StatusOK {
		t.Errorf("GET /v1/databases/todo = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, request{method: http.MethodPut, path: "/v1/databases/todo/records/lists/x", bearer: token.Token, body: `{"data":{"title":"x"}}`}); rec.Code != http.StatusForbidden {
		t.Errorf("PUT = %d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, f.do(t, request{path: "/api/local/v1/status", bearer: token.Token}), http.StatusForbidden, envelope.Forbidden)
	if rec := f.do(t, request{path: "/v1/tokens", bearer: token.Token}); rec.Code != http.StatusForbidden {
		t.Errorf("token GET /v1/tokens = %d", rec.Code)
	}

	authFile := filepath.Join(f.dirs.Home, AuthStoreFile)
	listed := f.do(t, request{path: "/v1/tokens", bearer: testSecret})
	data, err := os.ReadFile(authFile)
	if err != nil || strings.Contains(string(data), token.Token) || strings.Contains(listed.Body.String(), token.Token) || !strings.Contains(listed.Body.String(), token.ID) {
		t.Errorf("auth.json (%v) or the list holds the secret: %s / %s", err, data, listed.Body)
	}
	if err := daemonlifecycle.ValidateOwnerOnly(authFile); err != nil {
		t.Errorf("auth.json is not owner-only: %v", err)
	}

	if rec := f.do(t, request{method: http.MethodDelete, path: "/v1/tokens/" + token.ID, bearer: testSecret}); rec.Code != http.StatusOK {
		t.Errorf("revoke = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, request{path: "/v1/databases/todo", bearer: token.Token}); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token = %d", rec.Code)
	}
}

// auth.json is made owner-only by the server itself, not by the OVDB home's
// permissions: openvaultdb-go's 0600 means nothing on Windows, where the
// file would otherwise inherit whatever the home allows.
func TestAuthStoreOwnerOnlyInASharedHome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	authFile := filepath.Join(f.dirs.Home, AuthStoreFile)
	if goruntime.GOOS != "windows" {
		if err := os.Chmod(f.dirs.Home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(authFile, 0o644); err != nil {
			t.Fatal(err)
		}
		// Loaded at start: protected before anything is served.
		f.restart(t)
		if err := daemonlifecycle.ValidateOwnerOnly(authFile); err != nil {
			t.Errorf("auth.json after start: %v", err)
		}
	}
	if err := os.Remove(authFile); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	if rec := f.do(t, request{method: http.MethodPost, path: "/v1/tokens", bearer: testSecret,
		body: `{"databaseId":"todo","capabilities":["records:read"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if err := daemonlifecycle.ValidateOwnerOnly(authFile); err != nil {
		t.Errorf("auth.json written by a token create: %v", err)
	}
	// The home itself is still the person's to fix; the server did not
	// change it.
	if goruntime.GOOS != "windows" {
		if info, err := os.Stat(f.dirs.Home); err != nil || info.Mode().Perm() != 0o755 {
			t.Errorf("home mode changed: %v %v", info.Mode(), err)
		}
	}
}

// RFC 6749 §5.1: a token response, issued or refused, is never cached.
func TestTokenResponsesAreNotCached(t *testing.T) {
	t.Parallel()
	f := connectFixture(t)
	session := f.signIn(t, primaryHost)
	location, err := url.Parse(returnLocation(f.approve(t, session, "approve", sameOrigin(primaryHost))))
	if err != nil {
		t.Fatal(err)
	}
	issued := f.exchange(t, location.Query().Get("code"))
	refused := f.exchange(t, location.Query().Get("code"))
	for name, rec := range map[string]*httptest.ResponseRecorder{"issued": issued, "refused": refused} {
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Pragma") != "no-cache" {
			t.Errorf("%s /token (%d) headers = %v", name, rec.Code, rec.Header())
		}
	}
	if issued.Code != http.StatusOK || refused.Code != http.StatusBadRequest {
		t.Errorf("issued %d, refused %d", issued.Code, refused.Code)
	}
}

// Redirects the consent page accepts: https anywhere, http only back to
// this computer.
func TestConnectRedirectsAllowed(t *testing.T) {
	t.Parallel()
	f := connectFixture(t)
	session := f.signIn(t)
	for _, redirect := range []string{"https://app.example/cb", "http://localhost:5173/cb", "http://127.0.0.1:8080/cb",
		"http://[::1]:5173/cb", "http://todo.localhost:3000/cb", "HTTP://LOCALHOST/cb?x=1"} {
		query := connectQuery()
		query.Set("redirect_uri", redirect)
		if rec := f.do(t, request{path: "/authorize?" + query.Encode(), cookie: session}); rec.Code != http.StatusOK {
			t.Errorf("%s = %d %s", redirect, rec.Code, rec.Body)
		}
	}
}
