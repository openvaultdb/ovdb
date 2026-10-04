package manifest

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

const ownManifest = `format: ovdb-manifest/draft-1
id: chinook
title: Chinook music store
description: A sample database.
homepage: https://chinookdb.com/
url: https://chinookdb.com/ovdb/dbs/chinook
deployment:
  url: https://cloud.openvaultdb.com/ovdb/dbs/chinook
  engine: sqlite
  discovery: https://chinookdb.com/.well-known/openvaultdb
  recordset_page: https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}
model:
  address: modelspec://github.com/datatug/chinookdb/chinook
  modelspec: model/chinook.modelspec.json
  hcl: model/chinook.modelspec.hcl
meaning:
  file: model/chinook.meaning.yaml
  graph:
    id: chinook
    address: meaning://github.com/datatug/chinookdb
publisher:
  name: DataTug
  url: https://github.com/datatug
  repository: https://github.com/datatug/chinookdb
licences:
  data: MIT
  model: MIT
  meaning: CC0-1.0
recordsets:
  - Album
  - Artist
`

const pin = "8c9e62ed6641c0a00faa3867167d928af4c44b06"

const sharedManifest = `format: ovdb-manifest/draft-1
id: chinook-acme
title: Chinook at Acme
description: Hosted by Acme.
url: https://ovdb.acme.io/dbs/chinook
deployment:
  url: https://cloud.acme.io/ovdb/dbs/chinook
  engine: postgres
  discovery: https://ovdb.acme.io/.well-known/openvaultdb
model:
  address: modelspec://github.com/datatug/chinookdb/chinook?ref=` + pin + `
meaning:
  address: meaning://github.com/datatug/chinookdb?ref=` + pin + `
  file: model/chinook.meaning.yaml
  graph:
    id: chinook
publisher:
  name: Acme
  url: https://github.com/acme
licences:
  data: MIT
recordsets:
  - Album
`

const goodMD = "---\novdb: 1\npublish: [./ovdb.yaml]\n---\n# Title\n"

// edit replaces the first occurrence of old in text, and fails the test if there is none.
func edit(t *testing.T, text, old, replacement string) string {
	t.Helper()
	if !strings.Contains(text, old) {
		t.Fatalf("%q is not in the document", old)
	}
	return strings.Replace(text, old, replacement, 1)
}

