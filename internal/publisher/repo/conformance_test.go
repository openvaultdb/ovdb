package repo

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// The conformance cases of the mapping of a manifest (decision 0012 of openvaultdb/openvaultdb): the Directory keeps them in
// scripts/fixtures/manifest-conformance.json, the Chinook pre-check runs the same bytes, and the generator of the manifest slice copies them to
// ../manifest/testdata/reference/manifest-conformance.json (its digest is in digests.json). Each case replaces the format, recordsets and recordset_entities of
// a manifest with the keys it gives and is run against the model of `models`, in both vocabularies; it says the verdict, the earliest stage at which the verdict
// can be reached (manifest: without the model; file: with it) and, for an accepted case, whether the notice about recordset_entities is told. Here every case is
// run through both stages of this check, in both profiles.

type conformanceCase struct {
	ID       string
	Manifest map[string]json.RawMessage
	Verdict  string
	Notice   bool
	Stage    string
}

type conformanceFile struct {
	Models map[string]json.RawMessage
	Cases  []conformanceCase
	Pairs  []struct {
		ID      string
		Cases   []string
		Verdict string
	}
}

func readConformance(t testing.TB) conformanceFile {
	t.Helper()
	raw, err := os.ReadFile("../manifest/testdata/reference/manifest-conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var f conformanceFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// conformanceManifest is the own manifest of this package with the keys of the case: the format line is replaced (or left out), and recordsets and
// recordset_entities are written as the JSON of the case, which is YAML.
func conformanceManifest(c conformanceCase) string {
	doc := ownManifest
	head, _, _ := strings.Cut(doc, "recordsets:\n")
	if format, ok := c.Manifest["format"]; ok {
		head = strings.Replace(head, "format: ovdb-manifest/draft-1", "format: "+string(format), 1)
	} else {
		head = strings.Replace(head, "format: ovdb-manifest/draft-1\n", "", 1)
	}
	for _, key := range []string{"recordsets", "recordset_entities"} {
		if value, ok := c.Manifest[key]; ok {
			head += key + ": " + string(value) + "\n"
		}
	}
	return head
}

// conformanceRepository is the good repository with the manifest of the case and the model of the case, whose module is the repository's.
func conformanceRepository(c conformanceCase, model json.RawMessage) *Memory {
	r := goodRepository()
	r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(conformanceManifest(c))}
	r.Nodes[modelPath] = Node{Kind: File, Content: []byte(strings.Replace(string(model), `"name":"shop"`, `"name":"chinook"`, 1))}
	return r
}

func TestEveryConformanceCaseOfTheMappingHasItsVerdictAtItsStage(t *testing.T) {
	f := readConformance(t)
	if len(f.Cases) < 50 || len(f.Models) != 2 {
		t.Fatalf("%d cases, %d models", len(f.Cases), len(f.Models))
	}
	mappings := map[string][]manifest.Recordset{}
	stages := map[string]int{}
	for _, c := range f.Cases {
		stages[c.Verdict+" "+c.Stage]++
		doc := conformanceManifest(c)
		for _, profile := range []manifest.Profile{manifest.Directory, manifest.Publisher} {
			// Without the model: a case of the manifest stage is refused, and a case of the file stage is not.
			m, findings := manifest.CheckManifest([]byte(doc), "ovdb.yaml", profile)
			if c.Verdict == "refuse" && c.Stage == "manifest" && len(findings) == 0 {
				t.Errorf("%s, profile %v: the manifest alone is accepted, and the case is refused at the manifest stage:\n%s", c.ID, profile, doc)
			}
			if (c.Verdict == "accept" || c.Stage == "file") && len(findings) != 0 {
				t.Errorf("%s, profile %v: the manifest alone is refused (%v), and the case is %s at the %q stage", c.ID, profile, findings, c.Verdict, c.Stage)
			}
			if c.Verdict == "accept" {
				mappings[c.ID] = m.Mapping.Value
				for i := range mappings[c.ID] {
					mappings[c.ID][i].Line = 0
					for j := range mappings[c.ID][i].Columns {
						mappings[c.ID][i].Columns[j].Line = 0
					}
				}
			}
			// With the model, in both vocabularies: the verdict of the case.
			for name, model := range f.Models {
				r := Check(conformanceRepository(c, model), Options{Profile: profile})
				if (c.Verdict == "accept") != r.OK() {
					t.Errorf("%s, profile %v, model %s: the case is %s, and the repository check finds %s\n%s", c.ID, profile, name, c.Verdict, findingTexts(r), doc)
				}
				if c.Verdict == "accept" && (len(r.Notices) == 1) != c.Notice {
					t.Errorf("%s, profile %v, model %s: the notice is told %v times, and the case says %v", c.ID, profile, name, len(r.Notices), c.Notice)
				}
				assertBounded(t, r.Findings)
			}
		}
	}
	// Two accepted cases that differ only in form give the same normalised mapping.
	for _, pair := range f.Pairs {
		if len(pair.Cases) != 2 || pair.Verdict != "accept" || !reflect.DeepEqual(mappings[pair.Cases[0]], mappings[pair.Cases[1]]) || len(mappings[pair.Cases[0]]) == 0 {
			t.Errorf("pair %s: the mappings are %+v and %+v", pair.ID, mappings[pair.Cases[0]], mappings[pair.Cases[1]])
		}
	}
	// The cases are not all of one kind.
	for _, stage := range []string{"accept ", "refuse manifest", "refuse file"} {
		if stages[stage] == 0 {
			t.Errorf("the conformance file has no case of kind %q: %v", stage, stages)
		}
	}
}
