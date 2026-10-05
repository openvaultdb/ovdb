package manifest

import (
	"strings"
	"testing"
)

// The database descriptor against the Directory at its pin: each case of testdata/reference/descriptor.json is a manifest and a descriptor, and the verdict
// of the Directory's own code on the pair (manifestProblems with databaseManifest, databaseDescriptorProblems, and the manifest's id against the
// descriptor's localId). The Directory profile must give that verdict on every case; the Publisher profile must refuse everything the Directory refuses,
// and may refuse more only by a rule of its own, which is the point of the second test.

// descriptorKinds are the recorded kinds of the descriptor: what Go refuses that the Directory accepts, by a bound or a choice of this check, each with the
// text of the finding that shows it.
var descriptorKinds = map[string]string{
	"descriptor-depth": "nested more than 64 levels deep",
}

type descriptorCase struct {
	Note       string `json:"note"`
	Manifest   string `json:"manifest"`
	Descriptor string `json:"descriptor"`
	Reference  int    `json:"reference"`
	// Recorded names the recorded kind that Go is expected to refuse the pair by though the Directory accepts it.
	Recorded string `json:"recorded"`
}

type descriptorFile struct {
	Format string           `json:"format"`
	Cases  []descriptorCase `json:"cases"`
}

// judgePair judges a manifest with the descriptor that goes with it, as the repository check does: the manifest first, with the origin rule of its
// discovery waived, and the descriptor only when the manifest has no findings.
func judgePair(profile Profile, c descriptorCase) []Finding {
	j, _ := NewJudge(profile)
	j.DescriptorPaired(true)
	m, _, findings := j.ManifestWithAttachment([]byte(c.Manifest), "ovdb.yaml")
	if len(findings) != 0 {
		return findings
	}
	_, more := j.Descriptor([]byte(c.Descriptor), "ovdb-database.json", m, "ovdb.yaml")
	return more
}

func readDescriptorGolden(t *testing.T) descriptorFile {
	t.Helper()
	var g descriptorFile
	readGolden(t, "descriptor.json", &g)
	if g.Format != "ovdb-publisher-descriptor-reference/1" || len(g.Cases) < 200 {
		t.Fatalf("descriptor.json: format %q, %d cases", g.Format, len(g.Cases))
	}
	return g
}

func TestDescriptorDirectoryProfileAgreesWithTheDirectory(t *testing.T) {
	g := readDescriptorGolden(t)
	accepted, refused := 0, 0
	for _, c := range g.Cases {
		findings := judgePair(Directory, c)
		if c.Recorded != "" {
			if len(findings) == 0 || c.Reference != 1 || descriptorKinds[c.Recorded] == "" || !strings.Contains(findings[0].Message, descriptorKinds[c.Recorded]) {
				t.Errorf("%s: expected to be refused by the recorded kind %s, got %v", c.Note, c.Recorded, findings)
			}
			continue
		}
		switch {
		case len(findings) == 0 && c.Reference == 1:
			accepted++
		case len(findings) != 0 && c.Reference == 0:
			refused++
		case len(findings) == 0:
			t.Errorf("%s: Go accepts what the Directory refuses", c.Note)
		default:
			t.Errorf("%s: Go refuses what the Directory accepts: %v", c.Note, findings)
		}
		for _, f := range findings {
			if f.Rule == "" || f.Message == "" || f.Line < 0 {
				t.Errorf("%s: a malformed finding %+v", c.Note, f)
			}
		}
	}
	if accepted < 30 || refused < 200 {
		t.Errorf("the cases hold %d accepted and %d refused pairs", accepted, refused)
	}
}

// Under the Publisher profile Go refuses every pair the Directory refuses. It refuses a pair that the Directory accepts only by a rule of the Publisher
// profile's own that the manifest of that pair breaks.
func TestDescriptorPublisherProfileRefusesWhatTheDirectoryRefuses(t *testing.T) {
	g := readDescriptorGolden(t)
	extra := 0
	for _, c := range g.Cases {
		findings := judgePair(Publisher, c)
		if c.Recorded != "" {
			continue
		}
		if len(findings) == 0 && c.Reference == 0 {
			t.Errorf("%s: the Publisher profile accepts what the Directory refuses", c.Note)
		}
		if len(findings) != 0 && c.Reference == 1 {
			extra++
			for _, f := range findings {
				if f.Rule != "manifest-discovery" && f.Rule != "manifest-id" {
					t.Errorf("%s: the Publisher profile refuses what the Directory accepts, by %s: %s", c.Note, f.Rule, f.Message)
				}
			}
		}
	}
	if extra == 0 {
		t.Error("the Publisher profile has no rule of its own that these pairs break; the test is vacuous")
	}
	t.Logf("%d pairs refused by the Publisher profile alone", extra)
}

