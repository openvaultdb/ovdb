package cli_test

// The journey regression gate (configuration-parity#REQ:journey-a-terminal,
// REQ:journey-c-agent, REQ:journey-d-todo-demo): Journeys A, C and D whole,
// telemetry included. Journey D's browser half — the skill
// consent step in the web console and the agent's change appearing in the
// open app — is web/e2e/todo.spec.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
	"github.com/openvaultdb/ovdb/internal/tui"
)

// drive feeds one key to the TUI and runs every command it returns, as a
// bubbletea Program would.
func drive(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	pending := []tea.Cmd{func() tea.Msg { return msg }}
	for len(pending) > 0 {
		cmd := pending[0]
		pending = pending[1:]
		if cmd == nil {
			continue
		}
		result := cmd()
		switch result := result.(type) {
		case nil:
			continue
		case tea.BatchMsg:
			pending = append(pending, result...)
			continue
		case tea.QuitMsg:
			return m
		}
		var next tea.Cmd
		m, next = m.Update(result)
		pending = append(pending, next)
	}
	return m
}

func screenText(m tea.Model) string {
	return strings.Join(strings.Fields(regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]").ReplaceAllString(m.View().Content, "")), " ")
}

// Journey A: `ovdb` → Create a database → inGitDB, suggested location, name
// notes → Use in this project → answer the telemetry prompt → quit; then
// `ovdb add` and `ovdb list` work in that project without --db
// (AC:journey-a-passes).
func TestJourneyATerminal(t *testing.T) {
	e := previewEnv(t)
	project := e.in(filepath.Join(t.TempDir(), "shop"))
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := e.in(filepath.Join(project, "src"))
	lookup := dbcontext.Find(src)
	local := &client.Local{
		Dirs: e.dirs, Version: testVersion, Port: e.port(),
		Getenv: func(key string) string { return e.vars[key] },
		Where:     dbcontext.Request{Dirs: lookup.Dirs, Root: lookup.Root},
		Telemetry: e.app.TUIRecorder(e.dirs.Home),
		Command: func(port int) *exec.Cmd {
			command := exec.Command(os.Args[0], "server", "run", "--port", strconv.Itoa(port))
			command.Env = append(append(os.Environ(), childEnv+"=1"), e.dirs.Env()...)
			return command
		},
	}
	var m tea.Model = tui.New(context.Background(), local, func(string) error { return nil }, 80, 24)
	m = drive(t, m, m.Init()())
	press := func(keys ...string) {
		for _, k := range keys {
			m = drive(t, m, tea.KeyPressMsg{Text: k})
		}
	}
	if view := screenText(m); !strings.Contains(view, "What would you like to do?") || !strings.Contains(view, "> Try a demo") {
		t.Fatalf("Home:\n%s", view)
	}
	press("down", "enter", "enter") // Create a database → inGitDB
	press("n", "o", "t", "e", "s")
	// A long location wraps, so compare without whitespace.
	if view := strings.ReplaceAll(screenText(m), " ", ""); !strings.Contains(view, filepath.Join(e.dirs.Data, "notes")) {
		t.Fatalf("suggested location missing:\n%s", view)
	}
	press("enter", "enter")
	if view := screenText(m); !strings.Contains(view, "Created database notes") || !strings.Contains(view, "u use in project") ||
		!strings.Contains(view, "Help improve OpenVaultDB?") || !strings.Contains(view, "t Turn on · n No thanks · w What's collected?") {
		t.Fatalf("Result:\n%s", view)
	}
	press("u")
	if view := screenText(m); !strings.Contains(view, "Now using notes for this project ("+project+")") || !strings.Contains(view, "Help improve OpenVaultDB?") {
		t.Fatalf("Use in this project:\n%s", view)
	}
	press("n") // the telemetry prompt: No thanks
	if view := screenText(m); strings.Contains(view, "Help improve OpenVaultDB?") || !strings.Contains(view, "usage statistics stay off") {
		t.Fatalf("after No thanks:\n%s", view)
	}
	press("enter")
	if view := screenText(m); !strings.Contains(view, "1 database · using notes (this project) · OVDB server running at") {
		t.Errorf("Home summary:\n%s", view)
	}
	press("q")

	// The server was running, so the deciding channel is the instance
	// secret's, cli (review F4), not what the TUI would claim.
	if status := e.telemetryStatus().Telemetry; status.State != "disabled" || status.Channel != "cli" || status.HasInstallID {
		t.Errorf("telemetry after the prompt = %+v", status)
	}
	if r := e.ok("add", "/items", `{"title":"Hello"}`); !strings.HasPrefix(r.stdout, "notes: added /items/") {
		t.Errorf("add = %q", r.stdout)
	}
	if r := e.ok("list", "/items"); !strings.HasPrefix(r.stdout, "notes:/items\n") || !strings.Contains(r.stdout, `{"title":"Hello"}`) {
		t.Errorf("list = %q", r.stdout)
	}
}

// Journey C: an agent without a skill, stdin closed and no terminal, reads
// the status and its five options, creates a database, selects it for the
// project and round-trips a record with absolute paths; no command waits for
// input and telemetry stays not_asked (AC:journey-c-passes).
func TestJourneyCAgent(t *testing.T) {
	e := previewEnv(t)
	e.vars["CLAUDECODE"] = "1"
	e.app.IsTerminal = func(uintptr) bool { return false } // stdin closed, no terminal
	e.in(t.TempDir())

	// `ovdb status` lives in package main; its preview branch is App.Status.
	statusCmd := &cobra.Command{Use: "status"}
	var statusOut bytes.Buffer
	statusCmd.SetOut(&statusOut)
	statusCmd.SetContext(context.Background())
	if err := e.app.Status(statusCmd, true); err != nil {
		t.Fatal(err)
	}
	status := result{stdout: statusOut.String()}
	var document struct {
		Next []struct{ Label, Command string } `json:"next"`
	}
	if err := json.Unmarshal([]byte(status.stdout), &document); err != nil || len(document.Next) == 0 {
		t.Fatalf("status --json = %s (%v)", status.stdout, err)
	}
	// The five options an agent relays (ai-agent-skills#AC:skill-less-agent-learns-options).
	for _, want := range []string{"ovdb", "ovdb open", "ovdb databases create <name>", "ovdb demo install --yes", "ovdb skills install openvaultdb --yes"} {
		found := false
		for _, n := range document.Next {
			found = found || n.Command == want
		}
		if !found {
			t.Errorf("status next lacks %q: %+v", want, document.Next)
		}
	}
	if last := document.Next[len(document.Next)-1]; !strings.HasSuffix(last.Label, "(ask the person first)") {
		t.Errorf("skill entry label = %q", last.Label)
	}
	// Without the person's yes an agent cannot install it, and nothing waits.
	_ = decodeError(t, e.run("skills", "install", "openvaultdb", "--json"), envelope.ConfirmationRequired)
	e.ok("databases", "create", "notes", "--json")
	if r := e.ok("use", "notes", "--json"); !strings.Contains(r.stdout, `"scope":"project"`) {
		t.Errorf("use --json = %s", r.stdout)
	}
	add := e.ok("add", "/items", `{"title":"Hello"}`, "--db", "notes", "--json")
	var key struct{ Key string }
	if err := json.Unmarshal([]byte(add.stdout), &key); err != nil || !strings.HasPrefix(key.Key, "/items/") {
		t.Fatalf("add --json = %s", add.stdout)
	}
	if r := e.ok("get", key.Key, "--db", "notes", "--json"); !strings.Contains(r.stdout, `"data":{"title":"Hello"}`) {
		t.Errorf("get --json = %s", r.stdout)
	}
	// Enabling without the person's relayed yes is refused, and nothing
	// changed the state.
	_ = decodeError(t, e.run("telemetry", "enable", "--json"), envelope.ConfirmationRequired)
	if state := e.telemetryStatus().Telemetry.State; state != "not_asked" {
		t.Errorf("telemetry = %s, want not_asked", state)
	}
}

// Journey D (partial): `ovdb` → Try a demo → install → Open TODO app signs
// the browser in to /apps/todo/ → Install TODO AI skill after the consent
// step; an agent (no terminal) then changes the same lists with the
// commands the skill maps the request to, and reads them back
// and opens Explore data (AC:journey-d-passes).
func TestJourneyDTodoDemo(t *testing.T) {
	e := previewEnv(t)
	e.in(t.TempDir())
	var opened []string
	local := &client.Local{
		Dirs: e.dirs, Version: testVersion, Port: e.port(), ConsoleBuilt: func() bool { return true },
		Getenv: func(key string) string { return e.vars[key] },
		Command: func(port int) *exec.Cmd {
			command := exec.Command(os.Args[0], "server", "run", "--port", strconv.Itoa(port))
			command.Env = append(append(os.Environ(), childEnv+"=1"), e.dirs.Env()...)
			return command
		},
	}
	var m tea.Model = tui.New(context.Background(), local, func(link string) error { opened = append(opened, link); return nil }, 80, 24)
	m = drive(t, m, m.Init()())
	press := func(keys ...string) {
		for _, k := range keys {
			m = drive(t, m, tea.KeyPressMsg{Text: k})
		}
	}
	if view := screenText(m); !strings.Contains(view, "> Try a demo") {
		t.Fatalf("Home:\n%s", view)
	}
	press("enter", "enter") // Try a demo → install
	if view := screenText(m); !strings.Contains(view, "The TODO demo is ready") || !strings.Contains(view, "Open TODO app ovdb demo open") {
		t.Fatalf("Result:\n%s", view)
	}
	press("o")
	if len(opened) != 1 || !strings.Contains(opened[0], "next=%2Fapps%2Ftodo%2F") {
		t.Fatalf("opened %v", opened)
	}
	// Install TODO AI skill: the consent step names Claude Code's exact
	// directory before anything is written.
	if err := os.MkdirAll(filepath.Join(e.vars["HOME"], ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(e.userHome(), ".claude", "skills", "openvaultdb-todo-demo")
	press("s")
	if view := strings.ReplaceAll(screenText(m), " ", ""); !strings.Contains(view, "InstalltheTODOAIskill?") || !strings.Contains(view, skillDir) {
		t.Fatalf("consent:\n%s", screenText(m))
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("offer wrote the skill: %v", err)
	}
	press("down", "enter")
	if view := screenText(m); !strings.Contains(view, "Installed the TODO AI skill") {
		t.Fatalf("skill result:\n%s", view)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	// Explore data from the demo Result: DataTug shows the lists, not the
	// items yet.
	press("enter", "enter")
	if view := screenText(m); !strings.Contains(view, "Explore data ovdb explore --db todo") {
		t.Fatalf("installed demo Result:\n%s", view)
	}
	press("e")
	if view := screenText(m); !strings.Contains(view, "DataTug shows your two lists, not their items yet.") {
		t.Errorf("Explore data:\n%s", view)
	}

	e.vars[cli.EnvNonInteractive] = "1"
	e.ok("add", "/lists/to-buy/items", `{"title":"Tea","done":false}`, "--db", "todo")
	e.ok("add", "/lists/to-watch/items", `{"title":"Arrival","done":false}`, "--db", "todo")
	for path, titles := range map[string][]string{"/lists/to-buy/items": {"Milk", "Bananas", "Coffee", "Tea"}, "/lists/to-watch/items": {"The Matrix", "Interstellar", "Arrival"}} {
		list := e.ok("list", path, "--db", "todo", "--json").stdout
		for _, title := range titles {
			if !strings.Contains(list, `"title":"`+title+`"`) {
				t.Errorf("%s lacks %s: %s", path, title, list)
			}
		}
	}
}
