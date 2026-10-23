package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
)

// PrepareDynamicSourceRight checks original publisher Git objects and prepares
// detached declaration/notice metadata for an independently reviewed runtime
// binding. The pin verifies publisher metadata only, never mutable source bytes.
// It does not clear source.Definition.RequireExecution or establish semantic,
// legal, paid-use, runtime or consumer admission. executorSource is trusted
// configuration: its local recordset may differ from the publisher's native name.
func PrepareDynamicSourceRight(reader Reader, options Options, nativeRecordset string, executorSource license.Identity) (license.SourceRight, error) {
	original, _ := OriginalObjects(reader)
	result := Check(original, options)
	if !result.OK() || !result.Manifest.SourceDefinition.Usable() || result.Manifest.SourceDefinitionEvidence == nil {
		return license.SourceRight{}, fmt.Errorf("dynamic rights require a clean original-object publisher definition check")
	}
	m := result.Manifest
	evidence := *m.SourceDefinitionEvidence
	if _, ok := m.SourceDefinition.Value.Recordsets[nativeRecordset]; !ok {
		return license.SourceRight{}, fmt.Errorf("dynamic rights require an exact native recordset")
	}
	for _, value := range []string{executorSource.ServerID, executorSource.DatabaseID, executorSource.Recordset} {
		if !dynamicIdentity(value) {
			return license.SourceRight{}, fmt.Errorf("dynamic rights require an explicit bounded executor source identity")
		}
	}
	head, err := original.Head()
	if err != nil || head != evidence.Manifest.Revision || !fullSHA(head) ||
		evidence.Origin != "publisher-definition-verified" || evidence.InputVerification != "dynamic-unpinned" {
		return license.SourceRight{}, fmt.Errorf("dynamic definition identity changed during preparation")
	}
	data, err := original.Blob(evidence.Manifest.Path, MaxFileBytes)
	if err != nil {
		return license.SourceRight{}, fmt.Errorf("dynamic definition bytes unavailable")
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != evidence.Manifest.Bytes || hex.EncodeToString(sum[:]) != evidence.Manifest.SHA256 {
		return license.SourceRight{}, fmt.Errorf("dynamic definition bytes changed during preparation")
	}
	d := m.SourceDefinition.Value
	attribution, freeSource := d.Rights.Attribution, d.Rights.FreeSource
	return license.SourceRight{
		SourceID: executorSource.SourceID(), Source: executorSource,
		Declaration: d.Rights.Declaration.Normalized(), DeclarationScope: license.DatabaseScope,
		DeclaredAt:     license.Identity{ServerID: executorSource.ServerID, DatabaseID: executorSource.DatabaseID},
		EvidenceOrigin: evidence.Origin, Pins: []license.Pin{evidence.Manifest},
		Attribution: &attribution, FreeSource: &freeSource,
		Transformations: slices.Clone(d.Rights.Transformations),
	}, nil
}

func dynamicIdentity(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}
