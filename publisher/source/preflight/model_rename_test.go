package preflight

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// This file is test support for the guard on the ECB model files (model_guard_test.go): the rename of ModelSpec's three words, computed from a
// model in the earlier spelling. It is test code because no production code needs it; the released binary rewrites no model.
//
// The reference is `modelspec rewrite` (the ModelSpec CLI, 0.2.0), which replaces the old spellings as byte ranges and leaves every other byte of a
// file as it was (decisions 0018, 0020 and 0022 of the ModelSpec specification):
//
//	HCL   the block type entity becomes record, the block type property directly inside a record becomes field, and the attribute entity
//	      directly inside a member becomes record
//	JSON  the identifier 1.0-draft becomes 1.0-draft-2, and the keys entities, properties (of a record type) and entity (of a member) become
//	      records, fields and record
//
// The functions below make the same renames, as byte ranges, in the shapes the tests show: the bytes the reference wrote for those documents
// (TestTheRenameIsWhatTheReferenceWrites) and the pinned ECB files (TestTheRenameOfTheBaselineModelIsWhatTheReferenceWrote). They refuse, with an
// error, the shapes TestTheRenameRefusesWhatItDoesNotKnow lists: an escaped key, a heredoc or a template in a string, a value that is an object,
// unbalanced braces or brackets, and a JSON document that is not one object in the earlier spelling. They are not a general reader of either
// format: an input outside those shapes (an HCL that is already in the current spelling comes back unchanged, and an HCL that the reference refuses
// as unparseable may still be renamed) is not what the guard relies on. The guard compares the result with the two digests of the reference's
// output, so any difference from what the reference writes for the pinned files fails it.

// edit is one byte range of a file and what takes its place.
type edit struct {
	start, end int
	text       string
}

// apply returns data with the edits made. The edits are in the order of their positions and do not overlap.
func apply(data []byte, edits []edit) []byte {
	var out bytes.Buffer
	at := 0
	for _, e := range edits {
		out.Write(data[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.Write(data[at:])
	return out.Bytes()
}

// renameJSON returns the JSON form of a model in the earlier spelling, in the current one.
func renameJSON(data []byte) ([]byte, error) {
	type frame struct {
		key       string // the last key read in this object
		expectKey bool
		object    bool
	}
	var (
		edits    []edit
		stack    []frame
		started  bool
		sawIdent bool
	)
	dec := json.NewDecoder(bytes.NewReader(data))
	replace := func(s, text string) error {
		end := int(dec.InputOffset())
		quoted := `"` + s + `"`
		if end < len(quoted) || string(data[end-len(quoted):end]) != quoted {
			return fmt.Errorf("the %q at byte %d is not written as itself", s, end)
		}
		edits = append(edits, edit{end - len(quoted), end, `"` + text + `"`})
		return nil
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(stack) == 0 {
			if d, ok := tok.(json.Delim); started || !ok || d != '{' {
				return nil, errors.New("the document is not one object")
			}
			started = true
			stack = append(stack, frame{object: true, expectKey: true})
			continue
		}
		top := &stack[len(stack)-1]
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				stack = append(stack, frame{object: v == '{', expectKey: v == '{'})
			default:
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectKey = true
				}
			}
			continue
		case string:
			if top.object && top.expectKey {
				top.key, top.expectKey = v, false
				switch {
				case len(stack) == 1 && v == "entities":
					err = replace(v, "records")
				case len(stack) == 3 && stack[0].key == "entities" && v == "properties":
					err = replace(v, "fields")
				case len(stack) == 5 && stack[0].key == "entities" && stack[2].key == "properties" && v == "entity":
					err = replace(v, "record")
				}
				if err != nil {
					return nil, err
				}
				continue
			}
			if len(stack) == 1 && top.key == "modelspec" {
				if v != "1.0-draft" {
					return nil, fmt.Errorf("the identifier is %q, not the earlier one", v)
				}
				if err := replace(v, "1.0-draft-2"); err != nil {
					return nil, err
				}
				sawIdent = true
			}
		}
		if top.object {
			top.expectKey = true
		}
	}
	if len(stack) != 0 {
		return nil, errors.New("the document ends inside a value")
	}
	if !sawIdent {
		return nil, errors.New("the document does not say that it is in the earlier spelling")
	}
	return apply(data, edits), nil
}

