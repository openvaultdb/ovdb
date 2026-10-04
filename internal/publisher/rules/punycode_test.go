package rules

import (
	"strings"
	"testing"
)

func TestPunycodeRoundTrip(t *testing.T) {
	for _, text := range []string{"bücher", "münchen", "ñandú", "çà", "ÿ", "à-é", "a-ü-b", "ü1", "1ü", "ǆ", "ω", "я", "日本語", "ａ", "ä", "aé", "éa", strings.Repeat("é", 30)} {
		runes := []rune(text)
		encoded := encodePunycode(runes)
		decoded, ok := decodePunycode(encoded, true)
		if !ok || string(decoded) != text {
			t.Errorf("%q encodes to %q, which decodes to %q, %v", text, encoded, string(decoded), ok)
		}
	}
	// The values of RFC 3492 section 7.1 that fit a Latin-1 label.
	for text, want := range map[string]string{"bücher": "bcher-kva", "münchen": "mnchen-3ya", "ü": "tda"} {
		if got := encodePunycode([]rune(text)); got != want {
			t.Errorf("encodePunycode(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestPunycodeLabel(t *testing.T) {
	for _, label := range []string{"xn--bcher-kva", "xn--mnchen-3ya", "xn--tda", "xn--ida", "xn--9ca", "xn--a-eha"} {
		if !punycodeLabel(label, punyLatin1) {
			t.Errorf("%s is refused", label)
		}
	}
	for _, label := range []string{
		"xn--",               // nothing after the prefix
		"xn---",              // a lone leading delimiter
		"xn--a",              // U+0080, not a letter
		"xn--abc-",           // ASCII only
		"xn--bcher-kv",       // a truncated number
		"xn--BCHER-KVA",      // upper-case digits are not punycode digits here
		"xn--80ak6aa92e",     // Cyrillic
		"xn--fiq228c",        // CJK
		"xn--zzzzzzzzzz",     // a number too large to be anything
		"xn--bch\u00e9r-kva", // a non-ASCII byte among the basic code points
		"xn----eha",          // decodes to -ü: starts with a hyphen
		"xn----dha",          // decodes to ü-: ends with a hyphen
	} {
		if punycodeLabel(label, punyLatin1) {
			t.Errorf("%s is accepted", label)
		}
	}
	if !punycodeLabel("xn--80ak6aa92e", punyWellFormed) || punycodeLabel("xn--bcher-kv", punyWellFormed) {
		t.Error("punyWellFormed does not accept exactly the canonical labels of any text")
	}
	if !punycodeLabel("xn--", punyUnchecked) || !punycodeLabel("xn--bcher-kv", punyUnchecked) {
		t.Error("punyUnchecked refuses a label")
	}
}

func TestPunycodeDecodeRefusals(t *testing.T) {
	for _, s := range []string{"-a", "a\x80-kva", "zzzzzzzzzzzz", strings.Repeat("9", 20), "A", "kv", "!"} {
		if got, ok := decodePunycode(s, true); ok {
			t.Errorf("decodePunycode(%q) = %q, want a refusal", s, string(got))
		}
	}
	// A code point beyond Unicode, reached only when code points are not limited to Latin-1.
	var found bool
	for _, s := range []string{"zzzzz", "zzzzzz", "zzzzzzz", "9zzzz", "99zzzz", "9999zzz"} {
		if _, ok := decodePunycode(s, true); !ok {
			found = true
		}
	}
	if !found {
		t.Error("no probe reached the Unicode limit")
	}
	if _, ok := decodePunycode("tda", false); !ok {
		t.Error("ü is refused")
	}
	if _, ok := decodePunycode("80ak6aa92e", false); ok {
		t.Error("Cyrillic is accepted as Latin-1")
	}
}

func TestPunycodeCharacters(t *testing.T) {
	for d, want := range map[int]byte{0: 'a', 25: 'z', 26: '0', 35: '9'} {
		if got := punyChar(d); got != want {
			t.Errorf("punyChar(%d) = %c", d, got)
		}
		if got := punyDigit(want); got != d {
			t.Errorf("punyDigit(%c) = %d", want, got)
		}
	}
	for _, c := range []byte{'A', '-', '/', '{', 0x80} {
		if punyDigit(c) != -1 {
			t.Errorf("punyDigit(%q) is a digit", c)
		}
	}
	for r, want := range map[rune]bool{0xDF: false, 0xE0: true, 0xF6: true, 0xF7: false, 0xF8: true, 0xFF: true, 0x100: false, 'a': false} {
		if latin1Letter(r) != want {
			t.Errorf("latin1Letter(%U) = %v", r, !want)
		}
	}
}
