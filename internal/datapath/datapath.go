// Package datapath is the file-like path people and agents type to name a
// place inside a database: `/lists/to-buy/items`. Odd segments are
// collections and even segments record ids. Ids are written and shown
// escaped the way record.EscapeID (dal-go/record) escapes them, so `a/b.txt`
// is `a%2Fb%2Etxt` everywhere: in input, output and the server's keys.
//
// See spec/features/database-context-navigation (REQ:path-resolution) and
// decision 0008.
package datapath

import (
	"strings"

	"github.com/dal-go/record"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// Kind is what a path names.
type Kind string

// Path kinds.
const (
	Root       Kind = "root"
	Collection Kind = "collection"
	Record     Kind = "record"
)

// Path is an absolute location as unescaped segments; the zero value is "/".
type Path struct {
	segments []string
}

// escapes are the sequences record.EscapeID produces; any other `%` is not a
// valid id character.
var escapes = map[string]string{"%2F": "/", "%2E": ".", "%24": "$", "%23": "#", "%5B": "[", "%5D": "]"}

// Segments returns the unescaped segments.
func (p Path) Segments() []string { return append([]string(nil), p.segments...) }

// Kind reports whether p is the root, a collection or a record.
func (p Path) Kind() Kind {
	switch {
	case len(p.segments) == 0:
		return Root
	case len(p.segments)%2 == 1:
		return Collection
	default:
		return Record
	}
}

// String is p as people read and type it: absolute, ids escaped.
func (p Path) String() string {
	if len(p.segments) == 0 {
		return "/"
	}
	parts := make([]string, len(p.segments))
	for i, segment := range p.segments {
		parts[i] = record.EscapeID(segment)
	}
	return "/" + strings.Join(parts, "/")
}

// Key is p as the server's key path: escaped segments without the leading
// slash ("lists/to-buy"); "" for the root.
func (p Path) Key() string { return strings.TrimPrefix(p.String(), "/") }

// URLKey is Key made safe for a URL path: every byte outside the unreserved
// set is percent-encoded, except the `%` of an EscapeID sequence, which the
// server unescapes per segment.
func (p Path) URLKey() string {
	var b strings.Builder
	for i, segment := range p.segments {
		if i > 0 {
			b.WriteByte('/')
		}
		for _, c := range []byte(record.EscapeID(segment)) {
			if c == '%' || c == '-' || c == '_' || c == '~' || c == '.' ||
				c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				b.WriteByte(c)
				continue
			}
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// Parent is p without its last segment; the root's parent is the root.
func (p Path) Parent() Path {
	if len(p.segments) == 0 {
		return p
	}
	return Path{segments: p.segments[:len(p.segments)-1]}
}

// Name is the last segment unescaped, "" for the root.
func (p Path) Name() string {
	if len(p.segments) == 0 {
		return ""
	}
	return p.segments[len(p.segments)-1]
}

// Child is p with one more (unescaped) segment.
func (p Path) Child(segment string) Path {
	return Path{segments: append(append([]string(nil), p.segments...), segment)}
}

// FromKey parses a server key ("lists/to-buy", escaped segments).
func FromKey(key string) (Path, error) {
	return Resolve(Path{}, "/"+key)
}

// Parse is Resolve from the root.
func Parse(input string) (Path, error) { return Resolve(Path{}, input) }

// Resolve applies input to base: a leading `/` is absolute, anything else is
// relative; `.` stays, `..` goes up one segment and never above `/`, and
// empty segments are ignored. A `%` that does not start one of the six
// record.EscapeID sequences is invalid_argument.
func Resolve(base Path, input string) (Path, error) {
	segments := append([]string(nil), base.segments...)
	if strings.HasPrefix(input, "/") {
		segments = nil
	}
	for _, part := range strings.Split(input, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
			continue
		}
		segment, err := unescape(part)
		if err != nil {
			return Path{}, invalid(input, err.Error())
		}
		segments = append(segments, segment)
	}
	return Path{segments: segments}, nil
}

type badEscape string

func (e badEscape) Error() string { return string(e) }

func unescape(part string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		if part[i] != '%' {
			b.WriteByte(part[i])
			continue
		}
		if i+3 <= len(part) {
			if raw, ok := escapes[strings.ToUpper(part[i:i+3])]; ok {
				b.WriteString(raw)
				i += 2
				continue
			}
		}
		return "", badEscape(uicopy.T("path.bad_percent", map[string]string{"segment": part}))
	}
	return b.String(), nil
}

func invalid(input, reason string) *envelope.Error {
	return envelope.New(envelope.InvalidArgument, uicopy.T("path.invalid", map[string]string{"path": input})).
		WithReason(reason).
		WithNext(envelope.Next{Label: uicopy.T("next.path_escapes", nil)})
}
