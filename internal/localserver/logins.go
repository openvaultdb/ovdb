package localserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// LoginLinkTTL is how long a login code stays valid (REQ:login-links).
const LoginLinkTTL = 10 * time.Minute

// LoginLink is the body of POST /api/local/v1/login-links.
type LoginLink struct {
	Schema      int       `json:"schema"`
	URL         string    `json:"url"`
	FallbackURL string    `json:"fallback_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// LoginLinkRequest is its optional request body.
type LoginLinkRequest struct {
	Next string `json:"next,omitempty"`
}

// loginLinks keeps single-use codes in memory, hashed, so a code is never
// logged or persisted. Increment 1b's POST /login consumes them
// (and only the POST: REQ:login-exchange-on-post).
type loginLinks struct {
	now   func() time.Time
	mu    sync.Mutex
	codes map[string]time.Time // sha256(code) → expiry
}

func newLoginLinks(now func() time.Time) *loginLinks {
	return &loginLinks{now: now, codes: map[string]time.Time{}}
}

func (l *loginLinks) create() (code string, expiresAt time.Time, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	code = base64.RawURLEncoding.EncodeToString(buf)
	now := l.now()
	expiresAt = now.Add(LoginLinkTTL).UTC().Truncate(time.Second)
	l.mu.Lock()
	defer l.mu.Unlock()
	for hash, expiry := range l.codes {
		if !now.Before(expiry) {
			delete(l.codes, hash)
		}
	}
	l.codes[hashCode(code)] = expiresAt
	return code, expiresAt, nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func (s *localServer) loginLink(w http.ResponseWriter, r *http.Request) {
	var request LoginLinkRequest
	if !decodeBody(w, r, &request) {
		return
	}
	if request.Next != "" && !isLocalPath(request.Next) {
		envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("api.login_next_invalid", nil)))
		return
	}
	code, expiresAt, err := s.logins.create()
	if err != nil {
		writeError(w, err)
		return
	}
	query := url.Values{"code": {code}}
	if request.Next != "" {
		query.Set("next", request.Next)
	}
	suffix := "/login?" + query.Encode()
	port := s.opts.Record.Port
	envelope.WriteJSON(w, http.StatusOK, LoginLink{
		Schema: envelope.Schema, URL: setup.PrimaryAddress(port) + suffix,
		FallbackURL: setup.FallbackAddress(port) + suffix, ExpiresAt: expiresAt,
	})
}

// isLocalPath accepts an absolute path on this server and rejects anything
// a browser could treat as another origin ("//host", "/\host", "http:").
func isLocalPath(next string) bool {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return false
	}
	parsed, err := url.Parse(next)
	return err == nil && parsed.Scheme == "" && parsed.Host == ""
}
