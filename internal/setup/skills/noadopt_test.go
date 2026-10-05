package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/skillsync"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills/skillstest"
)

// useSync replaces the skillsync call of this package for one test.
func useSync(t *testing.T, fake func(context.Context, skillsync.Config, skillsync.Options) (skillsync.Report, error)) {
	t.Helper()
	previous := syncSkills
	syncSkills = fake
	t.Cleanup(func() { syncSkills = previous })
}

func claudeTarget(t *testing.T, e Env, d Definition) (string, RequestTarget) {
	t.Helper()
	dir := filepath.Join(e.Home, ".claude", "skills")
	return dir, RequestTarget{Harness: "claude", SkillsDir: dir}
}

// The state of a folder comes from the library's dry run with NoAdopt: the
// library says "would have been adopted" (Change.Adoptable), and ovdb does not
// classify the folder itself. A folder that is empty on disk but reported
// adoptable is adoptable, and the dry run is the one that does not adopt.
func TestStateOfAFolderIsWhatTheLibraryReports(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, _ := claudeTarget(t, e, d)
	if err := os.MkdirAll(filepath.Join(dir, d.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	var seen skillsync.Options
	useSync(t, func(_ context.Context, _ skillsync.Config, opts skillsync.Options) (skillsync.Report, error) {
		seen = opts
		return skillsync.Report{Changes: []skillsync.Change{{Name: d.Dir, Action: skillsync.Conflict, Reason: "unmanaged target", Adoptable: true}}}, nil
	})
	if got := stateOf(dir, d); got != StateAdoptable {
		t.Errorf("state = %s, want adoptable on the library's word", got)
	}
	if !seen.DryRun || !seen.NoAdopt {
		t.Errorf("options = %+v, want a dry run that does not adopt", seen)
	}
}

// A pending recovery journal makes the library's dry run fail before it plans
// anything. That is its own state, on the list and in a plan, never "not
// installed", "another skill" or "not adoptable"; a real install finishes the
// recovery first (and then refuses to adopt what nobody agreed to).
func TestPendingRecoveryIsItsOwnStateAndInstallingFinishesIt(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	original := skillText(t, d.Dir) + "\nMy note.\n"
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": original})
	skillstest.PutPendingRecovery(t, dir)

	listed := Inspect(e).Skills[0].Targets[0]
	if listed.State != StateRecoveryPending || listed.Installed || listed.StateReason == "" {
		t.Fatalf("listed = %+v, want recovery_pending with the library's reason", listed)
	}
	if planned := e.Plan(d, []RequestTarget{target})[0]; planned.State != StateRecoveryPending {
		t.Errorf("planned = %+v", planned)
	}
	if status := Status(e); len(status[0].InstalledFor) != 0 {
		t.Errorf("status = %+v", status[0])
	}

	// A dry run install cannot recover: it says so, with what to do.
	_, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, true, false, Consent{})
	var failure *envelope.Error
	if !errors.As(err, &failure) || !strings.Contains(failure.Reason, "interrupted") || !strings.Contains(failure.Reason, "without --dry-run") {
		t.Fatalf("dry run err = %v", err)
	}
	if !skillstest.RecoveryPending(dir) {
		t.Fatal("a dry run recovered the journal")
	}

	// The real install recovers first and refuses what it was not asked to adopt.
	_, err = Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{})
	if !errors.As(err, &failure) || failure.Code != envelope.AlreadyExists {
		t.Fatalf("install err = %v, want the refusal of an unmanaged folder", err)
	}
	if skillstest.RecoveryPending(dir) {
		t.Error("the install left the journal")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, d.Dir, "SKILL.md")); string(got) != original {
		t.Errorf("folder changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, adoptedBackupDirName)); !os.IsNotExist(err) {
		t.Errorf("a backup was made for a folder nobody agreed to adopt: %v", err)
	}
	if got := Inspect(e).Skills[0].Targets[0].State; got != StateAdoptable {
		t.Errorf("after recovery the folder is %s, want adoptable", got)
	}
}

// With the person's yes for that folder the same install adopts it after the
// recovery; a yes for another folder does not cover it.
func TestPendingRecoveryDoesNotChangeWhoMayAdopt(t *testing.T) {
	for name, tc := range map[string]struct {
		consent func(dir string, d Definition) Consent
		adopted bool
	}{
		"its folder":      {func(dir string, d Definition) Consent { return Consent{Dirs: []string{filepath.Join(dir, d.Dir)}} }, true},
		"another folder":  {func(dir string, d Definition) Consent { return Consent{Dirs: []string{filepath.Join(dir, "other")}} }, false},
		"its harness":     {func(string, Definition) Consent { return Consent{Harnesses: []string{"claude"}} }, true},
		"another harness": {func(string, Definition) Consent { return Consent{Harnesses: []string{"codex"}} }, false},
		"every folder (v0.22.0 to v0.28.x clients)": {func(string, Definition) Consent { return Consent{All: true} }, true},
	} {
		t.Run(name, func(t *testing.T) {
			e := testEnv(t)
			d, _ := Find(Storage)
			dir, target := claudeTarget(t, e, d)
			putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": skillText(t, d.Dir) + "\nMine.\n"})
			skillstest.PutPendingRecovery(t, dir)
			doc, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, tc.consent(dir, d))
			if tc.adopted {
				if err != nil || doc.Outcomes[0].Result != "adopted" || doc.Outcomes[0].BackupPath == "" {
					t.Fatalf("doc = %+v, err = %v", doc, err)
				}
				return
			}
			if err == nil || doc.Outcomes[0].Result != "conflict" || doc.Outcomes[0].Reason != "unmanaged target" {
				t.Fatalf("doc = %+v, err = %v, want the refusal", doc, err)
			}
		})
	}
}

