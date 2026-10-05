package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

func committedFixture(t *testing.T, nodes map[string]Node) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, nil, "init", "--quiet", "-b", "main")
	for name, node := range nodes {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, node.Content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, nil, "add", "-A")
	git(t, dir, nil, "commit", "--quiet", "-m", "fixture")
	return dir
}

func TestImmutableSourceProofIgnoresGitReplacements(t *testing.T) {
	for _, kind := range []string{"commit", "tree", "blob"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, nil, "init", "--quiet", "-b", "main")
			original, substituted := []byte("original at pin"), []byte("substituted bytes")
			blobA := git(t, dir, original, "hash-object", "-w", "--stdin")
			blobB := git(t, dir, substituted, "hash-object", "-w", "--stdin")
			treeA := git(t, dir, []byte("100644 blob "+blobA+"\trows.json\n"), "mktree")
			treeB := git(t, dir, []byte("100644 blob "+blobB+"\trows.json\n"), "mktree")
			commitA := git(t, dir, nil, "commit-tree", "-m", "A", treeA)
			commitB := git(t, dir, nil, "commit-tree", "-m", "B", treeB)
			git(t, dir, nil, "update-ref", "refs/heads/main", commitA)
			pair := map[string][2]string{"commit": {commitA, commitB}, "tree": {treeA, treeB}, "blob": {blobA, blobB}}[kind]
			git(t, dir, nil, "replace", pair[0], pair[1])
			ref := representation.Reference{Repository: "https://github.com/example/input", Revision: commitA, Path: "rows.json", SHA256: representation.Hash(substituted)}
			deps := DependencyReaders{{ref.Repository, ref.Revision}: NewGit(ExecRunner{Dir: dir})}
			if proof := VerifySourceData(sourceDocument(ref), deps); proof.Stage != SourceDataRefused {
				t.Fatalf("replacement forged pinned bytes: %+v", proof)
			}
			ref.SHA256 = representation.Hash(original)
			if proof := VerifySourceData(sourceDocument(ref), deps); proof.Stage != SourceDataChecked {
				t.Fatalf("original bytes unavailable: %+v", proof)
			}
		})
	}
}

func TestOriginalProviderAttachmentCannotBeHiddenByReplacement(t *testing.T) {
	p, _, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	dir := committedFixture(t, p.Nodes)
	commitA := git(t, dir, nil, "rev-parse", "HEAD")
	var m map[string]any
	if err := json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "representation_contract")
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "ovdb.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "add", "ovdb.yaml")
	git(t, dir, nil, "commit", "--quiet", "-m", "hide attachment")
	commitB := git(t, dir, nil, "rev-parse", "HEAD")
	git(t, dir, nil, "update-ref", "refs/heads/main", commitA)
	git(t, dir, nil, "replace", commitA, commitB)
	if result := Check(NewGit(ExecRunner{Dir: dir}), Options{Profile: manifest.Publisher}); result.OK() {
		t.Fatal("original attached manifest accepted without required dependency proofs")
	}
}

func TestLegacyProviderStillUsesReplacementView(t *testing.T) {
	p, _, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	var m map[string]any
	_ = json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m)
	delete(m, "representation_contract")
	raw, _ := json.Marshal(m)
	p.Nodes["ovdb.yaml"] = Node{Kind: File, Content: raw}
	dir := committedFixture(t, p.Nodes)
	a := git(t, dir, nil, "rev-parse", "HEAD")
	m["title"] = "Legacy replacement title"
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "ovdb.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "commit", "--quiet", "-am", "replacement")
	b := git(t, dir, nil, "rev-parse", "HEAD")
	git(t, dir, nil, "update-ref", "refs/heads/main", a)
	git(t, dir, nil, "replace", a, b)
	result := Check(NewGit(ExecRunner{Dir: dir}), Options{Profile: manifest.Publisher})
	if !result.OK() || result.Manifest.Title.Value != "Legacy replacement title" {
		t.Fatalf("legacy changed: %+v", result)
	}
}

func TestOriginalOVDBMdCannotHideAttachment(t *testing.T) {
	p, _, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	dir := committedFixture(t, p.Nodes)
	a := git(t, dir, nil, "rev-parse", "HEAD")
	var m map[string]any
	_ = json.Unmarshal(p.Nodes["ovdb.yaml"].Content, &m)
	delete(m, "representation_contract")
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "legacy.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "OVDB.md"), []byte("---\novdb: 1\npublish: [./legacy.yaml]\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "add", "-A")
	git(t, dir, nil, "commit", "--quiet", "-m", "hide original listed manifest")
	b := git(t, dir, nil, "rev-parse", "HEAD")
	git(t, dir, nil, "update-ref", "refs/heads/main", a)
	git(t, dir, nil, "replace", a, b)
	if result := Check(NewGit(ExecRunner{Dir: dir}), Options{Profile: manifest.Publisher}); result.OK() {
		t.Fatal("replacement OVDB.md hid required attachment proof")
	}
}

func TestAttachedProviderMetadataUsesOriginalBlobs(t *testing.T) {
	p, deps, _ := defaultNativeFixture(t, "real-ror", []byte("raw"))
	valid := p.Nodes["contract.json"].Content
	p.Nodes["contract.json"] = Node{Kind: File, Content: []byte("original invalid attachment bytes")}
	dir := committedFixture(t, p.Nodes)
	a := git(t, dir, nil, "rev-parse", "HEAD:contract.json")
	b := git(t, dir, valid, "hash-object", "-w", "--stdin")
	git(t, dir, nil, "replace", a, b)
	r := NewGit(ExecRunner{Dir: dir})
	m, _ := manifest.CheckManifest(p.Nodes["ovdb.yaml"].Content, "ovdb.yaml", manifest.Publisher)
	attachment, _ := representation.ParseAttachment(p.Nodes["ovdb.yaml"].Content)
	if findings := CheckRepresentation(r, m, *attachment, deps); len(findings) == 0 || findings[0].Rule != "representation-hash" {
		t.Fatalf("helper accepted replacement metadata: %v", findings)
	}
	if result := Check(r, Options{Profile: manifest.Publisher, Dependencies: deps}); result.OK() {
		t.Fatal("default accepted replacement metadata")
	}
}

