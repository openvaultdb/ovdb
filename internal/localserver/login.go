package localserver

import (
	"net/http"

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
// page on another site cannot sign the browser in to its own session.
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
		token, err := s.sessions.create()
		if err != nil {
			writeError(w, err)
			return
		}
		http.SetCookie(w, s.sessionCookie(token))
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

// sessionCookie is the console cookie (REQ:sessions): HttpOnly so page
// scripts never see it, SameSite=Lax so links from chats and documents
// arrive signed in, host-only (no Domain), and named with the port.
func (s *localServer) sessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name: SessionCookieName(s.opts.Record.Port), Value: token, Path: "/",
		MaxAge: int(SessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
}
