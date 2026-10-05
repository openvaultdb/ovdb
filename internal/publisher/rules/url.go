package rules

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rule names the rule that a refused input broke. It is stable: callers and
// tests match on it, never on the message.
type Rule string

// The rules a URL can break.
const (
	RuleLength          Rule = "length"          // longer than the bound of the function
	RuleNotURL          Rule = "not-a-url"       // empty, or not https://host/path at all
	RuleCharacter       Rule = "character"       // whitespace, a control character, a backslash or a non-ASCII byte
	RuleScheme          Rule = "scheme"          // a scheme other than https
	RuleUserinfo        Rule = "userinfo"        // credentials in the authority
	RulePort            Rule = "port"            // a port, even :443
	RuleQuery           Rule = "query"           // a ? in the URL
	RuleFragment        Rule = "fragment"        // a # in the URL
	RulePercent         Rule = "percent-escape"  // a % in the URL
	RuleHostCharacter   Rule = "host-character"  // a character outside a-z 0-9 hyphen and dot in the host
	RuleHostCase        Rule = "host-case"       // an upper-case letter in the host
	RuleHostLabel       Rule = "host-label"      // an empty label, a label over 63 bytes, a hyphen at either end of a label
	RuleHostLength      Rule = "host-length"     // a host over 253 bytes
	RuleHostSingleLabel Rule = "host-single"     // a host of one label
	RuleHostNumeric     Rule = "host-numeric"    // a host that is, or could be read as, an IP address
	RuleHostReserved    Rule = "host-reserved"   // a local, internal or reserved name
	RuleHostPunycode    Rule = "host-punycode"   // an xn-- label that is not valid punycode for a host name
	RuleHostIDN         Rule = "host-idn"        // an xn-- label of letters this tool does not accept yet
	RuleNoPath          Rule = "no-path"         // no path: write https://host/
	RulePathCharacter   Rule = "path-character"  // a character outside A-Z a-z 0-9 . _ ~ / and - in the path
	RulePathEmptySeg    Rule = "path-empty"      // //
	RulePathDotSegment  Rule = "path-dot"        // a . or .. segment
	RulePlaceholder     Rule = "placeholder"     // {name} missing, repeated, or outside the path
	RuleHomepageLength  Rule = "homepage-length" // a homepage over 200 bytes
)

// Problem is why a URL is refused.
type Problem struct {
	Rule   Rule
	Detail string
}

// Error returns the detail.
func (p *Problem) Error() string { return p.Detail }

func problem(rule Rule, format string, args ...any) *Problem {
	return &Problem{Rule: rule, Detail: fmt.Sprintf(format, args...)}
}

// maxShown is the most bytes of an input that a message repeats.
const maxShown = 40

// Quote is [show] for other packages: the one way a piece of input goes into a
// message, cut to a short length, quoted and escaped to printable ASCII.
func Quote(s string) string { return show(s) }

// show is the one way a piece of input goes into a message: cut to maxShown
// bytes, quoted, and escaped to printable ASCII, so that no message holds a
// control character, an escape sequence, a line break (a forged log line or
// workflow command) or more than a short piece of what was given.
func show(s string) string {
	cut := len(s) > maxShown
	if cut {
		n := maxShown
		for n > 0 && !utf8.RuneStart(s[n]) { // never cut inside a character
			n--
		}
		for n > 0 && isMark(s[n:]) { // nor between a letter and the mark that combines with it
			n--
			for n > 0 && !utf8.RuneStart(s[n]) {
				n--
			}
		}
		s = s[:n]
	}
	quoted := strconv.QuoteToASCII(s)
	if cut {
		quoted += "..."
	}
	return quoted
}

// isMark reports whether s starts with a combining mark (category M), which belongs to the character before it.
func isMark(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.Is(unicode.M, r)
}

// placeholder is the one literal a template URL may hold, in its path.
const placeholder = "{name}"

// options carries the limits of one run. The public functions use defaults;
// the tests lift one limit at a time to prove that a recorded difference from
// the references is exactly that limit and nothing else.
type options struct {
	template bool
	maxLen   int
	puny     punycodeMode
	// encoded allows canonical percent-encoded path segments, and example allows a host under .example: a global database identity (GlobalDatabaseID).
	encoded, example bool
}

func defaults(template bool) options {
	return options{template: template, maxLen: MaxURLLength}
}

