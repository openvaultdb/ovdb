package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	"github.com/openvaultdb/ovdb/internal/setup/skills/skillstest"
	embedded "github.com/openvaultdb/ovdb/skills"
)

func userHomeOf(m Model) string { return m.local.Getenv("HOME") }

// ai-agent-skills#AC:ui-shows-targets-before-install in the TUI, with
// todo-demo#AC:next-actions-after-install: after Try a demo, Install TODO AI
// skill opens the consent step showing the purpose, Claude Code (found) with
// its exact directory and Codex as not found, with the cursor on Claude Code
// rather than Install skill; Not now writes nothing and returns to the
// Result; Install skill writes only the TODO skill.
func TestTodoSkillConsentAfterDemo(t *testing.T) {
	m := realModel(t, freePort(t))
	home := userHomeOf(m)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	m = send(t, m, key("enter")) // Try a demo
	m = send(t, m, key("enter")) // install
	if view := flat(m.View().Content); m.screen != ScreenResult || !strings.Contains(view, "Install TODO AI skill ovdb skills install todo-demo") {
		t.Fatalf("Result:\n%s", view)
	}

	m = send(t, m, key("s"))
	dir := filepath.Join(home, ".claude", "skills", "openvaultdb-todo-demo")
	view := flat(m.View().Content)
	for _, want := range []string{
		"Install the TODO AI skill?", "Lets your AI agent read and change your To buy and To watch lists.",
		`"add tea to my shopping list and Arrival to my watch list"`, "Install for: > [x] Claude Code", "Codex — not found", "Install skill Not now",
	} {
		if m.screen != ScreenSkills || !strings.Contains(view, want) {
			t.Errorf("consent lacks %q:\n%s", want, view)
		}
	}
	if !strings.Contains(strings.ReplaceAll(view, " ", ""), dir) {
		t.Errorf("consent lacks the exact directory %s:\n%s", dir, view)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); !os.IsNotExist(err) {
		t.Fatalf("the offer wrote files: %v", err)
	}
	noWiderThan(t, m.View().Content, 80)
	if lines := strings.Count(m.View().Content, "\n") + 1; lines > 24 {
		t.Errorf("consent is %d lines at 80×24", lines)
	}

	// Enter on the agent only unticks it; Install skill with nothing chosen
	// does nothing.
	m = send(t, m, key("enter"))
	if !strings.Contains(flat(m.View().Content), "> [ ] Claude Code") {
		t.Errorf("toggle:\n%s", flat(m.View().Content))
	}
	m = send(t, m, key("down"))
	m = send(t, m, key("enter"))
	if m.screen != ScreenSkills || m.busy != nil {
		t.Errorf("installed with no agent chosen: %s", m.screen)
	}
	m = send(t, m, key("down"))
	m = send(t, m, key("enter")) // Not now
	if m.screen != ScreenResult || !strings.Contains(flat(m.View().Content), "The TODO demo is ready") {
		t.Fatalf("Not now:\n%s", flat(m.View().Content))
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); !os.IsNotExist(err) {
		t.Fatalf("Not now wrote files: %v", err)
	}

	m = send(t, m, key("s"))
	m = send(t, m, key("down"))
	m = send(t, m, key("enter")) // Install skill
	view = flat(m.View().Content)
	if m.screen != ScreenResult || !strings.Contains(view, "Installed the TODO AI skill") || !strings.Contains(strings.ReplaceAll(view, " ", ""), dir) ||
		!strings.Contains(view, "o open the TODO app · Enter done") {
		t.Fatalf("installed:\n%s", view)
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".claude", "skills"))
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != ".cli-helpers-skills-sync.json,openvaultdb-todo-demo" {
		t.Errorf("skills folder = %v", names)
	}
}

