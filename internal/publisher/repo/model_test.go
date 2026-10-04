package repo

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestReadModel(t *testing.T) {
	nest := func(n int) string {
		return `{"x":` + strings.Repeat("[", n) + strings.Repeat("]", n) + `,"module":{"name":"m"},"entities":{}}`
	}
	for name, c := range map[string]struct {
		in        string
		module    string
		entities  []string // nil when there is no entities object
		err       error    // the error, or nil; a syntax error is matched by its type
		syntax    bool
		truncated bool
	}{
		"a model":                            {in: `{"module":{"name":"chinook"},"entities":{"B":{},"A":[1,{"x":null}]}}`, module: "chinook", entities: []string{"A", "B"}},
		"white space around it":              {in: " \t\r\n{\"module\":{\"name\":\"m\"},\"entities\":{}}\n\n", module: "m", entities: []string{}},
		"a repeated module, the last wins":   {in: `{"module":{"name":"a"},"module":{"name":"b"},"entities":{}}`, module: "b", entities: []string{}},
		"a repeated module, the last bad":    {in: `{"module":{"name":"a"},"module":5,"entities":{}}`, module: "", entities: []string{}},
		"a repeated module that is a list":   {in: `{"module":{"name":"a"},"module":[{"name":"b"}],"entities":{}}`, module: "", entities: []string{}},
		"a repeated name, the last wins":     {in: `{"module":{"name":"a","name":"b"},"entities":{}}`, module: "b", entities: []string{}},
		"a repeated name, the last bad":      {in: `{"module":{"name":"a","name":7},"entities":{}}`, module: "", entities: []string{}},
		"a name with something else beside":  {in: `{"module":{"id":"x","name":"a","other":[[1]]},"entities":{}}`, module: "a", entities: []string{}},
		"a repeated entities, the last":      {in: `{"entities":{"X":1},"module":{"name":"a"},"entities":{"Y":1}}`, module: "a", entities: []string{"Y"}},
		"a repeated entities, then a list":   {in: `{"entities":{"X":1},"entities":[]}`, entities: nil},
		"a repeated entity":                  {in: `{"entities":{"X":1,"X":2}}`, entities: []string{"X"}},
		"__proto__ as an entity":             {in: `{"entities":{"__proto__":{}}}`, entities: []string{"__proto__"}},
		"a key with an escape":               {in: `{"module":{"name":"a"},"entities":{}}`, module: "a", entities: []string{}},
		"a name with an escape":              {in: `{"module":{"name":"ab"}}`, module: "ab"},
		"a name that is not a module name":   {in: `{"module":{"name":"1a"}}`},
		"a name with a line break":           {in: `{"module":{"name":"a\n"}}`},
		"a lone surrogate":                   {in: `{"x":"\ud800","module":{"name":"a"}}`, module: "a"},
		"bytes that are not UTF-8":           {in: "{\"x\":\"a\xffb\",\"module\":{\"name\":\"a\"}}", module: "a"},
		"a number too big for a double":      {in: `{"x":1e999,"y":-0,"z":123456789012345678901234567890,"module":{"name":"a"}}`, module: "a"},
		"module a string":                    {in: `{"module":"a"}`},
		"entities null":                      {in: `{"entities":null}`},
		"entities an object of nonsense":     {in: `{"entities":{"a":[[[{}]]]}}`, entities: []string{"a"}},
		"100 levels":                         {in: nest(99), module: "m", entities: []string{}},
		"101 levels":                         {in: nest(100), err: errDepth},
		"keys written in capitals":           {in: `{"Module":{"name":"a"},"Entities":{"X":1}}`},
		"the key Name in capitals":           {in: `{"module":{"Name":"a"},"entities":{}}`, entities: []string{}},
		"the key Entities in capitals":       {in: `{"module":{"name":"a"},"Entities":{"X":1}}`, module: "a"},
		"the key Module in capitals":         {in: `{"Module":{"name":"a"},"entities":{}}`, entities: []string{}},
		"100 levels below module":            {in: `{"module":{"name":"a","x":` + strings.Repeat("[", 98) + strings.Repeat("]", 98) + `},"entities":{}}`, module: "a", entities: []string{}},
		"101 levels below module":            {in: `{"module":{"name":"a","x":` + strings.Repeat("[", 99) + strings.Repeat("]", 99) + `},"entities":{}}`, err: errDepth},
		"100 levels below an entity":         {in: `{"entities":{"E":{"x":` + strings.Repeat("[", 97) + strings.Repeat("]", 97) + `}}}`, entities: []string{"E"}},
		"101 levels below an entity":         {in: `{"entities":{"E":{"x":` + strings.Repeat("[", 98) + strings.Repeat("]", 98) + `}}}`, err: errDepth},
		"a value of 100 levels in an entity": {in: `{"entities":{"E":` + strings.Repeat("[", 98) + strings.Repeat("]", 98) + `}}`, entities: []string{"E"}},
		"MaxEntities entities":               {in: manyEntities(MaxEntities), module: "", entities: slices.Sorted(maps.Keys(entityNames(MaxEntities)))},
		"one more than MaxEntities":          {in: manyEntities(MaxEntities + 1), err: errEntities},
		"101 levels in an entity":            {in: `{"entities":{"a":` + strings.Repeat("[", 99) + strings.Repeat("]", 99) + `}}`, err: errDepth},
		"a value of 100000 levels":           {in: nest(100000), err: errDepth},
		"empty":                              {in: "", err: io.EOF},
		"only white space":                   {in: " \n", err: io.EOF},
		"a list":                             {in: `[]`, err: errNotObject},
		"null":                               {in: `null`, err: errNotObject},
		"a string":                           {in: `"x"`, err: errNotObject},
		"a number":                           {in: `0`, err: errNotObject},
		"text after":                         {in: `{} x`, syntax: true},
		"a second value":                     {in: `{}{}`, err: errTrailing},
		"a byte order mark":                  {in: "\xef\xbb\xbf{}", syntax: true},
		"a trailing comma":                   {in: `{"a":1,}`, syntax: true},
		"a comment":                          {in: `{/**/}`, syntax: true},
		"a key that is not a string":         {in: `{1:2}`, syntax: true},
		"ends in a value":                    {in: `{"a":`, truncated: true},
		"ends in a list":                     {in: `{"a":[1,2`, truncated: true},
		"ends in a module":                   {in: `{"module":{"name":`, truncated: true},
		"ends in entities":                   {in: `{"entities":{"a":`, truncated: true},
		"a bad token in an unrelated list":   {in: `{"a":[1,x]}`, syntax: true},
		"a bad token in a module":            {in: `{"module":{"name":x}}`, syntax: true},
		"a bad token in entities":            {in: `{"entities":{"a":x}}`, syntax: true},
		"a bad token in a nested object":     {in: `{"a":{"b":x}}`, syntax: true},
		"a key without a value":              {in: `{"a"}`, syntax: true},
		"a missing key":                      {in: `{,}`, syntax: true},
	} {
		spec, err := readModel([]byte(c.in))
		var syntax *json.SyntaxError
		switch {
		case c.truncated:
			if err == nil {
				t.Errorf("%s: accepted a truncated value", name)
			}
			continue
		case c.syntax:
			if !errors.As(err, &syntax) {
				t.Errorf("%s: err = %v, want a syntax error", name, err)
			}
			continue
		case c.err != nil:
			if !errors.Is(err, c.err) {
				t.Errorf("%s: err = %v, want %v", name, err, c.err)
			}
			continue
		case err != nil:
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if spec.module != c.module || spec.hasEntities != (c.entities != nil) || !slices.Equal(spec.entities, c.entities) {
			t.Errorf("%s: spec = %+v, want module %q entities %v", name, spec, c.module, c.entities)
		}
	}
}

// manyEntities is a model with n entities called e0, e1 ... in base 36.
func manyEntities(n int) string {
	var b strings.Builder
	b.WriteString(`{"entities":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"e` + strconv.FormatInt(int64(i), 36) + `":{}`)
	}
	b.WriteString("}}")
	return b.String()
}

