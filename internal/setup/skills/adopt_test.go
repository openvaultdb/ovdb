package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// putCopy writes files (relative path to text) into skill folder dir the way
// a person who put a copy there by hand would have.
func putCopy(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// skillsync v0.26.0 takes over an unmanaged folder that is already the
// bundled skill (its SKILL.md names it, and nothing in it is foreign) instead
// of refusing it. The target says so before anything is written
// (adoptable), a dry run reports it as adopted with no backup yet and writes
// nothing, the install keeps the person's copy in a backup that the outcome
// names, and afterwards the skill is installed and unchanged.
func TestAdoptsAnUnmanagedCopyOfTheSkill(t *testing.T) {
	e := testEnv(t)
	skillsDir := filepath.Join(e.Home, ".claude", "skills")
	// Differing bytes on a shared path are fine: the copy is backed up first.
	original := skillText(t, "openvaultdb") + "\nMy own note.\n"
	putCopy(t, filepath.Join(skillsDir, "openvaultdb"), map[string]string{"SKILL.md": original})

	target := Inspect(e).Skills[0].Targets[0]
	if target.State != StateAdoptable || target.Installed || !target.Detected {
		t.Fatalf("before = %+v, want adoptable and not installed", target)
	}
	if status := Status(e); len(status[0].InstalledFor) != 0 {
		t.Errorf("status counts a copy OVDB doesn't manage as installed: %+v", status[0])
	}

	before := tree(t, skillsDir)
	dry := mustInstall(t, e, InstallRequest{Skill: Storage, Harnesses: []string{"claude"}, DryRun: true, Adopt: true})
	if o := dry.Outcomes[0]; o.Result != "adopted" || o.BackupPath != "" || o.Reason != "" || dry.AlreadyUpToDate || !dry.DryRun {
		t.Fatalf("dry run = %+v", dry)
	}
	if after := tree(t, skillsDir); len(after) != len(before) || after["openvaultdb/SKILL.md"] != original {
		t.Errorf("the dry run wrote: %v", after)
	}

	doc := mustInstall(t, e, InstallRequest{Skill: Storage, Harnesses: []string{"claude"}, Adopt: true})
	outcome := doc.Outcomes[0]
	if outcome.Result != "adopted" || doc.AlreadyUpToDate || outcome.State != StateInstalled || !outcome.Installed {
		t.Fatalf("install = %+v", doc)
	}
	if !strings.HasPrefix(outcome.BackupPath, skillsDir+string(filepath.Separator)) {
		t.Fatalf("backup path %q is not under %s", outcome.BackupPath, skillsDir)
	}
	if kept, err := os.ReadFile(filepath.Join(outcome.BackupPath, "SKILL.md")); err != nil || string(kept) != original {
		t.Errorf("the backup doesn't hold the person's copy: %q, %v", kept, err)
	}
	if now := tree(t, filepath.Join(skillsDir, "openvaultdb"))["SKILL.md"]; now != skillText(t, "openvaultdb") {
		t.Errorf("the skill was not put in place: %q", now)
	}
	if len(doc.Next) == 0 {
		t.Error("an adoption has no next steps")
	}

	again := mustInstall(t, e, InstallRequest{Skill: Storage, Harnesses: []string{"claude"}, Adopt: true})
	if !again.AlreadyUpToDate || again.Outcomes[0].Result != "unchanged" || again.Outcomes[0].BackupPath != "" {
		t.Errorf("second install = %+v", again)
	}
	if target := Inspect(e).Skills[0].Targets[0]; target.State != StateInstalled || !target.Installed {
		t.Errorf("after = %+v", target)
	}
}

// The same adoption, but of a copy that is exactly the bundled skill, written
// to the JSON body the API and --json share: the outcome carries
// backup_path, and only for an adoption.
func TestAdoptedOutcomeJSONNamesTheBackup(t *testing.T) {
	e := testEnv(t)
	putCopy(t, filepath.Join(e.Home, ".claude", "skills", "openvaultdb-todo-demo"), map[string]string{"SKILL.md": skillText(t, "openvaultdb-todo-demo")})
	doc := mustInstall(t, e, InstallRequest{Skill: Todo, Harnesses: []string{"claude"}, Adopt: true})
	body := string(envelope.Marshal(doc))
	if !strings.Contains(body, `"result":"adopted"`) || !strings.Contains(body, `"backup_path":"`) {
		t.Errorf("adopted outcome JSON = %s", body)
	}
	added := string(envelope.Marshal(mustInstall(t, e, InstallRequest{Skill: Todo, Harnesses: []string{"codex"}})))
	if strings.Contains(added, "backup_path") {
		t.Errorf("an added outcome names a backup: %s", added)
	}
}

// Adoption needs proof the folder is this skill: a SKILL.md that names it and
// nothing in the folder the skill doesn't ship. Anything else stays a
// conflict, is left exactly as it is, and the target is not offered to take
// over.
func TestDoesNotAdoptWhatIsNotTheSkill(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"a file the skill doesn't ship": {"SKILL.md": "---\nname: openvaultdb\n---\n", "notes.txt": "mine"},
		"another skill's name":          {"SKILL.md": "---\nname: specscore\n---\n"},
		"no front matter":               {"SKILL.md": "mine"},
		"no SKILL.md":                   {"README.md": "mine"},
	} {
		t.Run(name, func(t *testing.T) {
			e := testEnv(t)
			dir := filepath.Join(e.Home, ".claude", "skills", "openvaultdb")
			putCopy(t, dir, files)
			before := tree(t, dir)

			if state := Inspect(e).Skills[0].Targets[0].State; state != StateNotOVDB {
				t.Errorf("state = %s, want %s", state, StateNotOVDB)
			}
			d, targets, _ := e.Resolve(InstallRequest{Skill: Storage, Harnesses: []string{"claude"}, Adopt: true})
			doc, err := Build{}.Install(context.Background(), e, d, targets, false, false, true)
			problem := envelope.As(err)
			if problem == nil || problem.Code != envelope.AlreadyExists || !strings.Contains(problem.Reason, "wasn't installed by OVDB") {
				t.Fatalf("install = %v", err)
			}
			if doc.Outcomes[0].Result != "conflict" || doc.Outcomes[0].BackupPath != "" {
				t.Errorf("outcome = %+v", doc.Outcomes[0])
			}
			after := tree(t, dir)
			if len(after) != len(before) {
				t.Errorf("files changed: %v -> %v", before, after)
			}
			for path, text := range before {
				if after[path] != text {
					t.Errorf("%s changed", path)
				}
			}
		})
	}
}