// Home → AI agent skills lists both skills and where they are installed;
// Enter opens a skill's consent step, Esc goes back to the list, then Home.
func TestSkillsScreenFromHome(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	for range 4 { // Browse data is disabled without databases
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	view := flat(m.View().Content)
	for _, want := range []string{"AI agent skills", "> OpenVaultDB skill", "TODO AI skill", "Not installed"} {
		if m.screen != ScreenSkills || !strings.Contains(view, want) {
			t.Errorf("skills lacks %q:\n%s", want, view)
		}
	}
	m = send(t, m, key("down"))
	m = send(t, m, key("enter"))
	if view := flat(m.View().Content); !strings.Contains(view, "Install the TODO AI skill?") || !strings.Contains(view, "Claude Code — not found") {
		t.Errorf("consent:\n%s", view)
	}
	m = send(t, m, key("esc"))
	if m.screen != ScreenSkills || m.skills.consent != nil {
		t.Errorf("esc from consent = %s", m.screen)
	}
	m = send(t, m, key("esc"))
	if m.screen != ScreenHome {
		t.Errorf("esc from list = %s", m.screen)
	}
	for _, size := range [][2]int{{60, 20}, {80, 24}} {
		small := testModel(t, size[0], size[1])
		small = send(t, small, key("down"))
		small.skills = m.skills
		small.screen = ScreenSkills
		small.skills.openConsent("todo-demo")
		noWiderThan(t, small.View().Content, size[0])
		if lines := strings.Count(small.View().Content, "\n") + 1; lines > size[1] {
			t.Errorf("%v: %d lines:\n%s", size, lines, stripANSI(small.View().Content))
		}
	}
}

// Review F7 in the TUI: a skill the person changed since install is offered
// unticked, says installing replaces the changes, and is replaced only when
// ticked; someone else's folder of that name can't be chosen.
func TestConsentForChangedSkill(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	home := userHomeOf(m)
	claude := filepath.Join(home, ".claude", "skills", "openvaultdb-todo-demo")
	codex := filepath.Join(home, ".codex", "skills", "openvaultdb-todo-demo")
	for _, dir := range []string{claude, codex} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// An OVDB install of the Claude copy, then edited.
	env, _ := skills.EnvFrom(m.local.Getenv)
	d, _ := skills.Find(skills.Todo)
	if err := os.RemoveAll(claude); err != nil {
		t.Fatal(err)
	}
	if _, err := (skills.Build{}).Install(t.Context(), env, d, []skills.RequestTarget{{Harness: "claude", SkillsDir: filepath.Dir(claude)}}, false, false, skills.Consent{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "SKILL.md"), []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(skills.Todo))
	view := flat(m.View().Content)
	for _, want := range []string{"> [ ] Claude Code", "changed since install — installing replaces your changes", "Codex — another skill with this name"} {
		if !strings.Contains(view, want) {
			t.Errorf("consent lacks %q:\n%s", want, view)
		}
	}
	if items := m.skills.consentItems(); len(items) != 3 {
		t.Errorf("items = %v", items)
	}
	if harnesses, replace, _ := m.skills.chosen(); len(harnesses) != 0 || replace {
		t.Errorf("chosen by default = %v %v", harnesses, replace)
	}
	m = send(t, m, key("space"))
	if harnesses, replace, _ := m.skills.chosen(); len(harnesses) != 1 || !replace {
		t.Errorf("chosen = %v %v", harnesses, replace)
	}
}

// skillsync v0.26.0 takes over a folder that is already the bundled skill. In
// the TUI that copy is offered unticked (like one the person changed), the
// consent step says installing takes it over and keeps a backup, the list does
// not count it as installed, and ticking it adopts it: the Result says where
// the copy that was there is kept.
func TestConsentForAnAdoptableSkill(t *testing.T) {
	m := realModel(t, freePort(t))
	home := userHomeOf(m)
	bundled, err := fs.ReadFile(embedded.FS, "openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude", "skills", "openvaultdb")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), bundled, 0o600); err != nil {
		t.Fatal(err)
	}

	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(""))
	if view := flat(m.View().Content); !strings.Contains(view, "Not installed") || strings.Contains(view, "Installed for") {
		t.Errorf("list counts a copy OVDB doesn't manage as installed:\n%s", view)
	}
	m = drain(t, m, m.loadSkillsCmd(skills.Storage))
	view := flat(m.View().Content)
	for _, want := range []string{"> [ ] Claude Code", "already here — installing takes it over and keeps a backup of your copy"} {
		if !strings.Contains(view, want) {
			t.Errorf("consent lacks %q:\n%s", want, view)
		}
	}
	if harnesses, replace, _ := m.skills.chosen(); len(harnesses) != 0 || replace {
		t.Errorf("chosen by default = %v %v", harnesses, replace)
	}
	m = send(t, m, key("space"))
	if harnesses, replace, _ := m.skills.chosen(); len(harnesses) != 1 || replace {
		t.Errorf("chosen = %v %v: taking over a copy is not replacing a changed one", harnesses, replace)
	}
	m = send(t, m, key("down"))
	m = send(t, m, key("enter")) // Install skill
	view = flat(m.View().Content)
	if m.screen != ScreenResult || !strings.Contains(view, "Installed the OpenVaultDB skill") ||
		!strings.Contains(view, "(already there, now managed by OVDB; your copy is kept at") {
		t.Fatalf("installed:\n%s", view)
	}
	if !strings.Contains(strings.ReplaceAll(view, " ", ""), filepath.Join(home, ".claude", "skills", ".cli-helpers-skills-adopted-backup")) {
		t.Errorf("the Result doesn't name the backup folder:\n%s", view)
	}
}

