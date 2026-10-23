package rules

import "strings"

// repositoryHosts are the hosts a repository may live on, each with the number
// of path segments that name a repository there. Adding a host is a reviewed
// change to this list.
var repositoryHosts = map[string]int{"github.com": 2}

// RepositoryKey returns the canonical key of the repository that s names, and
// whether s is one: an https URL on an allow-listed host with exactly the
// host's number of path segments, each of A-Z a-z 0-9 . _ - and none of them .
// or .., the last not ending in .git in any case; no trailing slash, port, user,
// query or fragment. The key is host/org/repo, as written.
func RepositoryKey(s string) (string, bool) {
	return repositoryKey(s, MaxRepositoryLength)
}

// repositoryKey is RepositoryKey with the bound as a parameter: the tests lift
// it to prove that a recorded difference from the references is the bound.
func repositoryKey(s string, limit int) (string, bool) {
	if len(s) > limit {
		return "", false
	}
	rest, ok := strings.CutPrefix(s, scheme)
	if !ok {
		return "", false
	}
	hostEnd := strings.IndexByte(rest, '/')
	if hostEnd < 0 {
		return "", false
	}
	want, allowed := repositoryHosts[rest[:hostEnd]]
	if !allowed {
		return "", false
	}
	segments, segStart := 0, hostEnd+1
	for i := segStart; i <= len(rest); i++ {
		if i < len(rest) && rest[i] != '/' {
			if !isLetter(rest[i]) && !isDigit(rest[i]) && rest[i] != '_' && rest[i] != '.' && rest[i] != '-' {
				return "", false
			}
			continue
		}
		seg := rest[segStart:i]
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
		segments++
		segStart = i + 1
	}
	if segments != want || hasSuffixFold(rest, ".git") {
		return "", false
	}
	return rest, true
}

// CompareKey is the key to compare two repositories by: [RepositoryKey] in
// lower case, since a host names one repository whatever the case it is written
// in. It returns false when s is not a repository.
func CompareKey(s string) (string, bool) {
	key, ok := RepositoryKey(s)
	if !ok {
		return "", false
	}
	return strings.ToLower(key), true // the key is ASCII, so this is ASCII case folding
}

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// IsRepositoryPath reports whether s is a path to a file inside a repository,
// as a manifest names one: relative (no leading /), only A-Z a-z 0-9 . _ / and
// -, no //, no . or .. segment, no trailing /, at most 1024 bytes. A backslash,
// a glob character and a space are not in the set, so none passes.
func IsRepositoryPath(s string) bool {
	return isRepositoryPath(s, MaxPathLength)
}

// isRepositoryPath is IsRepositoryPath with the bound as a parameter, for the
// same reason as repositoryKey.
func isRepositoryPath(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	segLen, dotsOnly := 0, true
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '/':
			if segLen == 0 || dotsOnly && segLen <= 2 {
				return false
			}
			segLen, dotsOnly = 0, true
		case c == '.':
			segLen++
		case isLetter(c) || isDigit(c) || c == '_' || c == '-':
			segLen++
			dotsOnly = false
		default:
			return false
		}
	}
	return segLen > 0 && (!dotsOnly || segLen > 2)
}

// IsPublishEntry reports whether s is an entry of an OVDB.md publish list: an
// explicit path, ./ followed by a repository path (the explicit form is what
// says the publisher meant a file of this repository and not a pattern).
func IsPublishEntry(s string) bool {
	rest, ok := strings.CutPrefix(s, "./")
	return ok && IsRepositoryPath(rest)
}
