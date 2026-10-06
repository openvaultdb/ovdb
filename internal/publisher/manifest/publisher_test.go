package manifest

import (
	"slices"
	"strings"
	"testing"
)

// sharedPublisherManifest is the shared sample with the publisher.repository that the Publisher profile requires.
func sharedPublisherManifest(t *testing.T) string {
	return edit(t, sharedManifest, "  url: https://github.com/acme\n", "  url: https://github.com/acme\n  repository: https://github.com/acme/chinook-hosting\n")
}

func TestPublisherAcceptsTheSamples(t *testing.T) {
	for name, doc := range map[string]string{"own": ownManifest, "shared": sharedPublisherManifest(t)} {
		r := Check([]byte(goodMD), "ovdb.yaml", []byte(doc), Publisher)
		if !r.OK() || r.Profile != Publisher || !r.Manifest.ID.Usable() || !r.Manifest.PublisherRepository.Usable() {
			t.Errorf("%s: %v", name, r.Findings)
		}
	}
	// What the Directory profile accepts and the Publisher profile does not: a manifest without publisher.repository.
	if r := Check([]byte(goodMD), "ovdb.yaml", []byte(sharedManifest), Directory); !r.OK() {
		t.Fatalf("the Directory profile: %v", r.Findings)
	}
	r := Check([]byte(goodMD), "ovdb.yaml", []byte(sharedManifest), Publisher)
	if r.OK() || !r.Manifest.PublisherRepository.Absent() {
		t.Errorf("the Publisher profile requires publisher.repository: %v", r.Findings)
	}
}

