// Package skillstest is the test support of the packages that show or install
// AI agent skills: what an interrupted install leaves behind.
package skillstest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PutPendingRecovery leaves in skillsDir the recovery journal an interrupted
// install leaves: a journal in skillsync's own format (schema 2) for an
// unrelated skill that was being added, with its transaction directory.
// skillsync reads it like one it wrote itself: a dry run fails with
// ErrRecoveryPending, and a real sync recovers it first.
func PutPendingRecovery(t testing.TB, skillsDir string) {
	t.Helper()
	const id = "pending1"
	if err := os.MkdirAll(filepath.Join(skillsDir, ".cli-helpers-skills-txn-"+id), 0o700); err != nil {
		t.Fatal(err)
	}
	journal := fmt.Sprintf(`{"schema":2,"id":%q,"transaction":%q,"changes":[{"name":"zz-unrelated","new":%q,"existed":false,"phase":"prepared"}]}`,
		id, ".cli-helpers-skills-txn-"+id, strings.Repeat("a", 64))
	if err := os.WriteFile(filepath.Join(skillsDir, ".cli-helpers-skills-recovery.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
}

// RecoveryPending reports whether skillsDir still has that journal.
func RecoveryPending(skillsDir string) bool {
	_, err := os.Lstat(filepath.Join(skillsDir, ".cli-helpers-skills-recovery.json"))
	return err == nil
}

// PutUnrecoverableRecovery leaves the journal of an adoption interrupted after
// the folder was replaced and before skillsync wrote its state: a change of the
// skill folder name that existed before and that no state owns. skillsync's
// recovery refuses it on every later sync ("recovery target ... lacks prior
// ownership", strongo/cli-helpers#45).
func PutUnrecoverableRecovery(t testing.TB, skillsDir, name string) {
	t.Helper()
	const id = "stuck1"
	if err := os.MkdirAll(filepath.Join(skillsDir, ".cli-helpers-skills-txn-"+id), 0o700); err != nil {
		t.Fatal(err)
	}
	journal := fmt.Sprintf(`{"schema":2,"id":%q,"transaction":%q,"changes":[{"name":%q,"old":%q,"new":%q,"existed":true,"phase":"published"}]}`,
		id, ".cli-helpers-skills-txn-"+id, name, strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err := os.WriteFile(filepath.Join(skillsDir, ".cli-helpers-skills-recovery.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
}

// PutCommittedRecovery leaves what a crash after skillsync's state write and
// before the journal's cleanup leaves, for the skill name that is installed in
// skillsDir: the state names the transaction (recovery_id), and the journal and
// its directory are still there. A sync recovers it forward while the folder is
// as it was installed; once the folder differs, skillsync refuses it ("committed
// target ... differs").
func PutCommittedRecovery(t testing.TB, skillsDir, name string) {
	t.Helper()
	const id = "done1"
	statePath := filepath.Join(skillsDir, ".cli-helpers-skills-sync.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	digest := ""
	plugins, _ := state["plugins"].(map[string]any)
	for _, plugin := range plugins {
		skills, _ := plugin.(map[string]any)["skills"].(map[string]any)
		if d, ok := skills[name].(string); ok {
			digest = d
		}
	}
	if digest == "" {
		t.Fatalf("%s is not installed in %s", name, skillsDir)
	}
	state["recovery_id"] = id
	raw, _ = json.Marshal(state)
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, ".cli-helpers-skills-txn-"+id), 0o700); err != nil {
		t.Fatal(err)
	}
	journal := fmt.Sprintf(`{"schema":2,"id":%q,"transaction":%q,"changes":[{"name":%q,"new":%q,"existed":false,"phase":"published"}]}`,
		id, ".cli-helpers-skills-txn-"+id, name, digest)
	if err := os.WriteFile(filepath.Join(skillsDir, ".cli-helpers-skills-recovery.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
}

// MoveOut moves the named files and folders (glob patterns) of skillsDir to a
// directory of its own, as the advice tells a person to, and returns where.
func MoveOut(t testing.TB, skillsDir string, names ...string) string {
	t.Helper()
	keep := t.TempDir()
	for _, name := range names {
		matches, _ := filepath.Glob(filepath.Join(skillsDir, name))
		if len(matches) == 0 {
			t.Fatalf("nothing to move out matches %s", name)
		}
		for _, m := range matches {
			if err := os.Rename(m, filepath.Join(keep, filepath.Base(m))); err != nil {
				t.Fatal(err)
			}
		}
	}
	return keep
}
