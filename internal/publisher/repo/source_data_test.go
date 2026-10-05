package repo

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/publisher/representation"
)

type sourceReader struct {
	*Memory
	revision  string
	blobCalls int
	limits    []int
}

func (r *sourceReader) Head() (string, error) { return r.revision, r.Err }
func (r *sourceReader) Blob(path string, limit int) ([]byte, error) {
	r.blobCalls++
	r.limits = append(r.limits, limit)
	return r.Memory.Blob(path, limit)
}

func sourceProofFixture(body []byte, revision string) (representation.Reference, *sourceReader) {
	ref := representation.Reference{Repository: "https://github.com/example/input", Revision: revision, Path: "data/rows.json", SHA256: representation.Hash(body)}
	r := &sourceReader{Memory: &Memory{Nodes: map[string]Node{ref.Path: {Kind: File, Content: body}}}, revision: revision}
	return ref, r
}

func sourceDocument(refs ...representation.Reference) *representation.Document {
	doc := &representation.Document{Format: representation.Format3}
	for _, ref := range refs {
		ref := ref
		doc.Contracts = append(doc.Contracts, representation.Contract{Execution: representation.NativeIdentifier, Source: representation.Source{Data: &ref}})
	}
	return doc
}

func TestSourceDataProofExactReadersAndPerCheckDedupe(t *testing.T) {
	revA, revB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	refA, a := sourceProofFixture([]byte(`[{"key":"a"}]`), revA)
	refB, b := sourceProofFixture([]byte(`[{"key":"b"}]`), revB)
	deps := DependencyReaders{
		{Repository: refA.Repository, Revision: revA}: a,
		{Repository: refB.Repository, Revision: revB}: b,
	}
	proof := VerifySourceData(sourceDocument(refA, refA, refB), deps)
	if proof.Stage != SourceDataChecked || len(proof.Findings) != 0 || len(proof.References) != 2 || a.blobCalls != 1 || b.blobCalls != 1 || a.limits[0] != MaxSourceDataBytes || b.limits[0] != MaxSourceDataBytes {
		t.Fatalf("wrong proof or dedupe: %+v, calls %d/%d", proof, a.blobCalls, b.blobCalls)
	}
	// A second check cannot reuse the first check's successful proof.
	b.Nodes[refB.Path] = Node{Kind: File, Content: []byte("changed")}
	proof = VerifySourceData(sourceDocument(refA, refB), deps)
	if proof.Stage != SourceDataRefused || b.blobCalls != 2 {
		t.Fatalf("stale data proof reused: %+v, calls=%d", proof, b.blobCalls)
	}
	// The metadata-only entrypoint has no data-proof success to report.
	if got := VerifySourceData(&representation.Document{Format: representation.Format2}, deps); got.Stage != SourceDataOutsideScope || len(got.References) != 0 {
		t.Fatalf("legacy format gained data proof: %+v", got)
	}
	if got := VerifySourceData(nil, deps); got.Stage != SourceDataRefused {
		t.Fatalf("missing verified document escaped as outside scope: %+v", got)
	}
}

