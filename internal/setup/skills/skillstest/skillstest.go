// Package skillstest is the test support of the packages that show or install
// AI agent skills: what an interrupted install leaves behind.
package skillstest

import (
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