// A failed install that changed some agents says so on the problem screen: the
// reason carries it (the TUI shows the envelope's message and reason).
func TestFailedSkillInstallShowsWhatChanged(t *testing.T) {
	t.Parallel()
	m := testModel(t, 100, 30)
	failure := envelope.New(envelope.AlreadyExists, "Couldn't install the OpenVaultDB skill").
		WithReason("/h/.cursor/skills/openvaultdb already exists and wasn't installed by OVDB, so it was left as it is. Before it stopped, Kiro changed: /h/.kiro/skills/openvaultdb (already there, now managed by OVDB). Your copy is kept at /h/.kiro/skills/.cli-helpers-skills-adopted-backup/x/openvaultdb")
	next, _ := m.updateSkillsMsg(skillInstalledMsg{err: failure})
	view := strings.ReplaceAll(flat(next.View().Content), " ", "")
	if next.screen != ScreenProblem || !strings.Contains(view, "Beforeitstopped,Kiro") || !strings.Contains(view, "Yourcopyiskeptat/h/.kiro/skills/.cli-helpers-skills-adopted-backup/x/openvaultdb") {
		t.Errorf("problem screen:\n%s", flat(next.View().Content))
	}
}

func putBundledCopy(t *testing.T, dir string) {
	t.Helper()
	bundled, err := fs.ReadFile(embedded.FS, "openvaultdb/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), bundled, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The consent is bound to what the screen showed: the folders already there
// that the person ticked (adoptDirs of chosen), not a flag derived from a plan
// made when Install is pressed.
func TestConsentNamesTheFoldersTheScreenShowedAsAlreadyThere(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	home := userHomeOf(m)
	for _, harness := range []string{".claude", ".codex"} {
		putBundledCopy(t, filepath.Join(home, harness, "skills", "openvaultdb"))
	}
	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(skills.Storage))
	if _, _, dirs := m.skills.chosen(); len(dirs) != 0 {
		t.Errorf("adoptDirs by default = %v: a folder already there is offered unticked", dirs)
	}
	m = send(t, m, key("space")) // Claude Code
	_, _, dirs := m.skills.chosen()
	claude := filepath.Join(skills.Canonical(home), ".claude", "skills", "openvaultdb")
	if len(dirs) != 1 || dirs[0] != claude {
		t.Errorf("adoptDirs = %v, want only the ticked %s", dirs, claude)
	}
}

// A folder that becomes adoptable after the consent screen was drawn is not
// taken over by an Install that ticked it as "not installed": the TUI sends the
// folders it showed, so the library refuses the new one, and the one the person
// did tick as already there is adopted.
func TestAFolderThatBecameAdoptableAfterTheScreenIsNotTakenOver(t *testing.T) {
	m := realModel(t, freePort(t))
	home := userHomeOf(m)
	claude := filepath.Join(home, ".claude", "skills", "openvaultdb")
	codex := filepath.Join(home, ".codex", "skills", "openvaultdb")
	putBundledCopy(t, claude)
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(skills.Storage)) // the screen: Claude Code already there, Codex not installed
	m = send(t, m, key("space"))                     // tick Claude Code (Codex is ticked already)
	putBundledCopy(t, codex)                         // after the screen was drawn
	m = send(t, m, key("down"))
	m = send(t, m, key("down"))
	m = send(t, m, key("enter")) // Install skill
	if m.screen != ScreenProblem {
		t.Fatalf("screen = %s, want the refusal of the folder nobody was shown", m.screen)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", ".cli-helpers-skills-adopted-backup")); !os.IsNotExist(err) {
		t.Errorf("the folder that appeared after the screen was taken over: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", ".cli-helpers-skills-adopted-backup")); err != nil {
		t.Errorf("the folder the person ticked was not adopted: %v", err)
	}
}