// A request that does not ask for adoption (every client built before it
// existed) gets what v0.21.0 answered for such a folder: refused as
// already_exists, outcome conflict with skillsync's old reason, state not_ovdb
// (never "adoptable"), nothing written, no backup. Dry run and real alike.
func TestWithoutAdoptTheFolderIsRefusedAsBefore(t *testing.T) {
	e := testEnv(t)
	skillsDir := filepath.Join(e.Home, ".claude", "skills")
	putCopy(t, filepath.Join(skillsDir, "openvaultdb"), map[string]string{"SKILL.md": skillText(t, "openvaultdb")})
	before := tree(t, skillsDir)
	d, targets, _ := e.Resolve(InstallRequest{Skill: Storage, Harnesses: []string{"claude"}})
	for _, dryRun := range []bool{true, false} {
		doc, err := Build{}.Install(context.Background(), e, d, targets, dryRun, false, false)
		problem := envelope.As(err)
		if problem == nil || problem.Code != envelope.AlreadyExists || !strings.Contains(problem.Reason, "wasn't installed by OVDB") || problem.Targets != nil {
			t.Fatalf("dryRun=%v: err = %+v", dryRun, err)
		}
		if o := doc.Outcomes[0]; o.Result != "conflict" || o.Reason != "unmanaged target" || o.State != StateNotOVDB || o.BackupPath != "" {
			t.Errorf("dryRun=%v: outcome = %+v", dryRun, o)
		}
	}
	after := tree(t, skillsDir)
	if len(after) != len(before) || after["openvaultdb/SKILL.md"] != before["openvaultdb/SKILL.md"] {
		t.Errorf("the folder was touched: %v", after)
	}

	// The document a client that did not say it knows "adoptable" reads never has it.
	inspected := Inspect(e)
	if inspected.Skills[0].Targets[0].State != StateAdoptable {
		t.Fatalf("Inspect = %+v", inspected.Skills[0].Targets[0])
	}
	if state := inspected.WithoutAdoptable().Skills[0].Targets[0].State; state != StateNotOVDB {
		t.Errorf("WithoutAdoptable = %s", state)
	}
}

// An install that fails for one target says what it did to the others, for
// every kind of change and not only adoption (it said only the failure before
// this, since v0.21.0): in the reason, in words, and in the error's targets;
// a failure that changed nothing is exactly the document it always was.
func TestFailedInstallNamesTheTargetsThatChanged(t *testing.T) {
	e := testEnv(t)
	putCopy(t, filepath.Join(e.Home, ".codex", "skills", "openvaultdb"), map[string]string{"SKILL.md": "mine"})
	d, targets, _ := e.Resolve(InstallRequest{Skill: Storage, Harnesses: []string{"claude", "codex"}})

	_, err := Build{}.Install(context.Background(), e, d, targets, true, false, true)
	plan := envelope.As(err)
	claude := filepath.Join(e.Home, ".claude", "skills", "openvaultdb")
	if plan == nil || !strings.Contains(plan.Reason, "Claude Code would change: "+claude+" (added).") || plan.Targets == nil {
		t.Fatalf("dry run = %+v", err)
	}
	if _, statErr := os.Stat(claude); !os.IsNotExist(statErr) {
		t.Errorf("a dry run wrote %s", claude)
	}

	_, err = Build{}.Install(context.Background(), e, d, targets, false, false, true)
	failure := envelope.As(err)
	if failure == nil || failure.Code != envelope.AlreadyExists || !strings.Contains(failure.Reason, "Before it stopped, Claude Code changed: "+claude+" (added).") {
		t.Fatalf("install = %+v", err)
	}
	var outcomes []Outcome
	if json.Unmarshal(failure.Targets, &outcomes) != nil || len(outcomes) != 2 || outcomes[0].Result != "added" || outcomes[1].Result != "conflict" {
		t.Errorf("targets = %s", failure.Targets)
	}
	if _, statErr := os.Stat(filepath.Join(claude, "SKILL.md")); statErr != nil {
		t.Errorf("the added target is not there: %v", statErr)
	}

	// An update, too: the next run finds Claude Code installed, so it changes nothing.
	again, err := Build{}.Install(context.Background(), e, d, targets, false, false, true)
	if p := envelope.As(err); p == nil || p.Targets != nil || strings.Contains(p.Reason, "changed") || again.Outcomes[0].Result != "unchanged" {
		t.Errorf("a failure that changed nothing = %+v", err)
	}
}