func TestIsDescriptor(t *testing.T) {
	for doc, want := range map[string]bool{
		`{"format": "ovdb-database/draft-1"}`:   true,
		`{"format": "ovdb-database/draft-2"}`:   true,
		`{"format": "ovdb-database/"}`:          true,
		`format: ovdb-database/draft-1`:         true,
		`{"format": "ovdb-manifest/draft-1"}`:   false,
		`{"format": "OVDB-DATABASE/draft-1"}`:   false,
		`{"format": " ovdb-database/draft-1"}`:  false,
		`{"format": 7}`:                         false,
		`{"format": null}`:                      false,
		`{"id": "x"}`:                           false,
		`[{"format": "ovdb-database/draft-1"}]`: false,
		`null`:                                  false,
		`{`:                                     false,
		``:                                      false,
		`a: &x 1
format: *x`: false,
	} {
		if got := IsDescriptor([]byte(doc)); got != want {
			t.Errorf("IsDescriptor(%q) = %v, want %v", doc, got, want)
		}
	}
	if IsDescriptor([]byte(`{"format": "ovdb-database/draft-1", "x": "` + strings.Repeat("a", MaxDocumentBytes) + `"}`)) {
		t.Error("a document over the bound is read as a manifest, which says it is too big")
	}
}

// A descriptor over the bound is one finding, as a manifest is; and the line of a finding is where its key is.
func TestDescriptorBoundAndLines(t *testing.T) {
	j, _ := NewJudge(Directory)
	m, _, f := j.ManifestWithAttachment([]byte(descriptorManifestForLines), "ovdb.yaml")
	if len(f) != 0 {
		t.Fatalf("the manifest: %v", f)
	}
	_, findings := j.Descriptor([]byte(`{"x": "`+strings.Repeat("a", MaxDocumentBytes)+`"}`), "d.json", m, "ovdb.yaml")
	if len(findings) != 1 || findings[0].Rule != "document-size" {
		t.Errorf("findings %v", findings)
	}
	_, findings = j.Descriptor([]byte("{\n  \"format\": 7,\n  \"id\": \"x\"\n}"), "d.json", m, "ovdb.yaml")
	lines := map[string]int{}
	for _, f := range findings {
		lines[f.Rule] = f.Line
	}
	if lines["descriptor-format"] != 2 || lines["descriptor-id"] != 3 || lines["descriptor-required"] != 1 {
		t.Errorf("lines %v from %v", lines, findings)
	}
	d, findings := j.Descriptor([]byte(goodDescriptorForFacts), "d.json", m, "ovdb.yaml")
	if len(findings) != 0 || !d.Read || d.ID.Value != "https://demodb.dev/chinook/" || d.LocalID.Value != "chinook" || !d.ServerID.Valid || !d.ServerDBBaseURL.Valid || !d.APIURL.Valid || d.Discovery.Value != "https://demodb.dev/.well-known/openvaultdb" {
		t.Errorf("facts %+v, findings %v", d, findings)
	}
}

const goodDescriptorForFacts = `{"format": "ovdb-database/draft-1", "id": "https://demodb.dev/chinook/", "localId": "chinook", "serverId": "https://demodb.dev/ovdb",
 "serverDbBaseUrl": "https://demodb.dev/ovdb/db/chinook", "apiUrl": "https://demodb.dev/ovdb/api", "deployment": {"discovery": "https://demodb.dev/.well-known/openvaultdb"}}`

var descriptorManifestForLines = strings.NewReplacer(
	"url: https://chinookdb.com/ovdb/dbs/chinook\n", "url: https://demodb.dev/chinook/\n",
	"discovery: https://chinookdb.com/.well-known/openvaultdb", "discovery: https://demodb.dev/.well-known/openvaultdb").Replace(ownManifest)
