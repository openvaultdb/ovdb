package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The reading of ovdb-manifest/draft-2 (decision 0012 of openvaultdb/openvaultdb): an item of recordsets is a name or a map with name, record_type and columns.
// These tests pin each rule of it, in both profiles. The cases that the Directory and the Chinook pre-check run (testdata/reference/manifest-conformance.json)
// are run in conformance_test.go; the corpus of the reference verdicts holds the rest against the references' own code.

// draft2 is the own-form manifest of this package in the second format, with the recordsets the text gives (the lines under `recordsets:`) and, after them,
// whatever else the text adds at the end of the document.
func draft2(t *testing.T, lines string) string {
	t.Helper()
	doc := edit(t, ownManifest, "format: ovdb-manifest/draft-1", "format: ovdb-manifest/draft-2")
	return edit(t, doc, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n"+lines)
}

// judged is what both profiles say of a document: the findings must be the same refusal or acceptance, and the first profile's are returned.
func judged(t *testing.T, doc string) (Manifest, []Finding) {
	t.Helper()
	m, findings := CheckManifest([]byte(doc), "ovdb.yaml", Directory)
	for _, profile := range []Profile{Publisher} {
		if _, other := CheckManifest([]byte(doc), "ovdb.yaml", profile); (len(other) == 0) != (len(findings) == 0) {
			t.Errorf("the Directory profile finds %v, the Publisher profile %v, in\n%s", findings, other, doc)
		}
	}
	return m, findings
}

func messages(findings []Finding) string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule+": "+f.Message)
	}
	return strings.Join(out, "\n")
}

func TestDraft2Accepted(t *testing.T) {
	album, artist := Recordset{Name: "Album", RecordType: "Album"}, Recordset{Name: "Artist", RecordType: "Artist"}
	for _, c := range []struct {
		name, lines string
		want        []Recordset
		partial     bool // the first item's columns, by name, as written
	}{
		{"names", "  - Album\n  - Artist\n", []Recordset{album, artist}, false},
		{"a map with a name alone", "  - name: Album\n  - Artist\n", []Recordset{album, artist}, false},
		{"a record type equal to the name", "  - name: Album\n    record_type: Album\n  - Artist\n", []Recordset{album, artist}, false},
		{"a name that differs from its record type", "  - name: Albums\n    record_type: Album\n  - Artist\n", []Recordset{{Name: "Albums", RecordType: "Album"}, artist}, false},
		{"a native name with a space", "  - name: Order Lines\n    record_type: Album\n  - Artist\n", []Recordset{{Name: "Order Lines", RecordType: "Album"}, artist}, false},
		{"a column", "  - name: Album\n    columns:\n      created:\n        field: CreatedAt\n  - Artist\n", []Recordset{{Name: "Album", RecordType: "Album", Columns: []Column{{Name: "created", Field: "CreatedAt"}}}, artist}, false},
		{"an empty columns", "  - name: Album\n    columns: {}\n  - Artist\n", []Recordset{album, artist}, false},
		{"a column of the field's own name", "  - name: Album\n    columns:\n      Title:\n        field: Title\n  - Artist\n", []Recordset{{Name: "Album", RecordType: "Album", Columns: []Column{{Name: "Title", Field: "Title"}}}, artist}, false},
		{"two columns that swap their fields", "  - name: Album\n    columns:\n      Title:\n        field: Name\n      Name:\n        field: Title\n  - Artist\n", []Recordset{{Name: "Album", RecordType: "Album", Columns: []Column{{Name: "Title", Field: "Name"}, {Name: "Name", Field: "Title"}}}, artist}, false},
		{"a path into a component", "  - name: Album\n    columns:\n      year:\n        field: Created.Year\n  - Artist\n", []Recordset{{Name: "Album", RecordType: "Album", Columns: []Column{{Name: "year", Field: "Created.Year"}}}, artist}, false},
		{"a field that starts with an underscore", "  - name: Album\n    columns:\n      id:\n        field: _id\n  - Artist\n", []Recordset{{Name: "Album", RecordType: "Album", Columns: []Column{{Name: "id", Field: "_id"}}}, artist}, false},
		{"a column whose name is a native name", "  - name: Album\n    columns:\n      'Created At':\n        field: CreatedAt\n  - Artist\n", []Recordset{{Name: "Album", RecordType: "Album", Columns: []Column{{Name: "Created At", Field: "CreatedAt"}}}, artist}, false},
	} {
		m, findings := judged(t, draft2(t, c.lines))
		if len(findings) != 0 {
			t.Errorf("%s: %s", c.name, messages(findings))
			continue
		}
		if !m.Mapping.Usable() || !m.Draft2() || m.RecordsetEntities.Present || len(m.Notices) != 0 {
			t.Errorf("%s: the mapping %+v, draft2 %v, entities %+v, notices %v", c.name, m.Mapping, m.Draft2(), m.RecordsetEntities, m.Notices)
		}
		for i := range m.Mapping.Value {
			m.Mapping.Value[i].Line = 0
			for j := range m.Mapping.Value[i].Columns {
				m.Mapping.Value[i].Columns[j].Line = 0
			}
		}
		if !reflect.DeepEqual(m.Mapping.Value, c.want) {
			t.Errorf("%s: the mapping is %+v, want %+v", c.name, m.Mapping.Value, c.want)
		}
		var names []string
		for _, r := range c.want {
			names = append(names, r.Name)
		}
		usable(t, c.name+": Recordsets", m.Recordsets, names)
		usable(t, c.name+": Format", m.Format, ManifestFormatDraft2)
	}
}