func TestSourceDataProofRefusals(t *testing.T) {
	revision := strings.Repeat("a", 40)
	body := []byte(`[{"key":"a"}]`)
	ref, _ := sourceProofFixture(body, revision)
	key := DependencyKey{Repository: ref.Repository, Revision: revision}
	for name, change := range map[string]func(*representation.Reference, *sourceReader, DependencyReaders){
		"missing binding": func(_ *representation.Reference, _ *sourceReader, deps DependencyReaders) { delete(deps, key) },
		"wrong head": func(_ *representation.Reference, reader *sourceReader, _ DependencyReaders) {
			reader.revision = strings.Repeat("b", 40)
		},
		"wrong hash": func(reference *representation.Reference, _ *sourceReader, _ DependencyReaders) {
			reference.SHA256 = strings.Repeat("c", 64)
		},
		"missing object": func(_ *representation.Reference, reader *sourceReader, _ DependencyReaders) {
			delete(reader.Nodes, ref.Path)
		},
		"symlink": func(_ *representation.Reference, reader *sourceReader, _ DependencyReaders) {
			reader.Nodes[ref.Path] = Node{Kind: Symlink}
		},
		"submodule": func(_ *representation.Reference, reader *sourceReader, _ DependencyReaders) {
			reader.Nodes[ref.Path] = Node{Kind: Submodule}
		},
		"tree error": func(_ *representation.Reference, reader *sourceReader, _ DependencyReaders) {
			reader.BrokenDirs = map[string]error{"": errors.New("unreadable tree")}
		},
		"blob error": func(_ *representation.Reference, reader *sourceReader, _ DependencyReaders) {
			reader.BrokenBlobs = map[string]error{ref.Path: errors.New("unreadable")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			copyReader := &sourceReader{Memory: &Memory{Nodes: map[string]Node{ref.Path: {Kind: File, Content: body}}}, revision: revision}
			deps := DependencyReaders{key: copyReader}
			changed := ref
			change(&changed, copyReader, deps)
			proof := VerifySourceData(sourceDocument(changed), deps)
			if proof.Stage != SourceDataRefused || len(proof.Findings) != 1 || len(proof.References) != 0 {
				t.Fatalf("bad source accepted: %+v", proof)
			}
		})
	}
}

func TestSourceDataProofRequiresClosedContractShape(t *testing.T) {
	revision := strings.Repeat("a", 40)
	ref, reader := sourceProofFixture([]byte("raw"), revision)
	deps := DependencyReaders{{Repository: ref.Repository, Revision: revision}: reader}
	for _, doc := range []*representation.Document{
		{Format: representation.Format3},
		sourceDocument(make([]representation.Reference, 33)...),
		{Format: representation.Format3, Contracts: []representation.Contract{{Execution: representation.NativeIdentifier}}},
		{Format: representation.Format3, Contracts: []representation.Contract{{Execution: representation.LabelBridge, Source: representation.Source{Data: &ref}}}},
	} {
		proof := VerifySourceData(doc, deps)
		if proof.Stage != SourceDataRefused || len(proof.Findings) == 0 || reader.blobCalls != 0 {
			t.Fatalf("bad contract gained a byte proof: %+v", proof)
		}
	}
}

func TestSourceDataProofPreservesUnrunnableGitErrors(t *testing.T) {
	revision := strings.Repeat("a", 40)
	ref, _ := sourceProofFixture([]byte("raw"), revision)
	for _, cause := range []error{ErrOldGit, ErrCannotRun, TimeoutError{}} {
		reader := &sourceReader{Memory: &Memory{Err: cause}, revision: revision}
		proof := VerifySourceData(sourceDocument(ref), DependencyReaders{{Repository: ref.Repository, Revision: revision}: reader})
		if proof.Stage != SourceDataRefused || !errors.Is(proof.Unrunnable, cause) {
			t.Fatalf("Git error lost: %v, %+v", cause, proof)
		}
	}
}

func TestSourceDataProofHasIndependentFiveMiBLimit(t *testing.T) {
	revision := strings.Repeat("a", 40)
	for _, size := range []int{(4 << 20) + 1, MaxSourceDataBytes, MaxSourceDataBytes + 1} {
		body := bytes.Repeat([]byte("x"), size)
		ref, reader := sourceProofFixture(body, revision)
		proof := VerifySourceData(sourceDocument(ref), DependencyReaders{{Repository: ref.Repository, Revision: revision}: reader})
		want := SourceDataChecked
		if size > MaxSourceDataBytes {
			want = SourceDataRefused
		}
		if proof.Stage != want || reader.blobCalls != 1 || reader.limits[0] != MaxSourceDataBytes {
			t.Fatalf("size %d: %+v", size, proof)
		}
	}
}
