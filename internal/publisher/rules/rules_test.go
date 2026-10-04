package rules

import (
	"errors"
	"strings"
	"testing"
)

const host = "e.openvaultdb.com"

func ruleOf(t *testing.T, err error) Rule {
	t.Helper()
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("error %v is not a *Problem", err)
	}
	if p.Error() != p.Detail || p.Detail == "" {
		t.Fatalf("Problem %+v has no detail", p)
	}
	return p.Rule
}

func TestPublicHTTPSURL(t *testing.T) {
	accepted := []string{
		"https://example.org/x", "https://cloud.openvaultdb.com/ovdb/dbs/chinook", "https://ovdb.acme.com/sales",
		"https://" + host + "/", "https://" + host + "/a/", "https://" + host + "/A/b_c~d.e/f-g", "https://" + host + "/.well-known/openvaultdb",
		"https://a-b.c0.openvaultdb.com/x", "https://0a.openvaultdb.com/x", "https://a.0x.com/x", "https://a.1e3/x", "https://a.0xg/x", "https://a.a1/x",
		"https://xn--bcher-kva.de/ovdb", "https://xn--mnchen-3ya.de/x", "https://a.xn--bcher-kva.de/x",
		"https://" + strings.Repeat("a", 63) + ".com/x", "https://" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + "/x",
		"https://e.openvaultdb.com/...", "https://e.openvaultdb.com/.a", "https://e.openvaultdb.com/a.", "https://x.localx/y", "https://x.arpa.com/y", "https://home.arpa.com/y",
		"https://e.openvaultdb.com/" + strings.Repeat("a", MaxURLLength-len("https://e.openvaultdb.com/")),
	}
	for _, s := range accepted {
		if err := PublicHTTPSURL(s); err != nil {
			t.Errorf("PublicHTTPSURL(%s) = %v, want nil", shorten(s), err)
		}
	}
	refused := []struct {
		s    string
		rule Rule
	}{
		{"", RuleNotURL}, {"example.org/x", RuleNotURL}, {"//" + host + "/x", RuleNotURL}, {"https:/" + host + "/x", RuleNotURL}, {"https:" + host + "/x", RuleNotURL}, {"https://", RuleNotURL}, {"://x.com/", RuleNotURL},
		{"http://" + host + "/x", RuleScheme}, {"ftp://" + host + "/x", RuleScheme}, {"file:///etc/passwd", RuleScheme}, {"HTTPS://" + host + "/x", RuleScheme},
		{"https://" + host, RuleNoPath}, {"https://" + host + "/ ", RuleCharacter}, {" https://" + host + "/", RuleScheme}, {"https://" + host + "/\n", RuleCharacter},
		{"https://" + host + "\\@evil.com/x", RuleCharacter}, {"https://" + host + "/a\\b", RuleCharacter}, {"https://" + host + "/\u00e9", RuleCharacter}, {"https://" + host + "/\x00", RuleCharacter}, {"https://" + host + "/\x7f", RuleCharacter}, {"https://b\u00fccher.example.org/x", RuleCharacter},
		{"https://u@" + host + "/x", RuleUserinfo}, {"https://u:p@" + host + "/x", RulePort}, {"https://@" + host + "/x", RuleUserinfo}, {"https://" + host + "/a@b", RuleUserinfo},
		{"https://" + host + ":443/x", RulePort}, {"https://" + host + ":8443/x", RulePort}, {"https://" + host + ":/x", RulePort}, {"https://" + host + ":443", RulePort}, {"https://" + host + "/a:b", RulePort},
		{"https://" + host + "/x?", RuleQuery}, {"https://" + host + "/x?a=1", RuleQuery}, {"https://" + host + "?a", RuleQuery},
		{"https://" + host + "/x#", RuleFragment}, {"https://" + host + "#a", RuleFragment},
		{"https://" + host + "/a%2fb", RulePercent}, {"https://" + host + "/%2e%2e/x", RulePercent}, {"https://e%65.openvaultdb.com/x", RulePercent},
		{"https://E." + "openvaultdb.com/x", RuleHostCase}, {"https://e.openvaultdb.COM/x", RuleHostCase},
		{"https://e_e.openvaultdb.com/x", RuleHostCharacter}, {"https://e\"e.openvaultdb.com/x", RuleHostCharacter}, {"https://[::1]/x", RuleHostCharacter}, {"https://e e.openvaultdb.com/x", RuleCharacter}, {"https://{name}.openvaultdb.com/x", RuleHostCharacter},
		{"https://e.openvaultdb.com./x", RuleHostLabel}, {"https://.e.openvaultdb.com/x", RuleHostLabel}, {"https://e..openvaultdb.com/x", RuleHostLabel}, {"https:///x", RuleHostLabel},
		{"https://-e.openvaultdb.com/x", RuleHostLabel}, {"https://e-.openvaultdb.com/x", RuleHostLabel}, {"https://e.openvaultdb-/x", RuleHostLabel}, {"https://" + strings.Repeat("a", 64) + ".com/x", RuleHostLabel},
		{"https://" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) + "/x", RuleHostLength},
		{"https://" + strings.Repeat("a.", 140) + "com/x", RuleHostLength},
		{"https://localhost/x", RuleHostSingleLabel}, {"https://nas/x", RuleHostSingleLabel}, {"https://a/x", RuleHostSingleLabel},
		{"https://127.0.0.1/x", RuleHostNumeric}, {"https://127.1/x", RuleHostNumeric}, {"https://0x7f.1/x", RuleHostNumeric}, {"https://2130706433/x", RuleHostSingleLabel}, {"https://a.2130706433/x", RuleHostNumeric}, {"https://a.1/x", RuleHostNumeric}, {"https://a.00/x", RuleHostNumeric},
		{"https://a.0x/x", RuleHostNumeric}, {"https://a.0xff/x", RuleHostNumeric}, {"https://a.08/x", RuleHostNumeric},
		{"https://x.localhost/x", RuleHostReserved}, {"https://printer.local/x", RuleHostReserved}, {"https://x.internal/x", RuleHostReserved}, {"https://x.localdomain/x", RuleHostReserved}, {"https://x.lan/x", RuleHostReserved},
		{"https://box.home.arpa/x", RuleHostReserved}, {"https://home.arpa/x", RuleHostReserved}, {"https://1.0.0.127.in-addr.arpa/x", RuleHostReserved}, {"https://x.intranet/x", RuleHostReserved}, {"https://x.corp/x", RuleHostReserved},
		{"https://x.private/x", RuleHostReserved}, {"https://x.svc/x", RuleHostReserved}, {"https://x.home/x", RuleHostReserved}, {"https://x.test/x", RuleHostReserved}, {"https://x.example/x", RuleHostReserved},
		{"https://x.invalid/x", RuleHostReserved}, {"https://x.onion/x", RuleHostReserved},
		{"https://xn--/x", RuleHostLabel}, {"https://a.xn--/x", RuleHostLabel}, {"https://xn--a.openvaultdb.com/x", RuleHostPunycode}, {"https://xn--80ak6aa92e.com/x", RuleHostPunycode}, {"https://xn--bcher-kvb.de/x", RuleHostPunycode},
		{"https://" + host + "/a/./b", RulePathDotSegment}, {"https://" + host + "/a/../b", RulePathDotSegment}, {"https://" + host + "/.", RulePathDotSegment}, {"https://" + host + "/..", RulePathDotSegment}, {"https://" + host + "/a/.", RulePathDotSegment},
		{"https://" + host + "/a//b", RulePathEmptySeg}, {"https://" + host + "//a", RulePathEmptySeg}, {"https://" + host + "//", RulePathEmptySeg},
		{"https://" + host + "/a b", RuleCharacter}, {"https://" + host + "/a\"b", RulePathCharacter}, {"https://" + host + "/a'b", RulePathCharacter}, {"https://" + host + "/a&b", RulePathCharacter}, {"https://" + host + "/{name}", RulePathCharacter},
		{"https://" + host + "/a{b", RulePathCharacter}, {"https://" + host + "/a,b", RulePathCharacter},
		{"https://" + host + "/" + strings.Repeat("a", MaxURLLength-len("https://e.openvaultdb.com/")+1), RuleLength},
	}
	for _, c := range refused {
		err := PublicHTTPSURL(c.s)
		if err == nil {
			t.Errorf("PublicHTTPSURL(%s) = nil, want %s", shorten(c.s), c.rule)
			continue
		}
		if got := ruleOf(t, err); got != c.rule {
			t.Errorf("PublicHTTPSURL(%s) is refused as %s (%v), want %s", shorten(c.s), got, err, c.rule)
		}
	}
}

