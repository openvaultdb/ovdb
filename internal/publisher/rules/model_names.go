package rules

import "strings"

// ModelSourceName is the Go ModelSpec source-name blank test. It never normalizes
// the authored name; NEL is whitespace, while FEFF is not Go whitespace.
func ModelSourceName(name string) bool { return strings.TrimSpace(name) != "" }

// ModelConceptName is a local record/component/enum declaration name. Address,
// binding URI, native table and column path grammars are separate contracts.
func ModelConceptName(name string) bool {
	if !ModelSourceName(name) || strings.Contains(name, ".") {
		return false
	}
	switch name {
	case "records", "entities", "components", "enums", "collections", "recordsets":
		return false
	}
	return true
}

// ModelLabel preserves existing identifier diagnostics and quotes every broader
// authored source name, bounding controls, Unicode and message length.
func ModelLabel(name string) string {
	if name != "" && len(name) <= 40 {
		simple := true
		for _, c := range name {
			if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
				simple = false
				break
			}
		}
		if simple {
			return name
		}
	}
	return Quote(name)
}
