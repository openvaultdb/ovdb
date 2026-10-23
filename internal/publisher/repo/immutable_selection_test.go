package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

type originalFixtureReader struct {
	Reader
	original Reader
}

func (r originalFixtureReader) OriginalObjects() (Reader, bool) { return r.original, true }

func legacyNativeFixture(t *testing.T) *Memory {
	t.Helper()
	p, _, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	var m map[string]any
	if err := json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "representation_contract")
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
	return p
}

func TestCheckOriginalDiscoveryUncertaintyRefusesLegacyFallback(t *testing.T) {
	for _, mode := range []string{"head-error", "tree-error", "missing-ovdbmd", "unreadable-ovdbmd", "oversize-ovdbmd", "invalid-ovdbmd", "missing-manifest", "symlink-manifest", "unreadable-manifest", "oversize-manifest", "invalid-manifest", "past-manifest-limit"} {
		t.Run(mode, func(t *testing.T) {
			legacy := legacyNativeFixture(t)
			original := legacyNativeFixture(t)
			switch mode {
			case "head-error":
				original.Err = ErrCannotRun
			case "tree-error":
				original.BrokenDirs = map[string]error{"": ErrObjectMissing}
			case "missing-ovdbmd":
				delete(original.Nodes, "OVDB.md")
			case "unreadable-ovdbmd":
				original.BrokenBlobs = map[string]error{"OVDB.md": ErrTimeout}
			case "oversize-ovdbmd":
				original.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte(strings.Repeat("x", manifest.MaxDocumentBytes+2))}
			case "invalid-ovdbmd":
				original.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [broken\n---\n")}
			case "missing-manifest":
				delete(original.Nodes, "ovdb.yaml")
			case "symlink-manifest":
				original.Nodes["ovdb.yaml"] = Node{Kind: Symlink}
			case "unreadable-manifest":
				original.BrokenBlobs = map[string]error{"ovdb.yaml": ErrObjectCorrupt}
			case "oversize-manifest":
				original.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Repeat("x", manifest.MaxDocumentBytes+2))}
			case "invalid-manifest":
				original.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte("representation_contract: [broken")}
			case "past-manifest-limit":
				var paths []string
				for i := 0; i <= MaxManifests; i++ {
					path := strings.Repeat("a", i+1) + ".yaml"
					paths = append(paths, "./"+path)
					original.Nodes[path] = original.Nodes["ovdb.yaml"]
				}
				original.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(paths, ",") + "]\n---\n")}
			}
			if result := Check(originalFixtureReader{Reader: legacy, original: original}, Options{Profile: manifest.Publisher}); result.OK() {
				t.Fatal("uncertain original discovery accepted replacement-sensitive legacy view")
			} else {
				want := map[string]string{"head-error": RuleUnreadable, "tree-error": RuleObjectGone, "missing-ovdbmd": RuleOVDBMd, "unreadable-ovdbmd": RuleUnreadable, "oversize-ovdbmd": "document-size", "missing-manifest": RuleManifest, "symlink-manifest": RuleManifest, "unreadable-manifest": RuleObjectBad, "oversize-manifest": "document-size", "past-manifest-limit": RuleManifests}[mode]
				if want != "" {
					found := false
					for _, finding := range result.Findings {
						found = found || finding.Rule == want
					}
					if !found {
						t.Fatalf("lost original refusal rule %s: %+v", want, result.Findings)
					}
				}
			}
		})
	}
}

func TestCheckRealGitOriginalInvalidManifestCannotUseReplacement(t *testing.T) {
	p := legacyNativeFixture(t)
	valid := p.Nodes["ovdb.yaml"].Content
	p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte("representation_contract: [broken")}
	dir := committedFixture(t, p.Nodes)
	a := git(t, dir, nil, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "ovdb.yaml"), valid, 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "commit", "--quiet", "-am", "valid legacy replacement")
	b := git(t, dir, nil, "rev-parse", "HEAD")
	git(t, dir, nil, "update-ref", "refs/heads/main", a)
	git(t, dir, nil, "replace", a, b)
	if result := Check(NewGit(ExecRunner{Dir: dir}), Options{Profile: manifest.Publisher}); result.OK() {
		t.Fatal("invalid original manifest accepted through a valid legacy replacement")
	}
}

type orderedProviderReader struct {
	*Memory
	pinned bool
	reads  int
	before bool
}

func (r *orderedProviderReader) Head() (string, error) {
	r.pinned = true
	return r.Memory.Head()
}

