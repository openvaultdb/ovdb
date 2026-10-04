package manifest

import (
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// An address as the Directory spells it: modelspec://{host}/{org}/{repo}/{module}
// or meaning://{host}/{org}/{repo}, each with an optional ?ref= of 40 lower-case hex
// digits. The host is [A-Za-z0-9.-]+, org and repo [A-Za-z0-9._-]+, the module a
// letter or _ and then letters, digits and _. Whether the host is one the
// Directory reads is not decided here (the Directory decides it later).
type address struct {
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
func parseAddress(s, prefix string, withModule bool) (address, bool) {
	if len(s) > rules.MaxURLLength {
		return address{}, false
	}
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return address{}, false
	}
	var out address
	if at := strings.Index(rest, "?ref="); at >= 0 {
		out.Ref = rest[at+len("?ref="):]
		rest = rest[:at]
		if !rules.IsCommit(out.Ref) {
			return address{}, false
		}
	}
	parts := strings.Split(rest, "/")
	want := 3
	if withModule {
		want = 4
	}
	if len(parts) != want || !allChars(parts[0], isHostChar) || !allChars(parts[1], isNameChar) || !allChars(parts[2], isNameChar) {
		return address{}, false
	}
	out.Repository = strings.Join(parts[:3], "/")
	if withModule {
		out.Module = parts[3]
		if !isModuleName(out.Module) {
			return address{}, false
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

func parseModelAddress(s string) (address, bool) { return parseAddress(s, "modelspec://", true) }
func parseGraphAddress(s string) (address, bool) { return parseAddress(s, "meaning://", false) }

// twoLabelSuffixes are public suffixes of two labels, where the registered name
// sits one label further left (ovdb.co.uk is a registered name under co.uk).
var twoLabelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true, "me.uk": true, "com.au": true, "net.au": true, "org.au": true,
	"co.nz": true, "co.jp": true, "co.in": true, "co.za": true, "com.br": true, "com.cn": true, "com.mx": true, "com.tr": true, "com.ar": true,
}

// hasOvdbMarker reports whether a canonical url has ovdb as a complete path
// segment or as a subdomain: a host label left of the registered name, which is
// the last two labels, or the last three under a two-label suffix such as co.uk.
// So ovdb.acme.com and x.ovdb.acme.co.uk count; ovdb.com and ovdb.co.uk do not.
func hasOvdbMarker(u rules.URL) bool {
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "ovdb" {
			return true
		}
	}
	labels := strings.Split(u.Host, ".")
	suffix := 1
	if n := len(labels); n >= 2 && twoLabelSuffixes[labels[n-2]+"."+labels[n-1]] {
		suffix = 2
	}
	end := len(labels) - suffix - 1
	if end < 0 { // the references slice with a negative end, which counts from the right
		end = max(end+len(labels), 0)
	}
	for _, label := range labels[:end] {
		if label == "ovdb" {
			return true
		}
	}
	return false
}