func TestOriginalObjectsIgnoreGraftAncestryAndCallerEnvironment(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, nil, "init", "--quiet", "-b", "main")
	body := []byte("original pinned bytes")
	blob := git(t, dir, body, "hash-object", "-w", "--stdin")
	tree := git(t, dir, []byte("100644 blob "+blob+"\trows.json\n"), "mktree")
	a := git(t, dir, nil, "commit-tree", "-m", "A", tree)
	b := git(t, dir, nil, "commit-tree", "-m", "B", tree)
	git(t, dir, nil, "update-ref", "refs/heads/main", a)
	if err := os.WriteFile(filepath.Join(dir, ".git/info/grafts"), []byte(a+" "+b+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_NO_REPLACE_OBJECTS", "0")
	t.Setenv("GIT_GRAFT_FILE", filepath.Join(dir, "untrusted-graft-file"))
	t.Setenv("GIT_REPLACE_REF_BASE", "refs/untrusted-replacements/")
	ref := representation.Reference{Repository: "https://github.com/example/input", Revision: a, Path: "rows.json", SHA256: representation.Hash(body)}
	deps := DependencyReaders{{ref.Repository, ref.Revision}: NewGit(ExecRunner{Dir: dir})}
	if proof := VerifySourceData(sourceDocument(ref), deps); proof.Stage != SourceDataChecked {
		t.Fatalf("graft/environment changed direct commit tree: %+v", proof)
	}
}

func TestOriginalObjectsRecheckOriginalCommitType(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, nil, "init", "--quiet", "-b", "main")
	blob := git(t, dir, []byte("not a commit"), "hash-object", "-w", "--stdin")
	tree := git(t, dir, nil, "mktree")
	commit := git(t, dir, nil, "commit-tree", "-m", "replacement", tree)
	if err := os.WriteFile(filepath.Join(dir, ".git/HEAD"), []byte(blob+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "replace", "-f", blob, commit)
	reader := NewGit(ExecRunner{Dir: dir})
	if _, err := reader.Head(); err != nil {
		t.Fatalf("replacement-assisted type control: %v", err)
	}
	original, _ := OriginalObjects(reader)
	if again, changed := OriginalObjects(original); changed || again != original {
		t.Fatal("original view is not idempotent")
	}
	// The selected ID still names an original blob, even when HEAD has since
	// moved to a valid commit. Re-resolving HEAD would incorrectly accept it.
	if err := os.WriteFile(filepath.Join(dir, ".git/HEAD"), []byte(commit+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := original.Head(); err == nil {
		t.Fatal("replacement-assisted commit type was reused")
	}
}

type discoveryReader struct {
	*Memory
	reads []string
}

func (r *discoveryReader) Blob(path string, limit int) ([]byte, error) {
	r.reads = append(r.reads, path)
	return r.Memory.Blob(path, limit)
}

func TestAttachmentDiscoveryBoundsAndUnrunnableInputs(t *testing.T) {
	for _, mode := range []string{"head-error", "missing-ovdbmd", "oversize-ovdbmd", "missing-manifest", "oversize-manifest", "past-manifest-limit", "invalid-attachment", "no-attachment"} {
		t.Run(mode, func(t *testing.T) {
			r := &discoveryReader{Memory: legacyNativeFixture(t)}
			switch mode {
			case "head-error":
				r.Err = ErrCannotRun
			case "missing-ovdbmd":
				delete(r.Nodes, "OVDB.md")
			case "oversize-ovdbmd":
				r.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte(strings.Repeat("x", manifest.MaxDocumentBytes+2))}
			case "missing-manifest":
				delete(r.Nodes, "ovdb.yaml")
			case "oversize-manifest":
				r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte(strings.Repeat("x", manifest.MaxDocumentBytes+2))}
			case "invalid-attachment":
				r.Nodes["ovdb.yaml"] = Node{Kind: File, Content: []byte("representation_contract: null\n")}
			case "past-manifest-limit":
				var paths []string
				for i := 0; i <= MaxManifests; i++ {
					name := strings.Repeat("a", i+1) + ".yaml"
					paths = append(paths, "./"+name)
					r.Nodes[name] = Node{Kind: File, Content: []byte("title: legacy\n")}
					if i == MaxManifests {
						r.Nodes[name] = Node{Kind: File, Content: []byte("representation_contract: null\n")}
					}
				}
				r.Nodes["OVDB.md"] = Node{Kind: File, Content: []byte("---\novdb: 1\npublish: [" + strings.Join(paths, ",") + "]\n---\n")}
			}
			want := attachmentIndeterminate
			switch mode {
			case "invalid-attachment":
				want = attachmentPresent
			case "no-attachment":
				want = attachmentAbsent
			}
			if got, _, _ := discoverAttachment(r, manifest.Publisher); got != want {
				t.Fatalf("discovery %v reads=%v", got, r.reads)
			}
			if len(r.reads) > MaxManifests+1 {
				t.Fatal("discovery exceeded manifest bound")
			}
			for _, path := range r.reads {
				if path != "OVDB.md" && !strings.HasSuffix(path, ".yaml") {
					t.Fatalf("discovery read outside manifest stage: %s", path)
				}
			}
		})
	}
}
