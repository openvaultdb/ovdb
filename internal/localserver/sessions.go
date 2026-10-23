package localserver

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/openvaultdb/ovdb/internal/paths"
)

// Session lifetimes (REQ:sessions).
//
// Browsers send a host's cookies to every port on that host (RFC 6265
// §8.5): naming the cookie with the port avoids clashes, not disclosure. Any
// other program listening on 127.0.0.1 or localhost receives the console
// cookie when the browser visits it, and 127.0.0.1 and localhost are where
// dev servers live. So only sign-ins on ovdb.localhost get the long, sliding,
// persisted session people expect; a sign-in through a fallback host
// (127.0.0.1, localhost, [::1]) gets a browser-session cookie and a short
// fixed lifetime, limiting what a leaked cookie is worth. Sessions also
// cannot manage tokens or CORS origins (see route and putConfig).
const (
	// SessionTTL is the sliding expiry of an ovdb.localhost session: every
	// use pushes it out again.
	SessionTTL = 30 * 24 * time.Hour
	// FallbackSessionTTL is the absolute lifetime of a session created on a
	// fallback host. It does not slide.
	FallbackSessionTTL = 8 * time.Hour
	// sessionWriteInterval throttles sessions.json writes for renewals to
	// at most one a minute, so browsing does not rewrite the file per request.
	sessionWriteInterval = time.Minute
)

// SessionsFile holds console sessions, hashed, in the runtime directory. It
// survives restarts: runtime.removeRuntimeFiles keeps it.
const SessionsFile = "sessions.json"

// SessionCookieName is the console cookie for a port. Cookies ignore ports,
// so two OVDB servers on one host need different names.
func SessionCookieName(port int) string { return "ovdb_session_" + strconv.Itoa(port) }

type sessionsDocument struct {
	Schema   int             `json:"schema"`
	Sessions []sessionOnDisk `json:"sessions"`
}

type sessionOnDisk struct {
	Hash      string    `json:"hash"` // sha256 of the cookie value
	ExpiresAt time.Time `json:"expires_at"`
	Sliding   bool      `json:"sliding"` // renewed on use (ovdb.localhost sign-ins)
}

type session struct {
	expiresAt time.Time
	sliding   bool
}

// sessions is the console session store. Only hashes are kept, in memory and
// on disk, so neither sessions.json nor a heap dump yields a usable cookie.
//
// The file is the source of truth: a change made to it from outside (a
// session removed by hand, or by a later increment's sign-out) is picked up
// on the next request by its modification time.
type sessions struct {
	path string
	now  func() time.Time

	mu        sync.Mutex
	byHash    map[string]session // by hash
	loadedMod time.Time          // file mtime after the last load or write
	loadedLen int64              // and its size, for coarse mtime clocks
	loaded    bool
	dirty     bool      // renewals not yet written
	lastWrite time.Time // for the renewal throttle
}

func newSessions(runtimeDir string, now func() time.Time) *sessions {
	return &sessions{path: filepath.Join(runtimeDir, SessionsFile), now: now, byHash: map[string]session{}}
}

// reload rereads sessions.json when it changed since the last load or write.
// Callers hold mu.
func (s *sessions) reload() error {
	info, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		if s.loaded && !s.loadedMod.IsZero() {
			s.byHash = map[string]session{} // removed from outside
		}
		s.loaded, s.loadedMod = true, time.Time{}
		return nil
	}
	if err != nil {
		return err
	}
	if s.loaded && info.ModTime().Equal(s.loadedMod) && info.Size() == s.loadedLen {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var document sessionsDocument
	byHash := map[string]session{}
	// An unreadable file signs everyone out rather than failing every page.
	if json.Unmarshal(data, &document) == nil {
		for _, stored := range document.Sessions {
			byHash[stored.Hash] = session{expiresAt: stored.ExpiresAt, sliding: stored.Sliding}
		}
	}
	s.byHash, s.loaded, s.loadedMod, s.loadedLen, s.dirty = byHash, true, info.ModTime(), info.Size(), false
	return nil
}

// write persists the live sessions owner-only and atomically. Callers hold mu.
func (s *sessions) write() error {
	now := s.now()
	document := sessionsDocument{Schema: 1, Sessions: []sessionOnDisk{}}
	for hash, live := range s.byHash {
		if now.Before(live.expiresAt) {
			document.Sessions = append(document.Sessions, sessionOnDisk{Hash: hash, ExpiresAt: live.expiresAt.UTC(), Sliding: live.sliding})
		} else {
			delete(s.byHash, hash)
		}
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err := paths.WriteFilePrivate(s.path, append(data, '\n')); err != nil {
		return err
	}
	if info, err := os.Stat(s.path); err == nil {
		s.loadedMod, s.loadedLen = info.ModTime(), info.Size()
	}
	s.dirty, s.lastWrite = false, now
	return nil
}

// create starts a session and returns the cookie value. sliding sessions
// last SessionTTL from their last use; others FallbackSessionTTL from now.
func (s *sessions) create(sliding bool) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return "", err
	}
	ttl := FallbackSessionTTL
	if sliding {
		ttl = SessionTTL
	}
	s.byHash[hashCode(token)] = session{expiresAt: s.now().Add(ttl), sliding: sliding}
	return token, s.write()
}

// touch reports whether token is a live session and slides its expiry.
// renewed is true when this call wrote the renewal to disk, which is when
// the cookie's own expiry is worth refreshing too. Fallback-host sessions
// never renew.
func (s *sessions) touch(token string) (valid, renewed bool) {
	if token == "" {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reload() != nil {
		return false, false
	}
	hash := hashCode(token)
	live, ok := s.byHash[hash]
	now := s.now()
	if !ok || !now.Before(live.expiresAt) {
		return false, false
	}
	if !live.sliding {
		return true, false
	}
	s.byHash[hash] = session{expiresAt: now.Add(SessionTTL), sliding: true}
	s.dirty = true
	if now.Sub(s.lastWrite) >= sessionWriteInterval {
		renewed = s.write() == nil
	}
	return true, renewed
}

// remove ends the session for token (sign-out).
func (s *sessions) remove(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return err
	}
	hash := hashCode(token)
	if _, ok := s.byHash[hash]; !ok {
		return nil
	}
	delete(s.byHash, hash)
	return s.write()
}

// flush writes pending renewals; the server calls it on shutdown.
func (s *sessions) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil || !s.dirty {
		return err
	}
	return s.write()
}