// PublicHTTPSURL reports why s is not a public https URL, or nil.
//
// A public https URL is written exactly like this: https://, a host, a path.
//
//   - https only; no userinfo, no port (not even :443), no query, no fragment,
//     no percent escape, no whitespace, control character or backslash;
//   - the host is lower-case letters, digits and hyphens in dot-separated
//     labels of 1 to 63 bytes (none starting or ending with a hyphen), at least
//     two labels, at most 253 bytes, no trailing dot; no IP address, nothing a
//     reader could take for one (a last label of digits, or 0x and hex digits),
//     no single-label name, no local, internal or reserved suffix; an xn--
//     label only when it is the canonical punycode of Latin-1 letters;
//   - the path starts with / and holds only A-Z a-z 0-9 . _ ~ / and -, no //,
//     no . or .. segment;
//   - the spelling is the canonical one: there is exactly one way to write a
//     URL that passes.
//
// It does not check that anything exists, and it cannot see the address a name
// resolves to: a public-looking name can resolve to a private address, which
// only the party that connects can check.
func PublicHTTPSURL(s string) error {
	_, err := ParsePublicHTTPSURL(s)
	return err
}

// noURL is what a refused read returns: the zero URL would read as "a template
// with {name} at offset 0".
var noURL = URL{Placeholder: -1}

// URL is an accepted URL in its parts, so that the rules that look at a host or
// a path (the marker of a canonical url, the same-origin rule, a publisher url)
// never split the text again. The scheme is always https and there is no port,
// userinfo, query or fragment.
type URL struct {
	Host        string // lower-case labels joined by dots
	Path        string // starts with /; for a template, {name} as written
	Placeholder int    // for a template, the offset of {name} in Path; otherwise -1
}

// ParsePublicHTTPSURL is [PublicHTTPSURL] that returns the parts of an accepted
// URL.
func ParsePublicHTTPSURL(s string) (URL, error) {
	u, p := readURL(s, defaults(false))
	return u, asError(p)
}

// ParsePublicHTTPSURLTemplate is [PublicHTTPSURLTemplate] that returns the parts
// of an accepted URL.
func ParsePublicHTTPSURLTemplate(s string) (URL, error) {
	u, p := readURL(s, defaults(true))
	return u, asError(p)
}

// PublicHTTPSURLTemplate is [PublicHTTPSURL] for a template: the literal
// {name} must appear exactly once, in the path.
func PublicHTTPSURLTemplate(s string) error {
	_, err := ParsePublicHTTPSURLTemplate(s)
	return err
}

// GlobalDatabaseID reports why s is not a global database identity, or nil: the canonical url of a database as the Directory takes it since it
// published global identities (globalDatabaseIdProblem in urls.mjs, 574a7ad and d089fa8). It is a public https URL under every rule of [PublicHTTPSURL]
// (a trailing slash is fine, a path of one segment or of several), with two differences. A path segment may hold percent escapes, when the segment is
// the canonical encoding of its text (encodeURIComponent, and the five characters ! ' ( ) * encoded too, in upper case; see [EncodePathSegment]) and the
// text, decoded again and again, never becomes ".", "..", a slash, a backslash or a control character. And a host under .example is allowed.
func GlobalDatabaseID(s string) error {
	_, err := ParseGlobalDatabaseID(s)
	return err
}

// ParseGlobalDatabaseID is [GlobalDatabaseID] that returns the parts of an accepted identity.
func ParseGlobalDatabaseID(s string) (URL, error) {
	u, p := readURL(s, options{maxLen: MaxURLLength, encoded: true, example: true})
	return u, asError(p)
}

// Homepage reports why s is not a manifest's homepage: a public https URL of at
// most 200 bytes.
func Homepage(s string) error {
	return asError(checkHomepage(s, defaults(false)))
}

func checkHomepage(s string, o options) *Problem {
	if len(s) > MaxHomepageLength {
		return problem(RuleHomepageLength, "is longer than %d characters", MaxHomepageLength)
	}
	return checkURL(s, o)
}

// asError keeps a nil *Problem from becoming a non-nil error.
func asError(p *Problem) error {
	if p == nil {
		return nil
	}
	return p
}

const scheme = "https://"