func rulesOf(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func usable[T any](t *testing.T, name string, f Fact[T], want T) {
	t.Helper()
	if !f.Present || !f.Valid || !reflect.DeepEqual(f.Value, want) || f.Line == 0 || !f.Usable() || f.Absent() || f.Unusable() {
		t.Errorf("%s = %+v, want usable %v", name, f, want)
	}
}

func TestGoodDocuments(t *testing.T) {
	r := Check([]byte(goodMD), "ovdb.yaml", []byte(ownManifest), Directory)
	if !r.OK() || r.Profile != Directory {
		t.Fatalf("findings: %v", r.Findings)
	}
	m := r.Manifest
	if !m.Read || m.Form != FormOwn {
		t.Errorf("read %v, form %v", m.Read, m.Form)
	}
	usable(t, "Format", m.Format, ManifestFormat)
	usable(t, "ID", m.ID, "chinook")
	usable(t, "Title", m.Title, "Chinook music store")
	usable(t, "Description", m.Description, "A sample database.")
	usable(t, "URL", m.URL, "https://chinookdb.com/ovdb/dbs/chinook")
	usable(t, "Homepage", m.Homepage, "https://chinookdb.com/")
	usable(t, "DeploymentURL", m.DeploymentURL, "https://cloud.openvaultdb.com/ovdb/dbs/chinook")
	usable(t, "Engine", m.Engine, "sqlite")
	usable(t, "Discovery", m.Discovery, "https://chinookdb.com/.well-known/openvaultdb")
	usable(t, "RecordsetPage", m.RecordsetPage, "https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}")
	usable(t, "ModelSpec", m.ModelSpec, "model/chinook.modelspec.json")
	usable(t, "ModelHCL", m.ModelHCL, "model/chinook.modelspec.hcl")
	usable(t, "MeaningFile", m.MeaningFile, "model/chinook.meaning.yaml")
	usable(t, "ModelAddress", m.ModelAddress, Address{Text: "modelspec://github.com/datatug/chinookdb/chinook", Repository: "github.com/datatug/chinookdb", Module: "chinook"})
	usable(t, "GraphID", m.GraphID, "chinook")
	usable(t, "GraphAddress", m.GraphAddress, "meaning://github.com/datatug/chinookdb")
	usable(t, "LicenceData", m.LicenceData, "MIT")
	usable(t, "LicenceModel", m.LicenceModel, "MIT")
	usable(t, "LicenceMeaning", m.LicenceMeaning, "CC0-1.0")
	usable(t, "PublisherName", m.PublisherName, "DataTug")
	usable(t, "PublisherURL", m.PublisherURL, "https://github.com/datatug")
	usable(t, "PublisherRepository", m.PublisherRepository, "https://github.com/datatug/chinookdb")
	usable(t, "Recordsets", m.Recordsets, []string{"Album", "Artist"})
	if !m.MeaningAddress.Absent() || !m.RecordsetsPartial.Absent() || !m.ModelName.Absent() || m.MeaningAddress.Present || m.ModelName.Line != 0 {
		t.Errorf("keys that are not written are absent: %+v %+v %+v", m.MeaningAddress, m.RecordsetsPartial, m.ModelName)
	}
	md := r.OVDBMd
	if !md.Read || !md.Lists("ovdb.yaml") || md.Lists("other.yaml") || len(md.Repeated) != 0 {
		t.Errorf("OVDB.md facts: %+v", md)
	}
	usable(t, "Version", md.Version, "1")
	usable(t, "Publish", md.Publish, []string{"ovdb.yaml"})
	if md.Version.Line != 2 || md.Publish.Line != 3 {
		t.Errorf("lines of OVDB.md facts: %d %d", md.Version.Line, md.Publish.Line)
	}

	r = Check([]byte(goodMD), "ovdb.yaml", []byte(sharedManifest), Directory)
	if !r.OK() {
		t.Fatalf("shared findings: %v", r.Findings)
	}
	m = r.Manifest
	usable(t, "shared ModelAddress", m.ModelAddress, Address{Text: "modelspec://github.com/datatug/chinookdb/chinook?ref=" + pin, Repository: "github.com/datatug/chinookdb", Module: "chinook", Ref: pin})
	usable(t, "shared MeaningAddress", m.MeaningAddress, Address{Text: "meaning://github.com/datatug/chinookdb?ref=" + pin, Repository: "github.com/datatug/chinookdb", Ref: pin})
	usable(t, "shared MeaningFile", m.MeaningFile, "model/chinook.meaning.yaml")
	if m.Form != FormShared || !m.ModelSpec.Absent() || !m.Homepage.Absent() || !m.PublisherRepository.Absent() || !m.GraphAddress.Absent() || !m.LicenceModel.Absent() {
		t.Errorf("shared facts: %+v", m)
	}
	partial := edit(t, sharedManifest, "recordsets:", "recordsets_partial: true\nrecordsets:")
	if r := Check([]byte(goodMD), "ovdb.yaml", []byte(partial), Directory); !r.OK() {
		t.Errorf("partial: %v", r.Findings)
	} else {
		usable(t, "RecordsetsPartial", r.Manifest.RecordsetsPartial, true)
	}
	notPartial := edit(t, sharedManifest, "recordsets:", "recordsets_partial: false\nrecordsets:")
	if r := Check([]byte(goodMD), "ovdb.yaml", []byte(notPartial), Directory); !r.OK() {
		t.Errorf("not partial: %v", r.Findings)
	} else {
		usable(t, "RecordsetsPartial false", r.Manifest.RecordsetsPartial, false)
	}
	for _, ok := range []string{edit(t, sharedManifest, "  file:", "  graph2: x\n  file:"), edit(t, sharedManifest, "    id: chinook\n", "    id: chinook\n    address: meaning://github.com/datatug/chinookdb\n"),
		edit(t, sharedManifest, "licences:\n  data: MIT", "licences:\n  data: MIT\n  model: MIT\n  meaning: CC0-1.0")} {
		if r := Check([]byte(goodMD), "ovdb.yaml", []byte(ok), Directory); !r.OK() {
			t.Errorf("an accepted edit: %v", r.Findings)
		}
	}
}

// Every fact has three states, and the four spellings of the reviewer's
// publisher.repository, and model.name, are present and not usable where the key
// left out is absent: a caller never reads an unusable fact as an absent one.
func TestThreeStatesOfAFact(t *testing.T) {
	for _, c := range []struct {
		name, old, new string
		fact           func(Manifest) (present, valid bool)
		rule           string
	}{
		{"repository empty", "  repository: https://github.com/datatug/chinookdb\n", "  repository:\n", func(m Manifest) (bool, bool) { return m.PublisherRepository.Present, m.PublisherRepository.Valid }, "manifest-publisher"},
		{"repository quoted empty", "  repository: https://github.com/datatug/chinookdb\n", "  repository: \"\"\n", func(m Manifest) (bool, bool) { return m.PublisherRepository.Present, m.PublisherRepository.Valid }, "manifest-publisher"},
		{"repository number", "  repository: https://github.com/datatug/chinookdb\n", "  repository: 123\n", func(m Manifest) (bool, bool) { return m.PublisherRepository.Present, m.PublisherRepository.Valid }, "manifest-publisher"},
		{"repository list", "  repository: https://github.com/datatug/chinookdb\n", "  repository: [https://github.com/datatug/chinookdb]\n", func(m Manifest) (bool, bool) { return m.PublisherRepository.Present, m.PublisherRepository.Valid }, "manifest-publisher"},
		{"repository not a repository", "  repository: https://github.com/datatug/chinookdb\n", "  repository: https://github.com/datatug\n", func(m Manifest) (bool, bool) { return m.PublisherRepository.Present, m.PublisherRepository.Valid }, "manifest-publisher"},
		{"name number", "model:\n", "model:\n  name: 5\n", func(m Manifest) (bool, bool) { return m.ModelName.Present, m.ModelName.Valid }, "manifest-model"},
		{"name not a module", "model:\n", "model:\n  name: a-b\n", func(m Manifest) (bool, bool) { return m.ModelName.Present, m.ModelName.Valid }, "manifest-model"},
		{"name blank", "model:\n", "model:\n  name: \" \"\n", func(m Manifest) (bool, bool) { return m.ModelName.Present, m.ModelName.Valid }, "manifest-model"},
		{"partial in own", "recordsets:", "recordsets_partial: true\nrecordsets:", func(m Manifest) (bool, bool) { return m.RecordsetsPartial.Present, m.RecordsetsPartial.Valid }, "manifest-form"},
		{"meaning address in own", "  file: model/chinook.meaning.yaml\n", "  address: meaning://github.com/datatug/chinookdb?ref=" + pin + "\n  file: model/chinook.meaning.yaml\n", func(m Manifest) (bool, bool) { return m.MeaningAddress.Present, m.MeaningAddress.Valid }, "manifest-form"},
		{"homepage list", "homepage: https://chinookdb.com/\n", "homepage: [a]\n", func(m Manifest) (bool, bool) { return m.Homepage.Present, m.Homepage.Valid }, "manifest-homepage"},
		{"recordsets twice", "  - Artist\n", "  - Album\n", func(m Manifest) (bool, bool) { return m.Recordsets.Present, m.Recordsets.Valid }, "manifest-recordsets"},
	} {
		m, findings := CheckManifest([]byte(edit(t, ownManifest, c.old, c.new)), "ovdb.yaml", Directory)
		present, valid := c.fact(m)
		if !present || valid || !slices.Contains(rulesOf(findings), c.rule) {
			t.Errorf("%s: present %v valid %v rules %v: want present, not usable, and %s", c.name, present, valid, rulesOf(findings), c.rule)
		}
	}
	// The key left out is absent, and accepted.
	m, findings := CheckManifest([]byte(edit(t, ownManifest, "  repository: https://github.com/datatug/chinookdb\n", "")), "ovdb.yaml", Directory)
	if len(findings) != 0 || !m.PublisherRepository.Absent() || m.PublisherRepository.Present || m.PublisherRepository.Valid {
		t.Errorf("repository left out: %v %+v", findings, m.PublisherRepository)
	}
	// An unusable fact holds no value.
	m, _ = CheckManifest([]byte(edit(t, ownManifest, "  repository: https://github.com/datatug/chinookdb\n", "  repository: https://github.com/datatug\n")), "ovdb.yaml", Directory)
	if !m.PublisherRepository.Unusable() || m.PublisherRepository.Value != "" || m.PublisherRepository.Line == 0 {
		t.Errorf("%+v", m.PublisherRepository)
	}
	// The same for OVDB.md.
	for _, c := range []struct {
		doc              string
		version, publish string // "absent", "usable" or "unusable"
	}{
		{"---\novdb: 1\npublish: [./a]\n---\n", "usable", "usable"},
		{"---\novdb: 2\npublish: [./a]\n---\n", "unusable", "usable"},
		{"---\novdb: \"1\"\npublish: [./a, x]\n---\n", "unusable", "unusable"},
		{"---\npublish: []\n---\n", "absent", "unusable"},
		{"---\novdb:\n---\n", "unusable", "absent"},
	} {
		md, _ := CheckOVDBMd([]byte(c.doc), Directory)
		if state := stateName(md.Version); state != c.version {
			t.Errorf("%q: Version is %s, want %s", c.doc, state, c.version)
		}
		if state := stateName(md.Publish); state != c.publish {
			t.Errorf("%q: Publish is %s, want %s", c.doc, state, c.publish)
		}
	}
	if md, _ := CheckOVDBMd([]byte("# no front matter"), Directory); md.Read || md.Version.Present || md.Publish.Present {
		t.Errorf("an unread OVDB.md says nothing: %+v", md)
	}
	if m, _ := CheckManifest([]byte("- a"), "m.yaml", Directory); m.Read {
		t.Error("an unread manifest says nothing")
	}
}

func stateName[T any](f Fact[T]) string {
	switch {
	case f.Absent():
		return "absent"
	case f.Usable():
		return "usable"
	}
	return "unusable"
}

func TestManifestRefusals(t *testing.T) {
	url := "https://chinookdb.com/ovdb/dbs/chinook"
	for _, c := range []struct {
		name, doc, rule string
	}{
		{"format", edit(t, ownManifest, "ovdb-manifest/draft-1", "ovdb-manifest/v9"), "manifest-format"},
		{"format number", edit(t, ownManifest, "format: ovdb-manifest/draft-1", "format: 1"), "manifest-format"},
		{"id missing", edit(t, ownManifest, "id: chinook\n", ""), "manifest-required"},
		{"id blank", edit(t, ownManifest, "id: chinook\n", "id: \"\\ufeff\"\n"), "manifest-required"},
		{"id number", edit(t, ownManifest, "id: chinook\n", "id: 7\n"), "manifest-required"},
		{"title null", edit(t, ownManifest, "title: Chinook music store\n", "title:\n"), "manifest-required"},
		{"url missing", edit(t, ownManifest, "url: "+url+"\n", ""), "manifest-required"},
		{"url empty", edit(t, ownManifest, "url: "+url+"\n", "url: \"\"\n"), "manifest-required"},
		{"url number", edit(t, ownManifest, "url: "+url+"\n", "url: 7\n"), "manifest-url"},
		{"url http", edit(t, ownManifest, "url: https://chinookdb.com/ovdb", "url: http://chinookdb.com/ovdb"), "manifest-url"},
		{"url trailing slash", edit(t, ownManifest, "dbs/chinook\ndeployment", "dbs/chinook/\ndeployment"), "manifest-url"},
		{"url without marker", edit(t, ownManifest, "url: https://chinookdb.com/ovdb/dbs/chinook", "url: https://chinookdb.com/dbs/chinook"), "manifest-url"},
		{"homepage http", edit(t, ownManifest, "https://chinookdb.com/\n", "http://chinookdb.com/\n"), "manifest-homepage"},
		{"homepage null", edit(t, ownManifest, "homepage: https://chinookdb.com/\n", "homepage:\n"), "manifest-homepage"},
		{"homepage list", edit(t, ownManifest, "homepage: https://chinookdb.com/\n", "homepage: [a]\n"), "manifest-homepage"},
		{"deployment url port", edit(t, ownManifest, "https://cloud.openvaultdb.com/ovdb/dbs/chinook\n  engine", "https://cloud.openvaultdb.com:8443/ovdb/dbs/chinook\n  engine"), "manifest-url"},
		{"engine", edit(t, ownManifest, "engine: sqlite", "engine: 9db"), "manifest-engine"},
		{"engine missing", edit(t, ownManifest, "  engine: sqlite\n", ""), "manifest-required"},
		{"discovery origin", edit(t, ownManifest, "discovery: https://chinookdb.com/", "discovery: https://cloud.openvaultdb.com/"), "manifest-discovery"},
		{"discovery missing", edit(t, ownManifest, "  discovery: https://chinookdb.com/.well-known/openvaultdb\n", ""), "manifest-required"},
		{"recordset page without name", edit(t, ownManifest, "collections/{name}", "collections/x"), "manifest-url"},
		{"deployment missing", strings.Replace(ownManifest, ownManifest[strings.Index(ownManifest, "deployment:"):strings.Index(ownManifest, "model:")], "", 1), "manifest-required"},
		{"hcl suffix", edit(t, ownManifest, "chinook.modelspec.hcl", "chinook.hcl"), "manifest-model"},
		{"hcl number", edit(t, ownManifest, "hcl: model/chinook.modelspec.hcl", "hcl: 3"), "manifest-model"},
		{"modelspec missing", edit(t, ownManifest, "  modelspec: model/chinook.modelspec.json\n", ""), "manifest-required"},
		{"own model address bad", edit(t, ownManifest, "datatug/chinookdb/chinook\n", "datatug/chinookdb\n"), "manifest-model"},
		{"own meaning address", edit(t, ownManifest, "  file: model/chinook.meaning.yaml\n", "  address: meaning://github.com/datatug/chinookdb?ref="+pin+"\n  file: model/chinook.meaning.yaml\n"), "manifest-form"},
		{"own partial", edit(t, ownManifest, "recordsets:", "recordsets_partial: true\nrecordsets:"), "manifest-form"},
		{"meaning file missing", edit(t, ownManifest, "  file: model/chinook.meaning.yaml\n", ""), "manifest-required"},
		{"meaning file escapes", edit(t, ownManifest, "file: model/chinook.meaning.yaml", "file: ../x.yaml"), "manifest-meaning"},
		{"graph id missing", edit(t, ownManifest, "    id: chinook\n", ""), "manifest-required"},
		{"graph address not meaning", edit(t, ownManifest, "address: meaning://github.com/datatug/chinookdb\n", "address: https://x\n"), "manifest-meaning"},
		{"own licence missing", edit(t, ownManifest, "  model: MIT\n", ""), "manifest-required"},
		{"publisher name missing", edit(t, ownManifest, "  name: DataTug\n", ""), "manifest-required"},
		{"publisher url", edit(t, ownManifest, "url: https://github.com/datatug\n", "url: http://github.com/datatug\n"), "manifest-url"},
		{"licence data missing", edit(t, ownManifest, "  data: MIT\n", ""), "manifest-required"},
		{"licence shape", edit(t, ownManifest, "data: MIT", "data: see the README"), "manifest-licence"},
		{"recordsets empty", edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: []\n"), "manifest-recordsets"},
		{"recordsets mapping", edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: {a: b}\n"), "manifest-recordsets"},
		{"recordsets number", edit(t, ownManifest, "  - Artist\n", "  - 5\n"), "manifest-recordsets"},
		{"recordsets blank", edit(t, ownManifest, "  - Artist\n", "  - \" \"\n"), "manifest-recordsets"},
		{"not a mapping", "- a\n- b\n", "manifest-shape"},
		{"scalar", "just text\n", "manifest-shape"},
		{"empty", "", "manifest-shape"},
		{"anchor", edit(t, ownManifest, "id: chinook", "id: &a chinook"), "yaml-anchor"},
		{"tab", edit(t, ownManifest, "  engine: sqlite", "\tengine: sqlite"), "yaml"},
		{"duplicate key", ownManifest + "id: other\n", "yaml-duplicate-key"},
		{"shared no model", edit(t, sharedManifest, "model:\n  address: modelspec://github.com/datatug/chinookdb/chinook?ref="+pin+"\n", ""), "manifest-model"},
		{"shared model unpinned", edit(t, sharedManifest, "chinook?ref="+pin, "chinook"), "manifest-model"},
		{"shared model bad", edit(t, sharedManifest, "modelspec://github.com/datatug/chinookdb/chinook?ref="+pin, "modelspec://github.com/datatug/chinookdb?ref="+pin), "manifest-model"},
		{"shared model number", edit(t, sharedManifest, "address: modelspec://github.com/datatug/chinookdb/chinook?ref="+pin, "address: 4"), "manifest-model"},
		{"shared meaning missing", edit(t, sharedManifest, "  address: meaning://github.com/datatug/chinookdb?ref="+pin+"\n", ""), "manifest-meaning"},
		{"shared meaning unpinned", edit(t, sharedManifest, "chinookdb?ref="+pin+"\n  file", "chinookdb\n  file"), "manifest-meaning"},
		{"shared meaning bad", edit(t, sharedManifest, "meaning://github.com/datatug/chinookdb?ref="+pin, "https://github.com/datatug/chinookdb?ref="+pin), "manifest-meaning"},
		{"shared file", edit(t, sharedManifest, "file: model/chinook.meaning.yaml", "file: /abs.yaml"), "manifest-meaning"},
		{"shared graph address", edit(t, sharedManifest, "    id: chinook\n", "    id: chinook\n    address: https://x\n"), "manifest-meaning"},
		{"shared licence", edit(t, sharedManifest, "licences:\n  data: MIT", "licences:\n  data: MIT\n  model: not a licence"), "manifest-licence"},
		{"shared licence null", edit(t, sharedManifest, "licences:\n  data: MIT", "licences:\n  data: MIT\n  meaning:"), "manifest-licence"},
		{"shared partial string", edit(t, sharedManifest, "recordsets:", "recordsets_partial: yes\nrecordsets:"), "manifest-form"},
	} {
		r := Check([]byte(goodMD), "ovdb.yaml", []byte(c.doc), Directory)
		if r.OK() || !slices.Contains(rulesOf(r.Findings), c.rule) {
			t.Errorf("%s: want rule %s, got %v", c.name, c.rule, rulesOf(r.Findings))
		}
		for _, f := range r.Findings {
			if f.Severity != SeverityError || f.Document == "" || f.Message == "" || !printable(f.Message) || strings.Contains(f.String(), "\n") {
				t.Errorf("%s: finding %+v", c.name, f)
			}
		}
	}
}

func TestOVDBMdRefusals(t *testing.T) {
	for _, c := range []struct {
		name, doc, rule string
	}{
		{"no front matter", "# title\n", "ovdbmd-frontmatter"},
		{"empty", "", "ovdbmd-frontmatter"},
		{"BOM", "\ufeff---\novdb: 1\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-frontmatter"},
		{"no line end after opener", "---", "ovdbmd-frontmatter"},
		{"never closed", "---\novdb: 1\npublish: [./ovdb.yaml]\n", "ovdbmd-frontmatter"},
		{"closer with text", "---\novdb: 1\npublish: [./ovdb.yaml]\n---x\n", "ovdbmd-frontmatter"},
		{"empty front matter", "---\n\n---\n", "ovdbmd-frontmatter"},
		{"list front matter", "---\n- a\n---\n", "ovdbmd-frontmatter"},
		{"yaml error", "---\novdb: &a 1\n---\n", "yaml-anchor"},
		{"version string", "---\novdb: \"1\"\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-version"},
		{"version 2", "---\novdb: 2\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-version"},
		{"version missing", "---\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-version"},
		{"version float", "---\novdb: 1.5\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-version"},
		{"version bool", "---\novdb: true\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-version"},
		{"version mapping", "---\novdb: {a: b}\npublish: [./ovdb.yaml]\n---\n", "ovdbmd-version"},
		{"publish missing", "---\novdb: 1\n---\n", "ovdbmd-publish"},
		{"publish empty", "---\novdb: 1\npublish: []\n---\n", "ovdbmd-publish"},
		{"publish string", "---\novdb: 1\npublish: ./ovdb.yaml\n---\n", "ovdbmd-publish"},
		{"entry without dot", "---\novdb: 1\npublish: [ovdb.yaml]\n---\n", "ovdbmd-entry"},
		{"entry glob", "---\novdb: 1\npublish: [\"./*.yaml\"]\n---\n", "ovdbmd-entry"},
		{"entry escape", "---\novdb: 1\npublish: [./../x.yaml]\n---\n", "ovdbmd-entry"},
		{"entry number", "---\novdb: 1\npublish: [1]\n---\n", "ovdbmd-entry"},
		{"entry blank", "---\novdb: 1\npublish: [\" \"]\n---\n", "ovdbmd-entry"},
	} {
		_, findings := CheckOVDBMd([]byte(c.doc), Directory)
		if len(findings) == 0 || !slices.Contains(rulesOf(findings), c.rule) {
			t.Errorf("%s: want rule %s, got %v", c.name, c.rule, findings)
		}
	}
	// What is accepted: CRLF, an empty body, unknown keys, repeated entries (the Directory reads a set), more than one manifest.
	for name, doc := range map[string]string{
		"crlf": "---\r\novdb: 1\r\npublish: [./ovdb.yaml]\r\n---\r\n# t\r\n", "end of file": "---\novdb: 1\npublish: [./ovdb.yaml]\n---", "unknown key": "---\novdb: 1\nextra: 2\npublish: [./ovdb.yaml]\n---\n",
		"repeated": "---\novdb: 1\npublish: [./ovdb.yaml, ./ovdb.yaml]\n---\n", "two": "---\novdb: 1\npublish:\n  - ./a.yaml\n  - ./sub/b.yaml\n---\nbody\n---\nmore\n",
		"dashes in body": "---\novdb: 1\npublish: [./ovdb.yaml]\n---\n---\n",
	} {
		if md, findings := CheckOVDBMd([]byte(doc), Directory); len(findings) != 0 || !md.Publish.Usable() {
			t.Errorf("%s: %v", name, findings)
		}
	}
	md, findings := CheckOVDBMd([]byte("---\novdb: 1\npublish: [./a.yaml, x, ./sub/b.yaml]\n---\n"), Directory)
	if len(findings) != 1 || !slices.Equal(md.Entries, []string{"a.yaml", "sub/b.yaml"}) || md.Publish.Usable() || md.Publish.Value != nil || findings[0].Line != 3 {
		t.Errorf("a bad entry among good ones: %v %+v", findings, md)
	}
	if _, findings := CheckOVDBMd([]byte("---\novdb: 2\npublish: [./ovdb.yaml]\n---\n"), Directory); len(findings) != 1 || findings[0].Line != 2 || findings[0].Document != "OVDB.md" {
		t.Errorf("the line of ovdb: %v", findings)
	}
	if _, findings := CheckOVDBMd([]byte("---\novdb: &a 1\n---\n"), Directory); len(findings) != 1 || findings[0].Line != 2 {
		t.Errorf("a reader finding is placed in OVDB.md, after the --- line: %v", findings)
	}
}

func TestUnlistedAndBounds(t *testing.T) {
	r := Check([]byte(goodMD), "other.yaml", []byte(ownManifest), Directory)
	if r.OK() || !slices.Contains(rulesOf(r.Findings), "ovdbmd-unlisted") || !strings.Contains(r.Findings[0].Message, `"./other.yaml"`) {
		t.Errorf("an unlisted manifest: %v", r.Findings)
	}
	if r := Check([]byte("---\novdb: 1\npublish: []\n---\n"), "x.yaml", []byte(ownManifest), Directory); slices.Contains(rulesOf(r.Findings), "ovdbmd-unlisted") {
		t.Errorf("a list that is not valid is not also unlisted: %v", r.Findings)
	}
	big := strings.Repeat("a", MaxDocumentBytes+1)
	if _, f := CheckOVDBMd([]byte(big), Directory); len(f) != 1 || f[0].Rule != "document-size" {
		t.Errorf("a big OVDB.md: %v", f)
	}
	if _, f := CheckManifest([]byte(big), "ovdb.yaml", Directory); len(f) != 1 || f[0].Rule != "document-size" || f[0].Document != "ovdb.yaml" {
		t.Errorf("a big manifest: %v", f)
	}
	exact := "# " + strings.Repeat("a", MaxDocumentBytes-3) + "\n" + ownManifest[:0]
	if _, f := CheckManifest([]byte(exact), "ovdb.yaml", Directory); slices.Contains(rulesOf(f), "document-size") {
		t.Errorf("a document of exactly the bound: %v", f)
	}
	if _, f := CheckOVDBMd(nil, Directory); len(f) != 1 {
		t.Errorf("empty: %v", f)
	}
	if got := listed(nil); got != "nothing" {
		t.Errorf("listed(nil) = %q", got)
	}
}

func TestFindingsAreCapped(t *testing.T) {
	b := newBudget()
	c := newCollector("x", b)
	for i := 0; i < MaxFindings+7; i++ {
		c.add("r", i, "problem %d", i)
	}
	got := append(c.findings, b.notice("x")...)
	if len(got) != MaxFindings+1 || got[MaxFindings].Rule != RuleCapped || !strings.Contains(got[MaxFindings].Message, "7 more") {
		t.Errorf("%d findings, last %+v", len(got), got[len(got)-1])
	}
	fb := newBudget()
	few := newCollector("x", fb)
	few.add("r", 0, "one")
	if got := append(few.findings, fb.notice("x")...); len(got) != 1 || got[0].String() != "x: one" {
		t.Errorf("%v", got)
	}
	if f := (Finding{Document: "d", Line: 4, Message: "m"}); f.String() != "d:4: m" {
		t.Errorf("%s", f)
	}
	// A manifest with many problems is capped and still refused.
	var doc strings.Builder
	doc.WriteString("recordsets: []\n")
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&doc, "k%d: 1\n", i)
	}
	if _, f := CheckManifest([]byte(doc.String()), "m.yaml", Directory); len(f) == 0 || len(f) > MaxFindings+1 {
		t.Errorf("%d findings", len(f))
	}
}

func TestPlainAndDescribe(t *testing.T) {
	if got := plain("a\nb\x1b[0m\u00e9"); got != `a\nb\x1b[0m\u00e9` || !printable(got) {
		t.Errorf("plain = %q", got)
	}
	if got := plain(strings.Repeat("x", 400)); len(got) != 303 {
		t.Errorf("plain of a long message = %d bytes", len(got))
	}
	root, _ := parseYAML([]byte("a: ~\nb: x\nc: 1\nd: true\ne: {k: v}\nf: [1]\n"))
	for key, want := range map[string]string{"a": "null", "b": `"x"`, "c": `"1" (a number, not text)`, "d": `"true" (a boolean, not text)`, "e": "a mapping", "f": "a list", "zz": "nothing (the key is missing)"} {
		if got := describe(root.Field(key)); got != want {
			t.Errorf("describe(%s) = %q, want %q", key, got, want)
		}
	}
	if where(nil, "x") != 1 || where(root, "zz") != 1 || where(root, "c") != 3 {
		t.Error("where")
	}
	if isText(root.Field("c")) || !isText(root.Field("b")) || isText(nil) {
		t.Error("isText")
	}
}

func TestFrontMatter(t *testing.T) {
	for doc, want := range map[string]string{
		"---\nA\n---\n": "A", "---\r\nA\r\n---\r\n": "A", "---\nA\n---": "A", "---\n\n---\n": "", "---\nA\n\n---\nB\n---\n": "A\n", "---\r\nA\n---\r\nrest": "A", "---\nA\r\n---\n": "A",
	} {
		if got, ok := frontMatter([]byte(doc)); !ok || string(got) != want {
			t.Errorf("frontMatter(%q) = %q, %v, want %q", doc, got, ok, want)
		}
	}
	for _, doc := range []string{"", "---", "--", "-- -\nA\n---\n", "---\n---\n", "---\nA\n---x", "---\nA\n--", "---\nA\r\n---\r", " ---\nA\n---\n", "---\rA\n---\n", "---\nA\n----\n"} {
		if got, ok := frontMatter([]byte(doc)); ok {
			t.Errorf("frontMatter(%q) = %q, want none", doc, got)
		}
	}
}

func TestAddresses(t *testing.T) {
	for s, want := range map[string]Address{
		"modelspec://github.com/o/r/m":                     {"modelspec://github.com/o/r/m", "github.com/o/r", "m", ""},
		"modelspec://example.com/o.x/r_y-z/_M1?ref=" + pin: {"modelspec://example.com/o.x/r_y-z/_M1?ref=" + pin, "example.com/o.x/r_y-z", "_M1", pin},
		"modelspec://github.com/./../m":                    {"modelspec://github.com/./../m", "github.com/./..", "m", ""},
	} {
		if got, ok := parseModelAddress(s); !ok || got != want {
			t.Errorf("parseModelAddress(%q) = %+v, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "modelspec://github.com/o/r", "modelspec://github.com/o/r/m/x", "modelspec://github.com/o/r/1m", "modelspec://github.com/o/r/m.x", "modelspec://git_hub.com/o/r/m",
		"modelspec://github.com//r/m", "modelspec://github.com/o/r/m?ref=", "modelspec://github.com/o/r/m?ref=" + pin[:39], "modelspec://github.com/o/r/m?ref=" + strings.ToUpper(pin), "modelspec://github.com/o/r/m?ref=" + pin + "?ref=" + pin,
		"modelspec://github.com/o/r/m?x=1", "meaning://github.com/o/r/m", " modelspec://github.com/o/r/m", "modelspec://github.com/o/r/m\n", "modelspec://github.com/o/r/\u00e9",
		"modelspec://github.com/o/r/" + strings.Repeat("m", rules.MaxURLLength)} {
		if got, ok := parseModelAddress(s); ok {
			t.Errorf("parseModelAddress(%s) = %+v", rules.Quote(s), got)
		}
	}
	if got, ok := parseGraphAddress("meaning://github.com/o/r?ref=" + pin); !ok || got != (Address{"meaning://github.com/o/r?ref=" + pin, "github.com/o/r", "", pin}) {
		t.Errorf("parseGraphAddress = %+v, %v", got, ok)
	}
	for _, s := range []string{"meaning://github.com/o", "meaning://github.com/o/r/m", "modelspec://github.com/o/r"} {
		if _, ok := parseGraphAddress(s); ok {
			t.Errorf("parseGraphAddress(%q)", s)
		}
	}
}

func TestOvdbMarker(t *testing.T) {
	for _, c := range []struct {
		host, path string
		want       bool
	}{
		{"acme.com", "/ovdb/sales", true}, {"acme.com", "/data/ovdb/sales", true}, {"ovdb.acme.com", "/sales", true}, {"x.ovdb.acme.co.uk", "/sales", true}, {"ovdb.acme.co.uk", "/sales", true},
		{"acme.com", "/ovdbx/sales", false}, {"acme.com", "/xovdb/sales", false}, {"ovdb.com", "/sales", false}, {"ovdb.co.uk", "/sales", false}, {"ovdb.com.au", "/sales", false},
		{"acme.ovdb", "/sales", false}, {"notovdb.acme.com", "/sales", false}, {"ovdb.github.io", "/x", true}, {"co.uk", "/x", false}, {"a.b.c.d", "/ovdb", true}, {"x", "/x", false},
	} {
		if got := hasOvdbMarker(rules.URL{Host: c.host, Path: c.path}); got != c.want {
			t.Errorf("hasOvdbMarker(%s%s) = %v", c.host, c.path, got)
		}
	}
}

func TestUnreadableReaderOutput(t *testing.T) {
	c := newCollector("d", newBudget())
	readDocument(c, []byte("a: &x 1\n"), 4)
	if len(c.findings) != 1 || c.findings[0].Line != 5 || c.findings[0].Rule != "yaml-anchor" || !printable(c.findings[0].Message) {
		t.Errorf("%+v", c.findings)
	}
	c = newCollector("d", newBudget())
	readDocument(c, []byte("\xff"), 4)
	if len(c.findings) != 1 || !printable(c.findings[0].Message) || c.findings[0].Line > 5 {
		t.Errorf("%+v", c.findings)
	}
}

// A finding names the line where the value starts (see the README): a finding
// about a key that is not written names the first line of the mapping that lacks it.
func TestFindingLines(t *testing.T) {
	for _, c := range []struct {
		name, doc, rule string
		line            int
	}{
		{"format", edit(t, ownManifest, "ovdb-manifest/draft-1", "ovdb-manifest/v9"), "manifest-format", 1},
		{"format missing", edit(t, ownManifest, "format: ovdb-manifest/draft-1\n", ""), "manifest-format", 1},
		{"id blank", edit(t, ownManifest, "id: chinook\n", "id: \" \"\n"), "manifest-required", 2},
		{"title missing", edit(t, ownManifest, "title: Chinook music store\n", ""), "manifest-required", 1},
		{"homepage", edit(t, ownManifest, "https://chinookdb.com/\n", "http://chinookdb.com/\n"), "manifest-homepage", 5},
		{"url trailing slash", edit(t, ownManifest, "dbs/chinook\ndeployment", "dbs/chinook/\ndeployment"), "manifest-url", 6},
		{"deployment url", edit(t, ownManifest, "https://cloud.openvaultdb.com/ovdb/dbs/chinook\n  engine", "https://cloud.openvaultdb.com:8443/ovdb/dbs/chinook\n  engine"), "manifest-url", 8},
		{"engine", edit(t, ownManifest, "engine: sqlite", "engine: 9db"), "manifest-engine", 9},
		{"discovery origin", edit(t, ownManifest, "discovery: https://chinookdb.com/", "discovery: https://cloud.openvaultdb.com/"), "manifest-discovery", 10},
		{"recordset page", edit(t, ownManifest, "collections/{name}", "collections/x"), "manifest-url", 11},
		{"own model address", edit(t, ownManifest, "datatug/chinookdb/chinook\n", "datatug/chinookdb\n"), "manifest-model", 13},
		{"modelspec missing", edit(t, ownManifest, "  modelspec: model/chinook.modelspec.json\n", ""), "manifest-required", 13},
		{"hcl", edit(t, ownManifest, "chinook.modelspec.hcl", "chinook.hcl"), "manifest-model", 15},
		{"meaning address in own form", edit(t, ownManifest, "  file: model/chinook.meaning.yaml\n", "  address: meaning://github.com/datatug/chinookdb?ref="+pin+"\n  file: model/chinook.meaning.yaml\n"), "manifest-form", 17},
		{"meaning file", edit(t, ownManifest, "file: model/chinook.meaning.yaml", "file: ../x.yaml"), "manifest-meaning", 17},
		{"graph address", edit(t, ownManifest, "address: meaning://github.com/datatug/chinookdb\n", "address: https://x\n"), "manifest-meaning", 20},
		{"publisher url", edit(t, ownManifest, "url: https://github.com/datatug\n", "url: http://github.com/datatug\n"), "manifest-url", 23},
		{"publisher repository", edit(t, ownManifest, "chinookdb\nlicences", "chinookdb/x\nlicences"), "manifest-publisher", 24},
		{"licence", edit(t, ownManifest, "data: MIT", "data: see the README"), "manifest-licence", 26},
		{"own licence missing", edit(t, ownManifest, "  model: MIT\n", ""), "manifest-required", 26},
		{"partial in own form", edit(t, ownManifest, "recordsets:", "recordsets_partial: true\nrecordsets:"), "manifest-form", 29},
		{"recordsets empty", edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: []\n"), "manifest-recordsets", 29},
		{"recordsets twice", edit(t, ownManifest, "  - Artist\n", "  - Album\n"), "manifest-recordsets", 31},
		{"model name", edit(t, ownManifest, "model:\n", "model:\n  name: 5\n"), "manifest-model", 13},
		{"anchor", edit(t, ownManifest, "id: chinook", "id: &a chinook"), "yaml-anchor", 2},
		{"own address with a pin", edit(t, ownManifest, "datatug/chinookdb/chinook\n", "datatug/chinookdb/chinook?ref="+pin+"\n"), "manifest-model", 13},
		{"own address on another host", edit(t, ownManifest, "modelspec://github.com/", "modelspec://gitlab.com/"), "manifest-model", 13},
		{"own address in upper case", edit(t, ownManifest, "datatug/chinookdb/chinook\n", "DataTug/chinookdb/chinook\n"), "manifest-model", 13},
		{"shared model address on another host", edit(t, sharedManifest, "modelspec://github.com/", "modelspec://gitlab.com/"), "manifest-model", 11},
		{"shared meaning address on another host", edit(t, sharedManifest, "meaning://github.com/", "meaning://gitlab.com/"), "manifest-meaning", 13},
		{"shared model", edit(t, sharedManifest, "chinook?ref="+pin, "chinook"), "manifest-model", 11},
		{"shared meaning", edit(t, sharedManifest, "chinookdb?ref="+pin+"\n  file", "chinookdb\n  file"), "manifest-meaning", 13},
		{"shared spelling", edit(t, sharedManifest, "meaning://github.com/datatug/chinookdb?ref="+pin, "meaning://github.com/DataTug/chinookdb?ref="+pin), "manifest-meaning", 13},
		{"shared partial", edit(t, sharedManifest, "recordsets:", "recordsets_partial: yes\nrecordsets:"), "manifest-form", 22},
	} {
		_, findings := CheckManifest([]byte(c.doc), "ovdb.yaml", Directory)
		ok := false
		for _, f := range findings {
			ok = ok || f.Rule == c.rule && f.Line == c.line
		}
		if !ok {
			t.Errorf("%s: want rule %s at line %d, got %v", c.name, c.rule, c.line, findings)
		}
	}
}

func TestProfileAndDocumentNames(t *testing.T) {
	for _, profile := range []Profile{Profile(7), Profile(-1), Publisher + 1} {
		r := Check([]byte(goodMD), "ovdb.yaml", []byte(ownManifest), profile)
		if r.OK() || len(r.Findings) != 1 || r.Findings[0].Rule != RuleProfile || r.Manifest.Read || r.OVDBMd.Read {
			t.Errorf("profile %d: %+v", profile, r)
		}
		if md, f := CheckOVDBMd([]byte(goodMD), profile); md.Read || len(f) != 1 || f[0].Rule != RuleProfile {
			t.Errorf("CheckOVDBMd with profile %d: %+v %v", profile, md, f)
		}
		if m, f := CheckManifest([]byte(ownManifest), "ovdb.yaml", profile); m.Read || len(f) != 1 || f[0].Rule != RuleProfile || !printable(f[0].String()) {
			t.Errorf("CheckManifest with profile %d: %+v %v", profile, m, f)
		}
	}
	// A path with an escape sequence and a line break prints as one printable line.
	r := Check([]byte(goodMD), "ovdb\x1b[31m.yaml\n", []byte("- a"), Directory)
	if r.OK() {
		t.Fatal("accepted")
	}
	for _, f := range r.Findings {
		if !printable(f.String()) || strings.Contains(f.String(), "\x1b") && !strings.Contains(f.String(), `\x1b`) {
			t.Errorf("String() = %q", f.String())
		}
	}
	long := Finding{Document: strings.Repeat("d", 500), Message: "m"}
	if len(long.String()) > 300 {
		t.Errorf("a long document name is cut: %d bytes", len(long.String()))
	}
	if got := (Finding{Document: "plain.yaml", Line: 3, Message: "m"}).String(); got != "plain.yaml:3: m" {
		t.Errorf("a plain name is not quoted: %q", got)
	}
}

// One cap for a call, whatever the number of documents, the notice last; one
// message is as long as MaxMessageBytes and no more.
func TestOneCapAndBoundedMessages(t *testing.T) {
	// The reviewer's input: 101 bad entries and a manifest with many problems.
	md := "---\novdb: 1\npublish:\n" + strings.Repeat("  - x\n", 101) + "---\n"
	r := Check([]byte(md), "ovdb.yaml", []byte("format: 1\n"), Directory)
	if len(r.Findings) != MaxFindings+1 || r.Findings[MaxFindings].Rule != RuleCapped {
		t.Fatalf("%d findings, last %+v", len(r.Findings), r.Findings[len(r.Findings)-1])
	}
	for i, f := range r.Findings[:MaxFindings] {
		if f.Rule == RuleCapped || f.Rule != "ovdbmd-entry" {
			t.Fatalf("finding %d is %s", i, f.Rule)
		}
	}
	assertFindings(t, r.Findings)
	if !strings.Contains(r.Findings[MaxFindings].Message, " more findings") {
		t.Errorf("the notice: %q", r.Findings[MaxFindings].Message)
	}
	// With fewer problems in OVDB.md, the manifest's fill the rest of one budget.
	md = "---\novdb: 1\npublish: [./ovdb.yaml]\n---\n"
	r = Check([]byte(md), "ovdb.yaml", []byte("format: 1\n"), Directory)
	if len(r.Findings) < 10 || len(r.Findings) > MaxFindings {
		t.Errorf("%d findings", len(r.Findings))
	}
	// The unlisted message names a few entries however many there are.
	for _, entry := range []string{"./a", "./%d"} {
		var list []string
		for i := 0; i < 20000; i++ {
			list = append(list, strings.ReplaceAll(entry, "%d", fmt.Sprint(i)))
		}
		doc := "---\novdb: 1\npublish: [" + strings.Join(list, ",") + "]\n---\n"
		r := Check([]byte(doc), "ovdb.yaml", []byte(ownManifest), Directory)
		if len(r.Findings) != 1 || r.Findings[0].Rule != "ovdbmd-unlisted" || len(r.Findings[0].Message) > MaxMessageBytes || !strings.Contains(r.Findings[0].Message, " more") && entry != "./a" {
			t.Fatalf("%d findings: %.300v", len(r.Findings), r.Findings)
		}
	}
	// Whatever is long is cut where the finding is made.
	c := newCollector("d", newBudget())
	c.add("r", 1, "%s", strings.Repeat("x", 5000))
	if got := c.findings[0].Message; len(got) != MaxMessageBytes || !strings.HasSuffix(got, "...") {
		t.Errorf("a message of %d bytes", len(got))
	}
}

// publish is a set in first-seen order; what is written twice is kept apart.
func TestPublishIsASet(t *testing.T) {
	md, findings := CheckOVDBMd([]byte("---\novdb: 1\npublish: [./b.yaml, ./a.yaml, ./b.yaml, ./b.yaml, ./c.yaml, ./a.yaml]\n---\n"), Directory)
	if len(findings) != 0 || !slices.Equal(md.Entries, []string{"b.yaml", "a.yaml", "c.yaml"}) || !slices.Equal(md.Publish.Value, md.Entries) || !slices.Equal(md.Repeated, []string{"b.yaml", "a.yaml"}) {
		t.Errorf("%v %+v", findings, md)
	}
	md, _ = CheckOVDBMd([]byte("---\novdb: 1\npublish: [./a.yaml]\n---\n"), Directory)
	if md.Repeated != nil {
		t.Errorf("%+v", md)
	}
}

// The lines and the order of the findings of a call that has both documents.
func TestLinesOfOVDBMdFindingsAndTheNoticePosition(t *testing.T) {
	r := Check([]byte(goodMD), "other.yaml", []byte(ownManifest), Directory)
	if len(r.Findings) != 1 || r.Findings[0].Rule != "ovdbmd-unlisted" || r.Findings[0].Line != 3 || r.Findings[0].Document != "OVDB.md" {
		t.Errorf("unlisted: %v", r.Findings)
	}
	multi := "---\novdb: 1\npublish:\n  - ./a.yaml\n  - ./b.yaml\n---\n"
	if r := Check([]byte(multi), "c.yaml", []byte(ownManifest), Directory); len(r.Findings) != 1 || r.Findings[0].Line != 4 {
		t.Errorf("unlisted, block list: %v", r.Findings)
	}
	for doc, want := range map[string]int{
		"---\novdb: 1\npublish: []\n---\n":       3,
		"---\novdb: 1\npublish: x\n---\n":        3,
		"---\novdb: 1\n---\n":                    2,
		"---\novdb: 1\npublish:\n---\n":          3,
		"---\nextra: 1\novdb: 1\n---\n":          2,
		"---\novdb: 1\npublish: [./a, x]\n---\n": 3,
	} {
		_, findings := CheckOVDBMd([]byte(doc), Directory)
		ok := false
		for _, f := range findings {
			ok = ok || f.Line == want
		}
		if !ok {
			t.Errorf("%q: want a finding at line %d, got %v", doc, want, findings)
		}
	}
	// 95 findings of OVDB.md and a manifest with many: the manifest's findings fill the budget,
	// and the notice is the last of all, after them.
	md := "---\novdb: 1\npublish:\n" + strings.Repeat("  - x\n", 95) + "---\n"
	r = Check([]byte(md), "ovdb.yaml", []byte("format: 1\n"), Directory)
	if len(r.Findings) != MaxFindings+1 || r.Findings[MaxFindings].Rule != RuleCapped {
		t.Fatalf("%d findings, last %+v", len(r.Findings), r.Findings[len(r.Findings)-1])
	}
	for i, f := range r.Findings[:MaxFindings] {
		if (i < 95) != (f.Document == "OVDB.md") || f.Rule == RuleCapped {
			t.Fatalf("finding %d is %+v: OVDB.md's 95 come first, then the manifest's, then the notice", i, f)
		}
	}
	if r.Findings[MaxFindings].Document != "ovdb.yaml" {
		t.Errorf("the notice is about the call: %+v", r.Findings[MaxFindings])
	}
}

// The ten facts whose written, refused value must stay a present fact: five URLs,
// the format, the recordsets, both addresses (unparsable and not text) and, in the
// shared form, recordsets_partial.
func TestUnusableFactsStayPresent(t *testing.T) {
	type fact func(Manifest) (present, valid bool, value any)
	text := func(f Fact[string]) (bool, bool, any) { return f.Present, f.Valid, f.Value }
	for _, c := range []struct {
		name, doc string
		fact      fact
	}{
		{"url", edit(t, ownManifest, "url: https://chinookdb.com/ovdb/dbs/chinook\n", "url: http://chinookdb.com/ovdb/dbs/chinook\n"), func(m Manifest) (bool, bool, any) { return text(m.URL) }},
		{"url number", edit(t, ownManifest, "url: https://chinookdb.com/ovdb/dbs/chinook\n", "url: 7\n"), func(m Manifest) (bool, bool, any) { return text(m.URL) }},
		{"url empty", edit(t, ownManifest, "url: https://chinookdb.com/ovdb/dbs/chinook\n", "url: \"\"\n"), func(m Manifest) (bool, bool, any) { return text(m.URL) }},
		{"deployment.url", edit(t, ownManifest, "https://cloud.openvaultdb.com/ovdb/dbs/chinook\n  engine", "https://cloud.openvaultdb.com:8443/ovdb/dbs/chinook\n  engine"), func(m Manifest) (bool, bool, any) { return text(m.DeploymentURL) }},
		{"deployment.url number", edit(t, ownManifest, "  url: https://cloud.openvaultdb.com/ovdb/dbs/chinook\n  engine", "  url: 7\n  engine"), func(m Manifest) (bool, bool, any) { return text(m.DeploymentURL) }},
		{"discovery", edit(t, ownManifest, "discovery: https://chinookdb.com/", "discovery: http://chinookdb.com/"), func(m Manifest) (bool, bool, any) { return text(m.Discovery) }},
		{"discovery list", edit(t, ownManifest, "discovery: https://chinookdb.com/.well-known/openvaultdb", "discovery: [a]"), func(m Manifest) (bool, bool, any) { return text(m.Discovery) }},
		{"recordset page", edit(t, ownManifest, "collections/{name}", "collections/x"), func(m Manifest) (bool, bool, any) { return text(m.RecordsetPage) }},
		{"recordset page empty", edit(t, ownManifest, "recordset_page: https://cloud.openvaultdb.com/ovdb/dbs/chinook/collections/{name}", "recordset_page: \"\""), func(m Manifest) (bool, bool, any) { return text(m.RecordsetPage) }},
		{"publisher url", edit(t, ownManifest, "url: https://github.com/datatug\n", "url: http://github.com/datatug\n"), func(m Manifest) (bool, bool, any) { return text(m.PublisherURL) }},
		{"publisher url null", edit(t, ownManifest, "url: https://github.com/datatug\n", "url:\n"), func(m Manifest) (bool, bool, any) { return text(m.PublisherURL) }},
		{"format", edit(t, ownManifest, "ovdb-manifest/draft-1", "ovdb-manifest/v9"), func(m Manifest) (bool, bool, any) { return text(m.Format) }},
		{"format number", edit(t, ownManifest, "format: ovdb-manifest/draft-1", "format: 1"), func(m Manifest) (bool, bool, any) { return text(m.Format) }},
		{"recordsets mapping", edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: {a: b}\n"), func(m Manifest) (bool, bool, any) {
			return m.Recordsets.Present, m.Recordsets.Valid, m.Recordsets.Value
		}},
		{"recordsets string", edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: Album\n"), func(m Manifest) (bool, bool, any) {
			return m.Recordsets.Present, m.Recordsets.Valid, m.Recordsets.Value
		}},
		{"recordsets empty", edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: []\n"), func(m Manifest) (bool, bool, any) {
			return m.Recordsets.Present, m.Recordsets.Valid, m.Recordsets.Value
		}},
		{"recordsets number", edit(t, ownManifest, "  - Artist\n", "  - 5\n"), func(m Manifest) (bool, bool, any) {
			return m.Recordsets.Present, m.Recordsets.Valid, m.Recordsets.Value
		}},
		{"model address unparsable", edit(t, ownManifest, "datatug/chinookdb/chinook\n", "datatug/chinookdb\n"), func(m Manifest) (bool, bool, any) {
			return m.ModelAddress.Present, m.ModelAddress.Valid, m.ModelAddress.Value
		}},
		{"model address number", edit(t, ownManifest, "address: modelspec://github.com/datatug/chinookdb/chinook\n", "address: 4\n"), func(m Manifest) (bool, bool, any) {
			return m.ModelAddress.Present, m.ModelAddress.Valid, m.ModelAddress.Value
		}},
		{"shared model address unparsable", edit(t, sharedManifest, "chinook?ref="+pin, "?ref="+pin), func(m Manifest) (bool, bool, any) {
			return m.ModelAddress.Present, m.ModelAddress.Valid, m.ModelAddress.Value
		}},
		{"shared meaning address unparsable", edit(t, sharedManifest, "meaning://github.com/datatug/chinookdb?ref="+pin, "meaning://github.com/datatug?ref="+pin), func(m Manifest) (bool, bool, any) {
			return m.MeaningAddress.Present, m.MeaningAddress.Valid, m.MeaningAddress.Value
		}},
		{"shared meaning address number", edit(t, sharedManifest, "address: meaning://github.com/datatug/chinookdb?ref="+pin, "address: 4"), func(m Manifest) (bool, bool, any) {
			return m.MeaningAddress.Present, m.MeaningAddress.Valid, m.MeaningAddress.Value
		}},
		{"recordsets_partial not a boolean", edit(t, sharedManifest, "recordsets:", "recordsets_partial: yes\nrecordsets:"), func(m Manifest) (bool, bool, any) {
			return m.RecordsetsPartial.Present, m.RecordsetsPartial.Valid, m.RecordsetsPartial.Value
		}},
	} {
		m, findings := CheckManifest([]byte(c.doc), "ovdb.yaml", Directory)
		present, valid, value := c.fact(m)
		if !present || valid || len(findings) == 0 || !reflect.DeepEqual(value, reflect.Zero(reflect.TypeOf(value)).Interface()) {
			t.Errorf("%s: present %v valid %v value %v findings %v: want written, not usable, with no value, and a finding", c.name, present, valid, value, findings)
		}
	}
}
