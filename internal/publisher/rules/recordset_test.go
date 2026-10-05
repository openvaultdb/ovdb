package rules

import (
	"strings"
	"testing"
)

func TestRecordsetName(t *testing.T) {
	for name, want := range map[string]bool{
		"Album": true, "dbo.DatabaseLog": true, "Order Details": true, "é": true, "a.": true, "...": true,
		strings.Repeat("a", 256): true, strings.Repeat("a", 257): false,
		strings.Repeat("\U0001f600", 128): true, strings.Repeat("\U0001f600", 129): false,
		"": false, "  ": false, ".": false, "..": false, "a/b": false, "a\\b": false, "a\tb": false, "a\x7fb": false,
	} {
		if got := RecordsetName(name) == nil; got != want {
			t.Errorf("RecordsetName(%q) = %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"", ".", "a/b", strings.Repeat("a", 257)} {
		if msg := RecordsetName(name).Error(); !printableASCII(msg) {
			t.Errorf("the message for %q is not printable ASCII: %q", name, msg)
		}
	}
}

func TestEncodePathSegment(t *testing.T) {
	for in, want := range map[string]string{
		"Album": "Album", "a-b_c.d~e": "a-b_c.d~e", "Order Details": "Order%20Details", "a/b": "a%2Fb", "50%": "50%25",
		"!'()*": "%21%27%28%29%2A", "é": "%C3%A9", "\U0001f600": "%F0%9F%98%80", "": "",
	} {
		if got := EncodePathSegment(in); got != want {
			t.Errorf("EncodePathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordsetPage(t *testing.T) {
	const template = "https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}"
	cases := []struct {
		template, name string
		ok             bool
		why            string
	}{
		{template, "Album", true, "a plain name"},
		{template, "dbo.DatabaseLog", true, "a dotted name needs no encoding"},
		{template, "Order Details", true, "a space is encoded"},
		{template, "été", true, "a non-ASCII name is encoded"},
		{template + "/rows", "Order Details", true, "the segment is followed by more path"},
		{template, ".", false, "a dot segment, written plain"},
		{template, "..", false, "a dot segment, written plain"},
		{template, "a/b", false, "a slash is encoded and then refused"},
		{template, "a\\b", false, "a backslash"},
		{template, "a\tb", false, "a control character"},
		{template, "a\x7fb", false, "DEL"},
		{template, "%2e", false, "a name that reads, once decoded, as a dot"},
		{template, "%252e%252e", false, "an escape that decodes, in two steps, to .."},
		{template, "%252F", false, "an escape that decodes, in two steps, to a slash"},
		{template, "%2500", false, "an escape that decodes, in two steps, to a control character"},
		{template, "%25252e%25252e", false, "an escape that decodes, in three steps, to .."},
		{template, "%252e", false, "an escape that decodes, in two steps, to a dot (a name of .)"},
		{template, "%41%", true, "a lone percent stops the decoding: a router that throws never reaches a path separator"},
		{template, "%2", true, "a short escape stops the decoding"},
		{template, "%FF", true, "bytes that are not UTF-8 stop the decoding"},
		{template, "%252", true, "an escape of a percent and one digit"},
		{template, "%25C3", true, "an escape that decodes to bytes that are not a whole character"},
		{template, "%C3%A9", true, "a decodable pair of escapes"},
		{template, "%25C3%25A9", true, "decodes, in two steps, to a letter"},
		{"https://cloud.openvaultdb.com/c/{name}.html", "a b", false, "the segment is shared with the template"},
		{"https://cloud.openvaultdb.com/c/x{name}", "a b", false, "the segment is shared with the template"},
		{"https://cloud.openvaultdb.com/c/{name}x", "a b", false, "the segment is shared with the template"},
		{"https://{name}.openvaultdb.com/c", "a b", false, "{name} in the host"},
		{"https://{name}", "a b", false, "{name} is the whole host"},
		{"https://cloud.openvaultdb.com/{name}/{name}", "a b", false, "a second {name}"},
		{"https://cloud.openvaultdb.com/c/%41/{name}", "a b", false, "an escape of the template's own"},
		{"https://cloud.openvaultdb.com/c/{name}?x=1", "a b", false, "a query"},
		{"http://cloud.openvaultdb.com/c/{name}", "a b", false, "not https"},
		{"cloud.openvaultdb.com/c/{name}", "a b", false, "no scheme"},
		{"https://cloud.openvaultdb.com", "a b", false, "no placeholder, and a host without a path is not a URL here"},
		{"https://cloud.openvaultdb.com/c/", "a b", true, "no placeholder: the template is the URL"},
		{"https://cloud.openvaultdb.com/c/{name}", strings.Repeat("é", 400), true, "a long page: the Directory bounds the name, not the page"},
		{"https://cloud.openvaultdb.com/c/" + strings.Repeat("x", 2000) + "/{name}", strings.Repeat("€", 256), true, "a page of 2000 plus 2304 characters"},
	}
	for _, c := range cases {
		err := RecordsetPage(c.template, c.name)
		if (err == nil) != c.ok {
			t.Errorf("RecordsetPage(%q, %q) = %v, want ok %v (%s)", c.template, c.name, err, c.ok, c.why)
		}
		if err != nil && !printableASCII(err.Error()) {
			t.Errorf("the message for %q is not printable ASCII: %q", c.name, err)
		}
	}
}

func TestDecodeURIComponent(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "%41": "A", "%C3%A9": "é", "%25": "%", "a%20b": "a b", "é": "é"} {
		if got, ok := decodeURIComponent(in); !ok || got != want {
			t.Errorf("decodeURIComponent(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"%", "%4", "%zz", "%FF", "%C3", "%C0%80", "%ED%A0%80", "x%4"} {
		if _, ok := decodeURIComponent(in); ok {
			t.Errorf("decodeURIComponent(%q) succeeded", in)
		}
	}
}

func TestHexValue(t *testing.T) {
	for c, want := range map[byte]byte{'0': 0, '9': 9, 'a': 10, 'f': 15, 'A': 10, 'F': 15} {
		if got := hexValue(c); got != want {
			t.Errorf("hexValue(%q) = %d, want %d", c, got, want)
		}
	}
}

// A message quotes a name cut at a character, never inside one.
func TestQuoteNeverCutsInsideACharacter(t *testing.T) {
	got := Quote(strings.Repeat("€", 100))
	if strings.Contains(got, `\xe2`) || strings.Contains(got, `\x`) || !strings.HasSuffix(got, `"...`) {
		t.Errorf("Quote cut inside a character: %s", got)
	}
	if got := Quote(strings.Repeat("a", 100)); !strings.HasSuffix(got, `"...`) {
		t.Errorf("Quote of a long ASCII text: %s", got)
	}
}