func TestPublicHTTPSURLTemplate(t *testing.T) {
	for _, s := range []string{
		"https://" + host + "/{name}", "https://" + host + "/a/{name}/b", "https://" + host + "/a{name}b", "https://" + host + "/{name}.", "https://" + host + "/.{name}", "https://" + host + "/..{name}", "https://" + host + "/{name}/",
	} {
		if err := PublicHTTPSURLTemplate(s); err != nil {
			t.Errorf("PublicHTTPSURLTemplate(%q) = %v, want nil", s, err)
		}
	}
	for _, c := range []struct {
		s    string
		rule Rule
	}{
		{"https://" + host + "/x", RulePlaceholder}, {"https://" + host + "/{name}{name}", RulePlaceholder}, {"https://" + host + "/{name}/{name}", RulePlaceholder},
		{"https://{name}.openvaultdb.com/x", RulePlaceholder}, {"https://e.{name}/x", RulePlaceholder}, {"https://e.openvaultdb.com{name}/x", RulePlaceholder},
		{"https://" + host + "/{name", RulePathCharacter}, {"https://" + host + "/name}", RulePathCharacter}, {"https://" + host + "/{Name}", RulePathCharacter}, {"https://" + host + "/{}", RulePathCharacter},
		{"https://" + host + ":{name}/x", RulePort}, {"https://{name}@" + host + "/x", RulePlaceholder}, {"https://" + host + "/{name}/..", RulePathDotSegment}, {"https://" + host + "//{name}", RulePathEmptySeg},
		{"https://" + host, RuleNoPath}, {"https://169.254.169.{name}/latest", RulePlaceholder}, {"https://metadata.google.{name}/x", RulePlaceholder},
	} {
		err := PublicHTTPSURLTemplate(c.s)
		if err == nil {
			t.Errorf("PublicHTTPSURLTemplate(%q) = nil, want %s", c.s, c.rule)
			continue
		}
		if got := ruleOf(t, err); got != c.rule {
			t.Errorf("PublicHTTPSURLTemplate(%q) is refused as %s (%v), want %s", c.s, got, err, c.rule)
		}
	}
	// Without a template, the same placeholder is plain brace text and refused.
	if err := PublicHTTPSURL("https://" + host + "/{name}"); err == nil {
		t.Error("PublicHTTPSURL accepts {name}")
	}
}