// renameHCL returns the HCL form of a model in the earlier spelling, in the current one. It reads blocks, attributes, strings, comments and
// bracketed values, and refuses a heredoc, a template in a string, and a value that is an object.
func renameHCL(data []byte) ([]byte, error) {
	var (
		edits      []edit
		stack      []string // what each open block is: "record", "member" or "other"
		statement  = true   // the next token starts a statement
		header     bool     // a block type has been read on this line and its brace has not
		kind       string   // what that block will be
		brackets   int
		identifier = func(c byte) bool {
			return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		}
	)
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case c == '\n':
			if brackets == 0 {
				statement, header = true, false
			}
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#' || c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				return nil, errors.New("a comment is not closed")
			}
			i += end + 4
		case c == '"':
			j := i + 1
			for j < len(data) && data[j] != '"' {
				if data[j] == '\n' {
					return nil, errors.New("a string is not closed")
				}
				if data[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(data) {
				return nil, errors.New("a string is not closed")
			}
			if s := string(data[i : j+1]); strings.Contains(s, "${") || strings.Contains(s, "%{") {
				return nil, errors.New("a template in a string is not supported")
			}
			statement = false
			i = j + 1
		case c == '<' && i+1 < len(data) && data[i+1] == '<':
			return nil, errors.New("a heredoc is not supported")
		case c == '{':
			if !header {
				return nil, errors.New("a value that is an object is not supported")
			}
			stack = append(stack, kind)
			header, statement = false, true
			i++
		case c == '}':
			if len(stack) == 0 || brackets != 0 {
				return nil, errors.New("a brace is not matched")
			}
			stack = stack[:len(stack)-1]
			statement = true
			i++
		case c == '[' || c == '(':
			brackets++
			statement = false
			i++
		case c == ']' || c == ')':
			if brackets--; brackets < 0 {
				return nil, errors.New("a bracket is not matched")
			}
			statement = false
			i++
		case identifier(c):
			j := i
			for j < len(data) && identifier(data[j]) {
				j++
			}
			word := string(data[i:j])
			if statement && brackets == 0 {
				k := j
				for k < len(data) && (data[k] == ' ' || data[k] == '\t') {
					k++
				}
				parent := ""
				if len(stack) > 0 {
					parent = stack[len(stack)-1]
				}
				if k < len(data) && data[k] == '=' {
					if parent == "member" && word == "entity" {
						edits = append(edits, edit{i, j, "record"})
					}
				} else {
					kind = "other"
					switch {
					case parent == "" && word == "entity":
						edits = append(edits, edit{i, j, "record"})
						kind = "record"
					case parent == "record" && word == "property":
						edits = append(edits, edit{i, j, "field"})
						kind = "member"
					}
					header = true
				}
			}
			statement = false
			i = j
		default:
			statement = false
			i++
		}
	}
	if len(stack) != 0 || brackets != 0 {
		return nil, errors.New("the document ends inside a block")
	}
	return apply(data, edits), nil
}