func (r *orderedProviderReader) Entries(dir string) ([]Entry, error) {
	r.reads++
	r.before = r.before || !r.pinned
	return r.Memory.Entries(dir)
}

func (r *orderedProviderReader) Blob(path string, limit int) ([]byte, error) {
	r.reads++
	r.before = r.before || !r.pinned
	return r.Memory.Blob(path, limit)
}

func TestCheckRepresentationPinsProviderBeforeReading(t *testing.T) {
	p, _, deps := contractFixture(t)
	m, _ := manifest.CheckManifest(p.Nodes["ovdb.yaml"].Content, "ovdb.yaml", manifest.Publisher)
	a, _ := representation.ParseAttachment(p.Nodes["ovdb.yaml"].Content)
	r := &orderedProviderReader{Memory: p}
	if findings := CheckRepresentation(r, m, *a, deps); len(findings) != 0 {
		t.Fatal(findings)
	}
	if r.before || r.reads == 0 {
		t.Fatalf("provider read ordering: before-pin=%v reads=%d", r.before, r.reads)
	}
	r = &orderedProviderReader{Memory: &Memory{Err: ErrCannotRun}}
	if findings := CheckRepresentation(r, m, *a, deps); len(findings) == 0 || r.reads != 0 {
		t.Fatalf("unavailable provider must refuse before reads: %v reads=%d", findings, r.reads)
	}
}

func TestOriginalObjectsPreserveSelectedCommitWhenHEADChanges(t *testing.T) {
	p := legacyNativeFixture(t)
	dir := committedFixture(t, p.Nodes)
	r := NewGit(ExecRunner{Dir: dir})
	a, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "contract.json"), []byte("new head bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "commit", "--quiet", "-am", "new head")
	original, _ := OriginalObjects(r)
	if pin, err := original.Head(); err != nil || pin != a {
		t.Fatalf("original adaptation changed selected identity: got %q %v want %q", pin, err, a)
	}
	data, err := original.Blob("contract.json", MaxFileBytes)
	if err != nil || representation.Hash(data) != representation.Hash(p.Nodes["contract.json"].Content) {
		t.Fatalf("selected original bytes changed: %s %v", data, err)
	}
}

func TestCheckRepresentationPreservesSelectedOriginalProviderBytes(t *testing.T) {
	p, _, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	original := []byte("invalid original contract")
	p.Nodes["contract.json"] = Node{Kind: File, Content: original}
	dir := committedFixture(t, p.Nodes)
	r := NewGit(ExecRunner{Dir: dir})
	if _, err := r.Head(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "contract.json"), []byte("different newer contract"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "commit", "--quiet", "-am", "move provider head")
	a := representation.Reference{Path: "contract.json", SHA256: representation.Hash(original)}
	findings := CheckRepresentation(r, manifest.Manifest{}, a, nil)
	if len(findings) != 1 || findings[0].Rule != "representation-contract" {
		t.Fatalf("helper did not retain original contract bytes: %+v", findings)
	}
}

type moveHeadDuringDiscovery struct {
	Runner
	t        *testing.T
	dir      string
	selected string
	next     string
	moved    bool
}

func (r *moveHeadDuringDiscovery) Run(args []string, limit int) ([]byte, error) {
	out, err := r.Runner.Run(args, limit)
	if !r.moved && slices.Contains(args, "--no-replace-objects") && slices.Contains(args, r.selected+":ovdb.yaml") {
		git(r.t, r.dir, nil, "update-ref", "refs/heads/main", r.next)
		r.moved = true
	}
	return out, err
}

func TestCheckDiscoveryAndLegacyValidationUseOneSelectedCommit(t *testing.T) {
	p := legacyNativeFixture(t)
	dir := committedFixture(t, p.Nodes)
	a := git(t, dir, nil, "rev-parse", "HEAD")
	var m map[string]any
	if err := json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m); err != nil {
		t.Fatal(err)
	}
	writeTitle := func(title string) string {
		m["title"] = title
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ovdb.yaml"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		git(t, dir, nil, "commit", "--quiet", "-am", title)
		return git(t, dir, nil, "rev-parse", "HEAD")
	}
	b := writeTitle("New HEAD must not change selection")
	c := writeTitle("Replacement at selected commit remains supported")
	git(t, dir, nil, "update-ref", "refs/heads/main", a)
	git(t, dir, nil, "replace", a, c)
	runner := &moveHeadDuringDiscovery{Runner: ExecRunner{Dir: dir}, t: t, dir: dir, selected: a, next: b}
	result := Check(NewGit(runner), Options{Profile: manifest.Publisher})
	if !runner.moved || !result.OK() || result.Manifest.Title.Value != "Replacement at selected commit remains supported" {
		t.Fatalf("discovery and validation changed commit: moved=%v %+v", runner.moved, result)
	}
}

