package localserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// The connect flow (REQ:connect-flow-in-local-mode): an app sends the person
// to GET /authorize, a signed-in console session approves on the consent page,
// and the app exchanges the one-time code at POST /token for a scoped token.
//
// openvaultdb-go validates connect requests, mints codes and exchanges them;
// local mode only decides who may approve and renders the pages. Its own
// consent page cannot be used: it has an inline stylesheet the CSP refuses,
// and its 302 back to the app would break form-action 'self', which Chromium
// applies to a form's redirects. Local mode answers the approval with a page
// that sends the browser on instead.

const (
	authorizePath = "/authorize"
	tokenPath     = "/token"
	// connectFormLimit caps POST /authorize; it holds five short fields.
	connectFormLimit = 16 << 10
)

// connectFields are the connect request's parameters, in openvaultdb-go's
// names.
var connectFields = []string{"client_id", "redirect_uri", "db", "capabilities", "state"}

// connectView is what the consent and return pages show.
type connectView struct {
	App, Database, Redirect                                string
	Capabilities                                           []string
	Fields                                                 []connectField // the form's hidden inputs
	DatabaseLabel, AccessLabel, RedirectLabel, Allow, Deny string
	// Location is where the return page sends the browser.
	Location string
}

type connectField struct{ Name, Value string }

