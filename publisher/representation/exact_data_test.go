package representation

import (
	"encoding/json"
	"strings"
	"testing"
)

func exactDataFixture(t *testing.T, name string) (Document, Context, Reference) {
	t.Helper()
	data, ctx, _ := realFixture(t, name)
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	doc.Format = Format3
	ref := Reference{Repository: "https://github.com/example/input", Revision: strings.Repeat("b", 40), Path: "input/rows.json", SHA256: Hash([]byte(`[{"id":"one"}]`))}
	doc.Contracts[0].Source.Data = &ref
	return *doc, ctx, ref
}

func TestExactDataFormatIsClosedAndMetadataOnly(t *testing.T) {
	for _, name := range []string{"real-ror", "native-geonames"} {
		t.Run(name, func(t *testing.T) {
			doc, ctx, ref := exactDataFixture(t, name)
			calls := 0
			resolve := ctx.Resolve
			ctx.Resolve = func(r Reference) ([]byte, error) {
				if r == ref {
					t.Fatal("source.data reached metadata Resolve")
				}
				calls++
				return resolve(r)
			}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := Check(data, ctx)
			if err != nil || parsed.Format != Format3 || calls == 0 {
				t.Fatalf("format3 metadata check: %v, calls=%d", err, calls)
			}
			for _, schema := range [][]byte{Schema(), Schema2()} {
				s, err := compileSchema("old.json", schema)
				if err != nil {
					t.Fatal(err)
				}
				var value any
				_ = json.Unmarshal(data, &value)
				if err := s.Validate(value); err == nil {
					t.Fatal("old reader accepted format3")
				}
			}
			doc.Format = Format2
			legacyWithData, _ := json.Marshal(doc)
			if _, err := Parse(legacyWithData); err == nil {
				t.Fatal("format2 accepted source.data")
			}
			doc.Format = Format3
			doc.Contracts[0].Source.Data = nil
			missing, _ := json.Marshal(doc)
			if _, err := Parse(missing); err == nil {
				t.Fatal("format3 accepted absent source.data")
			}
			doc.Contracts[0].Source.Data = &ref
			doc.Contracts[0].Execution = LabelBridge
			wrongMode, _ := json.Marshal(doc)
			if _, err := Parse(wrongMode); err == nil {
				t.Fatal("format3 accepted label bridge")
			}
			doc.Contracts[0].Execution = NativeIdentifier
			closed, _ := json.Marshal(doc)
			unknown := strings.Replace(string(closed), `"data":{`, `"data":{"unknown":true,`, 1)
			if _, err := Parse([]byte(unknown)); err == nil {
				t.Fatal("format3 accepted unknown source.data field")
			}
			duplicate := strings.Replace(string(closed), `"data":{`, `"data":{},"data":{`, 1)
			if _, err := Parse([]byte(duplicate)); err == nil {
				t.Fatal("format3 accepted duplicate JSON member")
			}
		})
	}
}

func TestExactDataCoordinatesAndSourceEquality(t *testing.T) {
	doc, ctx, ref := exactDataFixture(t, "real-ror")
	for name, mutate := range map[string]func(*Reference){
		"repository": func(r *Reference) { r.Repository = "https://github.com/example/other" },
		"revision":   func(r *Reference) { r.Revision = strings.Repeat("c", 40) },
		"path":       func(r *Reference) { r.Path = "input/other.json" },
		"sha256":     func(r *Reference) { r.SHA256 = strings.Repeat("d", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			d := doc
			d.Contracts = append([]Contract(nil), doc.Contracts...)
			changed := ref
			mutate(&changed)
			d.Contracts[0].Source.Data = &changed
			if sameSource(doc.Contracts[0].Source, d.Contracts[0].Source) {
				t.Fatal("changed exact coordinate retained source identity")
			}
			original := doc.Contracts[0]
			if _, err := LookupNative(original, d.Contracts[0].Source, ctx, "id", func(NativeLookupRequest) ([]string, error) {
				t.Fatal("mismatched source reached target query")
				return nil, nil
			}); err == nil {
				t.Fatal("changed exact coordinate accepted for lookup")
			}
		})
	}
	for _, bad := range []Reference{
		{Repository: ctx.Repository, Revision: ref.Revision, Path: ref.Path, SHA256: ref.SHA256},
		{Repository: ref.Repository, Revision: "main", Path: ref.Path, SHA256: ref.SHA256},
		{Repository: ref.Repository, Revision: ref.Revision, Path: "../rows.json", SHA256: ref.SHA256},
		{Repository: ref.Repository, Revision: ref.Revision, Path: ref.Path, SHA256: "bad"},
	} {
		doc.Contracts[0].Source.Data = &bad
		data, _ := json.Marshal(doc)
		if _, err := Check(data, ctx); err == nil {
			t.Fatalf("invalid data coordinate accepted: %+v", bad)
		}
	}
}
