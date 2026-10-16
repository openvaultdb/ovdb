package manifest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// How a manifest says which ModelSpec record type each recordset has and which field each column holds (decision 0012 of openvaultdb/openvaultdb,
// approved on 2026-10-10). The reference is scripts/lib/manifest-mapping.mjs, the same file in openvaultdb/directory and demo-db/chinook, and the cases
// that both run (scripts/fixtures/manifest-conformance.json of the Directory, copied to testdata/reference/manifest-conformance.json) are run here too.
//
//	ovdb-manifest/draft-1  recordsets is a list of names; the optional map recordset_entities pairs a name with a record type; nothing says anything
//	                       about a column.
//	ovdb-manifest/draft-2  an item of recordsets is a name, or a map with name, record_type and columns (a map from a column's name to a map with the one
//	                       key field).
//
// The format line decides the form, and a manifest that mixes the two is refused. Whatever the form, the rest of the check reads Manifest.Mapping.

// Rules of the findings about the mapping, besides manifest-recordsets: the shape of the columns of a recordset, and the earlier form that is still read.
// The columns of a recordset against its record type's fields are repo-columns (package repo).
const (
	RuleColumns    = "manifest-columns"
	RuleDeprecated = "manifest-deprecated"
)

// SeverityNotice is a notice: what a publisher should change, without the documents being wrong. It is never a finding, and changes no verdict.
const SeverityNotice Severity = "notice"

// formatKind is the format a manifest declares.
type formatKind int

const (
	formatUnknown formatKind = iota
	formatDraft1
	formatDraft2
)

func formatOf(n *Node) formatKind {
	switch {
	case n == nil || n.Kind != kindString:
		return formatUnknown
	case n.Text == ManifestFormat:
		return formatDraft1
	case n.Text == ManifestFormatDraft2:
		return formatDraft2
	}
	return formatUnknown
}

// Recordset is one recordset as the manifest maps it.
type Recordset struct {
	// Name is the recordset's own name, the name the database uses for the table or collection.
	Name string
	// RecordType is the ModelSpec record type its rows have: the record_type of a draft-2 item, the pair in recordset_entities of a draft-1 manifest,
	// else the name itself.
	RecordType string
	// Columns are the columns the manifest lists for it, in the order written: draft-2 only, and none unless the publisher lists them.
	Columns []Column
	// Line is where the recordset is written (its name, in a map).
	Line int
}

// Column is a column of a recordset and the field it holds.
type Column struct {
	// Name is the column's own name. Field is a field of the record type, or names joined by single dots (a path into a component).
	Name, Field string
	// Line is where the column is written.
	Line int
}

// RecordTypes is the record type of each recordset, in the order of Recordsets. It reads the mapping; when the mapping is not usable no pair of
// recordset_entities and no record_type was read, and each recordset is taken to have the record type of its own name.
func (m Manifest) RecordTypes() []string {
	if !m.Mapping.Usable() {
		return slices.Clone(m.Recordsets.Value)
	}
	types := make([]string, len(m.Mapping.Value))
	for i, r := range m.Mapping.Value {
		types[i] = r.RecordType
	}
	return types
}

// Draft2 says whether the manifest declares ovdb-manifest/draft-2.
func (m Manifest) Draft2() bool { return m.Format.Usable() && m.Format.Value == ManifestFormatDraft2 }

// ListsColumns says whether the recordset of this own name lists at least one column. A recordset with no columns, or with `columns: {}`, lists none.
func (m Manifest) ListsColumns(name string) bool {
	if !m.Mapping.Usable() {
		return false
	}
	for _, r := range m.Mapping.Value {
		if r.Name == name {
			return len(r.Columns) > 0
		}
	}
	return false
}

// RecordsetsOfType are the own names of the recordsets whose rows have this record type, in the order written.
func (m Manifest) RecordsetsOfType(recordType string) []string {
	var names []string
	if m.Mapping.Usable() {
		for _, r := range m.Mapping.Value {
			if r.RecordType == recordType {
				names = append(names, r.Name)
			}
		}
	}
	return names
}

// itemKeys are the keys of a recordset's map that are read; any other is refused, so that a mistyped columns is caught.
var itemKeys = []string{"name", "record_type", "columns"}

// isFieldPath is the reference's /^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$/: one or more names joined by single dots.
func isFieldPath(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if !isModuleName(part) {
			return false
		}
	}
	return true
}