func TestHomepage(t *testing.T) {
	prefix := "https://" + host + "/"
	for _, n := range []int{199, 200} {
		if err := Homepage(prefix + strings.Repeat("a", n-len(prefix))); err != nil {
			t.Errorf("Homepage of %d bytes = %v", n, err)
		}
	}
	if got := ruleOf(t, Homepage(prefix+strings.Repeat("a", 201-len(prefix)))); got != RuleHomepageLength {
		t.Errorf("Homepage of 201 bytes is refused as %s", got)
	}
	if got := ruleOf(t, Homepage("http://"+host+"/")); got != RuleScheme {
		t.Errorf("Homepage over http is refused as %s", got)
	}
	if err := Homepage("https://example.com/~me/Chinook-1.0/"); err != nil {
		t.Errorf("Homepage = %v", err)
	}
}

func TestProblemIsNeverATypedNil(t *testing.T) {
	err := asError(nil)
	if err != nil {
		t.Fatalf("asError(nil) = %#v", err)
	}
	if asError(problem(RuleLength, "x")) == nil {
		t.Fatal("asError dropped a problem")
	}
}

func TestBadByteNamesTheRule(t *testing.T) {
	for _, c := range []struct {
		b    byte
		rule Rule
	}{
		{' ', RuleCharacter}, {0, RuleCharacter}, {0x7f, RuleCharacter}, {'\\', RuleCharacter}, {0x80, RuleCharacter}, {0xff, RuleCharacter},
		{'@', RuleUserinfo}, {':', RulePort}, {'?', RuleQuery}, {'#', RuleFragment}, {'%', RulePercent}, {'"', RulePathCharacter},
	} {
		if got := badByte(c.b, "path", RulePathCharacter).Rule; got != c.rule {
			t.Errorf("badByte(%#x) = %s, want %s", c.b, got, c.rule)
		}
	}
}

func TestNumericLabel(t *testing.T) {
	for label, want := range map[string]bool{
		"0": true, "1": true, "00": true, "08": true, "4294967296": true, "0x": true, "0x0": true, "0xff": true, "0xfg": false, "0xg": false,
		"x0": false, "0a": false, "a0": false, "1e3": false, "ff": false, "0b1": false, "0o7": false, "a": false, "xn--a": false,
	} {
		if got := numericLabel(label); got != want {
			t.Errorf("numericLabel(%q) = %v, want %v", label, got, want)
		}
	}
}

