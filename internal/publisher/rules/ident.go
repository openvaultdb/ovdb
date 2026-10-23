package rules

// IsID reports whether s is a database id: lower-case letters and digits in
// words joined by single hyphens, at most 80 bytes (^[a-z0-9]+(-[a-z0-9]+)*$).
func IsID(s string) bool {
	if s == "" || len(s) > MaxIDLength {
		return false
	}
	previousHyphen := true // a hyphen may not start the id, and may not follow another
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			previousHyphen = false
		case c == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

// IsCommit reports whether s is a full commit id: 40 lower-case hex digits.
func IsCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// IsEngine reports whether s is a deployment engine name: a letter, then up to
// 39 letters, digits and _ . + - (^[A-Za-z][A-Za-z0-9_.+-]{0,39}$).
func IsEngine(s string) bool {
	if s == "" || len(s) > MaxEngineLength || !isLetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; !isLetter(c) && !isDigit(c) && c != '_' && c != '.' && c != '+' && c != '-' {
			return false
		}
	}
	return true
}

// IsLicenceID reports whether s has the shape of an SPDX licence id: a letter
// or digit, then up to 63 letters, digits and . + - (^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$).
// It does not know which ids SPDX has assigned.
func IsLicenceID(s string) bool {
	if s == "" || len(s) > MaxLicenceLength || !isLetter(s[0]) && !isDigit(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; !isLetter(c) && !isDigit(c) && c != '.' && c != '+' && c != '-' {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// MaxLocalIDLength is the longest descriptor localId.
const MaxLocalIDLength = 40

// IsLocalID reports whether s is the localId of a database descriptor, as the Directory has it (localIdPattern): a lower-case letter, then up to 39
// lower-case letters, digits and hyphens (^[a-z][a-z0-9-]{0,39}$). Unlike IsID it allows a hyphen at the end and two in a row.
func IsLocalID(s string) bool {
	if s == "" || len(s) > MaxLocalIDLength || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; (c < 'a' || c > 'z') && !isDigit(c) && c != '-' {
			return false
		}
	}
	return true
}