// recordsetsDraft2 reads recordsets under ovdb-manifest/draft-2 (the reference's newFormProblems): a non-empty list whose items are a name or a map with
// name, record_type and columns; every name once and fit to be a name; one recordset for each record type. Recordsets holds the own names, usable when the
// shape of the list, of its items and of their names is good; the columns are judged for the mapping alone, so that a refused column leaves the names
// usable.
func (k *manifestChecker) recordsetsDraft2() {
	list := k.m.Field("recordsets")
	if list == nil || list.Kind != kindSeq || len(list.Items) == 0 {
		k.c.add("manifest-recordsets", where(k.m, "recordsets"), "recordsets must be a non-empty list of names or of items with a name")
		k.out.Recordsets = found(list, false, []string(nil))
		return
	}
	good := true
	k.columnsOK = true
	problem := func(line int, format string, args ...any) {
		good = false
		k.c.add("manifest-recordsets", line, format, args...)
	}
	var names []string
	seen := map[string]bool{}
	typeOwner := map[string]string{}
	for at, item := range list.Items {
		position := at + 1
		var name, recordType string
		var haveName, haveType bool
		var columns *Node
		switch item.Kind {
		case kindString:
			if !isText(item) {
				problem(item.Line, "recordsets item %d must be a name or a map with a name, got %s", position, describe(item))
				continue
			}
			name, recordType, haveName, haveType = item.Text, item.Text, true, true
		case kindMap:
			for _, key := range item.Keys {
				if !slices.Contains(itemKeys, key) {
					problem(item.Fields[key].Line, "recordsets item %d has the key %s; an item reads only name, record_type and columns", position, rules.Quote(key))
				}
			}
			if n := item.Field("name"); isText(n) {
				name, haveName = n.Text, true
			} else {
				problem(fieldLine(item, "name"), "recordsets item %d needs name: the recordset's own name", position)
			}
			if n := item.Field("record_type"); n == nil {
				recordType, haveType = name, haveName
			} else if n.Kind == kindString && isModuleName(n.Text) {
				recordType, haveType = n.Text, true
			} else {
				of := "item " + strconv.Itoa(position)
				if haveName {
					of = rules.Quote(name)
				}
				problem(n.Line, "recordsets %s: record_type must be a ModelSpec record type name (letters, digits and _, not starting with a digit), got %s", of, describe(n))
			}
			columns = item.Field("columns")
		default:
			problem(item.Line, "recordsets item %d must be a name or a map with a name, got %s", position, describe(item))
			continue
		}
		line := item.Line
		if n := item.Field("name"); item.Kind == kindMap && n != nil {
			line = n.Line
		}
		label := "item " + strconv.Itoa(position)
		if haveName {
			label = name
			if err := rules.RecordsetName(name); err != nil {
				problem(line, "recordsets name %s %s", rules.Quote(name), err.Error())
			}
			// A name listed twice is reported once, as that; its record type is not set against the first one's.
			repeated := seen[name]
			if repeated {
				problem(line, "recordsets lists a name twice: %s", rules.Quote(name))
			}
			seen[name] = true
			if haveType && !repeated {
				if owner, taken := typeOwner[recordType]; taken {
					problem(line, "recordsets %s and %s both have the record type %s; mappings must be one-to-one", rules.Quote(owner), rules.Quote(name), recordType)
				} else {
					typeOwner[recordType] = name
				}
			}
			names = append(names, name)
			k.nameLines = append(k.nameLines, line)
		}
		entry := Recordset{Name: name, RecordType: recordType, Line: line}
		if columns != nil {
			var ok bool
			if entry.Columns, ok = k.columns(label, columns); !ok {
				k.columnsOK = false
			}
		}
		if haveName && haveType {
			k.items = append(k.items, entry)
		}
	}
	k.out.Recordsets = found(list, good, names)
	if !good {
		k.items = nil
	}
}