// An interrupted install is shown on the list and on the consent step as what
// it is, with a note, and installing it is possible (the install finishes the
// recovery first).
func TestAnInterruptedInstallIsShownAsSuch(t *testing.T) {
	t.Parallel()
	m := testModel(t, 100, 30)
	home := userHomeOf(m)
	dir := filepath.Join(home, ".claude", "skills")
	putBundledCopy(t, filepath.Join(dir, "openvaultdb"))
	skillstest.PutPendingRecovery(t, dir)
	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(""))
	if view := flat(m.View().Content); !strings.Contains(view, "Interrupted install, installing finishes it or says what to do: Claude Code") {
		t.Errorf("list:\n%s", view)
	}
	m = drain(t, m, m.loadSkillsCmd(skills.Storage))
	view := flat(m.View().Content)
	if !strings.Contains(view, "> [x] Claude Code") || !strings.Contains(view, "an earlier install here was interrupted — installing finishes it, or says what to do") {
		t.Errorf("consent:\n%s", view)
	}
}

// A copy of this skill with a stray file is shown with the library's reason,
// which names the file, not only as "another skill with this name".
func TestAnotherFolderIsShownWithTheLibrarysReason(t *testing.T) {
	t.Parallel()
	m := testModel(t, 120, 30)
	dir := filepath.Join(userHomeOf(m), ".codex", "skills", "openvaultdb")
	putBundledCopy(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(skills.Storage))
	if view := flat(m.View().Content); !strings.Contains(view, "Codex — another skill with this name (unmanaged target") || !strings.Contains(view, ".DS_Store") {
		t.Errorf("consent:\n%s", view)
	}
}

// The advice for an interruption the library cannot recover from (#45) is
// shown whole on the problem screen.
func TestAnUnrecoverableInstallShowsItsAdvice(t *testing.T) {
	t.Parallel()
	m := testModel(t, 120, 40)
	failure := envelope.New(envelope.StorageUnavailable, "Couldn't install the OpenVaultDB skill").
		WithReason("An earlier install in /h/.claude/skills was interrupted, and OVDB can't finish or undo it (x). Your own copy of the folder is kept in /h/.claude/skills/.cli-helpers-skills-adopted-backup/t/openvaultdb. This is a known problem of the skills library (https://github.com/strongo/cli-helpers/issues/45).")
	next, _ := m.updateSkillsMsg(skillInstalledMsg{err: failure})
	view := strings.ReplaceAll(flat(next.View().Content), " ", "")
	for _, want := range []string{"wasinterrupted", "Yourowncopyofthefolderiskeptin/h/.claude/skills/.cli-helpers-skills-adopted-backup/t/openvaultdb", "cli-helpers/issues/45"} {
		if !strings.Contains(view, want) {
			t.Errorf("problem screen lacks %q:\n%s", want, flat(next.View().Content))
		}
	}
}

// OVDB's own skill whose record cannot be read is not "another skill with this
// name": the consent step says what is wrong, with the library's reason, and it
// cannot be chosen.
func TestAnUnusableRecordIsShownAsSuch(t *testing.T) {
	t.Parallel()
	m := testModel(t, 120, 30)
	dir := filepath.Join(userHomeOf(m), ".claude", "skills")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	putBundledCopy(t, filepath.Join(dir, "openvaultdb"))
	if err := os.WriteFile(filepath.Join(dir, ".cli-helpers-skills-sync.json"), []byte(`{"schema":2,"plug`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.screen = ScreenSkills
	m = drain(t, m, m.loadSkillsCmd(skills.Storage))
	view := flat(m.View().Content)
	if !strings.Contains(view, "Claude Code — its record can't be read (skills sync state is corrupt") || strings.Contains(view, "another skill with this name") || strings.Contains(view, "[x] Claude Code") {
		t.Errorf("consent:\n%s", view)
	}
}