// Each rule that the Publisher profile adds, with the line of its finding, and the fact it makes unusable.
func TestPublisherRules(t *testing.T) {
	shared := sharedPublisherManifest(t)
	for _, c := range []struct {
		name, doc, rule string
		line            int
		unusable        func(Manifest) bool // the fact the rule demotes, when it demotes one
	}{
		{"unknown key", ownManifest + "api_key: x\n", "manifest-keys", 32, nil},
		{"unknown deployment key", edit(t, ownManifest, "  engine: sqlite\n", "  engine: sqlite\n  token: x\n"), "manifest-keys", 10, nil},
		{"unknown model key", edit(t, ownManifest, "model:\n", "model:\n  extra: x\n"), "manifest-keys", 13, nil},
		{"unknown meaning key", edit(t, ownManifest, "meaning:\n", "meaning:\n  extra: x\n"), "manifest-keys", 17, nil},
		{"unknown graph key", edit(t, ownManifest, "  graph:\n", "  graph:\n    secret: x\n"), "manifest-keys", 19, nil},
		{"unknown publisher key", edit(t, ownManifest, "publisher:\n", "publisher:\n  email: x\n"), "manifest-keys", 22, nil},
		{"unknown licences key", edit(t, ownManifest, "licences:\n", "licences:\n  extra: x\n"), "manifest-keys", 26, nil},
		{"id", edit(t, ownManifest, "id: chinook\n", "id: Chinook\n"), "manifest-id", 2, func(m Manifest) bool { return m.ID.Unusable() }},
		{"discovery path", edit(t, ownManifest, ".well-known/openvaultdb", ".well-known/other"), "manifest-discovery", 10, func(m Manifest) bool { return m.Discovery.Unusable() }},
		{"recordset page origin", edit(t, ownManifest, "https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}", "https://other.example.com/c/{name}"), "manifest-url", 11, func(m Manifest) bool { return m.RecordsetPage.Unusable() }},
		{"publisher url", edit(t, ownManifest, "url: https://github.com/datatug\n", "url: https://github.com/datatug/x\n"), "manifest-publisher", 23, func(m Manifest) bool { return m.PublisherURL.Unusable() }},
		{"publisher url host", edit(t, ownManifest, "url: https://github.com/datatug\n", "url: https://example.com/datatug\n"), "manifest-publisher", 23, func(m Manifest) bool { return m.PublisherURL.Unusable() }},
		{"repository owner", edit(t, ownManifest, "repository: https://github.com/datatug/chinookdb", "repository: https://github.com/other/chinookdb"), "manifest-publisher", 24, func(m Manifest) bool { return m.PublisherRepository.Unusable() }},
		{"repository absent", edit(t, ownManifest, "  repository: https://github.com/datatug/chinookdb\n", ""), "manifest-required", 22, nil},
		{"address module", edit(t, ownManifest, "datatug/chinookdb/chinook\n", "datatug/chinookdb/_chinook\n"), "manifest-model", 13, func(m Manifest) bool { return m.ModelAddress.Unusable() }},
		{"model name", edit(t, ownManifest, "model:\n  address: modelspec://github.com/datatug/chinookdb/chinook\n", "model:\n  name: _x\n"), "manifest-model", 13, func(m Manifest) bool { return m.ModelName.Unusable() }},
		{"model name and address", edit(t, ownManifest, "model:\n", "model:\n  name: other\n"), "manifest-model", 13, func(m Manifest) bool { return m.ModelName.Unusable() }},
		{"modelspec suffix", edit(t, ownManifest, "chinook.modelspec.json", "chinook.json"), "manifest-model", 14, func(m Manifest) bool { return m.ModelSpec.Unusable() }},
		{"hcl required", edit(t, ownManifest, "  hcl: model/chinook.modelspec.hcl\n", ""), "manifest-required", 13, nil},
		{"own address", edit(t, ownManifest, "modelspec://github.com/datatug/chinookdb/chinook\n", "modelspec://github.com/acme/x/chinook\n"), "manifest-model", 13, func(m Manifest) bool { return m.ModelAddress.Unusable() }},
		{"graph address", edit(t, ownManifest, "address: meaning://github.com/datatug/chinookdb\n", "address: meaning://github.com/acme/x\n"), "manifest-meaning", 20, func(m Manifest) bool { return m.GraphAddress.Unusable() }},
		{"graph address case", edit(t, ownManifest, "address: meaning://github.com/datatug/chinookdb\n", "address: meaning://github.com/DataTug/ChinookDB\n"), "", 0, nil},
		{"graph id", edit(t, ownManifest, "    id: chinook\n", "    id: Chinook\n"), "manifest-meaning", 19, func(m Manifest) bool { return m.GraphID.Unusable() }},
		{"licence", edit(t, ownManifest, "data: MIT", "data: GPL-3.0-or-later"), "manifest-licence", 26, func(m Manifest) bool { return m.LicenceData.Unusable() }},
		{"licence case", edit(t, ownManifest, "  model: MIT\n", "  model: mit\n"), "manifest-licence", 27, func(m Manifest) bool { return m.LicenceModel.Unusable() }},
		// The Chinook checker wanted a recordset to be named as a ModelSpec entity; the Directory takes any name, and so does this profile (D0).
		{"native recordset name", edit(t, ownManifest, "  - Artist\n", "  - dbo.Artist 1a\n"), "", 0, nil},
		{"recordset name with a slash", edit(t, ownManifest, "  - Artist\n", "  - dbo/Artist\n"), "manifest-recordsets", 31, func(m Manifest) bool { return m.Recordsets.Unusable() }},
		{"recordset page", edit(t, ownManifest, "collections/{name}", "collections/"+strings.Repeat("x", 2040)+"/{name}"), "manifest-url", 11, nil},
		{"shared name", edit(t, shared, "model:\n", "model:\n  name: other\n"), "manifest-model", 11, func(m Manifest) bool { return m.ModelName.Unusable() }},
		{"shared own model address", edit(t, shared, "datatug/chinookdb/chinook?ref=", "acme/chinook-hosting/chinook?ref="), "manifest-model", 11, func(m Manifest) bool { return m.ModelAddress.Unusable() }},
		{"shared own meaning address", edit(t, shared, "meaning://github.com/datatug/chinookdb?ref=", "meaning://github.com/acme/chinook-hosting?ref="), "manifest-meaning", 13, func(m Manifest) bool { return m.MeaningAddress.Unusable() }},
		{"shared graph address", edit(t, shared, "    id: chinook\n", "    id: chinook\n    address: meaning://github.com/other/x\n"), "manifest-meaning", 17, func(m Manifest) bool { return m.GraphAddress.Unusable() }},
		{"shared module", edit(t, shared, "datatug/chinookdb/chinook?ref=", "datatug/chinookdb/_chinook?ref="), "manifest-model", 11, func(m Manifest) bool { return m.ModelAddress.Unusable() }},
	} {
		m, findings := CheckManifest([]byte(c.doc), "ovdb.yaml", Publisher)
		if c.rule == "" {
			if len(findings) != 0 {
				t.Errorf("%s: %v", c.name, findings)
			}
			continue
		}
		found := false
		for _, f := range findings {
			found = found || f.Rule == c.rule && f.Line == c.line
		}
		if !found {
			t.Errorf("%s: want rule %s at line %d, got %v", c.name, c.rule, c.line, findings)
		}
		if c.unusable != nil && !c.unusable(m) {
			t.Errorf("%s: the fact is not present and unusable: %+v", c.name, m)
		}
		// Whatever the Publisher profile refuses and the Directory profile accepts stays accepted by the Directory profile.
		for _, f := range findings {
			if !printable(f.Message) || len(f.Message) > MaxMessageBytes {
				t.Errorf("%s: finding %+v", c.name, f)
			}
		}
	}
}