// authorize serves the consent page to a console session and records its
// decision. Anyone else — no credential, a bearer token, the instance secret
// — gets the landing page with a sign-in hint and approves nothing. A POST
// with the session has already passed cross-origin protection, so another
// site cannot approve on the person's behalf.
func (s *localServer) authorize(w http.ResponseWriter, r *http.Request) {
	session := credentialOf(r) == credentialSession
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !session {
			writeConnectLanding(w, r, http.StatusOK)
			return
		}
		s.consent(w, r, r.URL.Query())
	case http.MethodPost:
		if !session {
			writeConnectLanding(w, r, http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, connectFormLimit)
		if err := r.ParseForm(); err != nil {
			writeConnectProblem(w, uicopy.T("connect.form_invalid", nil))
			return
		}
		s.decide(w, r, r.PostForm)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func writeConnectLanding(w http.ResponseWriter, r *http.Request, status int) {
	page := landingPage(r)
	page.Notice = uicopy.T("connect.sign_in_first", nil)
	writePage(w, status, "landing", page)
}

// consent validates the request with openvaultdb-go and shows what the app
// asks for.
func (s *localServer) consent(w http.ResponseWriter, r *http.Request, values url.Values) {
	values = onlyConnectFields(values, false)
	if problem := s.checkConnect(r, http.MethodGet, values); problem != "" {
		writeConnectProblem(w, problem)
		return
	}
	view := connectView{
		App: values.Get("client_id"), Database: values.Get("db"), Redirect: values.Get("redirect_uri"),
		DatabaseLabel: uicopy.T("connect.database", nil), AccessLabel: uicopy.T("connect.access", nil),
		RedirectLabel: uicopy.T("connect.returns_to", nil),
		Allow:         uicopy.T("connect.allow", nil), Deny: uicopy.T("connect.deny", nil),
	}
	for _, capability := range strings.Split(values.Get("capabilities"), ",") {
		if capability = strings.TrimSpace(capability); capability != "" {
			view.Capabilities = append(view.Capabilities, capability)
		}
	}
	for _, name := range connectFields {
		if value := values.Get(name); value != "" {
			view.Fields = append(view.Fields, connectField{Name: name, Value: value})
		}
	}
	params := map[string]string{"app": view.App, "database": view.Database}
	writePage(w, http.StatusOK, "consent", pageData{
		Title: uicopy.T("connect.title", params), Body: uicopy.T("connect.body", params), Connect: &view,
	})
}

// decide has openvaultdb-go record the decision — approve mints a one-time
// code — and sends the browser back to the app with the code or the denial.
func (s *localServer) decide(w http.ResponseWriter, r *http.Request, form url.Values) {
	values := onlyConnectFields(form, true)
	if values.Get("decision") != "approve" {
		values.Set("decision", "deny")
	}
	recorder := s.connectRequest(r, http.MethodPost, values)
	location := recorder.Header().Get("Location")
	if recorder.Code != http.StatusFound || location == "" {
		writeConnectProblem(w, v1Message(recorder))
		return
	}
	params := map[string]string{"app": values.Get("client_id")}
	writePage(w, http.StatusOK, "return", pageData{
		Title: uicopy.T("connect.returning", params), Body: uicopy.T("login.body", nil),
		Continue: uicopy.T("login.continue", nil), Connect: &connectView{Location: location},
	})
}

// checkConnect returns why a connect request is invalid, or "".
func (s *localServer) checkConnect(r *http.Request, method string, values url.Values) string {
	recorder := s.connectRequest(r, method, values)
	if recorder.Code == http.StatusOK {
		return ""
	}
	return v1Message(recorder)
}

// connectRequest runs /authorize in openvaultdb-go with values.
func (s *localServer) connectRequest(r *http.Request, method string, values url.Values) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	if message := webRedirectOnly(values); message != "" {
		recorder.Code = http.StatusBadRequest
		_ = json.NewEncoder(recorder.Body).Encode(v1Error{Error: v1ErrorDetail{Code: "bad_request", Message: message}})
		return recorder
	}
	if id := values.Get("db"); id != "" {
		s.registry.AwaitMount(r.Context(), id)
	}
	target, body := authorizePath+"?"+values.Encode(), io.Reader(nil)
	if method == http.MethodPost {
		target, body = authorizePath, strings.NewReader(values.Encode())
	}
	request, err := http.NewRequestWithContext(r.Context(), method, target, body)
	if err != nil {
		recorder.Code = http.StatusBadRequest
		return recorder
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	s.data.ServeHTTP(recorder, request)
	return recorder
}

// webRedirectOnly refuses a redirect_uri that is not http or https: the
// return page puts it in a link and a refresh, where another scheme could
// run something.
func webRedirectOnly(values url.Values) string {
	redirect, err := url.Parse(values.Get("redirect_uri"))
	if err == nil && redirect.Scheme != "" && redirect.Scheme != "http" && redirect.Scheme != "https" {
		return uicopy.T("connect.redirect_not_web", nil)
	}
	return ""
}

// onlyConnectFields keeps the connect parameters (and the decision), so
// nothing else reaches openvaultdb-go.
func onlyConnectFields(values url.Values, decision bool) url.Values {
	out := url.Values{}
	for _, name := range connectFields {
		if value := values.Get(name); value != "" {
			out.Set(name, value)
		}
	}
	if decision {
		out.Set("decision", values.Get("decision"))
	}
	return out
}

// v1Message is the message of a /v1 error body, redacted.
func v1Message(recorder *httptest.ResponseRecorder) string {
	var failure v1Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil || failure.Error.Message == "" {
		return uicopy.T("connect.form_invalid", nil)
	}
	return redact.String(failure.Error.Message)
}

func writeConnectProblem(w http.ResponseWriter, reason string) {
	writePage(w, http.StatusBadRequest, "problem", pageData{
		Title: uicopy.T("connect.invalid", nil), Body: reason, Assistant: uicopy.T("connect.invalid_hint", nil),
	})
}

// protectAuthStore makes auth.json owner-only after openvaultdb-go writes
// it. openvaultdb-go writes 0600, which Windows ignores: there the file would
// only inherit the OVDB home's ACL, and an OVDB home that is not owner-only
// is a warning, not a refusal. The file holds token hashes, not tokens.
func (s *localServer) protectAuthStore() {
	path := filepath.Join(s.opts.Dirs.Home, AuthStoreFile)
	if _, err := os.Lstat(path); err != nil {
		return
	}
	if err := daemonlifecycle.ProtectOwnerOnly(path); err != nil {
		logf(s.opts.ErrorLog, s.opts.Now)("protect %s: %v", path, err)
	}
}
