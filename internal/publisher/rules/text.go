package rules

// IsBlank reports whether s is empty or only white space, the way JavaScript's
// String.prototype.trim() sees it: a manifest field is "required" when
// `typeof v === 'string' && v.trim() !== ”`, and the references refuse a blank
// one. White space there is the ECMAScript WhiteSpace and LineTerminator
// characters: tab, line feed, vertical tab, form feed, carriage return, space,
// U+00A0, U+1680, U+2000 to U+200A, U+2028, U+2029, U+202F, U+205F, U+3000 and
// the byte order mark U+FEFF. It is not Go's: strings.TrimSpace strips U+0085
// (which JavaScript does not) and not U+FEFF (which JavaScript does), so a check
// of "is required" written with strings.TrimSpace would accept "\uFEFF" where
// both references refuse it. Use IsBlank for those checks.
//
// It reads up to the first character that is not white space and stops, so it
// needs no bound of its own; a byte that is not valid UTF-8 is not white space.
func IsBlank(s string) bool {
	for _, r := range s {
		if !jsWhiteSpace(r) {
			return false
		}
	}
	return true
}

func jsWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}
