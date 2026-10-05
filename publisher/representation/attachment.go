package representation

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseAttachment reads the optional closed path/SHA256 link without changing
// the existing manifest's acceptance rules. The default publisher remains closed
// until the canonical companion validators explicitly support this attachment.
func ParseAttachment(data []byte) (*Reference, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("manifest byte limit exceeded")
	}
	var manifest map[string]any
	if err := yaml.Unmarshal(data, &manifest); err != nil {
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
