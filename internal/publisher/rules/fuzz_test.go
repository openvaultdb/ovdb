package rules

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"testing"
)

// The fuzz targets run their seeds with `go test` and fuzz only when asked:
//
//	go test -run '^$' -fuzz FuzzPublicHTTPSURL -fuzztime 60s ./internal/publisher/rules
//	go test -run '^$' -fuzz FuzzRepositoryKey  -fuzztime 60s ./internal/publisher/rules
//	go test -run '^$' -fuzz FuzzRepositoryPath -fuzztime 60s ./internal/publisher/rules
//
// Each asserts that the function does not panic and that what it accepts has
// the properties it exists to guarantee, checked with Go's own net/url parser.
// net/url is the judge here and never the reader: the functions under test do
// not call it.

func FuzzPublicHTTPSURL(f *testing.F) {
	for _, seed := range []string{
		"", "https://", "https://e.openvaultdb.com/", "https://e.openvaultdb.com/a/b-c_d~e.f",
		"https://e.openvaultdb.com/{name}", "https://e.openvaultdb.com/a/{name}/b", "https://{name}.openvaultdb.com/x",
		"https://xn--bcher-kva.de/ovdb", "https://xn--80ak6aa92e.com/x", "https://a.1/x", "https://127.0.0.1/x", "https://[::1]/x",
		"https://user:pw@e.openvaultdb.com/x", "https://e.openvaultdb.com:443/x", "https://e.openvaultdb.com/x?a=1", "https://e.openvaultdb.com/x#a",
		"https://e.openvaultdb.com/a%2fb", "https://e.openvaultdb.com/a/../b", "https://e.openvaultdb.com//", "https://e.openvaultdb.com\\@evil.com/",
		"http://e.openvaultdb.com/x", "https:/e.openvaultdb.com/x", "https://E.openvaultdb.com/x", "https://e.openvaultdb.com./x", "https://localhost/x",
		"https://e.openvaultdb.com/é", "https://e.openvaultdb.com/\x00", "https://" + strings.Repeat("a", 64) + ".com/x",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, template := range []bool{false, true} {
			var err error
			if template {
				err = PublicHTTPSURLTemplate(s)
			} else {
				err = PublicHTTPSURL(s)
			}
			if err != nil {
				var p *Problem
				if !errors.As(err, &p) || p.Rule == "" || p.Detail == "" {
					t.Fatalf("%q: the refusal %v is not a described *Problem", s, err)
				}
				continue
			}
			probe := s
			if template {
				if strings.Count(s, placeholder) != 1 {
					t.Fatalf("%q is accepted as a template without exactly one %s", s, placeholder)
				}
				probe = strings.Replace(s, placeholder, "name", 1)
			}
			assertPlainHTTPS(t, s, probe)
		}
		if err := Homepage(s); err == nil && len(s) > MaxHomepageLength {
			t.Fatalf("%q is accepted as a homepage of %d bytes", s, len(s))
		}
	})
}

func assertPlainHTTPS(t *testing.T, accepted, probe string) {
	t.Helper()
	u, err := url.Parse(probe)
	if err != nil {
		t.Fatalf("%q is accepted, and net/url refuses it: %v", accepted, err)
	}
	switch {
	case u.Scheme != "https":
		t.Fatalf("%q is accepted with scheme %q", accepted, u.Scheme)
	case u.User != nil:
		t.Fatalf("%q is accepted with userinfo", accepted)
	case u.Port() != "" || strings.Contains(u.Host, ":"):
		t.Fatalf("%q is accepted with a port: host %q", accepted, u.Host)
	case u.RawQuery != "" || u.ForceQuery:
		t.Fatalf("%q is accepted with a query", accepted)
	case u.Fragment != "" || u.RawFragment != "":
		t.Fatalf("%q is accepted with a fragment", accepted)
	case u.Opaque != "":
		t.Fatalf("%q is accepted as an opaque URL", accepted)
	case u.Host != strings.ToLower(u.Host) || u.Hostname() != u.Host:
		t.Fatalf("%q is accepted with host %q, which is not a plain lower-case host name", accepted, u.Host)
	case u.String() != probe:
		t.Fatalf("%q is accepted, and net/url writes it as %q: it is not canonical", accepted, u.String())
	case u.Path == "" || !strings.HasPrefix(u.Path, "/"):
		t.Fatalf("%q is accepted with path %q", accepted, u.Path)
	}
	if want := strings.TrimPrefix(probe, "https://"); !strings.HasPrefix(want, u.Host+"/") {
		t.Fatalf("%q is accepted, and net/url reads host %q from it", accepted, u.Host)
	}
}

