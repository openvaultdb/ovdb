package web

import (
	"os"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/skillsync/cobracmd"
)

// The browser tests run ovdb in a home of their own (review F4, F5): global
// setup exports the binary it built, and ovdbEnv clears every variable that
// moves an AI agent's skills folder, so a developer's real agent config is
// never written.
func TestE2EEnvironmentIsolatesSkills(t *testing.T) {
	setup, err := os.ReadFile("e2e/global-setup.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(setup), "OVDB_E2E_BIN: binary") {
		t.Error("global-setup.ts does not export OVDB_E2E_BIN")
	}
	helpers, err := os.ReadFile("e2e/ovdb.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range cobracmd.DefaultHarnesses {
		for _, name := range []string{h.ConfigEnv, h.HomeEnv} {
			if name != "" && !strings.Contains(string(helpers), "'"+name+"'") {
				t.Errorf("e2e/ovdb.ts ovdbEnv does not clear %s (harness %s)", name, h.ID)
			}
		}
	}
}
