package repo

import (
	"fmt"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/datarights"
	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
)

func (c *checker) dataRights(m *manifest.Manifest, path string, doc []byte, deps DependencyReaders) {
	if !m.DataRights.Usable() || !m.DataDeclaration.Usable() {
		return
	}
	r, _ := OriginalObjects(c.r)
	head, err := r.Head()
	if err != nil {
		c.add(path, "data-rights", 0, "provider revision unavailable")
		return
	}
	profile := license.Directory
	if c.res.Profile == manifest.Publisher {
		profile = license.Publisher
	}
	source := license.Identity{ServerID: "", DatabaseID: m.URL.Value}
	// The server identity is verified from the separately pinned author artifact.
	if ref := m.DataRights.Value.Server; ref != nil {
		reader := r
		if ref.Repository != "" {
			reader = deps[DependencyKey{Repository: ref.Repository, Revision: ref.Revision}]
		}
		if reader == nil {
			c.add(path, "data-rights", 0, "immutable server dependency unavailable")
			return
		}
		// Verification below checks the hash before parsing identity. Use the paired
		// descriptor serverId if there is one; otherwise the server author supplies it.
		data, e := rightsBlob(reader, *ref)
		if e != nil {
			c.add(path, "data-rights", 0, "server reference: %s", ascii(e.Error()))
			return
		}
		id, e := datarights.ServerIdentity(data, *ref)
		if e != nil {
			c.add(path, "data-rights", 0, "server identity: %s", ascii(e.Error()))
			return
		}
		source.ServerID = id
	} else {
		// Database-only profiles still require their serving identity from a descriptor.
		source.ServerID = c.rightsServerID()
	}
	if source.ServerID == "" {
		c.add(path, "data-rights", 0, "a serving server identity is required")
		return
	}
	if descriptorID := c.rightsServerID(); descriptorID != "" && descriptorID != source.ServerID {
		c.add(path, "data-rights", 0, "server author identity differs from descriptor serverId")
		return
	}
	ctx := datarights.Context{Repository: m.PublisherRepository.Value, Revision: head, ManifestPath: path, ManifestBytes: doc, Source: source, Profile: profile}
	ctx.Read = func(ref datarights.Reference) ([]byte, error) {
		reader := r
		if ref.Repository != "" {
			reader = deps[DependencyKey{Repository: ref.Repository, Revision: ref.Revision}]
			if reader == nil {
				return nil, fmt.Errorf("explicit immutable dependency unavailable")
			}
			reader, _ = OriginalObjects(reader)
		}
		return rightsBlob(reader, ref)
	}
	evidence, err := datarights.Verify(*m.DataRights.Value, m.DataDeclaration.Value, m.Recordsets.Value, ctx)
	if err != nil {
		c.add(path, "data-rights", 0, "data_rights validation: %s", ascii(err.Error()))
		return
	}
	m.SourceRights = evidence
}
func rightsBlob(r Reader, ref datarights.Reference) ([]byte, error) {
	r, _ = OriginalObjects(r)
	if ref.Repository != "" {
		head, err := r.Head()
		if err != nil || head != ref.Revision {
			return nil, fmt.Errorf("immutable dependency revision mismatch")
		}
	}
	helper := &checker{r: r, dirs: map[string]dirResult{}}
	kind, _, err := helper.kind(ref.Path)
	if err != nil {
		return nil, err
	}
	if !kind.Regular() {
		return nil, fmt.Errorf("rights reference is not a tracked regular file")
	}
	return r.Blob(ref.Path, int(ref.Bytes)+1)
}
func (c *checker) rightsServerID() string {
	for _, doc := range c.rightsDescriptors {
		if manifest.IsDescriptor(doc) {
			return datarights.DescriptorServerID(doc)
		}
	}
	return ""
}
