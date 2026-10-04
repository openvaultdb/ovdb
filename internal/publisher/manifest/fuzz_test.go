package manifest

import (
	"regexp"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// The fuzz targets run their seeds with `go test` and fuzz only when asked:
//
//	go test -run '^$' -fuzz FuzzCheck   -fuzztime 60s ./internal/publisher/manifest
//	go test -run '^$' -fuzz FuzzAddress -fuzztime 60s ./internal/publisher/manifest
//
// The seeds are the documents of the reference corpus. FuzzCheck asserts that
// nothing panics and that every finding is well formed; FuzzAddress holds
// the hand-written address reader to the expressions of the Directory.

func checkFindings(t *testing.T, document string, findings []Finding) {
	t.Helper()
	if len(findings) > MaxFindings+1 {
		t.Fatalf("%d findings", len(findings))
	}
	for _, f := range findings {
		if f.Rule == "" || f.Message == "" || f.Severity != SeverityError || f.Line < 0 || f.Document == "" {
			t.Fatalf("a malformed finding %+v", f)
		}
		if !printable(f.Message) || !printable(f.Rule) {
			t.Fatalf("a finding with text that is not printable ASCII: %q", f.Message)
		}
		if !strings.HasPrefix(f.String(), f.Document) {
			t.Fatalf("String() of %+v", f)
		}
	}
}

func FuzzCheck(f *testing.F) {
	_, _, manifests, mds := loadReference(f)
	for i := 0; i < len(manifests) && i < len(mds)*10; i += 11 {
		f.Add([]byte(mds[i%len(mds)].Document), []byte(manifests[i].Document))
	}
	for _, c := range manifests[:20] {
		f.Add([]byte(mds[0].Document), []byte(c.Document))
	}
	f.Fuzz(func(t *testing.T, ovdbMd, manifest []byte) {
		result := Check(ovdbMd, "ovdb.yaml", manifest, Directory)
		checkFindings(t, "ovdb.yaml", result.Findings)
		if result.OK() != (len(result.Findings) == 0) {
			t.Fatal("OK disagrees with the findings")
		}
		md, mdFindings := CheckOVDBMd(ovdbMd, Directory)
		if len(mdFindings) == 0 && (!md.Valid || len(md.Publish) == 0) {
			t.Fatalf("OVDB.md without findings is not valid: %+v", md)
		}
		if md.Valid && md.PublishLine == 0 {
			t.Fatal("a valid OVDB.md has no publish line")
		}
		for _, p := range md.Publish {
			if !rules.IsRepositoryPath(p) {
				t.Fatalf("OVDB.md lists %q", p)
			}
		}
		m, findings := CheckManifest(manifest, "ovdb.yaml", Directory)
		if len(findings) == 0 {
			if rules.IsBlank(m.ID) || m.URL == "" || m.DeploymentURL == "" || m.Form != FormOwn && m.Form != FormShared {
				t.Fatalf("an accepted manifest with facts %+v", m)
			}
		}
	})
}

var (
	modelAddress = regexp.MustCompile(`^modelspec://([A-Za-z0-9.-]+)/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([A-Za-z_][A-Za-z0-9_]*)(\?ref=[0-9a-f]{40})?$`)
	graphAddress = regexp.MustCompile(`^meaning://([A-Za-z0-9.-]+)/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)(\?ref=[0-9a-f]{40})?$`)
)

func FuzzAddress(f *testing.F) {
	pin := "8c9e62ed6641c0a00faa3867167d928af4c44b06"
	for _, seed := range []string{
		"", "modelspec://github.com/datatug/chinookdb/chinook", "modelspec://github.com/datatug/chinookdb/chinook?ref=" + pin,
		"meaning://github.com/datatug/chinookdb", "meaning://github.com/datatug/chinookdb?ref=" + pin, "meaning://github.com/datatug/chinookdb/",
		"modelspec://github.com/datatug/chinookdb/1chinook", "modelspec://github.com/datatug/chinookdb/chinook?ref=ABC", "modelspec://github.com/datatug/chinookdb/chinook?ref=" + pin + "\n",
		"modelspec://github.com/datatug/chinookdb/chinook#x", "meaning://git hub.com/a/b", "meaning://é/a/b", "modelspec://a/b/c/d?ref=" + strings.ToUpper(pin),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, ok := parseModelAddress(s)
		if ok != modelAddress.MatchString(s) && len(s) <= rules.MaxURLLength {
			t.Fatalf("parseModelAddress(%q) = %v, the Directory's expression says %v", s, ok, !ok)
		}
		if ok && len(s) > rules.MaxURLLength {
			t.Fatalf("parseModelAddress accepts %d bytes", len(s))
		}
		_, ok = parseGraphAddress(s)
		if ok != graphAddress.MatchString(s) && len(s) <= rules.MaxURLLength {
			t.Fatalf("parseGraphAddress(%q) = %v, the Directory's expression says %v", s, ok, !ok)
		}
	})
}