// reservedLabels are the last labels of names that are never public: local,
// internal and reserved naming zones. A host is refused when its last label is
// one of them or when its last two labels are home.arpa.
var reservedLabels = map[string]bool{
	"localhost": true, "local": true, "internal": true, "localdomain": true, "lan": true, "arpa": true,
	"intranet": true, "corp": true, "private": true, "svc": true, "home": true, "test": true,
	"example": true, "invalid": true, "onion": true,
}

// homeArpa is the two-label reserved suffix; every name under it is also under
// arpa, so the table above already refuses it, and it is kept for the message.
const homeArpa = "home.arpa"

// checkURL is [readURL] for a caller that wants only the verdict.
func checkURL(s string, o options) *Problem {
	_, p := readURL(s, o)
	return p
}

// readURL is the reader. It looks at each byte once, from the left, in three
// stretches: the scheme (a fixed prefix), the host up to the first slash, and
// the path to the end.
func readURL(s string, o options) (URL, *Problem) {
	if len(s) > o.maxLen {
		return noURL, problem(RuleLength, "is longer than %d characters", o.maxLen)
	}
	rest, ok := strings.CutPrefix(s, scheme)
	if !ok {
		return noURL, notHTTPS(s)
	}
	pathStart, p := scanHost(rest, o)
	if p != nil {
		return noURL, p
	}
	at, p := scanPath(rest, pathStart, o)
	if p != nil {
		return noURL, p
	}
	return URL{Host: rest[:pathStart], Path: rest[pathStart:], Placeholder: at}, nil
}

// notHTTPS says why s does not start with https://. It runs only on a refusal.
func notHTTPS(s string) *Problem {
	if name, _, found := strings.Cut(s, "://"); found && name != "" {
		return problem(RuleScheme, "must be https, not %s", show(name))
	}
	return problem(RuleNotURL, "is not a URL: write https://host/path")
}

// badByte classifies a byte that no rule allows where it stands.
func badByte(c byte, where string, plain Rule) *Problem {
	switch {
	case c <= ' ' || c == 0x7f || c == '\\' || c >= 0x80:
		return problem(RuleCharacter, "contains whitespace, a control character, a backslash or a non-ASCII character")
	case c == '@':
		return problem(RuleUserinfo, "must not contain credentials (userinfo)")
	case c == ':':
		return problem(RulePort, "must not name a port (not even :443): a published URL is reached on the default https port")
	case c == '?':
		return problem(RuleQuery, "must not contain a query")
	case c == '#':
		return problem(RuleFragment, "must not contain a fragment")
	case c == '%':
		return problem(RulePercent, "must not contain a percent escape (write the character itself, or leave it out)")
	}
	return problem(plain, "%s may not contain %s", where, show(string([]byte{c})))
}

// scanHost reads the host at the start of rest, up to the first slash, and
// returns the index of that slash. rest has no scheme.
func scanHost(rest string, o options) (int, *Problem) {
	var (
		labelStart = 0
		prevLabel  string
		lastLabel  string
		labels     = 0
		i          = 0
	)
	endLabel := func(end int) *Problem {
		label := rest[labelStart:end]
		if p := checkLabel(label, o); p != nil {
			return p
		}
		prevLabel, lastLabel = lastLabel, label
		labels++
		labelStart = end + 1
		return nil
	}
	for ; i < len(rest) && rest[i] != '/'; i++ {
		c := rest[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			// the byte of a label
		case c == '.':
			if p := endLabel(i); p != nil {
				return 0, p
			}
		case c >= 'A' && c <= 'Z':
			return 0, problem(RuleHostCase, "host must be lower-case")
		case c == '{' && o.template && strings.HasPrefix(rest[i:], placeholder):
			return 0, problem(RulePlaceholder, "must have %s in the path only, never in the host, userinfo or port", placeholder)
		default:
			return 0, badByte(c, "host", RuleHostCharacter)
		}
		if i >= MaxHostLength {
			return 0, problem(RuleHostLength, "host must be at most %d characters in all", MaxHostLength)
		}
	}
	if i == len(rest) {
		if i == 0 {
			return 0, problem(RuleNotURL, "is not a URL: the host is missing")
		}
		return 0, problem(RuleNoPath, "must have a path: write https://host/")
	}
	if p := endLabel(i); p != nil {
		return 0, p
	}
	if labels < 2 {
		return 0, problem(RuleHostSingleLabel, "host %s is a single-label name, not a public host", show(lastLabel))
	}
	if numericLabel(lastLabel) {
		return 0, problem(RuleHostNumeric, "host %s is an IP address, or could be read as one; a public mapping names a host", show(rest[:i]))
	}
	if reservedLabels[lastLabel] && (!o.example || lastLabel != "example") || prevLabel+"."+lastLabel == homeArpa {
		return 0, problem(RuleHostReserved, "host %s is a local, internal or reserved name, not a public host", show(rest[:i]))
	}
	return i, nil
}