// What `modelspec rewrite --write` (ModelSpec CLI 0.2.0) wrote for these documents, which are in the earlier spelling. The component keeps the
// words, as it does in the reference: only a record type and its members are renamed. The CRLF case is the LF one with each line ending changed
// (the reference wrote exactly that).
const (
	hclEarlier = `# entity "Doc" { property "x" } is a comment and keeps its words.
component "Auditable" {
  property "createdAt" {
    type = "datetime"
  }
}

entity "User" {
  key = ["id"]
  use = ["Auditable"]
  property "id" { type = "uuid" }
  property "boss" {
    type   = "reference"
    entity = "User" # entity property
  }
  /* property "c" entity = "x" */
}
entity "Task" {
  property "owner" {
    entity = "User"
    type = "reference"
    note = "entity property"
  }
}
`
	hclCurrent = `# entity "Doc" { property "x" } is a comment and keeps its words.
component "Auditable" {
  property "createdAt" {
    type = "datetime"
  }
}

record "User" {
  key = ["id"]
  use = ["Auditable"]
  field "id" { type = "uuid" }
  field "boss" {
    type   = "reference"
    record = "User" # entity property
  }
  /* property "c" entity = "x" */
}
record "Task" {
  field "owner" {
    record = "User"
    type = "reference"
    note = "entity property"
  }
}
`
	jsonEarlier = `{
  "modelspec": "1.0-draft",
  "module": {"id": "x/y", "name": "m", "version": "0.1.0"},
  "components": {"Auditable": {"properties": {"createdAt": {"type": "datetime"}}}},
  "entities": {
    "User": {"key": ["id"], "properties": {"id": {"type": "uuid"}, "boss": {"type": "reference", "entity": "User"}}},
    "Task": {"properties": {"owner": {"entity": "User", "type": "reference"}}}
  }
}
`
	jsonCurrent = `{
  "modelspec": "1.0-draft-2",
  "module": {"id": "x/y", "name": "m", "version": "0.1.0"},
  "components": {"Auditable": {"properties": {"createdAt": {"type": "datetime"}}}},
  "records": {
    "User": {"key": ["id"], "fields": {"id": {"type": "uuid"}, "boss": {"type": "reference", "record": "User"}}},
    "Task": {"fields": {"owner": {"record": "User", "type": "reference"}}}
  }
}
`
)

func TestTheRenameIsWhatTheReferenceWrites(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	for _, c := range []struct {
		name, earlier, current string
		rename                 func([]byte) ([]byte, error)
	}{
		{"hcl", hclEarlier, hclCurrent, renameHCL},
		{"hcl with CRLF line endings", crlf(hclEarlier), crlf(hclCurrent), renameHCL},
		{"json", jsonEarlier, jsonCurrent, renameJSON},
		{"hcl without a final newline", strings.TrimSuffix(hclEarlier, "\n"), strings.TrimSuffix(hclCurrent, "\n"), renameHCL},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.rename([]byte(c.earlier))
			if err != nil || string(got) != c.current {
				t.Fatalf("%v\n%s", err, got)
			}
		})
	}
}

// The renamed model files of the ECB baseline: their bytes were written by `modelspec rewrite --write` (CLI 0.2.0) on the pinned files, and the
// HCL exports (`modelspec export`, with the module id, version and name of the JSON) to the JSON. Only the digests are kept here.
func TestTheRenameOfTheBaselineModelIsWhatTheReferenceWrote(t *testing.T) {
	for role, c := range map[string]struct {
		rename func([]byte) ([]byte, error)
		digest string
	}{
		"model-hcl":  {renameHCL, renamedModelHCLSHA256},
		"model-json": {renameJSON, renamedModelJSONSHA256},
	} {
		t.Run(role, func(t *testing.T) {
			got, err := c.rename(bytesAt(t, "../pinchain/testdata/"+role))
			if err != nil || hash(got) != c.digest {
				t.Fatal(err, hash(got))
			}
		})
	}
}

