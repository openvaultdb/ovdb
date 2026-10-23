package localserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// The chain, outermost first, is fixed by REQ:route-layout:
//
//	security headers → (panic recovery) → Host allowlist → authentication →
//	cross-origin protection (cookie requests) → CORS (bearer requests) → routes
//
// Each step is a func(http.Handler) http.Handler; New assembles them.

// securityHeaders sets the headers every local-mode response carries,
// including 401, 403 and the landing page (REQ:security-headers). The HTML
// headers are sent on every response too: they are harmless on JSON, and a
// response can never miss them by being mislabelled.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		header.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// recoverPanics turns a handler panic into a 500 internal envelope, keeping
// the security headers set outside it, and logs the panic redacted instead
// of letting net/http print it raw.
func recoverPanics(errorLog io.Writer) func(http.Handler) http.Handler {
	if errorLog == nil {
		errorLog = io.Discard
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered) // net/http's own way to abort a response
				}
				_, _ = fmt.Fprintf(redact.Writer{W: errorLog}, "panic serving %s %s: %v\n%s\n", r.Method, r.URL.Path, recovered, debug.Stack())
				envelope.Write(w, envelope.New(envelope.Internal, uicopy.T("api.internal", nil)))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// allowedHosts are the Host values a local-mode server answers, with the
// port. Anything else — another name resolving to 127.0.0.1 (DNS
// rebinding), a trailing dot, a missing port — gets 403 (REQ:host-allowlist).
func allowedHosts(port int) map[string]bool {
	suffix := ":" + strconv.Itoa(port)
	return map[string]bool{
		"ovdb.localhost" + suffix: true,
		"localhost" + suffix:      true,
		"127.0.0.1" + suffix:      true,
		"[::1]" + suffix:          true,
	}
}

func hostAllowlist(port int) func(http.Handler) http.Handler {
	allowed := allowedHosts(port)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allowed[strings.ToLower(r.Host)] {
				envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.host_not_allowed", nil)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// credential is how a request authenticated.
type credential int

const (
	credentialNone           credential = iota // no Authorization header and no live session
	credentialInvalid                          // a bearer that is neither secret nor token
	credentialInstanceSecret                   // the owner's CLI or TUI
	credentialScopedToken                      // a token from <OVDB_HOME>/auth.json
	credentialSession                          // the console cookie (owner-equivalent, browsers only)
)

type credentialKey struct{}

func credentialOf(r *http.Request) credential {
	c, _ := r.Context().Value(credentialKey{}).(credential)
	return c
}

// isBearer reports whether the request carried an Authorization header.
func (c credential) isBearer() bool {
	return c == credentialInvalid || c == credentialInstanceSecret || c == credentialScopedToken
}

// authenticate classifies the request's credential; routes decide what each
// credential may do (the credential table in REQ:credentials). An explicit
// Authorization header wins over the session cookie, even when it is
// invalid, so a tool never acts with a browser's session by accident.
func (s *localServer) authenticate(store *auth.Store) func(http.Handler) http.Handler {
	secret := s.opts.Secret
	cookieName := SessionCookieName(s.opts.Record.Port)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kind := credentialNone
			if token := auth.BearerToken(r); token != "" {
				switch {
				case subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1:
					kind = credentialInstanceSecret
				case store != nil && store.Lookup(token) != nil:
					kind = credentialScopedToken
				default:
					kind = credentialInvalid
				}
			} else if r.Header.Get("Authorization") != "" {
				kind = credentialInvalid
			} else if cookie, err := r.Cookie(cookieName); err == nil {
				if valid, renewed := s.sessions.touch(cookie.Value); valid {
					kind = credentialSession
					if renewed {
						http.SetCookie(w, s.sessionCookie(cookie.Value, true))
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), credentialKey{}, kind)))
		})
	}
}

// crossOrigin applies Go's http.CrossOriginProtection to non-safe requests
// authenticated by the session cookie, and to POST /login, which would
// otherwise let another site sign the browser in (REQ:cross-origin-protection).
// Bearer requests are not CSRF-able: a browser never adds the header on its
// own, so they pass.
func crossOrigin(next http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if credentialOf(r) == credentialSession || r.URL.Path == loginPath || r.URL.Path == logoutPath {
			if err := protection.Check(r); err != nil {
				envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.cross_origin", nil)))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// corsPaths are the only routes browser apps on other origins may call.
func corsPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/") || path == tokenPath
}

// cors lets browser apps on the origins listed in server.cors call /v1/… with
// bearer tokens and exchange connect codes at /token. Cookie requests never get CORS headers, and no
// response allows credentials, so a cross-origin page cannot read anything
// with the console session.
func cors(origins []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[origin] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !corsPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			// Every response on a CORS path depends on Origin, including
			// the ones without it, or a cache could serve one to the other.
			w.Header().Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin == "" || !allowed[strings.ToLower(origin)] {
				next.ServeHTTP(w, r)
				return
			}
			header := w.Header()
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				// A preflight carries no credentials; it only asks.
				header.Set("Access-Control-Allow-Origin", origin)
				header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				header.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// A code exchange at /token carries no credential: the code is
			// one. A cookie request never gets CORS headers.
			if credential := credentialOf(r); credential.isBearer() || (r.URL.Path == tokenPath && credential == credentialNone) {
				header.Set("Access-Control-Allow-Origin", origin)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// access is what a local API route requires.
type access int

const (
	accessOwner          access = iota // instance secret or console session
	accessInstanceSecret               // instance secret only: shutdown, login links
)

// allow reports whether c may use a route requiring a, writing the 401 or
// 403 when it may not.
func allow(w http.ResponseWriter, c credential, a access) bool {
	switch c {
	case credentialInstanceSecret:
		return true
	case credentialSession:
		if a == accessOwner {
			return true
		}
		envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.session_not_allowed", nil)))
	case credentialScopedToken:
		envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.token_not_allowed", nil)))
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="ovdb-local"`)
		envelope.Write(w, envelope.New(envelope.Unauthorized, uicopy.T("api.unauthorized", nil)).
			WithNext(envelope.Next{Label: uicopy.T("next.open_web_setup", nil), Command: "ovdb open"}))
	}
	return false
}
