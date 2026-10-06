package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

// directory-stage.json: the Directory's own file stage (analyseDatabase, own form) on repositories made from the Directory's own fixtures, at the pinned
// commit, with the outcome that Go is expected to have against it (testdata/reference/directory-stage.mjs says what each outcome is). repository.json
// holds the verdicts of the Chinook checker; this is the reference of the bar (plan decision D0), which no test of this package compared Go with before.
type stageCase struct {
	ID        string
	Group     string
	Note      string
	Ops       [][]json.RawMessage
	Directory string
	Problem   string
	Outcome   string
	// Go is the rule of Go that refuses a case that both refuse, and AcceptedWhen is the record or registry under which the Directory accepts the same files.
	Go           string
	AcceptedWhen string
}

type stageGolden struct {
	Format     string
	Reference  string
	Repository string
	Base       map[string]string
	Cases      []stageCase
}

func readStageGolden(t testing.TB) stageGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/reference/directory-stage.json")
	if err != nil {
		t.Fatal(err)
	}
	digests, err := os.ReadFile("testdata/reference/directory-stage.digest.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(digests, &want); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); want["repo/testdata/reference/directory-stage.json"] != hex.EncodeToString(sum[:]) {
		t.Error("directory-stage.json does not match its digest: it was edited by hand, or directory-stage.mjs was not run; run `node internal/publisher/repo/testdata/reference/directory-stage.mjs`")
	}
	var g stageGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if g.Format != "ovdb-directory-stage/1" || len(g.Cases) == 0 {
		t.Fatalf("golden: format %q, %d cases", g.Format, len(g.Cases))
	}
	return g
}

// stageStricterKinds are the ways in which Go refuses a repository that the Directory's file stage accepts, each with the reason. Each is a choice of the
// Publisher check that a slice may change (A0w makes model.hcl optional, as the Directory has it); none is a rule of the Directory.
var stageStricterKinds = map[string]string{
	"model-hcl-required":       "model.hcl is required by the manifest rules of the Publisher profile; the Directory reads it only when it is written, and takes the model file from the meaning file's models: entry (A0w)",
	"meaning-license-required": "the meaning file's license must be text and equal to licences.meaning; the Directory compares it only when the file has a text license",
	"meaning-id-compared":      "the meaning file's id must be meaning.graph.id; the Directory does not read the id of the file in the file stage (the registry holds the graph's id)",
}

// stageVerdict is what Go says of a case, with the profile and with --repository as a hoster runs it.
func stageVerdict(t testing.TB, g stageGolden, c stageCase, profile manifest.Profile) manifest.Result {
	t.Helper()
	m := build(t, golden{Base: g.Base}, c.Ops)
	repository := g.Repository
	return Check(m.memory(), Options{Profile: profile, Repository: &repository})
}

// firstRule is the rule of the first finding of a result, or "".
func firstRule(r manifest.Result) string {
	if len(r.Findings) == 0 {
		return ""
	}
	return r.Findings[0].Rule
}