func TestTheRenameRefusesWhatItDoesNotKnow(t *testing.T) {
	for name, c := range map[string]struct {
		rename func([]byte) ([]byte, error)
		input  string
	}{
		"json: not an object":             {renameJSON, `[]`},
		"json: not JSON":                  {renameJSON, `{`},
		"json: two objects":               {renameJSON, `{"modelspec":"1.0-draft"} {}`},
		"json: ends inside a value":       {renameJSON, `{"modelspec":"1.0-draft"`},
		"json: no identifier":             {renameJSON, `{"entities":{}}`},
		"json: the current identifier":    {renameJSON, `{"modelspec":"1.0-draft-2","records":{}}`},
		"json: an escaped key":            {renameJSON, `{"modelspec":"1.0-draft","enti\u0074ies":{}}`},
		"json: an escaped identifier":     {renameJSON, `{"modelspec":"1.0-dr\u0061ft","entities":{}}`},
		"json: an escaped member key":     {renameJSON, `{"modelspec":"1.0-draft","entities":{"A":{"properties":{"a":{"ent\u0069ty":"A"}}}}}`},
		"json: an escaped properties key": {renameJSON, `{"modelspec":"1.0-draft","entities":{"A":{"proper\u0074ies":{}}}}`},
		"hcl: a heredoc":                  {renameHCL, "entity \"A\" {\n  property \"a\" {\n    pattern = <<EOT\nx\nEOT\n  }\n}\n"},
		"hcl: a template":                 {renameHCL, "entity \"A\" {\n  property \"a\" {\n    pattern = \"${x}\"\n  }\n}\n"},
		"hcl: a percent template":         {renameHCL, "entity \"A\" {\n  property \"a\" {\n    pattern = \"%{x}\"\n  }\n}\n"},
		"hcl: an object value":            {renameHCL, "entity \"A\" {\n  tags = {a = 1}\n}\n"},
		"hcl: an unclosed comment":        {renameHCL, "/* entity"},
		"hcl: an unclosed string":         {renameHCL, "entity \"A {\n}\n"},
		"hcl: a string cut by the end":    {renameHCL, "entity \"A"},
		"hcl: an unmatched closing brace": {renameHCL, "}\n"},
		"hcl: an unclosed block":          {renameHCL, "entity \"A\" {\n"},
		"hcl: an unmatched bracket":       {renameHCL, "entity \"A\" {\n  key = [\"a\"]]\n}\n"},
		"hcl: an unclosed bracket":        {renameHCL, "entity \"A\" {\n  key = [\"a\"\n}\n"},
		"hcl: a brace in a bracket":       {renameHCL, "entity \"A\" {\n  key = [\"a\"\n}]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := c.rename([]byte(c.input)); err == nil {
				t.Fatalf("renamed to\n%s", got)
			}
		})
	}
}

// The renames are by position, not by word: the same words elsewhere are left as they are.
func TestTheRenameLeavesTheSameWordsElsewhere(t *testing.T) {
	hcl := "entity \"A\" {\n  note = \"property\"\n  key = [\n    \"entity\",\n  ]\n  property \"entity\" {\n    entity = \"A\"\n    other { entity = \"B\" }\n  }\n  other \"x\" {\n    property \"p\" {}\n  }\n}\nproperty \"p\" {}\n"
	want := "record \"A\" {\n  note = \"property\"\n  key = [\n    \"entity\",\n  ]\n  field \"entity\" {\n    record = \"A\"\n    other { entity = \"B\" }\n  }\n  other \"x\" {\n    property \"p\" {}\n  }\n}\nproperty \"p\" {}\n"
	if got, err := renameHCL([]byte(hcl)); err != nil || string(got) != want {
		t.Fatalf("%v\n%s", err, got)
	}
	json := `{"modelspec":"1.0-draft","properties":1,"entities":{"entities":{"entity":1,"properties":{"properties":{"entity":"A","properties":{}}}},"B":[{"entities":1}]},"entity":"x"}`
	wantJSON := `{"modelspec":"1.0-draft-2","properties":1,"records":{"entities":{"entity":1,"fields":{"properties":{"record":"A","properties":{}}}},"B":[{"entities":1}]},"entity":"x"}`
	if got, err := renameJSON([]byte(json)); err != nil || string(got) != wantJSON {
		t.Fatalf("%v\n%s", err, got)
	}
}
