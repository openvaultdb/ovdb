package rules

import (
	"strings"
	"unicode/utf8"
)

// The rules for the names of a database's recordsets, as the Directory has them since it took native names (nativeRecordsetNameProblem in
// directory.mjs, commits 1c7e126 and d089fa8): a recordset may be named as the publisher's own database names it, a table such as
// dbo.DatabaseLog or Order Details, and not only as a ModelSpec entity. The URL of its page is then made from the name written as one encoded path
// segment (encodePathSegment in urls.mjs).

// MaxRecordsetNameLength is the longest recordset name, in UTF-16 code units, as JavaScript's String.length counts them: the Directory's bound.
const MaxRecordsetNameLength = 256

// RuleRecordsetName is the rule a refused recordset name broke.
const RuleRecordsetName Rule = "recordset-name"

// RecordsetName reports why s is not a recordset name, or nil: text that is not blank by JavaScript's trim(), at most 256 UTF-16 code units, not "." or
// "..", and with no slash, backslash or control character (U+0000 to U+001F, U+007F). Any other character is allowed, a space, a dot and a non-ASCII
// letter among them.
func RecordsetName(s string) error {
	if IsBlank(s) {
		return problem(RuleRecordsetName, "must be a non-empty name")
	}
	if s == "." || s == ".." {
		return problem(RuleRecordsetName, "cannot be a dot path segment")
	}
	units := 0
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r < 0x20 || r == 0x7f:
			return problem(RuleRecordsetName, "must contain no slash, backslash or control character")
		case r >= 0x10000:
			units += 2
		default:
			units++
		}
		if units > MaxRecordsetNameLength {
			return problem(RuleRecordsetName, "must be at most %d characters", MaxRecordsetNameLength)
		}
	}
	return nil
}

// EncodePathSegment is JavaScript's encodeURIComponent with the five characters ! ' ( ) * encoded too (as the Directory's encodePathSegment does):
// every byte of the UTF-8 text except A-Z a-z 0-9 - _ . ~ is written as %XX in upper case.
func EncodePathSegment(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// RecordsetPage reports why the page of the recordset called name is not an acceptable public URL, or nil. The page is the template of
// deployment.recordset_page with {name} replaced by the name as one encoded path segment. A name that needs no encoding makes an ordinary public
// https URL. One that does is accepted only when the segment is all that the {name} makes (nothing of the template shares its path segment), the
// template holds {name} in its path, the name is neither "." nor "..", has no slash, backslash or control character, and nothing inside it, decoded
// again and again, becomes one of those: a router that decodes a second time must not find a path separator or a dot segment. The rest of the URL is
// held to the ordinary rules, and the whole page to the length of every URL.
func RecordsetPage(template, name string) error {
	return asError(checkRecordsetPage(template, name, defaults(false)))
}

func checkRecordsetPage(template, name string, o options) *Problem {
	encoded := EncodePathSegment(name)
	at := strings.Index(template, placeholder)
	if at < 0 || !strings.Contains(encoded, "%") {
		return checkURL(strings.Replace(template, placeholder, encoded, 1), o)
	}
	if len(template)-len(placeholder)+len(encoded) > o.maxLen {
		return problem(RuleLength, "is longer than %d characters", o.maxLen)
	}
	before, after := template[:at], template[at+len(placeholder):]
	authority := strings.Index(strings.TrimPrefix(template, scheme), "/")
	if !strings.HasPrefix(template, scheme) || authority < 0 || at < len(scheme)+authority ||
		!strings.HasSuffix(before, "/") || after != "" && !strings.HasPrefix(after, "/") {
		return problem(RulePercent, "must not contain a percent escape in the path (write the character itself, or leave it out)")
	}
	// The ordinary rules, on the page with a plain stand-in for the segment.
	if p := checkURL(before+"n"+after, o); p != nil {
		return p
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") || hasControl(name) {
		return problem(RulePercent, "has a non-canonical or unsafe encoded path segment")
	}
	for nested := name; hasEscape(nested); {
		next, ok := decodeURIComponent(nested)
		if !ok || next == nested {
			break
		}
		if next == "." || next == ".." || strings.ContainsAny(next, "/\\") || hasControl(next) {
			return problem(RulePercent, "has a nested percent escape that can become a path separator, control character or dot segment")
		}
		nested = next
	}
	return nil
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// hasEscape reports whether s holds a % and two hexadecimal digits (either case).
func hasEscape(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			return true
		}
	}
	return false
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func hexValue(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// decodeURIComponent is JavaScript's: every %XX is a byte, the bytes of a run must be UTF-8, and anything else (a % without two hexadecimal digits, a
// byte sequence that is not UTF-8) makes it fail, as it throws a URIError.
func decodeURIComponent(s string) (string, bool) {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", false
		}
		out = append(out, hexValue(s[i+1])<<4|hexValue(s[i+2]))
		i += 2
	}
	if !utf8.Valid(out) {
		return "", false
	}
	return string(out), true
}