func TestIsID(t *testing.T) {
	for s, want := range map[string]bool{
		"chinook": true, "chinook-acme": true, "a1": true, "9lives": true, "a": true, "0": true, "a-b-c": true,
		"": false, "-": false, "a-": false, "-a": false, "a--b": false, "A": false, "a_b": false, "a b": false, "a.b": false, "a\n": false, "é": false,
		strings.Repeat("a", 80): true, strings.Repeat("a", 81): false, "a" + strings.Repeat("-a", 39): true, "a" + strings.Repeat("-a", 40): false,
	} {
		if got := IsID(s); got != want {
			t.Errorf("IsID(%s) = %v, want %v", shorten(s), got, want)
		}
	}
}

func TestIsCommit(t *testing.T) {
	for s, want := range map[string]bool{
		strings.Repeat("a", 40): true, "0123456789abcdef0123456789abcdef01234567": true,
		"": false, strings.Repeat("a", 39): false, strings.Repeat("a", 41): false, strings.Repeat("A", 40): false, strings.Repeat("g", 40): false,
		strings.Repeat("a", 39) + "\n": false, "main": false, strings.Repeat("a", 39) + "é": false,
	} {
		if got := IsCommit(s); got != want {
			t.Errorf("IsCommit(%s) = %v, want %v", shorten(s), got, want)
		}
	}
}

func TestIsEngineAndLicence(t *testing.T) {
	for s, want := range map[string]bool{
		"sqlite": true, "postgres": true, "SQLite3": true, "my.engine+x_1-2": true, "a": true, strings.Repeat("a", 40): true,
		"": false, "9db": false, "-x": false, "x/y": false, "Cloudflare Workers": false, strings.Repeat("a", 41): false, "x\n": false, "é": false, "xé": false,
	} {
		if got := IsEngine(s); got != want {
			t.Errorf("IsEngine(%s) = %v, want %v", shorten(s), got, want)
		}
	}
	for s, want := range map[string]bool{
		"MIT": true, "CC0-1.0": true, "Apache-2.0": true, "GPL-3.0-only+": true, "0BSD": true, "1": true, strings.Repeat("a", 64): true, "1" + strings.Repeat("a", 63): true,
		"": false, "-": false, "+": false, ".": false, "see the README": false, "MIT OR Apache-2.0": false, "x_y": false, strings.Repeat("a", 65): false, "é": false, "aé": false,
	} {
		if got := IsLicenceID(s); got != want {
			t.Errorf("IsLicenceID(%s) = %v, want %v", shorten(s), got, want)
		}
	}
}

func TestRepositoryKey(t *testing.T) {
	for s, want := range map[string]string{
		"https://github.com/datatug/chinookdb": "github.com/datatug/chinookdb", "https://github.com/DataTug/ChinookDB": "github.com/DataTug/ChinookDB",
		"https://github.com/a-b/c.d_e": "github.com/a-b/c.d_e", "https://github.com/a/git": "github.com/a/git", "https://github.com/a/...": "github.com/a/...", "https://github.com/a/b.gitx": "github.com/a/b.gitx",
	} {
		if key, ok := RepositoryKey(s); !ok || key != want {
			t.Errorf("RepositoryKey(%q) = %q, %v, want %q", s, key, ok, want)
		}
	}
	for _, s := range []string{
		"", "github.com/a/b", "http://github.com/a/b", "HTTPS://github.com/a/b", "https://github.com", "https://github.com/", "https://github.com/a", "https://github.com/a/", "https://github.com/a/b/", "https://github.com//b", "https://github.com/a//b", "https://github.com/a/b/c",
		"https://github.com/a/b.git", "https://github.com/a/b.GIT", "https://github.com/a/.git", "https://github.com/./b", "https://github.com/a/.", "https://github.com/../b", "https://github.com/a/..",
		"https://www.github.com/a/b", "https://GitHub.com/a/b", "https://github.com:443/a/b", "https://user@github.com/a/b", "https://github.com/a/b?x=1", "https://github.com/a/b#x", "https://github.com/a/b c", "https://github.com/a/b%2e", "https://github.com/a/é",
		"https://gitlab.com/a/b", "https://127.0.0.1/a/b", "git@github.com:a/b.git", "https://github.com/a/b\n",
		"https://github.com/a/" + strings.Repeat("b", MaxRepositoryLength),
	} {
		if key, ok := RepositoryKey(s); ok {
			t.Errorf("RepositoryKey(%s) = %q, want a refusal", shorten(s), key)
		}
	}
	if _, ok := RepositoryKey("https://github.com/a/" + strings.Repeat("b", MaxRepositoryLength-len("https://github.com/a/"))); !ok {
		t.Error("RepositoryKey refuses a repository of exactly the bound")
	}
	if key, ok := CompareKey("https://github.com/DataTug/ChinookDB"); !ok || key != "github.com/datatug/chinookdb" {
		t.Errorf("CompareKey = %q, %v", key, ok)
	}
	if key, ok := CompareKey("https://github.com/DataTug"); ok || key != "" {
		t.Errorf("CompareKey of a non-repository = %q, %v", key, ok)
	}
}

