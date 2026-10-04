//go:build !windows

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In a terminal the person is asked before an adoption, and the question says
// the copy that is already there is taken over and kept as a backup. "n"
// leaves the folder as it was; "y" adopts it and the result says so.
func TestSkillAdoptionIsAskedInATerminal(t *testing.T) {
	e := previewEnv(t)
	dir := filepath.Join(e.userHome(), ".claude", "skills", "openvaultdb")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mine := bundledSkill(t, "openvaultdb") + "\nMy own note.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	declined := e.runWithTerminal("n\n", "skills", "install", "openvaultdb", "--harness", "claude")
	for _, want := range []string{"Install the OpenVaultDB skill?", "already here, not managed yet", "It also takes over the copy already in: " + dir + ". OVDB keeps a backup of it."} {
		if !strings.Contains(declined.stderr, want) {
			t.Errorf("the question lacks %q:\n%s", want, declined.stderr)
		}
	}
	if declined.code != 0 || !strings.Contains(declined.stdout, "Nothing installed.") {
		t.Errorf("declined = %+v", declined)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(data) != mine {
		t.Errorf("declining changed the folder: %q", data)
	}

	agreed := e.runWithTerminal("y\n", "skills", "install", "openvaultdb", "--harness", "claude")
	if agreed.code != 0 || !strings.Contains(agreed.stdout, "(already there, now managed by OVDB)") || !strings.Contains(agreed.stdout, "Your copy is kept at ") {
		t.Fatalf("agreed = %+v", agreed)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(data) != bundledSkill(t, "openvaultdb") {
		t.Errorf("the skill was not put in place: %q", data)
	}
}
