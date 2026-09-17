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
const (
	// SessionTTL is the sliding expiry: every use pushes it out again.
	SessionTTL = 30 * 24 * time.Hour
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
	byHash    map[string]time.Time // hash → expiry
	loadedMod time.Time            // file mtime after the last load or write
	loadedLen int64                // and its size, for coarse mtime clocks
	loaded    bool
	dirty     bool      // renewals not yet written
	lastWrite time.Time // for the renewal throttle
}

func newSessions(runtimeDir string, now func() time.Time) *sessions {
	return &sessions{path: filepath.Join(runtimeDir, SessionsFile), now: now, byHash: map[string]time.Time{}}
}

// reload rereads sessions.json when it changed since the last load or write.
// Callers hold mu.
func (s *sessions) reload() error {
	info, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		if s.loaded && !s.loadedMod.IsZero() {
			s.byHash = map[string]time.Time{} // removed from outside
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
	byHash := map[string]time.Time{}
	// An unreadable file signs everyone out rather than failing every page.
	if json.Unmarshal(data, &document) == nil {
		for _, session := range document.Sessions {
			byHash[session.Hash] = session.ExpiresAt
		}
	}
	s.byHash, s.loaded, s.loadedMod, s.loadedLen, s.dirty = byHash, true, info.ModTime(), info.Size(), false
	return nil
}

// write persists the live sessions owner-only and atomically. Callers hold mu.
func (s *sessions) write() error {
	now := s.now()
	document := sessionsDocument{Schema: 1, Sessions: []sessionOnDisk{}}
	for hash, expiresAt := range s.byHash {
		if now.Before(expiresAt) {
			document.Sessions = append(document.Sessions, sessionOnDisk{Hash: hash, ExpiresAt: expiresAt.UTC()})
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

// create starts a session and returns the cookie value.
func (s *sessions) create() (string, error) {
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
	s.byHash[hashCode(token)] = s.now().Add(SessionTTL)
	return token, s.write()
}

// touch reports whether token is a live session and slides its expiry.
// renewed is true when this call wrote the renewal to disk, which is when
// the cookie's own expiry is worth refreshing too.
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
	expiresAt, ok := s.byHash[hash]
	now := s.now()
	if !ok || !now.Before(expiresAt) {
		return false, false
	}
	s.byHash[hash] = now.Add(SessionTTL)
	s.dirty = true
	if now.Sub(s.lastWrite) >= sessionWriteInterval {
		renewed = s.write() == nil
	}
	return true, renewed
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