// columns reads the columns of one recordset (the reference's columnProblems): a map from a column's name to a map whose one key is field, a field
// name or a path of names joined by single dots, each field held by one column. It returns the columns as written, and false when any is refused.
func (k *manifestChecker) columns(recordset string, n *Node) ([]Column, bool) {
	where := "recordsets " + rules.Quote(recordset)
	if n.Kind != kindMap {
		k.c.add(RuleColumns, n.Line, "%s: columns must be a map from a column's name to { field: ... }, got %s", where, describe(n))
		return nil, false
	}
	ok := true
	problem := func(line int, format string, args ...any) {
		ok = false
		k.c.add(RuleColumns, line, "%s: %s", where, fmt.Sprintf(format, args...))
	}
	var columns []Column
	holders := map[string]string{}
	for _, name := range n.Keys {
		value := n.Fields[name]
		if err := rules.RecordsetName(name); err != nil {
			problem(value.Line, "column %s %s", rules.Quote(name), err.Error())
		}
		if value.Kind != kindMap {
			problem(value.Line, "column %s must be a map with field: (a column is written as a map, \"%s: { field: ... }\", not as text or a list), got %s", rules.Quote(name), name, describe(value))
			continue
		}
		for _, key := range value.Keys {
			if key != "field" {
				problem(value.Fields[key].Line, "column %s has the key %s; a column reads only field", rules.Quote(name), rules.Quote(key))
			}
		}
		field := value.Field("field")
		switch {
		case field == nil || field.Kind != kindString:
			problem(fieldLine(value, "field"), "column %s needs field: the name of the field it holds", rules.Quote(name))
			continue
		case !isFieldPath(field.Text):
			problem(field.Line, "column %s: field %s must be a field name, or names joined by single dots (letters, digits and _, each not starting with a digit)", rules.Quote(name), rules.Quote(field.Text))
			continue
		}
		if held, taken := holders[field.Text]; taken {
			problem(field.Line, "columns %s and %s both hold the field %s", rules.Quote(held), rules.Quote(name), field.Text)
			continue
		}
		holders[field.Text] = name
		columns = append(columns, Column{Name: name, Field: field.Text, Line: value.Line})
	}
	return columns, ok
}

// noticeEarlierKey is the notice for a draft-1 manifest that writes recordset_entities, empty or not (the reference's earlierKeyNotice).
const noticeEarlierKey = "recordset_entities is the earlier form of the mapping and is still read; under format: ovdb-manifest/draft-2 the same is one record_type: line under each recordset that recordset_entities lists (for example - name: Order Details, record_type: OrderDetails), and recordset_entities is removed; an empty recordset_entities can simply be removed"

// mapping makes the Mapping fact from what recordsets, recordset_entities and the columns left, and the notice about the earlier key. It runs after the
// page of every name has been judged, which can refuse the names.
func (k *manifestChecker) mapping() {
	names, entities := k.out.Recordsets, k.out.RecordsetEntities
	if !names.Present {
		return
	}
	mapping := Fact[[]Recordset]{Present: true, Line: names.Line}
	if k.format == formatDraft2 {
		if mapping.Valid = names.Usable() && k.columnsOK && !entities.Present; mapping.Valid {
			mapping.Value = k.items
		}
		k.out.Mapping = mapping
		return
	}
	if mapping.Valid = names.Usable() && (!entities.Present || entities.Valid); mapping.Valid {
		for _, name := range names.Value {
			record := name
			if mapped, ok := entities.Value[name]; ok {
				record = mapped
			}
			mapping.Value = append(mapping.Value, Recordset{Name: name, RecordType: record, Line: names.Line})
		}
		// The earlier key is read as it always was; a publisher who still writes it is told how to write the mapping now. The format is draft-1, or it
		// is refused and the notice would be noise.
		if entities.Present && k.format == formatDraft1 {
			k.c.notice(RuleDeprecated, entities.Line, "%s", noticeEarlierKey)
		}
	}
	k.out.Mapping = mapping
}

// refuseColumnsWithSource refuses the columns of a manifest that has an HTTP source definition, which maps its own fields with fieldMapping; a recordset
// that lists none is not affected. It says the first recordset that lists some, and returns whether it refused.
func (k *manifestChecker) refuseColumnsWithSource(line int) bool {
	if !k.out.Mapping.Usable() {
		return false
	}
	for _, r := range k.out.Mapping.Value {
		if len(r.Columns) > 0 {
			k.c.add(RuleColumns, line, "recordset %s lists columns, but a manifest with source_definition maps its fields with fieldMapping there: remove columns, or remove source_definition", rules.Quote(r.Name))
			return true
		}
	}
	return false
}
