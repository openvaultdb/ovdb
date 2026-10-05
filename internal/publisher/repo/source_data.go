package repo

import (
	"errors"
	"fmt"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

// MaxSourceDataBytes is the separate raw-byte proof limit. Metadata Resolve
// retains its independent four MiB artifact limit.
const MaxSourceDataBytes = 5 << 20

type SourceDataStage string

const (
	SourceDataOutsideScope SourceDataStage = "outside_scope"
	SourceDataChecked      SourceDataStage = "checked"
	SourceDataRefused      SourceDataStage = "refused"
)

// SourceDataProof contains only bounded proof metadata, never source bytes.
// A checked stage proves committed raw bytes and their hash, not JSON rows,
// source semantics, canonical admission, or runtime eligibility.
type SourceDataProof struct {
	Stage      SourceDataStage
	References []representation.Reference
	Findings   []manifest.Finding
	// Unrunnable preserves Git unavailable/version/timeout causes so the
	// CLI can return its environment exit status instead of refusal.
	Unrunnable error
}

// VerifySourceData is the opt-in data stage for a document already validated
// by representation.Check. It never provisions a reader or fetches a commit.
// Distinct exact references are read sequentially and cached for this call only.
func VerifySourceData(doc *representation.Document, dependencies DependencyReaders) SourceDataProof {
	return verifySourceData(doc, dependencies, map[representation.Reference]error{})
}

func verifySourceData(doc *representation.Document, dependencies DependencyReaders, cache map[representation.Reference]error) SourceDataProof {
	proof := SourceDataProof{Stage: SourceDataOutsideScope}
	if doc != nil && (doc.Format == representation.Format || doc.Format == representation.Format2) {
		return proof
	}
	j, _ := manifest.NewJudge(manifest.Publisher)
	c := &checker{j: j}
	if doc == nil || doc.Format != representation.Format3 {
		c.add("representation_contract", "representation-source-data", 0, "verified format3 document is required")
		proof.Stage = SourceDataRefused
		proof.Findings = c.res.Findings
		return proof
	}
	proof.Stage = SourceDataChecked
	if len(doc.Contracts) == 0 || len(doc.Contracts) > 32 {
		c.add("representation_contract", "representation-source-data", 0, "exact source-data contract count is outside the closed bound")
		proof.Stage = SourceDataRefused
		proof.Findings = c.res.Findings
		return proof
	}
	seen := map[representation.Reference]bool{}
	for _, contract := range doc.Contracts {
		if contract.Execution != representation.NativeIdentifier || contract.Source.Data == nil {
			c.add("representation_contract", "representation-source-data", 0, "exact source-data descriptor is required")
			proof.Stage = SourceDataRefused
			continue
		}
		ref := *contract.Source.Data
		if seen[ref] {
			continue
		}
		seen[ref] = true
		err, checked := cache[ref]
		if !checked {
			err = verifySourceDataReference(ref, dependencies)
			cache[ref] = err
		}
		if err != nil {
			if proof.Unrunnable == nil && (errors.Is(err, ErrCannotRun) || errors.Is(err, ErrOldGit)) {
				proof.Unrunnable = err
			}
			c.add("representation_contract", "representation-source-data", 0, "source data %s: %s", ref.Path, ascii(err.Error()))
			proof.Stage = SourceDataRefused
			continue
		}
		proof.References = append(proof.References, ref)
	}
	proof.Findings = c.res.Findings
	return proof
}

func verifySourceDataReference(ref representation.Reference, dependencies DependencyReaders) error {
	key := DependencyKey{Repository: ref.Repository, Revision: ref.Revision}
	r := dependencies[key]
	if r == nil {
		return fmt.Errorf("explicit immutable dependency unavailable")
	}
	r, _ = OriginalObjects(r)
	head, err := r.Head()
	if err != nil {
		return fmt.Errorf("dependency HEAD unavailable: %w", err)
	}
	if head != ref.Revision {
		return fmt.Errorf("dependency revision mismatch")
	}
	c := &checker{r: r, dirs: map[string]dirResult{}}
	kind, _, err := c.kind(ref.Path)
	if err != nil {
		return fmt.Errorf("dependency tree unreadable: %w", err)
	}
	if !kind.Regular() {
		return fmt.Errorf("source data is not a tracked regular file")
	}
	data, err := r.Blob(ref.Path, MaxSourceDataBytes)
	if err != nil {
		return fmt.Errorf("source data unreadable or over %d bytes: %w", MaxSourceDataBytes, err)
	}
	if len(data) > MaxSourceDataBytes || representation.Hash(data) != ref.SHA256 {
		return fmt.Errorf("raw source-data byte limit/checksum mismatch")
	}
	return nil
}
