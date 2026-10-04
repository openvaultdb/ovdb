package manifest

import (
	"fmt"
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

func TestGoodDocuments(t *testing.T) {
	r := Check([]byte(goodMD), "ovdb.yaml", []byte(ownManifest), Directory)
	if !r.OK() || r.Profile != Directory {
		t.Fatalf("findings: %v", r.Findings)
	}
	m := r.Manifest
	if m.Form != FormOwn || m.ID != "chinook" || m.URL != "https://chinookdb.com/ovdb/dbs/chinook" || m.Engine != "sqlite" || m.ModelSpecPath != "model/chinook.modelspec.json" ||
		m.ModelHCLPath != "model/chinook.modelspec.hcl" || m.MeaningFile != "model/chinook.meaning.yaml" || m.ModelRepository != "github.com/datatug/chinookdb" || m.ModelModule != "chinook" ||
		m.GraphID != "chinook" || m.GraphAddress != "meaning://github.com/datatug/chinookdb" || m.LicenceData != "MIT" || m.LicenceModel != "MIT" || m.LicenceMeaning != "CC0-1.0" ||
		m.PublisherRepository != "https://github.com/datatug/chinookdb" || !slices.Equal(m.Recordsets, []string{"Album", "Artist"}) || m.Homepage != "https://chinookdb.com/" ||
		m.RecordsetPage == "" || m.Title == "" || m.Description == "" || m.PublisherName != "DataTug" || m.PublisherURL != "https://github.com/datatug" || m.DeploymentURL == "" || m.Discovery == "" {
		t.Errorf("facts: %+v", m)
	}
	if !slices.Equal(r.OVDBMd.Publish, []string{"ovdb.yaml"}) || !r.OVDBMd.Valid || !r.OVDBMd.Lists("ovdb.yaml") || r.OVDBMd.Lists("other.yaml") || r.OVDBMd.PublishLine != 3 {
		t.Errorf("OVDB.md facts: %+v", r.OVDBMd)
	}

	r = Check([]byte(goodMD), "ovdb.yaml", []byte(sharedManifest), Directory)
	if !r.OK() {
		t.Fatalf("shared findings: %v", r.Findings)
	}
	m = r.Manifest
	if m.Form != FormShared || m.ModelRef != pin || m.MeaningRef != pin || m.MeaningRepository != "github.com/datatug/chinookdb" || m.ModelModule != "chinook" || m.MeaningFile != "model/chinook.meaning.yaml" || m.RecordsetsPartial {
		t.Errorf("shared facts: %+v", m)
	}
	partial := edit(t, sharedManifest, "recordsets:", "recordsets_partial: true\nrecordsets:")
	if r := Check([]byte(goodMD), "ovdb.yaml", []byte(partial), Directory); !r.OK() || !r.Manifest.RecordsetsPartial {
		t.Errorf("partial: %v %+v", r.Findings, r.Manifest)
	}
	for _, ok := range []string{edit(t, sharedManifest, "  file:", "  graph2: x\n  file:"), edit(t, sharedManifest, "    id: chinook\n", "    id: chinook\n    address: meaning://github.com/datatug/chinookdb\n"),
		edit(t, sharedManifest, "licences:\n  data: MIT", "licences:\n  data: MIT\n  model: MIT\n  meaning: CC0-1.0")} {
		if r := Check([]byte(goodMD), "ovdb.yaml", []byte(ok), Directory); !r.OK() {
			t.Errorf("an accepted edit: %v", r.Findings)
		}
	}
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
		{"engine missing", edit(t, ownManifest, "  engine: sqlite\n", ""), "manifest-engine"},
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
		{"meaning file escapes", edit(t, ownManifest, "file: model/chinook.meaning.yaml", "file: ../x.yaml"), "manifest-required"},
		{"graph id missing", edit(t, ownManifest, "    id: chinook\n", ""), "manifest-required"},
		{"graph address not meaning", edit(t, ownManifest, "address: meaning://github.com/datatug/chinookdb\n", "address: https://x\n"), "manifest-required"},
		{"own licence missing", edit(t, ownManifest, "  model: MIT\n", ""), "manifest-required"},
		{"publisher name missing", edit(t, ownManifest, "  name: DataTug\n", ""), "manifest-required"},
		{"publisher url", edit(t, ownManifest, "url: https://github.com/datatug\n", "url: http://github.com/datatug\n"), "manifest-url"},
		{"licence data missing", edit(t, ownManifest, "  data: MIT\n", ""), "manifest-required"},
		{"licence shape", edit(t, ownManifest, "data: MIT", "data: see the README"), "manifest-required"},
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
		{"shared file", edit(t, sharedManifest, "file: model/chinook.meaning.yaml", "file: /abs.yaml"), "manifest-required"},
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

func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
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
		if md, findings := CheckOVDBMd([]byte(doc), Directory); len(findings) != 0 || !md.Valid {
			t.Errorf("%s: %v", name, findings)
		}
	}
	md, findings := CheckOVDBMd([]byte("---\novdb: 1\npublish: [./a.yaml, x, ./sub/b.yaml]\n---\n"), Directory)
	if len(findings) != 1 || !slices.Equal(md.Publish, []string{"a.yaml", "sub/b.yaml"}) || findings[0].Line != 3 {
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
	c := &collector{document: "x"}
	for i := 0; i < MaxFindings+7; i++ {
		c.add("r", i, "problem %d", i)
	}
	got := c.result()
	if len(got) != MaxFindings+1 || got[MaxFindings].Rule != RuleCapped || !strings.Contains(got[MaxFindings].Message, "7 more") {
		t.Errorf("%d findings, last %+v", len(got), got[len(got)-1])
	}
	few := &collector{document: "x"}
	few.add("r", 0, "one")
	if got := few.result(); len(got) != 1 || got[0].String() != "x: one" {
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
	if text(nil) != "" || text(root.Field("c")) != "" || text(root.Field("b")) != "x" || isText(root.Field("c")) || !isText(root.Field("b")) {
		t.Error("text")
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
	for s, want := range map[string]address{
		"modelspec://github.com/o/r/m":                     {"github.com/o/r", "m", ""},
		"modelspec://example.com/o.x/r_y-z/_M1?ref=" + pin: {"example.com/o.x/r_y-z", "_M1", pin},
		"modelspec://github.com/./../m":                    {"github.com/./..", "m", ""},
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
	if got, ok := parseGraphAddress("meaning://github.com/o/r?ref=" + pin); !ok || got != (address{"github.com/o/r", "", pin}) {
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
	c := &collector{document: "d"}
	readDocument(c, []byte("a: &x 1\n"), 4)
	if len(c.findings) != 1 || c.findings[0].Line != 5 || c.findings[0].Rule != "yaml-anchor" || !printable(c.findings[0].Message) {
		t.Errorf("%+v", c.findings)
	}
	c = &collector{document: "d"}
	readDocument(c, []byte("\xff"), 4)
	if len(c.findings) != 1 || !printable(c.findings[0].Message) || c.findings[0].Line > 5 {
		t.Errorf("%+v", c.findings)
	}
}
