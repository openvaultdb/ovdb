package manifest

import (
	"reflect"
	"strings"
	"testing"
)

// Recordsets are named as the database names them (the Directory's native names, 1c7e126), and recordset_entities says which ModelSpec entity a native
// name is. These tests pin each rule; the reference corpus and probes (testdata/reference) hold them to the Directory's own code.

func withRecordsets(t *testing.T, lines string) string {
	t.Helper()
	return edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n"+lines)
}

func TestNativeRecordsetNames(t *testing.T) {
	for _, c := range []struct {
		name, lines string
		refused     bool
	}{
		{"an entity name", "  - Album\n", false},
		{"a dotted name", "  - dbo.DatabaseLog\n  - Album\n", false},
		{"a name with a space", "  - Order Details\n", false},
		{"a non-ASCII name", "  - été\n", false},
		{"a name of 256 characters", "  - " + strings.Repeat("a", 256) + "\n", false},
		{"a name of 257 characters", "  - " + strings.Repeat("a", 257) + "\n", true},
		{"128 astral characters", "  - \"" + strings.Repeat("\U0001f600", 128) + "\"\n", false},
		{"129 astral characters", "  - \"" + strings.Repeat("\U0001f600", 129) + "\"\n", true},
		{"a slash", "  - dbo/Album\n", true},
		{"a backslash", "  - 'dbo\\Album'\n", true},
		{"a control character", "  - \"a\\tb\"\n", true},
		{"a dot segment", "  - '..'\n", true},
		{"a single dot", "  - '.'\n", true},
		{"a blank name", "  - ' '\n", true},
	} {
		for _, profile := range []Profile{Directory, Publisher} {
			m, findings := CheckManifest([]byte(withRecordsets(t, c.lines)), "ovdb.yaml", profile)
			if (len(findings) > 0) != c.refused {
				t.Errorf("%s, %v profile: findings %v, refused should be %v", c.name, profile, findings, c.refused)
			}
			if c.refused && (len(rulesOf(findings)) == 0 || m.Recordsets.Usable()) {
				t.Errorf("%s, %v profile: the recordsets are still usable after a refusal: %+v", c.name, profile, m.Recordsets)
			}
		}
	}
}

func TestRecordsetEntities(t *testing.T) {
	base := "  - Album\n  - dbo.Artist\n  - Order Details\n"
	for _, c := range []struct {
		name, entities string
		lines          string
		refused        bool
		want           map[string]string
	}{
		{"one mapping", "recordset_entities:\n  dbo.Artist: Artist\n", base, false, map[string]string{"dbo.Artist": "Artist"}},
		{"two mappings", "recordset_entities:\n  dbo.Artist: Artist\n  Order Details: OrderDetails\n", base, false, map[string]string{"dbo.Artist": "Artist", "Order Details": "OrderDetails"}},
		{"a list", "recordset_entities:\n  - Artist\n", base, true, nil},
		{"a text", "recordset_entities: Artist\n", base, true, nil},
		{"an empty value", "recordset_entities:\n", base, true, nil},
		{"a name that is not listed", "recordset_entities:\n  Missing: Artist\n", base, true, nil},
		{"a name that is not a recordset name", "recordset_entities:\n  'a/b': Artist\n", "  - Album\n  - a/b\n", true, nil},
		{"an entity that is not an identifier", "recordset_entities:\n  dbo.Artist: 'not an entity'\n", base, true, nil},
		{"an entity that is a number", "recordset_entities:\n  dbo.Artist: 7\n", base, true, nil},
		{"an entity that is empty", "recordset_entities:\n  dbo.Artist:\n", base, true, nil},
		{"one entity twice", "recordset_entities:\n  dbo.Artist: Artist\n  Order Details: Artist\n", base, true, nil},
		{"no list to name from", "recordset_entities:\n  dbo.Artist: Artist\n", "  - ' '\n", true, nil},
	} {
		doc := edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n"+c.lines+c.entities)
		for _, profile := range []Profile{Directory, Publisher} {
			m, findings := CheckManifest([]byte(doc), "ovdb.yaml", profile)
			if (len(findings) > 0) != c.refused {
				t.Errorf("%s, %v profile: findings %v", c.name, profile, findings)
			}
			if !m.RecordsetEntities.Present || m.RecordsetEntities.Usable() == c.refused || !reflect.DeepEqual(m.RecordsetEntities.Value, c.want) {
				t.Errorf("%s, %v profile: the fact is %+v", c.name, profile, m.RecordsetEntities)
			}
		}
	}
	// A recordsets list that is not a list: every name is unlisted, and the fact stays present and unusable.
	doc := edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets: Album\nrecordset_entities:\n  Album: Album\n")
	if m, findings := CheckManifest([]byte(doc), "ovdb.yaml", Directory); len(findings) == 0 || m.RecordsetEntities.Usable() {
		t.Errorf("recordsets that is not a list: %v, %+v", findings, m.RecordsetEntities)
	}
	// Absent: not present.
	if m, _ := CheckManifest([]byte(ownManifest), "ovdb.yaml", Directory); m.RecordsetEntities.Present {
		t.Errorf("recordset_entities is written in the document that has none: %+v", m.RecordsetEntities)
	}
	// A recordset_entities that is a key of the manifest is an allowed key of the Publisher profile.
	doc = edit(t, ownManifest, "recordsets:\n  - Album\n  - Artist\n", "recordsets:\n  - Album\n  - dbo.Artist\nrecordset_entities:\n  dbo.Artist: Artist\n")
	if _, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher); len(findings) != 0 {
		t.Errorf("the Publisher profile refuses recordset_entities: %v", findings)
	}
}

func TestRecordsetPageOfNativeNames(t *testing.T) {
	doc := withRecordsets(t, "  - Album\n  - Order Details\n  - dbo.Artist\n")
	if _, findings := CheckManifest([]byte(doc), "ovdb.yaml", Publisher); len(findings) != 0 {
		t.Errorf("native names make pages that pass: %v", findings)
	}
	// A template that shares its segment with {name} cannot carry an encoded name.
	shared := edit(t, doc, "collections/{name}", "collections/c-{name}")
	if _, findings := CheckManifest([]byte(shared), "ovdb.yaml", Publisher); len(findings) != 1 || findings[0].Rule != "manifest-recordsets" {
		t.Errorf("a template that shares its segment: %v", findings)
	}
	// ... and one whose names need no encoding can.
	plain := edit(t, withRecordsets(t, "  - Album\n  - dbo.Artist\n"), "collections/{name}", "collections/c-{name}")
	if _, findings := CheckManifest([]byte(plain), "ovdb.yaml", Publisher); len(findings) != 0 {
		t.Errorf("a shared segment of plain names: %v", findings)
	}
}
