package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// drift.json lists what Go does not yet do as the reference (the Directory at its pinned commit) does, in two classes: where Go is looser than the
// reference (it accepts what the reference refuses), which breaks the bar until its slice lands and so goes first, and where Go is stricter. Each entry
// is accounted for by probes (manifests made for the purpose, with the reference's verdict, in drift.probes.json), by findings on the repositories
// registered in the Directory (repo/testdata/directory/verdicts.json), or by documents of the corpus. TestDrift fails when the list and what Go does
// disagree in either direction: a difference that is not listed, and an entry that is no longer true.

type driftEntry struct {
	Kind      string      `json:"kind"`
	Direction string      `json:"direction"`
	Profile   string      `json:"profile"`
	Slice     string      `json:"slice"`
	Why       string      `json:"why"`
	Probes    []string    `json:"probes"`
	Real      []driftPair `json:"real"`
	Corpus    int         `json:"corpus"`
}

type driftPair struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
}

type driftFile struct {
	Format    string `json:"format"`
	Reference struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
	} `json:"reference"`
	About      string       `json:"about"`
	GoLooser   []driftEntry `json:"goLooser"`
	GoStricter []driftEntry `json:"goStricter"`
}

func readDrift(t testing.TB) driftFile {
	t.Helper()
	var d driftFile
	readGolden(t, "drift.json", &d)
	return d
}

// recordedProbeKinds are the stricter kinds that only probes show (no document of the corpus has them, so they have no row in the tables of sharedKinds):
// what Go does differently from the reference on purpose, with the reason. A probe names one in `recorded`.
var recordedProbeKinds = map[string]string{
	"representation-hash-list": "a sha256 written as a list of one hash is read by the Directory's regular expression as that hash; Go refuses it (README)",
}

var driftSlice = regexp.MustCompile(`^(A0[a-z]|A[1-7]|X1|P-n)$`)

// driftCorpusLooser is the number of documents of the corpus on which Go is looser than the Directory, as drift.json says.
func driftCorpusLooser(t testing.TB) int {
	n := 0
	for _, e := range readDrift(t).GoLooser {
		if e.Profile == "directory" {
			n += e.Corpus
		}
	}
	return n
}

