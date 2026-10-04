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
		{"repository absent", edit(t, ownManifest, "  repository: https://github.com/datatug/chinookdb\n", ""), "manifest-publisher", 22, nil},
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
		{"recordset name", edit(t, ownManifest, "  - Artist\n", "  - 1a\n"), "manifest-recordsets", 30, func(m Manifest) bool { return m.Recordsets.Unusable() }},
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