func TestEveryCaseOfTheDirectoryStageIsInTheRelationItsOutcomeSays(t *testing.T) {
	g := readStageGolden(t)
	counts := map[string]int{}
	for _, c := range g.Cases {
		directory := stageVerdict(t, g, c, manifest.Directory)
		publisher := stageVerdict(t, g, c, manifest.Publisher)
		if !directory.OK() && publisher.OK() {
			t.Errorf("%s: the Directory profile refuses and the Publisher profile accepts: the Publisher profile is the Directory's rules plus extras", c.ID)
		}
		var relation string
		switch {
		case c.Directory == "refuses" && directory.OK():
			relation = "looser"
		case c.Directory == "accepts" && (!directory.OK() || !publisher.OK()):
			relation = "stricter"
		default:
			relation = "agree"
		}
		switch relation {
		case "agree":
			if c.Outcome != "agree" {
				t.Errorf("%s: Go and the Directory agree, and the golden says %q", c.ID, c.Outcome)
			}
		case "looser":
			if !strings.HasPrefix(c.Outcome, "looser:") && !strings.HasPrefix(c.Outcome, "out-of-reach:") {
				t.Errorf("%s: Go accepts what the Directory refuses (%s), and the golden says %q", c.ID, c.Problem, c.Outcome)
			}
		case "stricter":
			kind, ok := strings.CutPrefix(c.Outcome, "stricter:")
			if _, known := stageStricterKinds[kind]; !ok || !known {
				t.Errorf("%s: Go refuses what the Directory accepts, and the golden says %q", c.ID, c.Outcome)
			}
		}
		if first := firstRule(directory); c.Directory == "refuses" && c.Outcome == "agree" {
			if c.Go == "" || c.Go != first {
				t.Errorf("%s: Go refuses it as %q (%s profile), and the golden says it should be %q: a case agrees only through the rule that is meant", c.ID, first, "Directory", c.Go)
			}
		} else if c.Go != "" {
			t.Errorf("%s: the golden names the rule %q of Go for a case that is not a refusal both make", c.ID, c.Go)
		}
		if strings.HasPrefix(c.Outcome, "out-of-reach:") && c.AcceptedWhen == "" {
			t.Errorf("%s: an out-of-reach case says under which record or registry the Directory accepts the same files", c.ID)
		}
		counts[c.Outcome]++
	}
	for kind := range stageStricterKinds {
		if counts["stricter:"+kind] == 0 {
			t.Errorf("the stricter kind %q is recorded and no case shows it", kind)
		}
	}
	var names []string
	for outcome := range counts {
		names = append(names, outcome)
	}
	sort.Strings(names)
	for _, outcome := range names {
		t.Logf("%-36s %d", outcome, counts[outcome])
	}
}

// drift.json lists, for each rule of the Directory's file stage that Go lacks and a repository alone can check, the cases that show it and the slice
// that ports it; and this test holds the list to the golden in both directions, as TestDrift does for the manifest stage.
func TestDriftNamesEveryLooserCaseOfTheDirectoryStage(t *testing.T) {
	g := readStageGolden(t)
	raw, err := os.ReadFile("../manifest/testdata/reference/drift.json")
	if err != nil {
		t.Fatal(err)
	}
	var drift struct {
		GoLooser []struct {
			Kind       string
			Slice      string
			FileProbes []string
		}
	}
	if err := json.Unmarshal(raw, &drift); err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{} // case -> slice
	for _, e := range drift.GoLooser {
		for _, id := range e.FileProbes {
			if _, dup := listed[id]; dup {
				t.Errorf("case %s is listed twice in drift.json", id)
			}
			listed[id] = e.Slice
		}
	}
	for _, c := range g.Cases {
		slice, ok := strings.CutPrefix(c.Outcome, "looser:")
		switch {
		case ok && listed[c.ID] != slice:
			t.Errorf("case %s is looser in slice %s, and drift.json lists it as %q", c.ID, slice, listed[c.ID])
		case !ok && listed[c.ID] != "":
			t.Errorf("drift.json lists case %s (slice %s), whose outcome is %q", c.ID, listed[c.ID], c.Outcome)
		}
		delete(listed, c.ID)
	}
	for id := range listed {
		t.Errorf("drift.json lists the case %s, which directory-stage.json does not have", id)
	}
}

// The README holds the number of cases of each outcome, so that a case cannot be added, dropped or re-labelled without the table that says what the
// file stage's parity is made of.
func TestReadmeCountsTheOutcomesOfTheDirectoryStage(t *testing.T) {
	g := readStageGolden(t)
	counts := map[string]int{}
	for _, c := range g.Cases {
		counts[c.Outcome]++
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(string(readme), "\n") {
		if cells := strings.Split(line, "|"); len(cells) == 5 && strings.HasPrefix(strings.TrimSpace(cells[1]), "outcome: ") {
			rows[strings.TrimPrefix(strings.TrimSpace(cells[1]), "outcome: ")] = strings.TrimSpace(cells[2])
		}
	}
	for outcome, n := range counts {
		if got := rows[outcome]; got != itoa(n) {
			t.Errorf("README row of outcome %q is %q, want %d", outcome, got, n)
		}
	}
	for outcome := range rows {
		if counts[outcome] == 0 {
			t.Errorf("README has a row for the outcome %q, which no case has", outcome)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
