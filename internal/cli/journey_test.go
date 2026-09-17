package cli_test

// The journey regression gate (configuration-parity#REQ:journey-a-terminal,
// REQ:journey-c-agent), as far as increment 3 reaches: Journey A without the
// telemetry prompt (increment 9) and Journey C without skills (increment 7)
// or the demo (increment 4).

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

// Journey A (partial): `ovdb` → Create a database → inGitDB, suggested
// location, name notes → Use in this project → quit; then `ovdb add` and
// `ovdb list` work in that project without --db (AC:journey-a-passes).
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
		Where: dbcontext.Request{Dirs: lookup.Dirs, Root: lookup.Root},
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
	if view := screenText(m); !strings.Contains(view, "What would you like to do?") || !strings.Contains(view, "> Create a database") {
		t.Fatalf("Home:\n%s", view)
	}
	press("enter", "enter") // Create a database → inGitDB
	press("n", "o", "t", "e", "s")
	if view := screenText(m); !strings.Contains(view, filepath.Join(e.dirs.Data, "notes")) {
		t.Fatalf("suggested location missing:\n%s", view)
	}
	press("enter", "enter")
	if view := screenText(m); !strings.Contains(view, "Created database notes") || !strings.Contains(view, "u use it in this project") {
		t.Fatalf("Result:\n%s", view)
	}
	press("u")
	if view := screenText(m); !strings.Contains(view, "Now using notes for this project ("+project+")") {
		t.Fatalf("Use in this project:\n%s", view)
	}
	press("enter")
	if view := screenText(m); !strings.Contains(view, "1 database · using notes (this project) · OVDB server running at") {
		t.Errorf("Home summary:\n%s", view)
	}
	press("q")

	if r := e.ok("add", "/items", `{"title":"Hello"}`); !strings.HasPrefix(r.stdout, "notes: added /items/") {
		t.Errorf("add = %q", r.stdout)
	}
	if r := e.ok("list", "/items"); !strings.HasPrefix(r.stdout, "notes:/items\n") || !strings.Contains(r.stdout, `{"title":"Hello"}`) {
		t.Errorf("list = %q", r.stdout)
	}
}

// Journey C (partial): an agent without a skill, stdin closed and no
// terminal, reads the status, creates a database, selects it for the
// project and round-trips a record with absolute paths; no command waits
// for input (AC:journey-c-passes).
func TestJourneyCAgent(t *testing.T) {
	e := previewEnv(t)
	e.vars["CLAUDECODE"] = "1"
	e.vars[cli.EnvNonInteractive] = "1"
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
	for _, want := range []string{"ovdb open", "ovdb databases create <name>"} {
		found := false
		for _, n := range document.Next {
			found = found || n.Command == want
		}
		if !found {
			t.Errorf("status next lacks %q: %+v", want, document.Next)
		}
	}
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
}
