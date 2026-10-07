package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/openvaultdb/openvaultdb-go/pkg/license"
	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/publisher/source"
	"strings"
)

func (c *checker) sourceDefinition(m *manifest.Manifest, path string, data []byte) {
	if !m.SourceDefinition.Usable() || m.DataRights.Present {
		return
	}
	r, _ := OriginalObjects(c.r)
	head, err := r.Head()
	if err != nil || !fullSHA(head) || !m.PublisherRepository.Usable() {
		c.add(path, "source-definition", 0, "immutable publisher definition identity unavailable")
		return
	}
	sum := sha256.Sum256(data)
	m.SourceDefinitionEvidence = &source.Evidence{
		Origin: "publisher-definition-verified", InputVerification: "dynamic-unpinned",
		Manifest: license.Pin{Role: "provider", Repository: m.PublisherRepository.Value, Revision: head, Path: path, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))},
	}
}

func fullSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 20 && s == strings.ToLower(s)
}
