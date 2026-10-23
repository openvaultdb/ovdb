package manifest

import (
	"regexp"
	"slices"
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

func FuzzCheck(f *testing.F) {
	_, _, manifests, mds := loadReference(f)
	for i := 0; i < len(manifests) && i < len(mds)*10; i += 11 {
		f.Add(mds[i%len(mds)].Document, manifests[i].Document)
	}
	for _, c := range manifests[:20] {
		f.Add(mds[0].Document, c.Document)
	}
	f.Add([]byte("---\novdb: 1\npublish:\n"+strings.Repeat("  - x\n", 101)+"---\n"), []byte("format: 1\n"))
	f.Add([]byte("---\novdb: 1\npublish: ["+strings.Repeat("./a,", 5000)+"./a]\n---\n"), []byte(""))
	f.Fuzz(func(t *testing.T, ovdbMd, manifest []byte) {
		directory := Check(ovdbMd, "ovdb\x1b.yaml", manifest, Directory)
		publisher := Check(ovdbMd, "ovdb\x1b.yaml", manifest, Publisher)
		if !directory.OK() && publisher.OK() {
			t.Fatalf("a pair refused by the Directory profile and accepted by the Publisher profile: %q %q", ovdbMd, manifest)
		}
		for _, profile := range []Profile{Directory, Publisher} {
			checkProfile(t, profile, ovdbMd, manifest)
		}
	})
}

func checkProfile(t *testing.T, profile Profile, ovdbMd, manifest []byte) {
	t.Helper()
	result := Check(ovdbMd, "ovdb\x1b.yaml", manifest, profile)
	assertFindings(t, result.Findings)
	if result.OK() != (len(result.Findings) == 0) {
		t.Fatal("OK disagrees with the findings")
	}
	md, mdFindings := CheckOVDBMd(ovdbMd, profile)
	assertFindings(t, mdFindings)
	if len(mdFindings) == 0 && (!md.Read || !md.Version.Usable() || !md.Publish.Usable() || len(md.Entries) == 0) {
		t.Fatalf("OVDB.md without findings is not usable: %+v", md)
	}
	if (md.Version.Unusable() || md.Publish.Unusable()) && len(mdFindings) == 0 {
		t.Fatalf("an unusable fact without a finding: %+v", md)
	}
	if md.Publish.Usable() && !slices.Equal(md.Publish.Value, md.Entries) {
		t.Fatalf("publish %v is not the entries %v", md.Publish.Value, md.Entries)
	}
	seen := map[string]bool{}
	for _, p := range md.Entries {
		if !rules.IsRepositoryPath(p) || seen[p] {
			t.Fatalf("OVDB.md lists %q, a bad path or a repeat", p)
		}
		seen[p] = true
	}
	m, findings := CheckManifest(manifest, "ovdb.yaml", profile)
	assertFindings(t, findings)
	if len(findings) == 0 && (!m.Read || rules.IsBlank(m.ID.Value) || !m.URL.Usable() || !m.DeploymentURL.Usable() || m.Form != FormOwn && m.Form != FormShared || !m.Recordsets.Usable()) {
		t.Fatalf("an accepted manifest with facts %+v", m)
	}
	if profile == Publisher && len(findings) == 0 && (!m.PublisherRepository.Usable() || !rules.IsID(m.ID.Value)) {
		t.Fatalf("an accepted manifest of the Publisher profile with facts %+v", m)
	}
	for name, v := range factValues(m) {
		switch {
		case len(findings) == 0 && v == "<present and not usable>":
			t.Fatalf("an accepted manifest with an unusable fact %s", name)
		case !m.Read && name != "form" && v != nil:
			t.Fatalf("an unread manifest says %s = %v", name, v)
		}
	}
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