func TestSelectedGitViewRequiresExactCommitObject(t *testing.T) {
	dir := committedFixture(t, legacyNativeFixture(t).Nodes)
	git(t, dir, nil, "tag", "-a", "selected-tag", "-m", "not a commit object")
	tag := git(t, dir, nil, "rev-parse", "selected-tag")
	r := AtCommit(NewGit(ExecRunner{Dir: dir}), tag)
	if _, err := r.Head(); err != ErrMalformed {
		t.Fatalf("selected tag was silently peeled to another object: %v", err)
	}
	for _, id := range []string{"", "refs/heads/main"} {
		if _, err := AtCommit(NewGit(ExecRunner{Dir: dir}), id).Head(); err != ErrMalformed {
			t.Fatalf("selected identity %q must be an exact object ID: %v", id, err)
		}
	}
}

func TestLegacyAttachmentDiscoverySelectsOriginalAssociations(t *testing.T) {
	original := legacyNativeFixture(t)
	legacy, _, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	result := Check(originalFixtureReader{Reader: legacy, original: original}, Options{Profile: manifest.Publisher})
	if !result.OK() || result.Manifest.Title.Value != "Native fixture" {
		t.Fatalf("legacy attachment was used with original unattached association: %+v", result)
	}
}

// Preserve the first refusal even if an environmental failure disappears when
// the full original check retries the read; discovery never established absence.
type transientDiscoveryReader struct {
	*Memory
	err error
}

func (r *transientDiscoveryReader) Head() (string, error) {
	if r.err != nil {
		err := r.err
		r.err = nil
		return "", err
	}
	return r.Memory.Head()
}

func TestCheckRetainsOriginalDiscoveryRefusal(t *testing.T) {
	legacy := legacyNativeFixture(t)
	original := &transientDiscoveryReader{Memory: legacyNativeFixture(t), err: ErrCannotRun}
	result := Check(originalFixtureReader{Reader: legacy, original: original}, Options{Profile: manifest.Publisher})
	if result.OK() || !strings.Contains(result.Findings[0].Message, ErrCannotRun.Error()) {
		t.Fatalf("lost original discovery refusal: %+v", result)
	}
}

type lateLegacyAttachment struct {
	Reader
	original Reader
	manifest []byte
	reads    int
}

func (r *lateLegacyAttachment) OriginalObjects() (Reader, bool) { return r.original, true }

func (r *lateLegacyAttachment) Blob(path string, limit int) ([]byte, error) {
	if path == "ovdb.yaml" {
		r.reads++
		if r.reads > 1 {
			return r.manifest, nil
		}
	}
	return r.Reader.Blob(path, limit)
}

func TestLateLegacyAttachmentRestartsOriginalValidationWithFreshCaches(t *testing.T) {
	original := legacyNativeFixture(t)
	legacy := legacyNativeFixture(t)
	var m map[string]any
	if err := json.Unmarshal(legacy.Nodes["ovdb.yaml"].Content, &m); err != nil {
		t.Fatal(err)
	}
	m["title"] = "Legacy-only title must be discarded"
	raw, _ := json.Marshal(m)
	legacy.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
	// The first legacy manifest populates file/tree caches and findings before
	// the attachment in the second manifest triggers the restart.
	for _, p := range []*Memory{original, legacy} {
		p.Nodes["first.yaml"] = p.Nodes["ovdb.yaml"]
		p.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [./first.yaml, ./ovdb.yaml]\n---\n")}
	}
	model := m["model"].(map[string]any)["modelspec"].(string)
	legacy.Nodes[model] = Node{Kind: File, Content: []byte("{}")}
	m["representation_contract"] = nil
	attached, _ := json.Marshal(m)
	r := &lateLegacyAttachment{Reader: legacy, original: original, manifest: attached}
	result := Check(r, Options{Profile: manifest.Publisher})
	if !result.OK() || result.Manifest.Title.Value != "Native fixture" || r.reads < 2 {
		t.Fatalf("late attached proof inherited legacy manifest or caches: reads=%d %+v", r.reads, result)
	}
}