func TestDrift(t *testing.T) {
	d := readDrift(t)
	if d.Format != "ovdb-drift/1" {
		t.Fatalf("format %q", d.Format)
	}
	var probes struct {
		Format string `json:"format"`
		Probes []struct {
			ID        string `json:"id"`
			Note      string `json:"note"`
			Document  string `json:"document"`
			Reference int    `json:"reference"`
			// Recorded names a stricter kind of the README's tables (sharedKinds) that Go is expected to differ by on this probe: a choice of this check, not a
			// slice to land. The probe is accounted for by that kind, and fails if Go refuses it for another reason, or agrees with the reference.
			Recorded string `json:"recorded"`
		} `json:"probes"`
	}
	readGolden(t, "drift.probes.json", &probes)
	if probes.Format != "ovdb-drift-probes/1" {
		t.Fatalf("probes format %q", probes.Format)
	}

	// What is listed.
	type key struct{ profile, direction, probe string }
	listed := map[key]string{}
	listedPairs := map[string]map[driftPair]bool{"publisher": {}, "directory": {}}
	corpus := 0
	for class, entries := range map[string][]driftEntry{"looser": d.GoLooser, "stricter": d.GoStricter} {
		for _, e := range entries {
			if e.Direction != class {
				t.Errorf("entry %q (%s) is in the class %s but says %s", e.Kind, e.Profile, class, e.Direction)
			}
			if e.Profile != "directory" && e.Profile != "publisher" {
				t.Errorf("entry %q: profile %q", e.Kind, e.Profile)
			}
			if e.Kind == "" || e.Why == "" || !driftSlice.MatchString(e.Slice) {
				t.Errorf("entry %q (%s): a kind, a reason and a slice (A0a to A0z, A1 to A7, X1, P-n) are required: %+v", e.Kind, e.Profile, e)
			}
			if len(e.Probes) == 0 && len(e.Real) == 0 && e.Corpus == 0 {
				t.Errorf("entry %q (%s) accounts for nothing: no probe, no finding of a registered repository, no corpus document", e.Kind, e.Profile)
			}
			for _, p := range e.Probes {
				k := key{e.Profile, e.Direction, p}
				if _, dup := listed[k]; dup {
					t.Errorf("probe %s is listed twice for %s, %s", p, e.Profile, e.Direction)
				}
				listed[k] = e.Kind
			}
			for _, p := range e.Real {
				if listedPairs[e.Profile][p] {
					t.Errorf("finding %v is listed twice for %s", p, e.Profile)
				}
				listedPairs[e.Profile][p] = true
			}
			if e.Direction == "looser" && e.Profile == "directory" {
				corpus += e.Corpus
			} else if e.Corpus != 0 {
				t.Errorf("entry %q (%s) counts corpus documents; only the Directory profile's looser entries do", e.Kind, e.Profile)
			}
		}
	}

	// What Go does with the probes.
	observed := map[key]bool{}
	for _, p := range probes.Probes {
		for _, profile := range []struct {
			name string
			p    Profile
		}{{"directory", Directory}, {"publisher", Publisher}} {
			ok, first := acceptManifest(profile.p, []byte(p.Document))
			if p.Recorded != "" {
				if _, shared := sharedKinds[p.Recorded]; !shared && recordedProbeKinds[p.Recorded] == "" {
					t.Errorf("probe %s names the recorded kind %s, which is neither in sharedKinds nor in recordedProbeKinds", p.ID, p.Recorded)
				}
				if ok || p.Reference != 1 {
					t.Errorf("probe %s names the recorded kind %s but Go (%s profile) agrees with the reference: remove the kind from the probe", p.ID, p.Recorded, profile.name)
				} else if got := kindOf(first); got != p.Recorded {
					t.Errorf("probe %s: Go (%s profile) refuses it as %s, not as the recorded kind %s: %s", p.ID, profile.name, got, p.Recorded, first.Message)
				}
				continue
			}
			switch {
			case ok && p.Reference == 0:
				observed[key{profile.name, "looser", p.ID}] = true
			case !ok && p.Reference == 1:
				observed[key{profile.name, "stricter", p.ID}] = true
			}
		}
	}
	for k := range observed {
		if _, ok := listed[k]; !ok {
			t.Errorf("probe %s: Go (%s profile) is %s than the reference and drift.json does not list it", k.probe, k.profile, k.direction)
		}
	}
	for k, kind := range listed {
		if !observed[k] {
			t.Errorf("drift.json lists probe %s as %s than the reference for the %s profile (%s), but Go now agrees with the reference: remove it", k.probe, k.direction, k.profile, kind)
		}
	}
	known := map[string]bool{}
	for _, p := range probes.Probes {
		known[p.ID] = true
	}
	for k := range listed {
		if !known[k.probe] {
			t.Errorf("drift.json lists probe %s, which drift.probes.json does not have", k.probe)
		}
	}

	// What Go says of the repositories registered in the Directory, at the pinned commit of the registry.
	var real struct {
		Registry string `json:"registry"`
		Entries  []struct {
			ID        string                         `json:"id"`
			Publisher struct{ Findings []driftPair } `json:"publisher"`
			Directory struct{ Findings []driftPair } `json:"directory"`
		} `json:"entries"`
	}
	b, err := os.ReadFile(filepath.Join("..", "repo", "testdata", "directory", "verdicts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &real); err != nil {
		t.Fatal(err)
	}
	if want := d.Reference.Repository + "@" + d.Reference.Commit; real.Registry != want {
		t.Errorf("the registered repositories' verdicts are of %q, the drift list is of %q", real.Registry, want)
	}
	seen := map[string]map[driftPair]bool{"publisher": {}, "directory": {}}
	for _, e := range real.Entries {
		for _, p := range e.Publisher.Findings {
			seen["publisher"][p] = true
		}
		for _, p := range e.Directory.Findings {
			seen["directory"][p] = true
		}
	}
	for _, profile := range []string{"publisher", "directory"} {
		for p := range seen[profile] {
			if !listedPairs[profile][p] {
				t.Errorf("a registered repository has the finding %v under the %s profile, and drift.json does not account for it", p, profile)
			}
		}
		for p := range listedPairs[profile] {
			if !seen[profile][p] {
				t.Errorf("drift.json accounts for the finding %v under the %s profile, which no registered repository has any more: remove it", p, profile)
			}
		}
	}

	// What Go does with the corpus.
	_, _, manifests, mds := loadReference(t)
	loose := 0
	families := map[string]bool{}
	for _, c := range manifests {
		ok, _ := acceptManifest(Directory, c.Document)
		if ok && !directorySpec.verdict(c) {
			loose++
			families[c.Family] = true
		}
	}
	for _, c := range mds {
		ok, _ := acceptMd(Directory, c.Document, c.Path)
		if ok && !directorySpec.verdict(c) {
			loose++
			families[c.Family] = true
		}
	}
	if loose != corpus {
		t.Errorf("the corpus has %d documents on which the Directory profile is looser than the reference, and drift.json accounts for %d", loose, corpus)
	}
	var names []string
	for f := range families {
		names = append(names, f)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); corpus > 0 && got != "publisher: recordsets" {
		t.Errorf("the corpus documents on which Go is looser are of the families %q, drift.json says they are recordset names", got)
	}
	t.Logf("drift: %d listed looser, %d listed stricter, %d probes, %d corpus documents", len(d.GoLooser), len(d.GoStricter), len(probes.Probes), corpus)
}