func TestPublisherOVDBMd(t *testing.T) {
	for _, c := range []struct {
		name, doc, rule string
		line            int
	}{
		{"unknown key", "---\novdb: 1\npublish: [./ovdb.yaml]\ntoken: x\n---\n", "ovdbmd-keys", 4},
		{"unknown key first", "---\ntoken: x\novdb: 1\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-keys", 2},
		{"duplicate", "---\novdb: 1\npublish: [./a.yaml, ./b.yaml, ./a.yaml]\n---\n", "ovdbmd-duplicate", 3},
		{"duplicate in a block", "---\novdb: 1\npublish:\n  - ./a.yaml\n  - ./a.yaml\n---\n", "ovdbmd-duplicate", 5},
	} {
		md, findings := CheckOVDBMd([]byte(c.doc), Publisher)
		found := false
		for _, f := range findings {
			found = found || f.Rule == c.rule && f.Line == c.line
		}
		if !found {
			t.Errorf("%s: want rule %s at line %d, got %v", c.name, c.rule, c.line, findings)
		}
		// The Directory profile accepts both.
		if _, f := CheckOVDBMd([]byte(c.doc), Directory); len(f) != 0 {
			t.Errorf("%s: the Directory profile: %v", c.name, f)
		}
		if c.rule == "ovdbmd-duplicate" && (md.Publish.Usable() || !slices.Equal(md.Repeated, []string{"a.yaml"}) || len(md.Entries) == 0) {
			t.Errorf("%s: %+v", c.name, md)
		}
	}
}