// The decision is the library's, inside its lock: a folder that becomes
// adoptable after anything ovdb read, here at the very call into the library,
// is still refused when nobody agreed to it. A check made before Sync, as the
// version this replaces made, would have missed it.
func TestAFolderThatAppearsAtTheLibraryCallIsStillRefused(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	if got := e.Plan(d, []RequestTarget{target})[0].State; got != StateNotInstalled {
		t.Fatalf("planned = %s", got)
	}
	appears := skillText(t, d.Dir) + "\nAppeared later.\n"
	useSync(t, func(ctx context.Context, cfg skillsync.Config, opts skillsync.Options) (skillsync.Report, error) {
		putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": appears})
		return skillsync.Sync(ctx, cfg, opts)
	})
	doc, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{})
	if err == nil || doc.Outcomes[0].Result != "conflict" || doc.Outcomes[0].Reason != "unmanaged target" {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, d.Dir, "SKILL.md")); string(got) != appears {
		t.Errorf("the folder was taken over: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, adoptedBackupDirName)); !os.IsNotExist(err) {
		t.Errorf("backup made: %v", err)
	}
}

// Install hands the library NoAdopt for every folder the request does not
// name, and not for the ones it does.
func TestInstallLeavesAdoptionToTheLibraryPerFolder(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	claudeDir := filepath.Join(e.Home, ".claude", "skills")
	codexDir := filepath.Join(e.Home, ".codex", "skills")
	targets := []RequestTarget{{Harness: "claude", SkillsDir: claudeDir}, {Harness: "codex", SkillsDir: codexDir}}
	noAdopt := map[string]bool{}
	useSync(t, func(_ context.Context, _ skillsync.Config, opts skillsync.Options) (skillsync.Report, error) {
		noAdopt[opts.Dir] = opts.NoAdopt
		return skillsync.Report{}, nil
	})
	if _, err := (Build{}).Install(context.Background(), e, d, targets, true, false, Consent{Dirs: []string{filepath.Join(codexDir, d.Dir)}}); err != nil {
		t.Fatal(err)
	}
	if !noAdopt[claudeDir] || noAdopt[codexDir] {
		t.Errorf("NoAdopt per folder = %v, want only the folder nobody agreed to", noAdopt)
	}
}

// A request that does not name a folder is told what every version before
// adoption answers: the state of an adoptable folder in the outcome is
// not_ovdb, never a state that client has no text for.
func TestAnOutcomeNeverShowsAdoptableToARequestThatDidNotAgree(t *testing.T) {
	for _, dry := range []bool{true, false} {
		e := testEnv(t)
		d, _ := Find(Storage)
		dir, target := claudeTarget(t, e, d)
		putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": skillText(t, d.Dir) + "\nMine.\n"})
		doc, _ := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, dry, false, Consent{})
		if got := doc.Outcomes[0].State; got != StateNotOVDB {
			t.Errorf("dry=%v outcome state = %s, want not_ovdb", dry, got)
		}
		doc, _ = Build{}.Install(context.Background(), e, d, []RequestTarget{target}, true, false, Consent{All: true})
		if got := doc.Outcomes[0].State; got != StateAdoptable {
			t.Errorf("dry=%v with consent: outcome state = %s, want adoptable", dry, got)
		}
	}
}

// A request that did not ask can get "unchanged" for a folder whose adoption
// an earlier request that did ask began and a crash interrupted (the library
// finishes that transaction forward): that is a result, not an error, and says
// "already up to date".
func TestUnchangedForAFolderAnEarlierRequestAdoptedIsNotAnError(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": skillText(t, d.Dir) + "\nMine.\n"})
	if _, err := (Build{}).Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{All: true}); err != nil {
		t.Fatal(err)
	}
	doc, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{})
	if err != nil || !doc.AlreadyUpToDate || doc.Outcomes[0].Result != "unchanged" || doc.Outcomes[0].State != StateInstalled {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
}

