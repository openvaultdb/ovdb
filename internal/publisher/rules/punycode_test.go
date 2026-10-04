package rules

import (
	"strings"
	"testing"
)

// encodePunycode is the RFC 3492 encoder. The package has only the decoder; the
// tests build labels with this one and decode them back.
func encodePunycode(input []rune) string {
	var out []byte
	for _, r := range input {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	n, delta, bias := punyInitialN, 0, punyInitialBias
	for handled < len(input) {
		next := 0x110000
		for _, r := range input {
			if int(r) >= n && int(r) < next {
				next = int(r)
			}
		}
		delta += (next - n) * (handled + 1)
		n = next
		for _, r := range input {
			if int(r) < n {
				delta++
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := punyBase; ; k += punyBase {
				t := punyThreshold(k, bias)
				if q < t {
					break
				}
				out = append(out, punyChar(t+(q-t)%(punyBase-t)))
				q = (q - t) / (punyBase - t)
			}
			out = append(out, punyChar(q))
			bias = punyAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return string(out)
}

func punyChar(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

func TestPunycodeRoundTrip(t *testing.T) {
	for _, text := range []string{"bücher", "münchen", "ñandú", "çà", "ÿ", "à-é", "a-ü-b", "ü1", "1ü", "ǆ", "ω", "я", "日本語", "ａ", "a\u0308", "aé", "éa", strings.Repeat("é", 30)} {
		encoded := encodePunycode([]rune(text))
		decoded, ok := decodePunycode(encoded)
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

func label(text string) string { return "xn--" + encodePunycode([]rune(text)) }

func TestPunycodeLabel(t *testing.T) {
	for _, l := range []string{"xn--bcher-kva", "xn--mnchen-3ya", "xn--tda", "xn--ida", "xn--9ca", "xn--a-eha", label("a-ü"), label("ab-ü"), label("ü-a"), label("a--ü")} {
		if got := punycodeLabel(l, punyLatin1); got != punyAccepted {
			t.Errorf("%s is %d", l, got)
		}
	}
	invalid := []string{
		"xn--",               // nothing after the prefix
		"xn---",              // a lone leading delimiter
		"xn--a",              // U+0080 alone: no Latin-1 letter
		"xn--abc-",           // ASCII only
		"xn--bcher-kv",       // a truncated number
		"xn--BCHER-KVA",      // upper-case digits are not punycode digits here
		"xn--zzzzzzzzzz",     // a number too large to be anything
		"xn--bch\u00e9r-kva", // a non-ASCII byte among the basic code points
		"xn----eha",          // decodes to -ü: starts with a hyphen
		"xn----dha",          // decodes to ü-: ends with a hyphen
		label("xn--ü"),       // decodes to text that begins with xn--
		label("ab--ü"),       // hyphens in positions 3 and 4
		label("ab--c-ü"),     // the same, inside
		label("-ü"),          // starts with a hyphen
	}
	for _, l := range invalid {
		if got := punycodeLabel(l, punyLatin1); got == punyAccepted {
			t.Errorf("%s is accepted", l)
		}
	}
	for _, l := range []string{"xn--", "xn---", "xn--abc-", "xn--bcher-kv", "xn----eha", label("xn--ü"), label("ab--ü")} {
		if got := punycodeLabel(l, punyLatin1); got != punyInvalid {
			t.Errorf("%s is %d, want invalid", l, got)
		}
	}
	// Valid punycode of letters beyond Latin-1 is foreign, not invalid.
	for _, l := range []string{"xn--80ak6aa92e", "xn--fiq228c", label("ł"), label("č"), label("aß"), label("ǆ")} {
		if got := punycodeLabel(l, punyLatin1); got != punyForeign {
			t.Errorf("%s is %d, want foreign", l, got)
		}
	}
	// The modes lift one step each.
	if punycodeLabel(label("xn--ü"), punyAllowNested) != punyAccepted || punycodeLabel(label("xn--ł"), punyAllowNested) != punyForeign {
		t.Error("punyAllowNested does not lift exactly the hyphen rule")
	}
	if punycodeLabel("xn--80ak6aa92e", punyWellFormed) != punyAccepted || punycodeLabel(label("xn--ł"), punyWellFormed) != punyAccepted || punycodeLabel("xn--bcher-kv", punyWellFormed) != punyInvalid {
		t.Error("punyWellFormed does not accept exactly the valid labels of any text")
	}
	if punycodeLabel("xn--", punyUnchecked) != punyAccepted || punycodeLabel("xn--bcher-kv", punyUnchecked) != punyAccepted {
		t.Error("punyUnchecked refuses a label")
	}
}

func TestPunycodeDecodeRefusals(t *testing.T) {
	for _, s := range []string{"-a", "a\x80-kva", "zzzzzzzzzzzz", strings.Repeat("9", 20), "A", "kv", "!"} {
		if got, ok := decodePunycode(s); ok {
			t.Errorf("decodePunycode(%q) = %q, want a refusal", s, string(got))
		}
	}
	// A code point beyond Unicode.
	var beyond bool
	for _, s := range []string{"zzzzzz", "9zzzz", "99zzzz", "9999zzz", "99999zzz"} {
		if _, ok := decodePunycode(s); !ok {
			beyond = true
		}
	}
	if !beyond {
		t.Error("no probe reached the Unicode limit")
	}
	if _, ok := decodePunycode("tda"); !ok {
		t.Error("ü is refused")
	}
	if got, ok := decodePunycode("80ak6aa92e"); !ok || len(got) != 5 {
		t.Errorf("Cyrillic decodes to %q, %v", string(got), ok)
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