// The own form's meaning.graph.address is compared with the repository in ASCII case only, because
// Go's strings.ToLower and JavaScript's toLowerCase differ outside ASCII (review of slice 2b, blocker 1).
func TestGraphAddressIsComparedInAsciiCaseOnly(t *testing.T) {
	const own = "meaning://github.com/datatug/chinookdb"
	for _, c := range []struct {
		name, address string
		accepted      bool
	}{
		{"exact", own, true},
		{"ASCII upper case in the organisation and the repository", "meaning://github.com/DataTug/ChinookDB", true},
		// The Directory knows the host only as the literal github.com, so no record carries another spelling: the host is compared as written (kind graph-address-host-case).
		{"ASCII upper case in the host", "meaning://GITHUB.com/datatug/chinookdb", false},
		{"ASCII upper case everywhere", "meaning://GitHub.com/DataTug/ChinookDB", false},
		// Go lower-cases U+0130 to i; JavaScript to i and a combining dot, so the checker refuses these.
		{"dotted capital I in the name", "meaning://github.com/datatug/ch\u0130nookdb", false},
		{"dotted capital I in the owner", "meaning://github.com/datatug\u0130/chinookdb", false},
		{"dotted capital I in the host", "meaning://g\u0130thub.com/datatug/chinookdb", false},
		{"dotless i", "meaning://github.com/datatug/ch\u0131nookdb", false},
		{"long s", "meaning://github.com/datatug/chinookdb\u017f", false},
		{"sharp s", "meaning://github.com/datatug/chinookdb\u00df", false},
		{"fullwidth letter", "meaning://github.com/datatug/chinookd\uff22", false},
		{"combining mark", "meaning://github.com/datatug/chinookdb\u0307", false},
		// JavaScript folds the Kelvin sign onto k, so the checker accepts this one and Go refuses it: kind graph-address-case.
		{"Kelvin sign", "meaning://github.com/datatug/chinoo\u212adb", false},
	} {
		doc := edit(t, ownManifest, "address: "+own+"\n", "address: "+c.address+"\n")
		m, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher)
		if (len(findings) == 0) != c.accepted {
			t.Errorf("%s: accepted %v, want %v: %v", c.name, len(findings) == 0, c.accepted, findings)
		}
		if !c.accepted && (len(findings) == 0 || findings[0].Rule != "manifest-meaning" || findings[0].Line != 20 || !m.GraphAddress.Unusable()) {
			t.Errorf("%s: %v %+v", c.name, findings, m.GraphAddress)
		}
	}
	for in, want := range map[string]string{"": "", "abc": "abc", "ABC xyz-09": "abc xyz-09", "@AMZ[`az{": "@amz[`az{", "\u0130\u212a\u00c9\u0391": "\u0130\u212a\u00c9\u0391", "\xff\xc4": "\xff\xc4"} {
		if got := lowerASCII(in); got != want {
			t.Errorf("lowerASCII(%q) = %q, want %q", in, got, want)
		}
	}
}

// The own repository's owner is a whole path segment: an owner that is a prefix of another is not that owner.
func TestOwnerBoundary(t *testing.T) {
	for _, c := range []struct {
		name, url, repository string
		accepted              bool
	}{
		{"same owner", "https://github.com/datatug", "https://github.com/datatug/chinookdb", true},
		{"owner is a prefix of the other", "https://github.com/data", "https://github.com/datatug/chinookdb", false},
		{"owner is a prefix with a hyphen", "https://github.com/datatug", "https://github.com/datatug-labs/chinookdb", false},
		{"owner with a longer name", "https://github.com/datatug-labs", "https://github.com/datatug/chinookdb", false},
		{"owner in another case", "https://github.com/DataTug", "https://github.com/datatug/chinookdb", false},
	} {
		doc := edit(t, ownManifest, "  url: https://github.com/datatug\n", "  url: "+c.url+"\n")
		doc = edit(t, doc, "  repository: https://github.com/datatug/chinookdb\n", "  repository: "+c.repository+"\n")
		m, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher)
		var owned []Finding
		for _, f := range findings {
			if f.Rule == "manifest-publisher" && f.Line == 24 {
				owned = append(owned, f)
			}
		}
		if (len(owned) == 0) != c.accepted || (len(owned) > 0 && !m.PublisherRepository.Unusable()) {
			t.Errorf("%s: owner refused %v, want %v: %v", c.name, len(owned) > 0, !c.accepted, findings)
		}
	}
}

