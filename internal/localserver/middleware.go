package localserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// The chain, outermost first, is fixed by REQ:route-layout:
//
//	security headers → Host allowlist → authentication →
//	cross-origin protection (cookie requests, 1b) → CORS (bearer requests, 1b) → routes
//
// Each step is a func(http.Handler) http.Handler, so increment 1b inserts
// the cookie session into authenticate and adds its two steps to chain()
// without touching the others.

// securityHeaders sets the headers every local-mode response carries,
// including 401, 403 and the landing page (REQ:security-headers). The HTML
// headers are sent on every response too: they are harmless on JSON, and a
// response can never miss them by being mislabelled.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		header.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
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
	credentialNone           credential = iota // no Authorization header
	credentialInvalid                          // a bearer that is neither secret nor token
	credentialInstanceSecret                   // the owner's CLI or TUI
	credentialScopedToken                      // a token from <OVDB_HOME>/auth.json
	// credentialSession, the console cookie, arrives in increment 1b.
)

type credentialKey struct{}

func credentialOf(r *http.Request) credential {
	c, _ := r.Context().Value(credentialKey{}).(credential)
	return c
}

// authenticate classifies the request's credential; routes decide what each
// credential may do (the credential table in REQ:credentials).
func authenticate(secret string, store *auth.Store) func(http.Handler) http.Handler {
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
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), credentialKey{}, kind)))
		})
	}
}

// access is what a local API route requires.
type access int

const (
	accessOwner          access = iota // instance secret (and, from 1b, a console session)
	accessInstanceSecret               // instance secret only: shutdown, login links
)

// allow reports whether c may use a route requiring a, writing the 401 or
// 403 when it may not.
//
// Increment 1b adds the session rows: a session passes accessOwner and gets
// 403 on accessInstanceSecret.
func allow(w http.ResponseWriter, c credential, _ access) bool {
	switch c {
	case credentialInstanceSecret:
		return true
	case credentialScopedToken:
		envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.token_not_allowed", nil)))
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="ovdb-local"`)
		envelope.Write(w, envelope.New(envelope.Unauthorized, uicopy.T("api.unauthorized", nil)).
			WithNext(envelope.Next{Label: uicopy.T("next.open_web_setup", nil), Command: "ovdb open"}))
	}
	return false
}
