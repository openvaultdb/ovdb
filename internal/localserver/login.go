package localserver

import (
	"net/http"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
)

// loginFormLimit caps the POST /login body; it holds only a code and a path.
const loginFormLimit = 16 << 10

// login is the browser side of a login link (REQ:login-exchange-on-post).
//
// GET renders a page that posts the code back and changes nothing, so link
// previewers and chat unfurlers that fetch the link cannot spend it. POST
// consumes the code, starts a session and redirects to next or the console.
// POST has already passed cross-origin protection (see crossOrigin), so a
// page on another site cannot submit a code on the browser's behalf. (It can
// still send the browser to GET /login?code=…, which posts same-origin; that
// needs a code, and only the instance secret creates codes.)
func (s *localServer) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		query := r.URL.Query()
		code, next := query.Get("code"), query.Get("next")
		if code == "" {
			writeLanding(w, r, http.StatusOK)
			return
		}
		if !isLocalPath(next) {
			next = ""
		}
		writePage(w, http.StatusOK, "login", pageData{
			Title: uicopy.T("login.title", nil), Body: uicopy.T("login.body", nil),
			Continue: uicopy.T("login.continue", nil), Code: code, Next: next,
		})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, loginFormLimit)
		if err := r.ParseForm(); err != nil || !s.logins.consume(r.PostForm.Get("code")) {
			// Invalid, expired or already used: the landing page says how
			// to get a new link.
			writeLanding(w, r, http.StatusOK)
			return
		}
		sliding := isPrimaryHost(r.Host)
		token, err := s.sessions.create(sliding)
		if err != nil {
			writeError(w, err)
			return
		}
		http.SetCookie(w, s.sessionCookie(token, sliding))
		next := r.PostForm.Get("next")
		if !isLocalPath(next) {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// logout ends the session and clears the cookie, then shows the signed-out
// landing page. It is a POST behind cross-origin protection, so another site
// cannot sign the person out.
func (s *localServer) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(SessionCookieName(s.opts.Record.Port)); err == nil {
		if err := s.sessions.remove(cookie.Value); err != nil {
			writeError(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName(s.opts.Record.Port), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, signedOutPath, http.StatusSeeOther)
}

// isPrimaryHost reports whether host is ovdb.localhost (with any port; the
// Host allowlist has already checked the port).
func isPrimaryHost(host string) bool {
	return strings.HasPrefix(strings.ToLower(host), "ovdb.localhost:")
}

// sessionCookie is the console cookie (REQ:sessions): HttpOnly so page
// scripts never see it, SameSite=Lax so links from chats and documents
// arrive signed in, host-only (no Domain), and named with the port. A
// sliding (ovdb.localhost) session's cookie persists for SessionTTL; a
// fallback-host session's cookie ends with the browser session.
func (s *localServer) sessionCookie(token string, sliding bool) *http.Cookie {
	cookie := &http.Cookie{
		Name: SessionCookieName(s.opts.Record.Port), Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
	if sliding {
		cookie.MaxAge = int(SessionTTL.Seconds())
	}
	return cookie
}
