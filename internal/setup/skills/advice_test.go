package skills

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/skillsync"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/libguard"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/setup/skills/skillstest"
)

// Every way skillsync v0.27.0 returns ErrStateCorrupt, with where it can occur:
//
//	record  - reading .cli-helpers-skills-sync.json (readState): no journal, or a
//	          journal that is there as well;
//	legacy  - reading the old WB marker: only with Options.Legacy, which ovdb does
//	          not set, so it cannot occur;
//	journal - recovering the journal that is in the folder: the record reads, or
//	          (the double case) does not;
//	live    - while an install runs: no journal if its rollback worked, the
//	          journal if it did not.
//
// What a person is told is decided by what is on disk, never by these words;
// TestTheAdviceDependsOnTheFactsOnDiskNotTheWords holds that, and the guard below
// fails when the library gains a message that is not listed here.
var corruptStateMessages = map[string]string{
	"%w: state marker is a symlink":                  "record",
	"%w: parse %s: %v":                               "record",
	"%w: unsupported schema %d":                      "record",
	"%w: invalid plugin state":                       "record",
	"%w: invalid plugin provenance":                  "record",
	"%w: invalid supplier":                           "record",
	"%w: invalid supplier CLI version":               "record",
	"%w: supplier CLI version has no supplier":       "record",
	"%w: invalid owned skill":                        "record",
	"%w: %s claimed by both %s and %s":               "record",
	"%w: invalid legacy plugin state":                "legacy",
	"%w: legacy marker is a symlink":                 "legacy",
	"%w: parse legacy marker: %v":                    "legacy",
	"%w: invalid legacy marker":                      "legacy",
	"%w: invalid legacy skill":                       "legacy",
	"%w: validate legacy %s: %v":                     "legacy",
	"%w: legacy %s content differs":                  "legacy",
	"%w: digest legacy %s: %v":                       "legacy",
	"%w: invalid legacy wb_version":                  "legacy",
	"%w: invalid legacy CLI":                         "legacy",
	"%w: legacy skill %s is already owned by %s":     "legacy",
	"%w: recovery target %s lacks prior ownership":   "journal",
	"%w: recovery journal is a symlink":              "journal",
	"%w: recovery journal ancestry: %w":              "journal",
	"%w: invalid recovery journal":                   "journal",
	"%w: transaction directory ancestry: %w":         "journal",
	"%w: transaction directory is unsafe":            "journal",
	"%w: transaction %s directory ancestry: %w":      "journal",
	"%w: transaction %s directory is unsafe":         "journal",
	"%w: unsafe transaction content":                 "journal",
	"%w: digest transaction content: %v":             "journal",
	"%w: committed removal %s reappeared":            "journal",
	"%w: committed target %s differs":                "journal",
	"%w: backup %s changed after capture":            "journal",
	"%w: added target %s lacks transaction proof":    "journal",
	"%w: added target %s is not transaction content": "journal",
	"%w: original %s is not recoverable":             "journal",
	"%w: backup %s differs":                          "journal",
	"%w: target %s differs":                          "journal",
	"%w: recovery cleanup paths are unsafe":          "journal",
	"%w: path escapes transaction root":              "live",
	"%w: directory sync escaped transaction root":    "live",
	"%w: invalid transaction change":                 "live",
	"%w: staged skill digest differs":                "live",
	"%w: transaction proof digest differs":           "live",
	"%w: target %s changed after planning":           "live",
}

// libraryCorruptStateMessages are the format strings of every error the library
// builds from ErrStateCorrupt, read from the source of the version go.mod selects.
func libraryCorruptStateMessages(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, file := range libguard.Source(t, "github.com/strongo/cli-helpers/skillsync") {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Errorf" {
				return true
			}
			if ident, ok := call.Args[1].(*ast.Ident); !ok || ident.Name != "ErrStateCorrupt" {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if text, err := strconv.Unquote(lit.Value); err == nil {
					seen[text] = true
				}
			}
			return true
		})
	}
	var out []string
	for text := range seen {
		out = append(out, text)
	}
	sort.Strings(out)
	return out
}

