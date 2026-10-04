package skills

import (
	"context"
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
	dry := mustInstall(t, e, InstallRequest{Skill: Storage, Harnesses: []string{"claude"}, DryRun: true})
	if o := dry.Outcomes[0]; o.Result != "adopted" || o.BackupPath != "" || o.Reason != "" || dry.AlreadyUpToDate || !dry.DryRun {
		t.Fatalf("dry run = %+v", dry)
	}
	if after := tree(t, skillsDir); len(after) != len(before) || after["openvaultdb/SKILL.md"] != original {
		t.Errorf("the dry run wrote: %v", after)
	}

	doc := mustInstall(t, e, InstallRequest{Skill: Storage, Harnesses: []string{"claude"}})
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

	again := mustInstall(t, e, InstallRequest{Skill: Storage, Harnesses: []string{"claude"}})
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
	doc := mustInstall(t, e, InstallRequest{Skill: Todo, Harnesses: []string{"claude"}})
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
			d, targets, _ := e.Resolve(InstallRequest{Skill: Storage, Harnesses: []string{"claude"}})
			doc, err := Build{}.Install(context.Background(), e, d, targets, false, false)
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
