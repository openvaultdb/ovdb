// Package redact removes credentials from text before it leaves the server
// process: error reasons, status, mounts.json and server.log lines.
//
// See spec/features/local-server-and-web-console#REQ:redacted-errors.
package redact

import (
	"io"
	"regexp"
)

// Mask replaces every removed value.
const Mask = "[redacted]"

var rules = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// scheme://user:password@host → scheme://[redacted]@host. The user-info
	// runs to the last "@" of the token, so a password containing "@" or "/"
	// is removed whole.
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^\s"'<>]*@`), "${1}" + Mask + "@"},
	// go-sql-driver/mysql DSNs: user:password@tcp(host)/db, @unix(/sock).
	{regexp.MustCompile(`[^\s"'<>()]*@((?:tcp|tcp4|tcp6|udp|unix|memory)\()`), Mask + "@${1}"},
	// key=value, key: value and "key": "value" pairs whose key mentions a
	// secret. The value ends at whitespace, a separator or a closing quote.
	{regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]*(?:password|passwd|pwd|secret|token|key)[A-Za-z0-9_.\-]*"?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s;&,'"}]+)`), "${1}" + Mask},
	// Bearer and Basic credentials in echoed headers.
	{regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=\-]+`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)\b(basic\s+)[A-Za-z0-9+/]{8,}={0,2}`), "${1}" + Mask},
}

// String returns s with URL user-info, DSN credentials, secret-named
// key/value pairs and Bearer or Basic credentials replaced by Mask. It is
// deliberately over-eager: a harmless word masked in a log line costs less
// than a leaked password.
func String(s string) string {
	for _, rule := range rules {
		s = rule.pattern.ReplaceAllString(s, rule.replacement)
	}
	return s
}

// Writer redacts each Write before passing it on. Callers write whole lines
// (log.Logger does), so a credential is never split across two writes.
type Writer struct{ W io.Writer }

func (w Writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.W, String(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