// A recordset page is judged for every name of recordsets, in both profiles, as the Directory judges it: a name that needs encoding must be the whole of
// its path segment. The finding is at the line of the name, and recordsets is then not usable (the template alone is fine).
func TestRecordsetPageExpansion(t *testing.T) {
	doc := edit(t, edit(t, ownManifest, "  - Artist\n", "  - Artist\n  - Order Details\n"), "collections/{name}", "collections/{name}.html")
	for _, p := range []Profile{Publisher, Directory} {
		m, findings := CheckManifest([]byte(doc), "ovdb.yaml", p)
		if len(findings) != 1 || findings[0].Rule != "manifest-recordsets" || findings[0].Line != 32 || !strings.Contains(findings[0].Message, `the recordset page of "Order Details"`) {
			t.Fatalf("%v: %v", p, findings)
		}
		if !m.Recordsets.Present || m.Recordsets.Usable() || len(m.Recordsets.Value) != 0 || !m.RecordsetPage.Usable() {
			t.Errorf("recordsets %+v, page %+v", m.Recordsets, m.RecordsetPage)
		}
	}
	// Every name whose page fails is reported, not the first only.
	two := edit(t, ownManifest, "  - Artist\n", "  - Artist\n  - \"%2F\"\n  - \"%2e%2e\"\n")
	for _, p := range []Profile{Publisher, Directory} {
		if _, f := CheckManifest([]byte(two), "ovdb.yaml", p); len(f) != 2 || f[0].Line == f[1].Line {
			t.Errorf("%v: two bad names: %v", p, f)
		}
	}
	// A long name makes a long page, which the Directory takes: the name is bounded, the page is not.
	long := edit(t, ownManifest, "  - Artist\n", "  - Artist\n  - "+strings.Repeat("€", 256)+"\n")
	for _, p := range []Profile{Publisher, Directory} {
		if _, f := CheckManifest([]byte(long), "ovdb.yaml", p); len(f) != 0 {
			t.Errorf("%v: a name of 256 euro signs: %v", p, f)
		}
	}
}

// What the Chinook checker allows, held to the generator's copy of it (read from the checker) both ways.
func TestAllowListsAreTheCheckers(t *testing.T) {
	var golden struct {
		AllowLists struct {
			Licences []string            `json:"licences"`
			Keys     map[string][]string `json:"keys"`
		} `json:"allowLists"`
	}
	readGolden(t, "publisher.verdicts.json", &golden)
	if got, want := slices.Sorted(slices.Values(licenceIDs)), golden.AllowLists.Licences; !slices.Equal(got, want) || len(want) < 10 {
		t.Errorf("licence ids: Go has %v, the checker %v", got, want)
	}
	seen := map[string]bool{}
	for _, set := range allowedKeys {
		where := strings.Join(set.path, ".")
		seen[where] = true
		want, ok := golden.AllowLists.Keys[where]
		// The keys of the Directory that the checker did not know are allowed here too (D0): recordset_entities, since 1c7e126.
		if where == "" {
			want = slices.Sorted(slices.Values(append(slices.Clone(want), "recordset_entities")))
		}
		if got := slices.Sorted(slices.Values(set.keys)); !ok || !slices.Equal(got, want) {
			t.Errorf("keys of %q: Go has %v, the checker %v", where, got, want)
		}
	}
	for where := range golden.AllowLists.Keys {
		if !seen[where] {
			t.Errorf("the checker allows keys at %q, which Go does not know", where)
		}
	}
	if len(golden.AllowLists.Keys) != 7 {
		t.Errorf("%d mappings", len(golden.AllowLists.Keys))
	}
}

