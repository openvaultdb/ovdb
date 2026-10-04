package rules

import "strings"

// Punycode (RFC 3492), only as far as the host rule needs it: a decoder. A host
// label that starts with xn-- is accepted only when the text after the prefix
// is valid punycode that decodes to a label of ASCII letters, digits and hyphens
// and Latin-1 lower-case letters (U+00E0 to U+00FF without the division sign),
// with at least one Latin-1 letter, that does not begin or end with a hyphen and
// does not have hyphens in its third and fourth positions (which includes
// beginning with xn--: UTS #46 refuses such a label when hyphens are not
// checked, as the WHATWG parser does not check them).
//
// Why so little. The WHATWG parser that the references use runs full UTS #46
// processing on such a label: a code point that UTS #46 maps or disallows makes
// the whole URL fail, and which code points those are changes with every Unicode
// release. Go has no copy of those tables and this package takes none.
// Latin-1 lower-case letters have been valid in every version, so a label made
// of them is one the parser takes unchanged. Every other punycode label is
// refused (a recorded difference; see README.md). The Node version of the
// references accepts any xn-- label as written, even one that is not punycode,
// so no verdict here depends on what a Node version happens to do.
//
// There is no check that re-encoding the decoded label gives the label back. It
// was there once and decided nothing: no lower-case body of one to five bytes
// (every one was tried) decodes to a label that spells differently when
// encoded, so nothing that the host loop lets through has a second spelling.
// The encoder is kept in the tests, which build labels with it.
const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
	// punyLimit stops a hostile label from overflowing the arithmetic. A label
	// of at most 63 bytes that decodes to Latin-1 stays far below it.
	punyLimit = 1 << 40
)

// latin1Letter reports whether r is a lower-case letter of the Latin-1
// supplement: U+00E0 to U+00FF without the division sign.
func latin1Letter(r rune) bool { return r >= 0xE0 && r <= 0xFF && r != 0xF7 }

// punycodeMode says how much of a label's text is checked. The tests lift the
// check one step at a time to prove that a recorded difference from the
// references is exactly that step.
type punycodeMode int

const (
	punyLatin1      punycodeMode = iota // the rule
	punyAllowNested                     // without the rule on hyphens in positions 3 and 4
	punyWellFormed                      // valid punycode of any text
	punyUnchecked                       // any xn-- label, as the references take it
)

// punycodeVerdict is what the rule says about an xn-- label.
type punycodeVerdict int

const (
	punyAccepted punycodeVerdict = iota
	punyInvalid                  // not valid punycode for a host name
	punyForeign                  // valid, but with letters beyond Latin-1
)

// punycodeLabel judges label, which starts with xn--.
func punycodeLabel(label string, mode punycodeMode) punycodeVerdict {
	if mode == punyUnchecked {
		return punyAccepted
	}
	decoded, ok := decodePunycode(strings.TrimPrefix(label, "xn--"))
	if !ok || len(decoded) == 0 || decoded[0] == '-' || decoded[len(decoded)-1] == '-' {
		return punyInvalid
	}
	nonASCII, foreign := false, false
	for _, r := range decoded {
		if r >= 0x80 {
			nonASCII = true
			foreign = foreign || !latin1Letter(r)
		}
	}
	switch {
	case !nonASCII:
		return punyInvalid
	case mode == punyLatin1 && len(decoded) >= 4 && decoded[2] == '-' && decoded[3] == '-':
		return punyInvalid
	case foreign && mode != punyWellFormed:
		return punyForeign
	}
	return punyAccepted
}

func punyDigit(c byte) int {
	switch {
	case c >= 'a' && c <= 'z':
		return int(c - 'a')
	case c >= '0' && c <= '9':
		return int(c-'0') + 26
	}
	return -1
}

func punyThreshold(k, bias int) int {
	return min(max(k-bias, punyTMin), punyTMax)
}

func punyAdapt(delta, points int, first bool) int {
	if first {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / points
	k := 0
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}

// decodePunycode decodes the part of a label after xn--. It refuses a lone
// leading delimiter, a non-ASCII or upper-case character, a truncated number
// and a number so large that it is no code point.
func decodePunycode(s string) ([]rune, bool) {
	var out []rune
	pos := 0
	if cut := strings.LastIndexByte(s, '-'); cut >= 0 {
		if cut == 0 {
			return nil, false
		}
		for i := 0; i < cut; i++ {
			if s[i] >= 0x80 {
				return nil, false
			}
			out = append(out, rune(s[i]))
		}
		pos = cut + 1
	}
	n, i, bias := punyInitialN, 0, punyInitialBias
	for pos < len(s) {
		oldi, w := i, 1
		for k := punyBase; ; k += punyBase {
			if pos >= len(s) {
				return nil, false
			}
			digit := punyDigit(s[pos])
			pos++
			if digit < 0 {
				return nil, false
			}
			i += digit * w
			t := punyThreshold(k, bias)
			if digit < t {
				break
			}
			w *= punyBase - t
			if i > punyLimit || w > punyLimit {
				return nil, false
			}
		}
		points := len(out) + 1
		bias = punyAdapt(i-oldi, points, oldi == 0)
		n += i / points
		if n > 0x10FFFF {
			return nil, false
		}
		i %= points
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = rune(n)
		i++
	}
	return out, true
}