func TestEveryCorruptStateTheLibraryReturnsIsClassified(t *testing.T) {
	library := libraryCorruptStateMessages(t)
	if len(library) < 40 {
		t.Fatalf("read %d messages %v: the guard no longer finds them", len(library), library)
	}
	var listed []string
	for text := range corruptStateMessages {
		listed = append(listed, text)
	}
	sort.Strings(listed)
	for _, text := range library {
		if corruptStateMessages[text] == "" {
			t.Errorf("skillsync returns ErrStateCorrupt as %q, which corruptStateMessages does not classify (record, legacy, journal or live): decide which advice a person needs, and add it with a test that follows that advice", text)
		}
	}
	for _, text := range listed {
		if !slices.Contains(library, text) {
			t.Errorf("corruptStateMessages lists %q, which skillsync no longer returns", text)
		}
	}
}

// What a person is told depends on what is on disk, whatever the library said:
// for every message the library can give, in each of the four situations.
func TestTheAdviceDependsOnTheFactsOnDiskNotTheWords(t *testing.T) {
	d, _ := Find(Storage)
	for _, tc := range []struct {
		name            string
		journal, record bool // the journal is there; the record file cannot be read
		want            []string
		not             []string
	}{
		{"journal, record reads", true, false, []string{"was interrupted", "issues/45", ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*"}, []string{"can't be used", "stopped the install"}},
		{"journal, record unreadable", true, true, []string{"can't be read either", ".cli-helpers-skills-sync.json", ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*"}, []string{"stopped the install"}},
		{"no journal, record unreadable", false, true, []string{"can't be used", ".cli-helpers-skills-sync.json", "Another tool"}, []string{"issues/45", "was interrupted"}},
		{"no journal, record reads", false, false, []string{"stopped the install", "Install again"}, []string{"issues/45", "can't be used", "Another tool", "move"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEnv(t)
			dir, _ := claudeTarget(t, e, d)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.journal {
				skillstest.PutPendingRecovery(t, dir)
			}
			if tc.record {
				if err := os.WriteFile(filepath.Join(dir, skillsync.StateFileName), []byte(`{"schema":2,"plug`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			normalized := map[string]bool{}
			for text := range corruptStateMessages {
				args := []any{skillsync.ErrStateCorrupt}
				for i := strings.Count(text, "%") - 1; i > 0; i-- {
					args = append(args, "x")
				}
				library := fmt.Errorf(strings.ReplaceAll(text, "%d", "%v"), args...)
				advice := syncFailureAdvice(dir, d, library).reason
				for _, want := range tc.want {
					if !strings.Contains(advice, want) {
						t.Errorf("%q: advice lacks %q:\n%s", text, want, advice)
					}
				}
				for _, not := range tc.not {
					if strings.Contains(advice, not) {
						t.Errorf("%q: advice says %q, which is untrue here:\n%s", text, not, advice)
					}
				}
				normalized[strings.ReplaceAll(advice, redact.String(library.Error()), "<the library's reason>")] = true
			}
			// The same advice for every message, apart from the library's reason quoted in it.
			if len(normalized) != 1 {
				t.Errorf("%d different pieces of advice for one situation", len(normalized))
			}
		})
	}
}

// installedEnv is an environment with the storage skill installed for Claude
// Code through the real library.
func installedEnv(t *testing.T) (Env, Definition, string, RequestTarget) {
	t.Helper()
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	if _, err := (Build{}).Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{}); err != nil {
		t.Fatal(err)
	}
	return e, d, dir, target
}

func installError(t *testing.T, e Env, d Definition, target RequestTarget, consent Consent) *envelope.Error {
	t.Helper()
	_, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, consent)
	var failure *envelope.Error
	if err == nil {
		return nil
	}
	if ok := asEnvelope(err, &failure); !ok {
		t.Fatalf("err = %v", err)
	}
	return failure
}

func asEnvelope(err error, target **envelope.Error) bool {
	e, ok := err.(*envelope.Error)
	*target = e
	return ok
}

// "committed target differs": a crash after the state was written, then an edit
// of the installed skill. The advice (move the journal and the transaction folder
// out) leaves the person with the ordinary "changed since install" and their edit
// intact; it used to be answered with the state-file advice, which does not work.
func TestAdviceForACommittedJournalFollowedLeavesTheEditIntact(t *testing.T) {
	e, d, dir, target := installedEnv(t)
	skillstest.PutCommittedRecovery(t, dir, d.Dir)
	edited := skillText(t, d.Dir) + "\nMy edit.\n"
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": edited})

	failure := installError(t, e, d, target, Consent{})
	if failure == nil || !strings.Contains(failure.Reason, "committed target openvaultdb differs") || !strings.Contains(failure.Reason, "issues/45") ||
		strings.Contains(failure.Reason, "can't be used") || strings.Contains(failure.Reason, "Another tool") {
		t.Fatalf("advice = %+v", failure)
	}
	if listed := Inspect(e).Skills[0].Targets[0]; listed.State != StateRecoveryPending {
		t.Errorf("listed = %+v", listed)
	}
	// Moving the state file out, as the wrong advice said, leaves them stuck.
	keep := skillstest.MoveOut(t, dir, skillsync.StateFileName)
	if again := installError(t, e, d, target, Consent{}); again == nil || strings.Contains(again.Reason, "Installing finishes") {
		t.Fatalf("after moving only the state out: %+v", again)
	}
	if err := os.Rename(filepath.Join(keep, skillsync.StateFileName), filepath.Join(dir, skillsync.StateFileName)); err != nil {
		t.Fatal(err)
	}

	// Following the advice.
	skillstest.MoveOut(t, dir, ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*")
	if got := Inspect(e).Skills[0].Targets[0].State; got != StateChanged {
		t.Errorf("after the advice the state is %s, want changed since install", got)
	}
	refusal := installError(t, e, d, target, Consent{})
	if refusal == nil || refusal.Code != envelope.AlreadyExists || !strings.Contains(refusal.Reason, "was changed since OVDB installed it") {
		t.Errorf("want the ordinary changed-copy refusal, got %+v", refusal)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, d.Dir, "SKILL.md")); string(got) != edited {
		t.Errorf("the edit was lost: %q", got)
	}
}

// "lacks prior ownership" (#45): following the advice, an install that agrees to
// take the folder over succeeds, and the person's copy is in a backup.
func TestAdviceForAStuckAdoptionFollowedGetsThePersonUnstuck(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	mine := skillText(t, d.Dir) + "\nMine.\n"
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": mine})
	putCopy(t, filepath.Join(dir, adoptedBackupDirName, "20261005T120000.000000000Z", d.Dir), map[string]string{"SKILL.md": mine})
	skillstest.PutUnrecoverableRecovery(t, dir, d.Dir)

	failure := installError(t, e, d, target, Consent{All: true})
	if failure == nil || !strings.Contains(failure.Reason, "lacks prior ownership") || !strings.Contains(failure.Reason, "issues/45") {
		t.Fatalf("advice = %+v", failure)
	}
	skillstest.MoveOut(t, dir, ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*")
	doc, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{All: true})
	if err != nil || doc.Outcomes[0].Result != "adopted" {
		t.Fatalf("still stuck after the advice: %+v, %v", doc, err)
	}
	if kept, _ := os.ReadFile(filepath.Join(doc.Outcomes[0].BackupPath, "SKILL.md")); string(kept) != mine {
		t.Errorf("the person's copy is not in the backup: %q", kept)
	}
}

// A record that cannot be read: following the advice, the skills it named show as
// not managed yet and installing takes them over, with the edit in a backup.
func TestAdviceForAnUnreadableRecordFollowedGetsThePersonUnstuck(t *testing.T) {
	e, d, dir, target := installedEnv(t)
	edited := skillText(t, d.Dir) + "\nMy edit.\n"
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": edited})
	if err := os.WriteFile(filepath.Join(dir, skillsync.StateFileName), []byte(`{"schema":2,"plug`), 0o600); err != nil {
		t.Fatal(err)
	}
	listed := Inspect(e).Skills[0].Targets[0]
	if listed.State != StateRecordUnusable || !strings.Contains(listed.StateReason, "corrupt") {
		t.Errorf("an unreadable record of OVDB's own skill is labelled %+v", listed)
	}
	if failure := installError(t, e, d, target, Consent{All: true}); failure == nil || !strings.Contains(failure.Reason, "can't be used") {
		t.Fatalf("advice = %+v", failure)
	}
	skillstest.MoveOut(t, dir, skillsync.StateFileName)
	if got := Inspect(e).Skills[0].Targets[0].State; got != StateAdoptable {
		t.Errorf("after the advice the state is %s, want not managed yet", got)
	}
	doc, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{All: true})
	if err != nil || doc.Outcomes[0].Result != "adopted" {
		t.Fatalf("still stuck after the advice: %+v, %v", doc, err)
	}
	if kept, _ := os.ReadFile(filepath.Join(doc.Outcomes[0].BackupPath, "SKILL.md")); string(kept) != edited {
		t.Errorf("the edit is not in the backup: %q", kept)
	}
}

// The double case, found by running it: a journal and a record that cannot be
// read. Moving either one alone is not enough (each leaves the other's refusal);
// moving all three, as the advice says, gets the person unstuck with the edit in
// a backup.
func TestAdviceForAJournalAndAnUnreadableRecordFollowedGetsThePersonUnstuck(t *testing.T) {
	e, d, dir, target := installedEnv(t)
	skillstest.PutCommittedRecovery(t, dir, d.Dir)
	edited := skillText(t, d.Dir) + "\nMy edit.\n"
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": edited})
	if err := os.WriteFile(filepath.Join(dir, skillsync.StateFileName), []byte(`{"schema":2,"plug`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(e).Skills[0].Targets[0].State; got != StateRecoveryPending {
		t.Errorf("state = %s", got)
	}
	failure := installError(t, e, d, target, Consent{All: true})
	if failure == nil || !strings.Contains(failure.Reason, "can't be read either") {
		t.Fatalf("advice = %+v", failure)
	}
	skillstest.MoveOut(t, dir, skillsync.StateFileName, ".cli-helpers-skills-recovery.json", ".cli-helpers-skills-txn-*")
	doc, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{All: true})
	if err != nil || doc.Outcomes[0].Result != "adopted" {
		t.Fatalf("still stuck after the advice: %+v, %v", doc, err)
	}
	if kept, _ := os.ReadFile(filepath.Join(doc.Outcomes[0].BackupPath, "SKILL.md")); string(kept) != edited {
		t.Errorf("the edit is not in the backup: %q", kept)
	}
}

// A library stop in a running install, with nothing recorded on disk: the advice
// says so and to install again, which works once the cause has gone.
func TestAdviceForAStoppedInstallFollowedWorks(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	_, target := claudeTarget(t, e, d)
	calls := 0
	useSync(t, func(ctx context.Context, cfg skillsync.Config, opts skillsync.Options) (skillsync.Report, error) {
		calls++
		if calls == 1 {
			return skillsync.Report{}, fmt.Errorf("%w: target %s changed after planning", skillsync.ErrStateCorrupt, d.Dir)
		}
		return skillsync.Sync(ctx, cfg, opts)
	})
	failure := installError(t, e, d, target, Consent{})
	if failure == nil || !strings.Contains(failure.Reason, "stopped the install") || !strings.Contains(failure.Reason, "changed after planning") {
		t.Fatalf("advice = %+v", failure)
	}
	if failure := installError(t, e, d, target, Consent{}); failure != nil {
		t.Errorf("installing again: %+v", failure)
	}
}