func TestDraft2Refused(t *testing.T) {
	for _, c := range []struct {
		name, doc string
		rules     []string // the rules of the findings, in order
		text      string   // what the first finding says, in part
		names     bool     // Recordsets is still usable: only the columns are refused
	}{
		// the list
		{"not a list", "recordsets: Album\n", []string{"manifest-recordsets"}, "recordsets must be a non-empty list of names or of items with a name", false},
		{"an empty list", "recordsets: []\n", []string{"manifest-recordsets"}, "non-empty list", false},
		{"a null", "recordsets:\n", []string{"manifest-recordsets"}, "non-empty list", false},
		{"a number as an item", "recordsets:\n  - Album\n  - 12\n", []string{"manifest-recordsets"}, `recordsets item 2 must be a name or a map with a name, got "12" (a number, not text)`, false},
		{"a list as an item", "recordsets:\n  - Album\n  - [Artist]\n", []string{"manifest-recordsets"}, "recordsets item 2 must be a name or a map with a name, got a list", false},
		{"a null as an item", "recordsets:\n  - Album\n  - ~\n", []string{"manifest-recordsets"}, "recordsets item 2 must be a name or a map with a name, got null", false},
		{"a blank name as an item", "recordsets:\n  - Album\n  - ' '\n", []string{"manifest-recordsets"}, "recordsets item 2 must be a name or a map with a name", false},
		// the items
		{"a map with no name", "recordsets:\n  - record_type: Album\n  - Artist\n", []string{"manifest-recordsets"}, "recordsets item 1 needs name: the recordset's own name", false},
		{"a map with a blank name", "recordsets:\n  - name: ' '\n  - Artist\n", []string{"manifest-recordsets"}, "recordsets item 1 needs name", false},
		{"a map with a name that is a number", "recordsets:\n  - name: 12\n  - Artist\n", []string{"manifest-recordsets"}, "recordsets item 1 needs name", false},
		{"a mistyped key", "recordsets:\n  - name: Album\n    record_typ: Album\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets item 1 has the key "record_typ"; an item reads only name, record_type and columns`, false},
		{"two mistyped keys", "recordsets:\n  - name: Album\n    a: 1\n    b: 2\n  - Artist\n", []string{"manifest-recordsets", "manifest-recordsets"}, `has the key "a"`, false},
		{"a record type that is not an identifier", "recordsets:\n  - name: Albums\n    record_type: Album-Record\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets "Albums": record_type must be a ModelSpec record type name (letters, digits and _, not starting with a digit), got "Album-Record"`, false},
		{"a record type that is a number", "recordsets:\n  - name: Albums\n    record_type: 12\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets "Albums": record_type must be`, false},
		{"a record type that is empty", "recordsets:\n  - name: Albums\n    record_type: ''\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets "Albums": record_type must be`, false},
		{"a record type that is null", "recordsets:\n  - name: Albums\n    record_type:\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets "Albums": record_type must be`, false},
		{"a record type and no name", "recordsets:\n  - record_type: Album-Record\n  - Artist\n", []string{"manifest-recordsets", "manifest-recordsets"}, "recordsets item 1 needs name", false},
		{"a name that is not a name", "recordsets:\n  - name: Alb/um\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets name "Alb/um" must contain no slash`, false},
		{"a name twice", "recordsets:\n  - Album\n  - name: Album\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets lists a name twice: "Album"`, false},
		{"a name twice with another record type", "recordsets:\n  - Album\n  - name: Album\n    record_type: Artist\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets lists a name twice: "Album"`, false},
		{"a record type twice", "recordsets:\n  - Album\n  - name: albums\n    record_type: Album\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets "Album" and "albums" both have the record type Album; mappings must be one-to-one`, false},
		{"a record type twice, both written", "recordsets:\n  - name: a\n    record_type: Album\n  - name: b\n    record_type: Album\n  - Artist\n", []string{"manifest-recordsets"}, `recordsets "a" and "b" both have the record type Album`, false},
		// the columns
		{"columns that is a list", "recordsets:\n  - name: Album\n    columns: [created]\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": columns must be a map from a column's name to { field: ... }, got a list`, true},
		{"columns that is text", "recordsets:\n  - name: Album\n    columns: created\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": columns must be a map`, true},
		{"columns that is null", "recordsets:\n  - name: Album\n    columns:\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": columns must be a map`, true},
		{"columns on a map with no name", "recordsets:\n  - columns: []\n  - Artist\n", []string{"manifest-recordsets", "manifest-columns"}, "recordsets item 1 needs name", false},
		{"a column written bare", "recordsets:\n  - name: Album\n    columns:\n      created: CreatedAt\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": column "created" must be a map with field: (a column is written as a map, "created: { field: ... }", not as text or a list), got "CreatedAt"`, true},
		{"a column that is a number", "recordsets:\n  - name: Album\n    columns:\n      created: 12\n  - Artist\n", []string{"manifest-columns"}, `column "created" must be a map with field`, true},
		{"a column that is a list", "recordsets:\n  - name: Album\n    columns:\n      created: [CreatedAt]\n  - Artist\n", []string{"manifest-columns"}, `column "created" must be a map with field`, true},
		{"a column that is null", "recordsets:\n  - name: Album\n    columns:\n      created:\n  - Artist\n", []string{"manifest-columns"}, `column "created" must be a map with field`, true},
		{"a column with another key", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: CreatedAt\n        pattern: x\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": column "created" has the key "pattern"; a column reads only field`, true},
		{"the owner's own key", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: CreatedAt\n        value_regex_pattern: x\n  - Artist\n", []string{"manifest-columns"}, `has the key "value_regex_pattern"; a column reads only field`, true},
		{"a mistyped field", "recordsets:\n  - name: Album\n    columns:\n      created:\n        feild: CreatedAt\n  - Artist\n", []string{"manifest-columns", "manifest-columns"}, `column "created" has the key "feild"`, true},
		{"a column with no field", "recordsets:\n  - name: Album\n    columns:\n      created: {}\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": column "created" needs field: the name of the field it holds`, true},
		{"a field that is a number", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: 12\n  - Artist\n", []string{"manifest-columns"}, `column "created" needs field`, true},
		{"a field that is null", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field:\n  - Artist\n", []string{"manifest-columns"}, `column "created" needs field`, true},
		{"a field with a space", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: Created At\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": column "created": field "Created At" must be a field name, or names joined by single dots (letters, digits and _, each not starting with a digit)`, true},
		{"a field that starts with a dot", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: .CreatedAt\n  - Artist\n", []string{"manifest-columns"}, `field ".CreatedAt" must be a field name`, true},
		{"a field with two dots", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: a..b\n  - Artist\n", []string{"manifest-columns"}, `field "a..b" must be a field name`, true},
		{"a field that ends with a dot", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: a.\n  - Artist\n", []string{"manifest-columns"}, `field "a." must be a field name`, true},
		{"a field that starts with a digit", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: 1a\n  - Artist\n", []string{"manifest-columns"}, `field "1a" must be a field name`, true},
		{"an empty field", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: ''\n  - Artist\n", []string{"manifest-columns"}, `field "" must be a field name`, true},
		{"a field held twice", "recordsets:\n  - name: Album\n    columns:\n      created:\n        field: CreatedAt\n      made:\n        field: CreatedAt\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": columns "created" and "made" both hold the field CreatedAt`, true},
		{"a column name with a slash", "recordsets:\n  - name: Album\n    columns:\n      a/b:\n        field: CreatedAt\n  - Artist\n", []string{"manifest-columns"}, `recordsets "Album": column "a/b" must contain no slash`, true},
		{"a blank column name", "recordsets:\n  - name: Album\n    columns:\n      ' ':\n        field: CreatedAt\n  - Artist\n", []string{"manifest-columns"}, `column " " must be a non-empty name`, true},
		{"columns of an item with a record type that is refused", "recordsets:\n  - name: Album\n    record_type: 'A B'\n    columns:\n      a: b\n  - Artist\n", []string{"manifest-recordsets", "manifest-columns"}, "record_type must be", false},
		// the earlier key
		{"recordset_entities and a map item", "recordsets:\n  - name: Albums\n    record_type: Album\n  - Artist\nrecordset_entities:\n  Albums: Album\n", []string{"manifest-recordsets"}, "recordset_entities and record_type both state the mapping, and a manifest states it once", true},
		{"recordset_entities and names", "recordsets:\n  - Album\n  - Artist\nrecordset_entities:\n  Album: Album\n", []string{"manifest-recordsets"}, "recordset_entities is not read under format: ovdb-manifest/draft-2", true},
		{"an empty recordset_entities", "recordsets:\n  - Album\n  - Artist\nrecordset_entities: {}\n", []string{"manifest-recordsets"}, "an empty recordset_entities is refused too", true},
		{"a null recordset_entities", "recordsets:\n  - Album\n  - Artist\nrecordset_entities:\n", []string{"manifest-recordsets"}, "recordset_entities is not read", true},
	} {
		doc := edit(t, ownManifest, "format: ovdb-manifest/draft-1", "format: ovdb-manifest/draft-2")
		doc = edit(t, doc, "recordsets:\n  - Album\n  - Artist\n", c.doc)
		m, findings := judged(t, doc)
		if len(findings) == 0 {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		var got []string
		for _, f := range findings {
			got = append(got, f.Rule)
			if f.Line == 0 || f.Document != "ovdb.yaml" {
				t.Errorf("%s: a finding without its place: %+v", c.name, f)
			}
		}
		if !reflect.DeepEqual(got, c.rules) || !strings.Contains(findings[0].Message, c.text) {
			t.Errorf("%s: findings\n%s\nwant rules %v and %q", c.name, messages(findings), c.rules, c.text)
		}
		if m.Mapping.Usable() || !m.Mapping.Present || m.Recordsets.Usable() != c.names {
			t.Errorf("%s: mapping %+v, recordsets %+v, names usable should be %v", c.name, m.Mapping, m.Recordsets, c.names)
		}
		assertBoundedFindings(t, findings)
	}
}

// assertBoundedFindings holds the findings to the bound the package keeps.
func assertBoundedFindings(t *testing.T, findings []Finding) {
	t.Helper()
	for _, f := range findings {
		if len(f.Message) > MaxMessageBytes || !printable(f.Message) {
			t.Errorf("a message that is not bounded printable ASCII: %q", f.Message)
		}
	}
}

// A refused record_type of a map that has a name is reported by that name, and by its place when it has none.
func TestDraft2RecordTypeWithoutAName(t *testing.T) {
	_, findings := judged(t, draft2(t, "  - record_type: Album-Record\n  - Artist\n"))
	if got := messages(findings); !strings.Contains(got, "recordsets item 1: record_type must be a ModelSpec record type name") || !strings.Contains(got, "needs name") {
		t.Errorf("findings\n%s", got)
	}
}

// The recordsets of a manifest that the shapes above accept and the other rules refuse are still told apart: a name whose page is refused leaves no mapping.
func TestDraft2PageOfANativeName(t *testing.T) {
	doc := edit(t, draft2(t, "  - name: Album\n  - name: dbo.Artist\n    record_type: Artist\n"), "collections/{name}", "collections/c-{name}")
	if _, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher); len(findings) != 0 {
		t.Errorf("a name that needs no encoding: %v", findings)
	}
	doc = edit(t, draft2(t, "  - name: Order Lines\n    record_type: Album\n  - Artist\n"), "collections/{name}", "collections/c-{name}")
	m, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher)
	if len(findings) != 1 || findings[0].Rule != "manifest-recordsets" || !strings.Contains(findings[0].Message, `the recordset page of "Order Lines"`) {
		t.Fatalf("a name that needs encoding where the template shares its segment: %v", findings)
	}
	// The line is the name's, in the map.
	if lines := strings.Split(doc, "\n"); !strings.Contains(lines[findings[0].Line-1], "Order Lines") {
		t.Errorf("the finding is at line %d: %q", findings[0].Line, lines[findings[0].Line-1])
	}
	if m.Mapping.Usable() || m.Recordsets.Usable() {
		t.Errorf("the mapping is still usable after the refusal of a page: %+v %+v", m.Mapping, m.Recordsets)
	}
}

func TestTheFormat(t *testing.T) {
	for _, c := range []struct {
		name, format string
		accepted     bool
		value        string
	}{
		{"the first", "ovdb-manifest/draft-1", true, ManifestFormat},
		{"the second", "ovdb-manifest/draft-2", true, ManifestFormatDraft2},
		{"a third", "ovdb-manifest/draft-3", false, ""},
		{"another case", "OVDB-MANIFEST/DRAFT-2", false, ""},
		{"a number", "2", false, ""},
		{"a list", "[ovdb-manifest/draft-2]", false, ""},
		{"nothing", "", false, ""},
	} {
		doc := edit(t, ownManifest, "format: ovdb-manifest/draft-1", "format: "+c.format)
		m, findings := judged(t, doc)
		if c.accepted != (len(findings) == 0) || m.Format.Usable() != c.accepted || m.Format.Value != c.value {
			t.Errorf("%s: findings %v, format %+v", c.name, findings, m.Format)
		}
		if !c.accepted && (findings[0].Rule != "manifest-format" || !strings.Contains(findings[0].Message, "format must be ovdb-manifest/draft-1 or ovdb-manifest/draft-2, got ")) {
			t.Errorf("%s: the finding is %+v", c.name, findings[0])
		}
	}
	// A manifest without the line is refused for it, and its recordsets are read as the first format reads them.
	doc := strings.Replace(ownManifest, "format: ovdb-manifest/draft-1\n", "", 1)
	m, findings := judged(t, doc)
	if len(findings) != 1 || findings[0].Rule != "manifest-format" || m.Format.Present || !m.Mapping.Usable() || m.Draft2() {
		t.Errorf("no format: %v, %+v", findings, m.Format)
	}
}

// A map item under the first identifier is the second form under the wrong line: the verdict is the first form's refusal, and the message says so.
func TestAMapItemUnderTheFirstFormat(t *testing.T) {
	doc := edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n  - Album\n  - name: Artists\n    record_type: Artist\n")
	m, findings := judged(t, doc)
	if len(findings) != 1 || findings[0].Rule != "manifest-recordsets" || findings[0].Line == 0 ||
		findings[0].Message != "recordsets item 2 is a map, but ovdb-manifest/draft-1 lists recordsets by name only; record_type: and columns: are read under format: ovdb-manifest/draft-2" {
		t.Fatalf("findings %v", findings)
	}
	if m.Recordsets.Usable() || m.Mapping.Usable() || !m.Mapping.Present {
		t.Errorf("recordsets %+v, mapping %+v", m.Recordsets, m.Mapping)
	}
	// With the earlier key as well, the pairs are not read against a list that was not read: one finding, and the fact is present and not usable.
	m, findings = judged(t, doc+"recordset_entities:\n  Album: Album\n")
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "recordsets item 2 is a map") || !m.RecordsetEntities.Present || m.RecordsetEntities.Usable() || len(m.Notices) != 0 {
		t.Errorf("findings %v, entities %+v, notices %v", findings, m.RecordsetEntities, m.Notices)
	}
	// Under an identifier that is neither the item is a map in a list of names, and the first form's own message says what is wrong.
	unknown := edit(t, doc, "ovdb-manifest/draft-1", "ovdb-manifest/draft-3")
	_, findings = judged(t, unknown)
	if len(findings) != 2 || findings[0].Rule != "manifest-format" || !strings.HasPrefix(findings[1].Message, "recordsets must be a non-empty list of names") {
		t.Errorf("findings %v", findings)
	}
}

func TestNoticeOfTheEarlierKey(t *testing.T) {
	const prefix = "recordset_entities is the earlier form of the mapping and is still read; under format: ovdb-manifest/draft-2 the same is one record_type: line under each recordset"
	for _, c := range []struct {
		name, tail string
		notice     bool
	}{
		{"a pair", "recordsets:\n  - Album\n  - dbo.Artist\nrecordset_entities:\n  dbo.Artist: Artist\n", true},
		{"an empty key", "recordsets:\n  - Album\n  - Artist\nrecordset_entities: {}\n", true},
		{"no key", "recordsets:\n  - Album\n  - Artist\n", false},
		{"a key that is refused", "recordsets:\n  - Album\n  - Artist\nrecordset_entities:\n  Missing: Artist\n", false},
		{"a null key", "recordsets:\n  - Album\n  - Artist\nrecordset_entities:\n", false},
	} {
		doc := edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", c.tail)
		for _, profile := range []Profile{Directory, Publisher} {
			r := Check([]byte(goodMD), "ovdb.yaml", []byte(doc), profile)
			if c.notice != (len(r.Notices) == 1) || len(r.Notices) != len(r.Manifest.Notices) {
				t.Errorf("%s, profile %v: notices %v, findings %v", c.name, profile, r.Notices, r.Findings)
				continue
			}
			if !c.notice {
				continue
			}
			n := r.Notices[0]
			if !r.OK() || n.Rule != RuleDeprecated || n.Severity != SeverityNotice || n.Document != "ovdb.yaml" || n.Line == 0 || !strings.HasPrefix(n.Message, prefix) || len(n.Message) > MaxMessageBytes {
				t.Errorf("%s, profile %v: the notice is %+v, findings %v", c.name, profile, n, r.Findings)
			}
			if got := strings.Split(doc, "\n")[n.Line-1]; !strings.Contains(got, ":") {
				t.Errorf("%s: the notice is at line %d: %q", c.name, n.Line, got)
			}
		}
	}
	// A manifest that the page rule refuses draws no notice: the mapping is not usable.
	doc := edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n  - Album\n  - Order Lines\nrecordset_entities:\n  Order Lines: Artist\n")
	doc = edit(t, doc, "collections/{name}", "collections/c-{name}")
	if r := Check([]byte(goodMD), "ovdb.yaml", []byte(doc), Directory); r.OK() || len(r.Notices) != 0 {
		t.Errorf("findings %v, notices %v", r.Findings, r.Notices)
	}
	// Under an identifier that is neither, the key is read as it is under the first, and the format is what is refused: no notice for a manifest that is.
	unknown := edit(t, edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n  - Album\n  - Artist\nrecordset_entities: {}\n"), "ovdb-manifest/draft-1", "ovdb-manifest/draft-3")
	if r := Check([]byte(goodMD), "ovdb.yaml", []byte(unknown), Directory); r.OK() || len(r.Notices) != 0 {
		t.Errorf("findings %v, notices %v", r.Findings, r.Notices)
	}
	// A notice is not a finding: the budget of findings is not spent on it, and the judge of several manifests hands it over with the manifest.
	j, _ := NewJudge(Directory)
	m, findings := j.Manifest([]byte(edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n  - Album\n  - Artist\nrecordset_entities: {}\n")), "ovdb.yaml")
	if len(findings) != 0 || len(m.Notices) != 1 {
		t.Errorf("the judge: findings %v, notices %v", findings, m.Notices)
	}
}

func TestTheMappingOfTheFirstFormat(t *testing.T) {
	doc := edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n  - Album\n  - dbo.Artist\nrecordset_entities:\n  dbo.Artist: Artist\n")
	m, findings := judged(t, doc)
	if len(findings) != 0 || !m.Mapping.Usable() || m.Draft2() {
		t.Fatalf("findings %v, mapping %+v", findings, m.Mapping)
	}
	want := []Recordset{{Name: "Album", RecordType: "Album", Line: m.Mapping.Line}, {Name: "dbo.Artist", RecordType: "Artist", Line: m.Mapping.Line}}
	if !reflect.DeepEqual(m.Mapping.Value, want) || !reflect.DeepEqual(m.RecordTypes(), []string{"Album", "Artist"}) {
		t.Errorf("mapping %+v, record types %v", m.Mapping.Value, m.RecordTypes())
	}
	// The first form has no column.
	if m.ListsColumns("Album") || m.ListsColumns("dbo.Artist") || !reflect.DeepEqual(m.RecordsetsOfType("Artist"), []string{"dbo.Artist"}) {
		t.Errorf("columns of %v, recordsets of Artist %v", m.Mapping, m.RecordsetsOfType("Artist"))
	}
	// A mapping that is refused is not usable, and the record types are read as they were before the mapping: the names, with the pairs that are usable.
	m, findings = judged(t, doc+"  Album: Artist\n")
	if len(findings) == 0 || m.Mapping.Usable() || !m.Mapping.Present || !reflect.DeepEqual(m.RecordTypes(), []string{"Album", "dbo.Artist"}) {
		t.Errorf("findings %v, mapping %+v, record types %v", findings, m.Mapping, m.RecordTypes())
	}
	// A manifest with no recordsets has no mapping at all.
	m, findings = judged(t, strings.Replace(ownManifest, "recordsets:\n  - Album\n  - Artist\n", "", 1))
	if len(findings) == 0 || m.Mapping.Present {
		t.Errorf("findings %v, mapping %+v", findings, m.Mapping)
	}
}

func TestTheQuestionsAboutTheMapping(t *testing.T) {
	doc := draft2(t, "  - name: Albums\n    record_type: Album\n    columns:\n      created:\n        field: CreatedAt\n  - name: Artist\n    columns: {}\n  - name: Genres\n    record_type: Genre\n")
	m, findings := judged(t, doc)
	if len(findings) != 0 {
		t.Fatal(findings)
	}
	for name, want := range map[string]bool{"Albums": true, "Artist": false, "Genres": false, "Missing": false} {
		if got := m.ListsColumns(name); got != want {
			t.Errorf("ListsColumns(%q) = %v, want %v", name, got, want)
		}
	}
	if got := m.RecordsetsOfType("Album"); !reflect.DeepEqual(got, []string{"Albums"}) {
		t.Errorf("RecordsetsOfType(Album) = %v", got)
	}
	if got := m.RecordsetsOfType("Missing"); got != nil {
		t.Errorf("RecordsetsOfType(Missing) = %v", got)
	}
	if got := m.RecordTypes(); !reflect.DeepEqual(got, []string{"Album", "Artist", "Genre"}) {
		t.Errorf("RecordTypes() = %v", got)
	}
	// Nothing is answered for a manifest whose mapping is not usable.
	bad, _ := judged(t, draft2(t, "  - name: Albums\n    columns: [x]\n"))
	if bad.ListsColumns("Albums") || bad.RecordsetsOfType("Albums") != nil {
		t.Errorf("answers from a mapping that is refused")
	}
	// A column is held to the mapping of the HTTP source, which maps its fields itself.
	for _, c := range []struct {
		name, lines string
		refused     bool
	}{
		{"no column", "  - Album\n  - Artist\n", false},
		{"an empty columns", "  - name: Album\n    columns: {}\n  - Artist\n", false},
		{"a column", "  - name: Album\n    columns:\n      created:\n        field: CreatedAt\n  - Artist\n", true},
	} {
		doc := draft2(t, c.lines) + "source_definition: {}\n"
		m, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher)
		found := false
		for _, f := range findings {
			if f.Rule == RuleColumns && strings.Contains(f.Message, `recordset "Album" lists columns, but a manifest with source_definition maps its fields with fieldMapping`) {
				found = true
			}
		}
		if found != c.refused || (c.refused && m.Mapping.Usable()) {
			t.Errorf("%s: findings %v, mapping %+v", c.name, findings, m.Mapping)
		}
	}
}

// An HTTP source definition names the record type of each recordset it describes, and the manifest of the second format says which they are by its own
// mapping: a name alone is its own record type, and a record_type: is the one the definition must name.
func TestAnHTTPDefinitionIsMatchedToTheMappingOfTheSecondFormat(t *testing.T) {
	for _, c := range []struct {
		name       string
		recordsets []any
		refused    bool
	}{
		{"names", []any{"Album", "Artist"}, false},
		{"maps with a name alone", []any{map[string]any{"name": "Album"}, "Artist"}, false},
		{"a record type that is the definition's", []any{"Album", map[string]any{"name": "Artist", "record_type": "Artist"}}, false},
		{"a name the definition does not describe", []any{"Album", map[string]any{"name": "Artists", "record_type": "Artist"}}, true},
		{"a record type that is not the definition's", []any{"Album", map[string]any{"name": "Artist", "record_type": "Album2"}}, true},
		{"a mapping that is refused", []any{"Album", map[string]any{"name": "Artist", "columns": []any{}}}, true},
	} {
		var doc map[string]any
		if err := json.Unmarshal(httpManifest(t), &doc); err != nil {
			t.Fatal(err)
		}
		doc["format"], doc["recordsets"] = ManifestFormatDraft2, c.recordsets
		b, _ := json.Marshal(doc)
		m, findings := CheckManifest(b, "ovdb.yaml", Publisher)
		if c.refused == (len(findings) == 0) || m.SourceDefinition.Usable() == c.refused && !c.refused {
			t.Errorf("%s: findings %v, definition %+v", c.name, findings, m.SourceDefinition)
		}
		if !c.refused && !m.SourceDefinition.Usable() {
			t.Errorf("%s: the definition is not usable", c.name)
		}
	}
}
