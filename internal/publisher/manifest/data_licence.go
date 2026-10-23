package manifest

import (
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// dataConjunction is the bounded data-only profile. It preserves authored order
// and requires exact known atoms and separators; it never normalizes input.
func dataConjunction(s string) bool {
	if len(s) > rules.MaxLicenceLength {
		return false
	}
	atoms := strings.Split(s, " AND ")
	if len(atoms) < 2 || len(atoms) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, atom := range atoms {
		if !slices.Contains(licenceIDs, atom) || seen[atom] {
			return false
		}
		seen[atom] = true
	}
	return true
}

func dataLicenceProblem(s string) string {
	if dataConjunction(s) {
		return ""
	}
	return licenceProblem(s)
}
