package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func withoutMeaning(doc string) string {
	start, end := strings.Index(doc, "meaning:\n"), strings.Index(doc, "publisher:\n")
	return doc[:start] + doc[end:]
}

func TestOptionalMeaningBothManifestFormats(t *testing.T) {
	for _, format := range []string{ManifestFormat, ManifestFormatDraft2} {
		for _, profile := range []Profile{Directory, Publisher} {
			for _, form := range []string{"own", "shared"} {
				doc := ownManifest
				if form == "shared" {
					doc = strings.Replace(sharedManifest, "  url: https://github.com/acme\n", "  url: https://github.com/acme\n  repository: https://github.com/acme/chinook-hosting\n", 1)
				}
				doc = strings.Replace(doc, ManifestFormat, format, 1)
				doc = strings.Replace(withoutMeaning(doc), "  meaning: CC0-1.0\n", "", 1)
				t.Run(fmt.Sprintf("%s/%d/%s", format, profile, form), func(t *testing.T) {
					m, findings := CheckManifest([]byte(doc), "ovdb.yaml", profile)
					if len(findings) != 0 {
						t.Fatalf("model without meanings refused: %v", findings)
					}
					if !m.MeaningFile.Absent() || !m.MeaningAddress.Absent() || !m.GraphID.Absent() || !m.GraphAddress.Absent() || !m.LicenceMeaning.Absent() {
						t.Fatalf("absent section fabricated graph facts: %+v", m)
					}
					for _, value := range []string{"null", "{}", "[]", "false", "42", "meaning", "{file: model/chinook.meaning.yaml}", "{graph: {id: chinook}}"} {
						_, got := CheckManifest([]byte(doc+"meaning: "+value+"\n"), "ovdb.yaml", profile)
						if len(got) == 0 {
							t.Errorf("present malformed meaning %s accepted", value)
						}
					}
					for _, value := range []string{"null", "Unknown-9.0"} {
						bad := strings.Replace(doc, "  data: MIT\n", "  data: MIT\n  meaning: "+value+"\n", 1)
						_, got := CheckManifest([]byte(bad), "ovdb.yaml", Publisher)
						if len(got) == 0 {
							t.Errorf("supplied meaning licence %s bypassed validation", value)
						}
					}
				})
			}
		}
	}
}

func TestOptionalMeaningKeepsModelRequirements(t *testing.T) {
	own, shared := withoutMeaning(ownManifest), withoutMeaning(sharedManifest)
	for _, tc := range []struct{ doc, rule string }{
		{strings.Replace(own, "  modelspec: model/chinook.modelspec.json\n", "", 1), "manifest-required"},
		{strings.Replace(own, "  hcl: model/chinook.modelspec.hcl\n", "", 1), "manifest-required"},
		{strings.Replace(own, "  model: MIT\n", "", 1), "manifest-required"},
		{strings.Replace(own, "model/chinook.modelspec.json", "../escape.modelspec.json", 1), "manifest-model"},
		{strings.Replace(own, "datatug/chinookdb/chinook\n", "datatug/chinookdb/chinook?ref="+pin+"\n", 1), "manifest-model"},
		{strings.Replace(shared, "?ref="+pin, "", 1), "manifest-model"},
		{strings.Replace(shared, pin, "main", 1), "manifest-model"},
		{strings.Replace(shared, "model:\n  address: modelspec://github.com/datatug/chinookdb/chinook?ref="+pin+"\n", "", 1), "manifest-model"},
	} {
		for _, format := range []string{ManifestFormat, ManifestFormatDraft2} {
			_, findings := CheckManifest([]byte(strings.Replace(tc.doc, ManifestFormat, format, 1)), "ovdb.yaml", Publisher)
			if !slices.Contains(rulesOf(findings), tc.rule) {
				t.Errorf("want %s, got %v", tc.rule, findings)
			}
		}
	}
	// An identifier with no reader is still refused.
	_, findings := CheckManifest([]byte(strings.Replace(own, ManifestFormat, "ovdb-manifest/draft-3", 1)), "ovdb.yaml", Publisher)
	if !slices.Contains(rulesOf(findings), "manifest-format") {
		t.Fatal("unsupported format accepted")
	}
}

// optionalMeaningReferenceCase names only the exact transformations of frozen
// source documents whose verdict changes under the optional-section contract.
// No pinned reference verdict is rewritten or inferred from the Go checker.
func optionalMeaningReferenceCase(c referenceCase, bases map[string]string, profile Profile) bool {
	if c.Base == "chinook-yaml" && c.Family == "line" && profile == Directory {
		return bytes.Equal(c.Document, []byte(strings.Replace(bases[c.Base], "\nmeaning:\n", "\n", 1)))
	}
	if c.Base != "own-json" && c.Base != "shared-json" {
		return false
	}
	if c.Family != "remove" && !(c.Family == "rename" && profile == Directory) {
		return false
	}
	var original, current map[string]any
	if json.Unmarshal([]byte(bases[c.Base]), &original) != nil || json.Unmarshal(c.Document, &current) != nil {
		return false
	}
	meaning, exists := original["meaning"]
	if !exists {
		return false
	}
	delete(original, "meaning")
	if c.Family == "rename" {
		original["meaningx"] = meaning
	}
	return reflect.DeepEqual(original, current)
}

func TestFrozenReferenceOptionalMeaningCases(t *testing.T) {
	corpus, _, cases, _ := loadReference(t)
	for _, profile := range []Profile{Directory, Publisher} {
		count := 0
		for _, c := range cases {
			if !optionalMeaningReferenceCase(c, corpus.Bases, profile) {
				continue
			}
			count++
			if c.Verdict || (profile == Publisher && c.Publisher) {
				t.Fatal("optional meaning case no longer differs from frozen reference; revisit accounting")
			}
			_, findings := CheckManifest(c.Document, "ovdb.yaml", profile)
			if len(findings) != 0 {
				t.Fatalf("source-grounded optional meaning case refused: %v", findings)
			}
		}
		want := 5
		if profile == Publisher {
			want = 2
		}
		if count != want {
			t.Fatalf("%d: %d optional meaning cases, want %d", profile, count, want)
		}
	}
}

func TestOptionalMeaningReferenceMatcherIsNarrow(t *testing.T) {
	corpus, _, cases, _ := loadReference(t)
	for _, c := range cases {
		if !optionalMeaningReferenceCase(c, corpus.Bases, Directory) {
			continue
		}
		// Both JSON and YAML accept an appended mapping entry. Every supplied
		// malformed meaning and every unrelated edit must remain outside the
		// source-derived exception, regardless of the production verdict.
		if c.Base == "chinook-yaml" {
			for _, suffix := range []string{"meaning: null\n", "unrelated: changed\n"} {
				altered := c
				altered.Document = append(append([]byte(nil), c.Document...), []byte(suffix)...)
				if optionalMeaningReferenceCase(altered, corpus.Bases, Directory) {
					t.Fatal("YAML matcher accepted another transformation")
				}
			}
			continue
		}
		var original map[string]any
		if err := json.Unmarshal(c.Document, &original); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"meaning", "unrelated", "model"} {
			altered := c
			fields := make(map[string]any, len(original)+1)
			for name, value := range original {
				fields[name] = value
			}
			fields[key] = nil
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			altered.Document = raw
			for _, profile := range []Profile{Directory, Publisher} {
				if optionalMeaningReferenceCase(altered, corpus.Bases, profile) {
					t.Fatalf("matcher accepted changed %s", key)
				}
			}
		}
	}
}