func FuzzRepositoryKey(f *testing.F) {
	for _, seed := range []string{
		"", "https://github.com/datatug/chinookdb", "https://github.com/DataTug/ChinookDB", "https://github.com/a/b.git", "https://github.com/a/b/",
		"https://github.com/a", "https://github.com/a/b/c", "https://github.com//b", "https://github.com/./b", "https://github.com/a/..", "https://www.github.com/a/b",
		"https://user@github.com/a/b", "https://github.com:443/a/b", "https://github.com/a/b?x=1", "https://github.com/a/b#x", "http://github.com/a/b",
		"git@github.com:a/b.git", "https://github.com/a/é", "https://gitlab.com/a/b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		key, ok := RepositoryKey(s)
		compare, compareOK := CompareKey(s)
		if ok != compareOK {
			t.Fatalf("%q: RepositoryKey says %v, CompareKey says %v", s, ok, compareOK)
		}
		if !ok {
			if key != "" || compare != "" {
				t.Fatalf("%q is refused with keys %q and %q", s, key, compare)
			}
			return
		}
		if compare != strings.ToLower(key) {
			t.Fatalf("%q: CompareKey %q is not RepositoryKey %q in lower case", s, compare, key)
		}
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("%q is accepted, and net/url refuses it: %v", s, err)
		}
		segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		switch {
		case u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.Port() != "":
			t.Fatalf("%q is accepted as %q on host %q", s, u.Scheme, u.Host)
		case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "":
			t.Fatalf("%q is accepted with a query, fragment or opaque part", s)
		case len(segments) != 2 || strings.HasSuffix(strings.ToLower(u.Path), ".git") || strings.HasSuffix(u.Path, "/"):
			t.Fatalf("%q is accepted with path %q", s, u.Path)
		case key != "github.com"+u.Path || u.String() != s:
			t.Fatalf("%q is accepted with key %q", s, key)
		}
		for _, segment := range segments {
			if segment == "" || segment == "." || segment == ".." {
				t.Fatalf("%q is accepted with the path segment %q", s, segment)
			}
		}
	})
}

func FuzzRepositoryPath(f *testing.F) {
	for _, seed := range []string{
		"", "ovdb.yaml", "a/b/c", "./a", "../a", "a/..", "a//b", "/a", "a/", "a\\b", "a b", "x*.yaml", "é", ".", "..", "...", "a/./b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if IsPublishEntry(s) && !strings.HasPrefix(s, "./") {
			t.Fatalf("%q is a publish entry without ./", s)
		}
		if !IsRepositoryPath(s) {
			return
		}
		if len(s) > MaxPathLength || path.IsAbs(s) || strings.Contains(s, `\`) || strings.HasSuffix(s, "/") || path.Clean(s) != s {
			t.Fatalf("%q is accepted as a path inside a repository", s)
		}
		for _, segment := range strings.Split(s, "/") {
			if segment == "." || segment == ".." || segment == "" {
				t.Fatalf("%q is accepted with the segment %q", s, segment)
			}
		}
		if !IsPublishEntry("./" + s) {
			t.Fatalf("./%s is not a publish entry", s)
		}
	})
}
