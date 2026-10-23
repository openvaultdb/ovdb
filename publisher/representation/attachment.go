package representation

import (
	"fmt"
	"strings"
)

// ParseAttachment reads the optional closed provider-local path/SHA256 link.
// The default repository check additionally verifies structural closure and the
// separate mandatory format3 source-data proofs.
func ParseAttachment(data []byte) (*Reference, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("manifest byte limit exceeded")
	}
	var manifest map[string]any
	if err := singleYAML(data, &manifest); err != nil {
		return nil, err
	}
	raw, present := manifest["representation_contract"]
	if !present {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok || len(object) != 2 {
		return nil, fmt.Errorf("attachment must contain only path and sha256")
	}
	p, ok := object["path"].(string)
	if !ok || !path(p) || !strings.HasSuffix(p, ".json") {
		return nil, fmt.Errorf("attachment requires a repository-local JSON path")
	}
	h, ok := object["sha256"].(string)
	if !ok || !hex(h, 64) {
		return nil, fmt.Errorf("attachment requires lower-case SHA256")
	}
	return &Reference{Path: p, SHA256: h}, nil
}
