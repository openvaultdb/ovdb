package manifest

import (
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// Address is an address as the Directory spells it: modelspec://{host}/{org}/{repo}/{module}
// or meaning://{host}/{org}/{repo}, each with an optional ?ref= of 40 lower-case hex
// digits. The host is [A-Za-z0-9.-]+, org and repo [A-Za-z0-9._-]+, the module a
// letter or _ and then letters, digits and _. Whether the host is one the
// Directory reads is a separate rule (see addressProblem).
type Address struct {
	Text       string // as written
	Repository string // host/org/repo, as written
	Module     string // the model's module; "" for a graph address
	Ref        string // the pin; "" when there is none
}

func isHostChar(c byte) bool { return c == '.' || c == '-' || isAlnum(c) }
func isNameChar(c byte) bool { return c == '.' || c == '-' || c == '_' || isAlnum(c) }
func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// parseAddress reads an address with the scheme prefix ("modelspec://" or
// "meaning://") and the number of path segments after the host: 2 and a module for a
// model, 2 and none for a graph.
func parseAddress(s, prefix string, withModule bool) (Address, bool) {
	if len(s) > rules.MaxURLLength {
		return Address{}, false
	}
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return Address{}, false
	}
	out := Address{Text: s}
	if at := strings.Index(rest, "?ref="); at >= 0 {
		out.Ref = rest[at+len("?ref="):]
		rest = rest[:at]
		if !rules.IsCommit(out.Ref) {
			return Address{}, false
		}
	}
	parts := strings.Split(rest, "/")
	want := 3
	if withModule {
		want = 4
	}
	if len(parts) != want || !allChars(parts[0], isHostChar) || !allChars(parts[1], isNameChar) || !allChars(parts[2], isNameChar) {
		return Address{}, false
	}
	out.Repository = strings.Join(parts[:3], "/")
	if withModule {
		out.Module = parts[3]
		if !isModuleName(out.Module) {
			return Address{}, false
		}
	}
	return out, true
}

func allChars(s string, ok func(byte) bool) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

func isModuleName(s string) bool {
	if s == "" || s[0] != '_' && (s[0] < 'a' || s[0] > 'z') && (s[0] < 'A' || s[0] > 'Z') {
		return false
	}
	return allChars(s, func(c byte) bool { return c == '_' || isAlnum(c) })
}

func parseModelAddress(s string) (Address, bool) { return parseAddress(s, "modelspec://", true) }
func parseGraphAddress(s string) (Address, bool) { return parseAddress(s, "meaning://", false) }