// The messages of the Publisher profile say what that profile asks: publisher.repository is required, not optional.
func TestPublisherMessagesDoNotOfferToLeaveTheRepositoryOut(t *testing.T) {
	for _, repository := range []string{"https://github.com/datatug/chinookdb/", "5", "\"\"", "https://example.com/datatug/chinookdb", "https://github.com/datatug/chinookdb?ref=x"} {
		doc := edit(t, ownManifest, "  repository: https://github.com/datatug/chinookdb\n", "  repository: "+repository+"\n")
		_, publisher := CheckManifest([]byte(doc), "ovdb.yaml", Publisher)
		_, directory := CheckManifest([]byte(doc), "ovdb.yaml", Directory)
		if len(publisher) == 0 || len(directory) == 0 {
			t.Fatalf("%s: publisher %v, directory %v", repository, publisher, directory)
		}
		for _, f := range publisher {
			if strings.Contains(f.Message, "leave publisher.repository out") || strings.Contains(f.Message, "when given") {
				t.Errorf("%s: the Publisher profile offers to leave the field out: %v", repository, f)
			}
		}
		if !strings.Contains(directory[0].Message, "leave publisher.repository out") {
			t.Errorf("%s: the Directory profile no longer offers it: %v", repository, directory[0])
		}
	}
}

// A repeated entry does not hide that the manifest is not listed: both findings are made, as in the Directory profile.
func TestDuplicateEntryDoesNotHideUnlisted(t *testing.T) {
	const md = "---\novdb: 1\npublish: [./a.yaml, ./a.yaml]\n---\n"
	for _, profile := range []Profile{Directory, Publisher} {
		r := Check([]byte(md), "ovdb.yaml", []byte(ownManifest), profile)
		if !slices.Contains(rulesOf(r.Findings), "ovdbmd-unlisted") {
			t.Errorf("%v: %v", profile, r.Findings)
		}
		if profile == Publisher && !slices.Contains(rulesOf(r.Findings), "ovdbmd-duplicate") {
			t.Errorf("%v: %v", profile, r.Findings)
		}
	}
}

// Three premises that code of the Publisher profile relies on, pinned: the mutants that remove that code cannot change a verdict, so a test of the
// premise is what keeps the code honest (the independent review of slice 2b named the four mutants; the fourth, demote, lost its test of presence
// because demote is called on usable facts only).
func TestPublisherPremises(t *testing.T) {
	// 1. A recordset page with two placeholders is refused before the expansion is judged (so replacing one or all of them is the same).
	twice := edit(t, ownManifest, "collections/{name}", "collections/{name}/{name}")
	for _, profile := range []Profile{Directory, Publisher} {
		m, findings := CheckManifest([]byte(twice), "ovdb.yaml", profile)
		if !slices.Contains(rulesOf(findings), "manifest-url") || m.RecordsetPage.Usable() {
			t.Errorf("%v: a template with two {name} is %+v, findings %v", profile, m.RecordsetPage, findings)
		}
	}
	// 2. A discovery document on another host is one finding, the Directory's, whatever its path: the Publisher profile does not add a second one for the
	// path (the rule of the path is judged only for the host of url).
	other := edit(t, ownManifest, "https://chinookdb.com/.well-known/openvaultdb", "https://other.example.com/.well-known/other")
	_, findings := CheckManifest([]byte(other), "ovdb.yaml", Publisher)
	if len(findings) != 1 || findings[0].Rule != "manifest-discovery" || !strings.Contains(findings[0].Message, "same origin") {
		t.Errorf("a discovery on another host and at another path: %v", findings)
	}
	// 3. The owner of publisher.url is judged by the characters of a GitHub name: the URL rule lets a tilde through, the owner is not one.
	tilde := edit(t, ownManifest, "url: https://github.com/datatug\n", "url: https://github.com/data~tug\n")
	m, findings := CheckManifest([]byte(tilde), "ovdb.yaml", Publisher)
	if !m.PublisherURL.Unusable() || len(findings) == 0 || findings[0].Rule != "manifest-publisher" || findings[0].Line != 23 {
		t.Errorf("an owner with a tilde: %+v, %v", m.PublisherURL, findings)
	}
}
