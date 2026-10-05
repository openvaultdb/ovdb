package repo

import (
	"fmt"
	"slices"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/representation"
)

// CheckRepresentation is a proposed opt-in attachment check, deliberately not
// called by Check until the closed canonical publisher and Directory companions
// land. It checks immutable documents, not semantic acceptance or admission.
func CheckRepresentation(r Reader, m manifest.Manifest, a representation.Reference, dependencies map[string]Reader) []manifest.Finding {
	j, _ := manifest.NewJudge(manifest.Publisher)
	c := &checker{r: r, j: j, dirs: map[string]dirResult{}, seen: map[string]bool{}, files: map[string]fileRead{}}
	document := "representation_contract"
	if a.Repository != "" || a.Revision != "" {
		c.add(document, "representation-reference", 0, "attachment must be provider-local")
		return c.res.Findings
	}
	f := m.PublisherRepository
	if !c.require(document, RuleFile, f.Line, "representation_contract", a.Path) {
		return c.res.Findings
	}
	data, ok := c.readable(document, f.Line, "representation_contract", a.Path)
	if !ok {
		return c.res.Findings
	}
	if representation.Hash(data) != a.SHA256 {
		c.add(a.Path, "representation-hash", 0, "representation_contract SHA256 does not match committed bytes")
		return c.res.Findings
	}
	head, err := c.r.Head()
	if err != nil {
		c.add(a.Path, "representation-reference", 0, "provider revision unavailable: %s", ascii(err.Error()))
		return c.res.Findings
	}
	ctx := representation.Context{Repository: m.PublisherRepository.Value, Revision: head}
	ctx.Resolve = func(ref representation.Reference) ([]byte, error) {
		reader := c.r
		if ref.Repository != "" {
			reader = dependencies[ref.Repository]
			if reader == nil {
				return nil, fmt.Errorf("explicit immutable dependency unavailable")
			}
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
		return c.res.Findings
	}
	for _, contract := range doc.Contracts {
		if m.Form != manifest.FormOwn || !m.ModelSpec.Usable() || !m.MeaningFile.Usable() || contract.Target.Model.Path != m.ModelSpec.Value || contract.Target.Binding.Document.Path != m.MeaningFile.Value || !slices.Contains(m.Recordsets.Value, contract.Bridge.Table) || !slices.Contains(m.Recordsets.Value, contract.Target.Entity) {
			c.add(a.Path, "representation-manifest-link", 0, "target model/binding and native target/bridge recordsets must match this publisher manifest")
		}
	}

	return c.res.Findings
}