// checkLabel checks one label of a host: 1 to 63 bytes, no hyphen at either
// end, and an xn-- label only when it is accepted punycode.
func checkLabel(label string, o options) *Problem {
	switch {
	case label == "":
		return problem(RuleHostLabel, "host has an empty label (a leading, doubled or trailing dot)")
	case len(label) > MaxLabelLength:
		return problem(RuleHostLabel, "host label %s is longer than %d characters", show(label), MaxLabelLength)
	case label[0] == '-' || label[len(label)-1] == '-':
		return problem(RuleHostLabel, "host label %s starts or ends with a hyphen", show(label))
	case !strings.HasPrefix(label, "xn--"):
		return nil
	}
	switch punycodeLabel(label, o.puny) {
	case punyInvalid:
		return problem(RuleHostPunycode, "host label %s is not valid punycode for a host name (or it decodes to text that begins with xn-- or has hyphens in positions 3 and 4); write the host name in ASCII letters, digits and hyphens", show(label))
	case punyForeign:
		return problem(RuleHostIDN, "host label %s is an internationalised name with letters that are not accepted yet: an xn-- label may only spell Latin-1 letters (accented ones such as e with an acute accent, u with a diaeresis, n with a tilde) with ASCII letters, digits and hyphens, and other scripts and letters are not accepted; use an ASCII host name", show(label))
	}
	return nil
}

// numericLabel reports whether label, as the last label of a host, makes a
// WHATWG URL parser read the whole host as an IPv4 address (or refuse it): all
// decimal digits, or 0x followed by any number of hex digits. This is the
// parser's "ends in a number" test, on lower-case ASCII.
func numericLabel(label string) bool {
	digits, hex := true, true
	body, prefixed := strings.CutPrefix(label, "0x")
	for i := 0; i < len(label); i++ {
		if c := label[i]; c < '0' || c > '9' {
			digits = false
		}
	}
	if prefixed {
		for i := 0; i < len(body); i++ {
			if c := body[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				hex = false
			}
		}
	}
	return digits || (prefixed && hex)
}

// scanPath reads the path of rest, which starts at the slash at index start. It
// returns the offset in the path of {name} (-1 when there is none).
func scanPath(rest string, start int, o options) (int, *Problem) {
	var (
		segLen       = 0
		dotsOnly     = true
		placeholders = 0
		offset       = -1
		segStart     = start + 1
		escaped      = false
	)
	endSegment := func(end int) *Problem {
		if segLen > 0 && dotsOnly && segLen <= 2 {
			return problem(RulePathDotSegment, "must not have a . or .. segment")
		}
		if escaped {
			if p := encodedSegmentProblem(rest[segStart:end]); p != nil {
				return p
			}
		}
		segLen, dotsOnly, escaped = 0, true, false
		return nil
	}
	for i := start + 1; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c == '/':
			if segLen == 0 {
				return 0, problem(RulePathEmptySeg, "has an empty path segment (//)")
			}
			if p := endSegment(i); p != nil {
				return 0, p
			}
			segStart = i + 1
		case c == '%' && o.encoded:
			segLen++
			dotsOnly = false
			escaped = true
		case c == '.':
			segLen++
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '~', c == '-':
			segLen++
			dotsOnly = false
		case c == '{' && o.template && strings.HasPrefix(rest[i:], placeholder):
			placeholders++
			offset = i - start
			if placeholders > 1 {
				return 0, problem(RulePlaceholder, "must contain %s exactly once", placeholder)
			}
			i += len(placeholder) - 1
			segLen += len("name")
			dotsOnly = false
		default:
			return 0, badByte(c, "path", RulePathCharacter)
		}
	}
	if p := endSegment(len(rest)); p != nil {
		return 0, p
	}
	if o.template && placeholders != 1 {
		return 0, problem(RulePlaceholder, "must contain %s exactly once", placeholder)
	}
	return offset, nil
}