// strongo/cli-helpers#45: an adoption interrupted before the state was written
// leaves a journal every later sync refuses with a corrupt-state error. ovdb
// shows advice a person can act on: what happened, where their copy is kept,
// that the library issue exists and how to go on, not the bare internal error.
func TestAnUnrecoverableJournalIsShownWithAdvice(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": "x"})
	backup := filepath.Join(dir, adoptedBackupDirName, "20261005T120000.000000000Z", d.Dir)
	putCopy(t, backup, map[string]string{"SKILL.md": "my original"})
	stuck := fmt.Errorf("%w: recovery target %s lacks prior ownership", skillsync.ErrStateCorrupt, d.Dir)
	useSync(t, func(context.Context, skillsync.Config, skillsync.Options) (skillsync.Report, error) {
		return skillsync.Report{}, stuck
	})

	// The list shows it as its own state, with the library's reason.
	listed := Inspect(e).Skills[0].Targets[0]
	if listed.State != StateRecoveryPending || !strings.Contains(listed.StateReason, "lacks prior ownership") {
		t.Errorf("listed = %+v", listed)
	}
	for _, dry := range []bool{true, false} {
		_, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, dry, false, Consent{})
		var failure *envelope.Error
		if !errors.As(err, &failure) {
			t.Fatalf("dry=%v err = %v", dry, err)
		}
		for _, want := range []string{"interrupted", backup, "issues/45", ".cli-helpers-skills-recovery.json", "install again"} {
			if !strings.Contains(failure.Reason, want) {
				t.Errorf("dry=%v advice lacks %q: %s", dry, want, failure.Reason)
			}
		}
		if strings.HasPrefix(failure.Reason, "Couldn't write") {
			t.Errorf("advice is wrapped as a write failure: %s", failure.Reason)
		}
	}
	// Without a backup to point at, the advice still names the issue and the way on.
	if err := os.RemoveAll(filepath.Join(dir, adoptedBackupDirName)); err != nil {
		t.Fatal(err)
	}
	_, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{})
	var failure *envelope.Error
	if !errors.As(err, &failure) || !strings.Contains(failure.Reason, "issues/45") || strings.Contains(failure.Reason, adoptedBackupDirName) {
		t.Errorf("no backup: %v", err)
	}
}

// A folder that is the skill plus a stray file (a ".DS_Store") is not "another
// skill with this name": the library's reason, which names the file, goes with
// the state.
func TestAnotherFolderCarriesTheLibrarysReason(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, _ := claudeTarget(t, e, d)
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": skillText(t, d.Dir), ".DS_Store": "junk"})
	target := Inspect(e).Skills[0].Targets[0]
	if target.State != StateNotOVDB || !strings.Contains(target.StateReason, ".DS_Store") {
		t.Fatalf("target = %+v, want not_ovdb naming .DS_Store", target)
	}
	raw, _ := json.Marshal(target)
	if !strings.Contains(string(raw), `"state_reason":`) {
		t.Errorf("json = %s", raw)
	}
	// The install outcome carries the reason in its own field, not twice.
	_, target2 := claudeTarget(t, e, d)
	doc, _ := Build{}.Install(context.Background(), e, d, []RequestTarget{target2}, true, false, Consent{})
	if doc.Outcomes[0].StateReason != "" || !strings.Contains(doc.Outcomes[0].Reason, ".DS_Store") {
		t.Errorf("outcome = %+v", doc.Outcomes[0])
	}
}

// A client that does not know the new vocabulary is not sent it.
func TestDocumentForOlderClients(t *testing.T) {
	doc := Document{Skills: []Skill{{Targets: []Target{{State: StateRecoveryPending, StateReason: "r"}, {State: StateNotOVDB, StateReason: "s"}}}}}
	if target, ok := doc.RecoveryPending(); !ok || target.StateReason != "r" {
		t.Errorf("RecoveryPending = %+v, %v", target, ok)
	}
	stripped := doc.WithoutStateReasons()
	for _, target := range stripped.Skills[0].Targets {
		if target.StateReason != "" {
			t.Errorf("target still has a reason: %+v", target)
		}
	}
	if doc.Skills[0].Targets[0].StateReason != "r" {
		t.Error("WithoutStateReasons changed the document it was called on")
	}
	if _, ok := (Document{}).RecoveryPending(); ok {
		t.Error("an empty document has a pending recovery")
	}
}

// The same advice without a seam: a journal the library's own recovery refuses
// ("lacks prior ownership"), with a backup the person's copy is kept in.
func TestTheLibrarysOwnUnrecoverableJournalIsShownWithAdvice(t *testing.T) {
	e := testEnv(t)
	d, _ := Find(Storage)
	dir, target := claudeTarget(t, e, d)
	putCopy(t, filepath.Join(dir, d.Dir), map[string]string{"SKILL.md": skillText(t, d.Dir)})
	backup := filepath.Join(dir, adoptedBackupDirName, "20261005T120000.000000000Z", d.Dir)
	putCopy(t, backup, map[string]string{"SKILL.md": "my original"})
	skillstest.PutUnrecoverableRecovery(t, dir, d.Dir)

	if got := Inspect(e).Skills[0].Targets[0].State; got != StateRecoveryPending {
		t.Errorf("state = %s", got)
	}
	_, err := Build{}.Install(context.Background(), e, d, []RequestTarget{target}, false, false, Consent{All: true})
	var failure *envelope.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"lacks prior ownership", backup, "issues/45"} {
		if !strings.Contains(failure.Reason, want) {
			t.Errorf("advice lacks %q: %s", want, failure.Reason)
		}
	}
}
