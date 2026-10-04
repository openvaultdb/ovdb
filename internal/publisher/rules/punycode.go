package rules

import "strings"

// Punycode (RFC 3492), only as far as the host rule needs it. A host label that
// starts with xn-- is accepted only when its text after the prefix is the
// canonical punycode of a label whose letters are Latin-1 lower-case letters
// and ASCII letters, digits and hyphens, with at least one Latin-1 letter.
//
// Why so little. The WHATWG parser that the references use runs full UTS #46
// processing on such a label: a label that is not valid punycode, or decodes to
// a code point that UTS #46 maps or disallows, makes the whole URL fail, and
// which code points those are changes with every Unicode release. Go has no
// copy of those tables and this package takes none. Latin-1 lower-case letters
// have been valid in every version, so a label made of them, and written the
// way an encoder writes it, is one the parser takes unchanged. Every other
// punycode label is refused (a recorded difference; see README.md).
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
	punyLatin1     punycodeMode = iota // the rule: canonical punycode of Latin-1 letters
	punyWellFormed                     // canonical punycode of any code points
	punyUnchecked                      // any xn-- label, as the references take it
)

// punycodeLabel reports whether label (which starts with xn--) is acceptable.
func punycodeLabel(label string, mode punycodeMode) bool {
	if mode == punyUnchecked {
		return true
	}
	body := strings.TrimPrefix(label, "xn--")
	decoded, ok := decodePunycode(body, mode == punyWellFormed)
	if !ok || len(decoded) == 0 {
		return false
	}
	if decoded[0] == '-' || decoded[len(decoded)-1] == '-' {
		return false
	}
	nonASCII := false
	for _, r := range decoded {
		switch {
		case r < 0x80:
		case mode == punyWellFormed || latin1Letter(r):
			nonASCII = true
		default:
			return false
		}
	}
	return nonASCII && encodePunycode(decoded) == body
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
// leading delimiter, an upper-case digit, a truncated number and a number so
// large that it can only be a code point beyond Latin-1 (unless anyCode).
func decodePunycode(s string, anyCode bool) ([]rune, bool) {
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
		if n > 0xFF && !anyCode || n > 0x10FFFF {
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

// encodePunycode is the RFC 3492 encoder. The decoder accepts some spellings
// that an encoder never writes; the label is accepted only if encoding what it
// decodes to gives the label back.
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