func entityNames(n int) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < n; i++ {
		out["e"+strconv.FormatInt(int64(i), 36)] = true
	}
	return out
}

func TestIsModulePattern(t *testing.T) {
	for in, want := range map[string]bool{"a": true, "A": true, "chinook": true, "a_1": true, "aZ09_": true, "": false, "1a": false, "_a": false, "a-b": false, "a b": false, "a\n": false, "é": false, "aé": false, "a\x00": false} {
		if isModulePattern(in) != want {
			t.Errorf("isModulePattern(%q) = %v", in, !want)
		}
	}
}

// What readModel accepts, the standard library says is JSON, and an object; it never panics, and its answer does not depend on the file being
// valid UTF-8.
func FuzzReadModel(f *testing.F) {
	for _, seed := range []string{goodModel, `{}`, `[]`, ``, `{"module":{"name":"a"},"module":1}`, `{"entities":{"__proto__":0}}`, "\xef\xbb\xbf{}", `{"a":[[[[]]]]}{}`, `{"module":{"name":"a\ud800"}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		spec, err := readModel(data)
		if err != nil {
			return
		}
		if !json.Valid(data) {
			t.Fatalf("accepted what the standard library refuses: %q", data)
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(data, &top) != nil {
			t.Fatalf("accepted what is not an object: %q", data)
		}
		if spec.module != "" && !isModulePattern(spec.module) || !slices.IsSorted(spec.entities) || (!spec.hasEntities && len(spec.entities) > 0) {
			t.Fatalf("spec %+v", spec)
		}
	})
}
