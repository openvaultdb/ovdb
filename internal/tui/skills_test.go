package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/setup/skills"
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
	if view := flat(m.View().Content); m.screen != ScreenResult || !strings.Contains(view, "Install TODO AI skill (ask the person first) ovdb skills install todo-demo") {
		t.Fatalf("Result:\n%s", view)
	}

	m = send(t, m, key("s"))
	dir := filepath.Join(home, ".claude", "skills", "openvaultdb-todo-demo")
	view := flat(m.View().Content)
	for _, want := range []string{
		"Install the TODO AI skill?", "Lets your AI agent read and change your To buy and To watch lists.",
		`"add bananas and coffee to my shopping list"`, "Install for: > [x] Claude Code", "Codex — not found", "Install skill Not now",
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
	if _, err := (skills.Build{}).Install(t.Context(), env, d, []skills.RequestTarget{{Harness: "claude", SkillsDir: filepath.Dir(claude)}}, false, false); err != nil {
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
	if harnesses, replace := m.skills.chosen(); len(harnesses) != 0 || replace {
		t.Errorf("chosen by default = %v %v", harnesses, replace)
	}
	m = send(t, m, key("space"))
	if harnesses, replace := m.skills.chosen(); len(harnesses) != 1 || !replace {
		t.Errorf("chosen = %v %v", harnesses, replace)
	}
}
