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

// IsRepresentationPath reports whether s is the path of a representation_contract envelope, as the Directory has it (representationPath and the .json
// suffix of checkRepresentationEnvelope): at most 1024 bytes of A-Z a-z 0-9 _ . - in segments joined by single slashes, a segment being "$records" or a
// run of those characters, not "." or ".." or ".git" in any case, and the path ending in .json. The Directory's own regular expression takes nothing
// else, so a segment with a $ other than "$records" and a non-ASCII byte are refused.
func IsRepresentationPath(s string) bool {
	if len(s) == 0 || len(s) > 1024 || len(s) < len(".json") || s[len(s)-len(".json"):] != ".json" {
		return false
	}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i < len(s) && s[i] != '/' {
			continue
		}
		segment := s[start:i]
		start = i + 1
		if segment == "$records" {
			continue
		}
		if segment == "" || segment == "." || segment == ".." || equalFoldASCII(segment, ".git") {
			return false
		}
		for j := 0; j < len(segment); j++ {
			if c := segment[j]; !(isLetter(c) || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
				return false
			}
		}
	}
	return true
}

// IsRepresentationHash reports whether s is the sha256 of a representation_contract envelope: 64 lower-case hex digits.
func IsRepresentationHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func equalFoldASCII(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}
