// Package redact removes credentials from text before it leaves the server
// process: error reasons, status, mounts.json and server.log lines.
//
// See spec/features/local-server-and-web-console#REQ:redacted-errors.
package redact

import "regexp"

// Mask replaces every removed value.
const Mask = "[redacted]"

var (
	// scheme://user:password@host → scheme://[redacted]@host. The user-info
	// part is everything between "//" and the last "@" before the host.
	urlUserInfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/\s@]+@`)

	// key=value and key: value pairs whose key mentions a secret. The value
	// ends at whitespace, a separator or a closing quote.
	secretPair = regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]*(?:password|passwd|pwd|secret|token|key)[A-Za-z0-9_.\-]*\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s;&,'"]+)`)

	// Bearer credentials in echoed headers.
	bearer = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=\-]+`)
)

// String returns s with URL user-info, secret-named key/value pairs and
// bearer credentials replaced by Mask. It is deliberately over-eager: a
// harmless word masked in a log line costs less than a leaked password.
func String(s string) string {
	s = urlUserInfo.ReplaceAllString(s, "${1}"+Mask+"@")
	s = secretPair.ReplaceAllString(s, "${1}"+Mask)
	s = bearer.ReplaceAllString(s, "${1}"+Mask)
	return s
}
