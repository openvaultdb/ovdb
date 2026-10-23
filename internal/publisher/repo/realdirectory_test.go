package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// The databases registered in the OVDB Directory at the commit the references pin (internal/publisher/references.mjs), checked as they are
// registered: each repository at the commit its record names, by the real git, under both profiles, with --repository the record's repository.
//
//	testdata/directory/entries.json   the entries (made by entries.mjs from the Directory at the pin; its --check is in the goldens job)
//	testdata/directory/verdicts.json  what this check says of each entry: the findings, by file and rule, with their counts
//
// The default `go test` reads both files and judges nothing on the network. With OVDB_REAL_DIRECTORY=1 the test fetches each repository (the network, and
// git) and compares what it finds with verdicts.json; with OVDB_REAL_DIRECTORY=update it writes verdicts.json. The verdicts are not a promise that the
// entries pass: they are the record of what Go says of them, which testdata/../../manifest/testdata/reference/drift.json must explain finding by finding.

type realEntry struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Manifest   string `json:"manifest"`
	URL        string `json:"url"`
}

type realEntries struct {
	Format   string `json:"format"`
	Registry struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
	} `json:"registry"`
	Entries []realEntry `json:"entries"`
}

// RealFinding is a kind of finding and how many of that kind an entry has.
type RealFinding struct {
	Path  string `json:"path"`
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

// RealProfile is the verdict of one profile on one entry.
type RealProfile struct {
	OK       bool          `json:"ok"`
	Findings []RealFinding `json:"findings"`
}

type realVerdict struct {
	ID        string      `json:"id"`
	Commit    string      `json:"commit"`
	Publisher RealProfile `json:"publisher"`
	Directory RealProfile `json:"directory"`
}

type realVerdicts struct {
	Format   string        `json:"format"`
	Registry string        `json:"registry"`
	Entries  []realVerdict `json:"entries"`
}

func readJSON(t testing.TB, name string, into any) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "directory", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b
}

func summarise(result manifest.Result) RealProfile {
	counts := map[[2]string]int{}
	for _, f := range result.Findings {
		counts[[2]string{f.Document, f.Rule}]++
	}
	out := RealProfile{OK: result.OK(), Findings: []RealFinding{}}
	for k, n := range counts {
		out.Findings = append(out.Findings, RealFinding{Path: k[0], Rule: k[1], Count: n})
	}
	sort.Slice(out.Findings, func(i, j int) bool {
		a, b := out.Findings[i], out.Findings[j]
		return a.Path < b.Path || a.Path == b.Path && a.Rule < b.Rule
	})
	return out
}

// fetchEntry makes the repository of an entry at its commit, as a plain clone of one commit.
func fetchEntry(t testing.TB, e realEntry) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), e.ID)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "init", "--quiet", "-b", "main")
	git(t, dir, nil, "fetch", "--quiet", "--depth", "1", e.Repository, e.Commit)
	git(t, dir, nil, "checkout", "--quiet", "--detach", "FETCH_HEAD")
	if got := git(t, dir, nil, "rev-parse", "HEAD"); got != e.Commit {
		t.Fatalf("%s: fetched %s, wanted %s", e.ID, got, e.Commit)
	}
	return dir
}

func realVerdictOf(t testing.TB, e realEntry) realVerdict {
	t.Helper()
	dir := fetchEntry(t, e)
	repository := e.Repository
	out := realVerdict{ID: e.ID, Commit: e.Commit}
	for _, p := range []struct {
		profile manifest.Profile
		into    *RealProfile
	}{{manifest.Publisher, &out.Publisher}, {manifest.Directory, &out.Directory}} {
		*p.into = summarise(Check(NewGit(ExecRunner{Dir: dir}), Options{Repository: &repository, Profile: p.profile}))
	}
	return out
}

func TestRealDirectoryRecordsAreConsistent(t *testing.T) {
	var entries realEntries
	var verdicts realVerdicts
	readJSON(t, "entries.json", &entries)
	readJSON(t, "verdicts.json", &verdicts)
	if entries.Format != "ovdb-real-entries/1" || verdicts.Format != "ovdb-real-verdicts/1" {
		t.Fatalf("formats: %q, %q", entries.Format, verdicts.Format)
	}
	if want := entries.Registry.Repository + "@" + entries.Registry.Commit; verdicts.Registry != want {
		t.Errorf("verdicts are of %q, entries are of %q", verdicts.Registry, want)
	}
	if len(entries.Entries) < 6 || len(verdicts.Entries) != len(entries.Entries) {
		t.Fatalf("%d entries, %d verdicts", len(entries.Entries), len(verdicts.Entries))
	}
	for i, e := range entries.Entries {
		v := verdicts.Entries[i]
		if v.ID != e.ID || v.Commit != e.Commit {
			t.Errorf("verdict %d is of %s@%s, entry is %s@%s", i, v.ID, v.Commit, e.ID, e.Commit)
		}
		if e.Manifest == "" || e.URL == "" || e.Repository == "" {
			t.Errorf("entry %s is incomplete: %+v", e.ID, e)
		}
		for name, p := range map[string]RealProfile{"publisher": v.Publisher, "directory": v.Directory} {
			if p.OK != (len(p.Findings) == 0) {
				t.Errorf("%s %s: ok is %v with %d kinds of finding", e.ID, name, p.OK, len(p.Findings))
			}
		}
	}
}

func TestRealDirectoryEntries(t *testing.T) {
	mode := os.Getenv("OVDB_REAL_DIRECTORY")
	if mode == "" {
		t.Skip("set OVDB_REAL_DIRECTORY=1 to fetch the Directory's registered repositories and compare with testdata/directory/verdicts.json (OVDB_REAL_DIRECTORY=update writes it)")
	}
	var entries realEntries
	readJSON(t, "entries.json", &entries)
	got := realVerdicts{Format: "ovdb-real-verdicts/1", Registry: entries.Registry.Repository + "@" + entries.Registry.Commit}
	for _, e := range entries.Entries {
		got.Entries = append(got.Entries, realVerdictOf(t, e))
	}
	if mode == "update" {
		b, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("testdata", "directory", "verdicts.json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	var want realVerdicts
	readJSON(t, "verdicts.json", &want)
	if !reflect.DeepEqual(got, want) {
		gb, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("the verdicts of the registered repositories are not testdata/directory/verdicts.json; run with OVDB_REAL_DIRECTORY=update and explain the change in manifest/testdata/reference/drift.json:\n%s", gb)
	}
}
