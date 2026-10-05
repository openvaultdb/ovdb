package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	"github.com/openvaultdb/ovdb/internal/setup/skills/skillstest"
)

func putSkillFolder(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// `skills list` says an interrupted install is one, and a copy of the skill
// with a stray file is another's folder with the library's reason (which
// names the file), never "not installed", in text and in --json.
func TestSkillsListSaysWhatIsWrongWithAFolder(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	bundled := bundledSkill(t, "openvaultdb")
	claudeSkills := filepath.Join(e.userHome(), ".claude", "skills")
	putSkillFolder(t, filepath.Join(claudeSkills, "openvaultdb"), map[string]string{"SKILL.md": bundled})
	skillstest.PutPendingRecovery(t, claudeSkills)
	putSkillFolder(t, filepath.Join(e.userHome(), ".codex", "skills", "openvaultdb"), map[string]string{"SKILL.md": bundled, ".DS_Store": "x"})

	list := e.ok("skills", "list")
	for _, want := range []string{"interrupted install", "another skill with this name", "unmanaged target", ".DS_Store"} {
		if !strings.Contains(list.stdout, want) {
			t.Errorf("list lacks %q:\n%s", want, list.stdout)
		}
	}
	// The folder column starts in the same place whatever the state says.
	offsets := map[int]bool{}
	for _, line := range strings.Split(list.stdout, "\n") {
		if i := strings.Index(line, e.userHome()); i >= 0 {
			offsets[i] = true
		}
	}
	if len(offsets) != 1 {
		t.Errorf("the folder column is not aligned (offsets %v):\n%s", offsets, list.stdout)
	}
	var document skills.Document
	if err := json.Unmarshal([]byte(e.ok("skills", "list", "--json").stdout), &document); err != nil {
		t.Fatal(err)
	}
	got := map[string]skills.Target{}
	for _, target := range document.Skills[0].Targets {
		got[target.Harness] = target
	}
	if claude := got["claude"]; claude.State != skills.StateRecoveryPending || claude.Installed || claude.StateReason == "" {
		t.Errorf("claude = %+v", claude)
	}
	if codex := got["codex"]; codex.State != skills.StateNotOVDB || !strings.Contains(codex.StateReason, ".DS_Store") {
		t.Errorf("codex = %+v", codex)
	}
}

// With a recovery journal pending, a dry run says so and what to do (the
// library cannot plan then), and the real install finishes the recovery first
// and refuses the folder nobody agreed to; asked again, it is adoptable and the
// install adopts it.
func TestSkillInstallWithAnInterruptedInstallPending(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	bundled := bundledSkill(t, "openvaultdb")
	claudeSkills := filepath.Join(e.userHome(), ".claude", "skills")
	folder := filepath.Join(claudeSkills, "openvaultdb")
	putSkillFolder(t, folder, map[string]string{"SKILL.md": bundled + "\nMine.\n"})
	skillstest.PutPendingRecovery(t, claudeSkills)

	dry := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--dry-run", "--json")
	problem := decodeError(t, dry, envelope.StorageUnavailable)
	if !strings.Contains(problem.Reason, "interrupted") || !strings.Contains(problem.Reason, "without --dry-run") {
		t.Errorf("dry run = %+v", problem)
	}
	if !skillstest.RecoveryPending(claudeSkills) {
		t.Fatal("a dry run recovered the journal")
	}

	refused := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--yes", "--json")
	_ = decodeError(t, refused, envelope.AlreadyExists)
	if skillstest.RecoveryPending(claudeSkills) {
		t.Error("the install did not finish the recovery")
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "SKILL.md")); string(got) != bundled+"\nMine.\n" {
		t.Errorf("folder changed: %q", got)
	}

	adopted := e.ok("skills", "install", "openvaultdb", "--harness", "claude", "--yes", "--json")
	var document skills.InstallDocument
	if err := json.Unmarshal([]byte(adopted.stdout), &document); err != nil || document.Outcomes[0].Result != "adopted" {
		t.Errorf("second install = %s, %v", adopted.stdout, err)
	}
}

// strongo/cli-helpers#45: a journal the library's recovery refuses leaves
// every install failing. The command shows advice: what happened, where the
// copy is kept, the library issue and the way on, not the internal error alone.
func TestSkillInstallShowsAdviceForAnUnrecoverableInterruptedInstall(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	bundled := bundledSkill(t, "openvaultdb")
	claudeSkills := filepath.Join(e.userHome(), ".claude", "skills")
	putSkillFolder(t, filepath.Join(claudeSkills, "openvaultdb"), map[string]string{"SKILL.md": bundled})
	backup := filepath.Join(claudeSkills, ".cli-helpers-skills-adopted-backup", "20261005T120000.000000000Z", "openvaultdb")
	putSkillFolder(t, backup, map[string]string{"SKILL.md": "my original"})
	skillstest.PutUnrecoverableRecovery(t, claudeSkills, "openvaultdb")

	for _, args := range [][]string{
		{"skills", "install", "openvaultdb", "--harness", "claude", "--yes", "--json"},
		{"skills", "install", "openvaultdb", "--harness", "claude", "--yes"},
	} {
		r := e.run(args...)
		out := r.stdout + r.stderr
		if problem := envelope.Decode([]byte(r.stdout)); problem != nil {
			// JSON escapes a Windows path's backslashes: compare the decoded words.
			out = problem.Message + " " + problem.Reason
		}
		if r.code != 1 {
			t.Fatalf("%v = %+v", args, r)
		}
		flat := strings.Join(strings.Fields(out), " ")
		for _, want := range []string{"lacks prior ownership", "interrupted", "issues/45", ".cli-helpers-skills-recovery.json"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%v lacks %q:\n%s", args, want, out)
			}
		}
		if !strings.Contains(strings.ReplaceAll(flat, " ", ""), backup) {
			t.Errorf("%v does not say where the copy is kept (%s):\n%s", args, backup, out)
		}
	}
}

// A record the skills library cannot use is not an interrupted install: the
// list and the install say what it is and what to do, with the library's own
// reason, and nothing about a journal that is not there.
func TestSkillsWithAnUnreadableRecordAreNotCalledInterrupted(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	if r := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); r.code != 0 {
		t.Fatalf("install: %+v", r)
	}
	claudeSkills := filepath.Join(e.userHome(), ".claude", "skills")
	if err := os.WriteFile(filepath.Join(claudeSkills, ".cli-helpers-skills-sync.json"), []byte(`{"schema":2,"plug`), 0o600); err != nil {
		t.Fatal(err)
	}
	list := e.ok("skills", "list")
	if strings.Contains(list.stdout, "interrupted") || !strings.Contains(list.stdout, "corrupt") {
		t.Errorf("list:\n%s", list.stdout)
	}
	r := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--yes")
	flat := strings.Join(strings.Fields(r.stdout+r.stderr), " ")
	if r.code != 1 || strings.Contains(flat, "interrupted") || strings.Contains(flat, "issues/45") ||
		!strings.Contains(flat, ".cli-helpers-skills-sync.json") || !strings.Contains(flat, "Another tool") {
		t.Errorf("install = %+v", r)
	}
}