func TestIsRepositoryPath(t *testing.T) {
	for s, want := range map[string]bool{
		"ovdb.yaml": true, "model/chinook.modelspec.hcl": true, "a/b/c.yaml": true, "a": true, ".a": true, "a.": true, "...": true, ".github/workflows/x.yml": true, "a/.hidden": true, "a/..a": true,
		"": false, "/": false, "/a": false, "a/": false, "a//b": false, "//a": false, ".": false, "..": false, "./a": false, "a/.": false, "a/./b": false, "a/..": false, "a/../b": false, "../a": false, "a/b/..": false,
		"x*.yaml": false, "a b": false, "a\\b": false, "C:\\a": false, "a:b": false, "~a": false, "a%2fb": false, "é.yaml": false, "a\nb": false,
		strings.Repeat("a", 1024): true, strings.Repeat("a", 1025): false, strings.Repeat("a/", 512): false, strings.Repeat("a/", 511) + "a": true,
	} {
		if got := IsRepositoryPath(s); got != want {
			t.Errorf("IsRepositoryPath(%s) = %v, want %v", shorten(s), got, want)
		}
	}
	for s, want := range map[string]bool{
		"./ovdb.yaml": true, "./model/x.hcl": true, "./.github/x": true,
		"ovdb.yaml": false, "": false, "./": false, "./.": false, "./..": false, "./a/": false, "././a": false, "/ovdb.yaml": false, ".//a": false, "./x*.yaml": false, ".\\a": false, " ./a": false,
	} {
		if got := IsPublishEntry(s); got != want {
			t.Errorf("IsPublishEntry(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestCompare(t *testing.T) {
	const base = "https://a.openvaultdb.com/ovdb/dbs/chinook"
	for _, c := range []struct {
		a, b string
		want Relation
	}{
		{base, base, Same}, {base, strings.ToUpper(base), Same}, {base + "/", base, Same}, {base + "//", base + "/", Same}, {base, base + "///", Same},
		{base + "/x", base, Under}, {base + "/x/y", base + "/", Under}, {strings.ToUpper(base) + "/X", base, Under}, {base + "/{name}", base, Under},
		{base, base + "/x", Apart}, {base + "2", base, Apart}, {base + "2/x", base, Apart}, {"https://b.openvaultdb.com/ovdb/dbs/chinook", base, Apart},
		{"https://a.openvaultdb.com", "https://a.openvaultdb.com/", Same}, {"https://a.openvaultdb.com/x", "https://a.openvaultdb.com/", Under}, {"/x", "", Under}, {"", "", Same}, {"", "x", Apart}, {"a", "ab", Apart},
		{base + "/\u00e0", base, Incomparable}, {base, base + "/\u212aelvin", Incomparable}, {base + strings.Repeat("a", MaxClaimLength), base, Incomparable}, {base, base + strings.Repeat("a", MaxClaimLength), Incomparable},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", shorten(c.a), shorten(c.b), got, c.want)
		}
	}
	if form, ok := ClaimedForm("HTTPS://A.Example/B//"); !ok || form != "https://a.example/b" {
		t.Errorf("ClaimedForm = %q, %v", form, ok)
	}
	if form, ok := ClaimedForm("///"); !ok || form != "" {
		t.Errorf("ClaimedForm(///) = %q, %v", form, ok)
	}
	if form, ok := ClaimedForm(strings.Repeat("a", MaxClaimLength)); !ok || len(form) != MaxClaimLength {
		t.Errorf("ClaimedForm at the bound = %d bytes, %v", len(form), ok)
	}
}
