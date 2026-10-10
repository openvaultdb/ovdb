package repo

import (
	"fmt"
	"slices"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

// DependencyKey identifies a repository at one immutable revision.
type DependencyKey struct{ Repository, Revision string }

// DependencyReaders are explicitly provisioned by a trusted caller.
type DependencyReaders map[DependencyKey]Reader

// CheckRepresentation is a metadata-only attachment check.
// A successful result does not verify format3 source.data bytes, semantic
// acceptance, or admission; callers needing byte proof use VerifySourceData.
func CheckRepresentation(r Reader, m manifest.Manifest, a representation.Reference, dependencies DependencyReaders) []manifest.Finding {
	r, _ = OriginalObjects(r)
	j, _ := manifest.NewJudge(manifest.Publisher)
	c := &checker{r: r, j: j, dirs: map[string]dirResult{}, seen: map[string]bool{}, files: map[string]fileRead{}}
	c.representation(m, a, dependencies)
	return append(c.res.Findings, j.Notice("representation_contract")...)
}

func (c *checker) representation(m manifest.Manifest, a representation.Reference, dependencies DependencyReaders) *representation.Document {
	document := "representation_contract"
	if a.Repository != "" || a.Revision != "" {
		c.add(document, "representation-reference", 0, "attachment must be provider-local")
		return nil
	}
	head, err := c.r.Head()
	if err != nil {
		c.add(a.Path, "representation-reference", 0, "provider revision unavailable: %s", ascii(err.Error()))
		return nil
	}
	f := m.PublisherRepository
	if !c.require(document, RuleFile, f.Line, "representation_contract", a.Path) {
		return nil
	}
	data, ok := c.readable(document, f.Line, "representation_contract", a.Path)
	if !ok {
		return nil
	}
	if representation.Hash(data) != a.SHA256 {
		c.add(a.Path, "representation-hash", 0, "representation_contract SHA256 does not match committed bytes")
		return nil
	}
	ctx := representation.Context{Repository: m.PublisherRepository.Value, Revision: head}
	ctx.Resolve = func(ref representation.Reference) ([]byte, error) {
		reader := c.r
		if ref.Repository != "" {
			reader = dependencies[DependencyKey{Repository: ref.Repository, Revision: ref.Revision}]
			if reader == nil {
				return nil, fmt.Errorf("explicit immutable dependency unavailable")
			}
			reader, _ = OriginalObjects(reader)
			revision, err := reader.Head()
			if err != nil || revision != ref.Revision {
				return nil, fmt.Errorf("dependency revision mismatch or unreadable")
			}
		}
		other := &checker{r: reader, dirs: map[string]dirResult{}}
		kind, _, err := other.kind(ref.Path)
		if err != nil {
			return nil, err
		}
		if !kind.Regular() {
			return nil, fmt.Errorf("reference is not a tracked regular file")
		}
		return reader.Blob(ref.Path, representation.MaxArtifactBytes)
	}
	doc, err := representation.Check(data, ctx)
	if err != nil {
		c.add(a.Path, "representation-contract", 0, "structural validation failed: %s", ascii(err.Error()))
		return nil
	}
	// Targets name ModelSpec record types; bridge tables retain native recordset names.
	entities := m.RecordTypes()
	for _, contract := range doc.Contracts {
		if m.Form != manifest.FormOwn || !m.ModelSpec.Usable() || !m.MeaningFile.Usable() || contract.Target.Model.Path != m.ModelSpec.Value || contract.Target.Binding.Document.Path != m.MeaningFile.Value || (contract.Execution != representation.NativeIdentifier && !slices.Contains(m.Recordsets.Value, contract.Bridge.Table)) || !slices.Contains(entities, contract.Target.Entity) {
			c.add(a.Path, "representation-manifest-link", 0, "target model/binding and native target/bridge recordsets must match this publisher manifest")
		}
		// A contract reads the columns of its target and of its bridge table by the model's names, so a recordset that it names lists none (decision 0012,
		// N20; the reference's hasColumns: a recordset with `columns: {}` lists none).
		for _, name := range m.RecordsetsOfType(contract.Target.Entity) {
			if m.ListsColumns(name) {
				c.add(a.Path, RuleColumns, 0, "recordset %s lists columns, but a representation contract reads its columns by the model's names: remove columns", rules.Quote(name))
			}
		}
		if contract.Execution != representation.NativeIdentifier && m.ListsColumns(contract.Bridge.Table) {
			c.add(a.Path, RuleColumns, 0, "recordset %s lists columns, but a representation contract reads its columns by the model's names: remove columns", rules.Quote(contract.Bridge.Table))
		}
	}

	return doc
}
