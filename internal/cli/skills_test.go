package cli_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// userHome is the client's HOME in e.
func (e *env) userHome() string { return e.vars["HOME"] }

// skillFiles lists every file under the client's home, where skills go.
func (e *env) skillFiles() []string {
	var files []string
	_ = filepath.WalkDir(e.userHome(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// AC:agent-cannot-install-silently: without a terminal, installing a skill
// without --yes prints where it would go and exits 1 with
// confirmation_required, writing nothing; `ovdb demo install --yes` installs
// no skill.
func TestSkillInstallNeedsYesWithoutTerminal(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	if err := os.MkdirAll(filepath.Join(e.userHome(), ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := e.run("skills", "install", "todo-demo", "--json")
	problem := decodeError(t, r, envelope.ConfirmationRequired)
	want := filepath.Join(e.userHome(), ".claude", "skills", "openvaultdb-todo-demo")
	if !strings.Contains(problem.Reason, want) || len(problem.Next) != 1 || problem.Next[0].Command != "ovdb skills install todo-demo --yes" {
		t.Errorf("confirmation = %+v", problem)
	}
	human := e.run("skills", "install", "todo-demo")
	if human.code != 1 || !strings.Contains(human.stdout+human.stderr, want) || !strings.Contains(human.stdout+human.stderr, "--yes") {
		t.Errorf("human confirmation = %+v", human)
	}

	if r := e.run("demo", "install", "--yes"); r.code != 0 {
		t.Fatalf("demo install: %+v", r)
	}
	if files := e.skillFiles(); len(files) != 0 {
		t.Errorf("files written: %v", files)
	}
	status := e.run("demo", "status", "--json")
	if !strings.Contains(status.stdout, `"command":"ovdb skills install todo-demo","action":"install_skill"`) {
		t.Errorf("demo next lacks the TODO skill: %s", status.stdout)
	}
}

// AC:web-cannot-target-arbitrary-dir, the CLI half: --dir outside the home
// fails before anything starts or is written.
func TestSkillInstallDirOutsideHomeRefused(t *testing.T) {
	e := previewEnv(t)
	for _, dir := range []string{"/etc/x", filepath.Join(filepath.Dir(e.userHome()), "elsewhere")} {
		problem := decodeError(t, e.run("skills", "install", "openvaultdb", "--dir", dir, "--yes", "--json"), envelope.InvalidArgument)
		if !strings.Contains(problem.Reason, "outside your home folder") {
			t.Errorf("--dir %s = %+v", dir, problem)
		}
	}
	if _, err := os.Stat(e.dirs.Runtime); !os.IsNotExist(err) {
		t.Errorf("a refused install touched the runtime directory: %v", err)
	}
	if files := e.skillFiles(); len(files) != 0 {
		t.Errorf("files written: %v", files)
	}
}

// AC:install-one-skill-only through the CLI, and
// local-server-and-web-console#AC:client-resolves-skill-dirs: a server
// started with CODEX_HOME=/x installs where the client's CODEX_HOME=/y says.
func TestSkillInstallThroughServer(t *testing.T) {
	e := previewEnv(t)
	serverCodex, clientCodex := filepath.Join(t.TempDir(), "x"), filepath.Join(t.TempDir(), "y")
	e.app.ChildEnv = append(e.app.ChildEnv, "CODEX_HOME="+serverCodex)
	if r := e.run("server", "start"); r.code != 0 {
		t.Fatalf("start: %+v", r)
	}
	e.vars["CODEX_HOME"] = clientCodex
	r := e.run("skills", "install", "openvaultdb", "--harness", "codex", "--yes", "--json")
	var document skills.InstallDocument
	if err := json.Unmarshal([]byte(r.stdout), &document); r.code != 0 || err != nil || document.Outcomes[0].Result != "added" {
		t.Fatalf("install = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(clientCodex, "skills", "openvaultdb", "SKILL.md")); err != nil {
		t.Errorf("not under the client's CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(serverCodex); !os.IsNotExist(err) {
		t.Errorf("written under the server's CODEX_HOME: %v", err)
	}

	unrelated := filepath.Join(e.userHome(), ".claude", "skills", "specscore", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(unrelated), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("specscore"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := e.run("skills", "install", "todo-demo", "--harness", "claude", "--yes")
	if first.code != 0 || !strings.HasPrefix(first.stdout, "Installed the TODO AI skill\n") ||
		!strings.Contains(first.stdout, filepath.Join(e.userHome(), ".claude", "skills", "openvaultdb-todo-demo")+"  (added)") {
		t.Fatalf("first = %+v", first)
	}
	second := e.run("skills", "install", "todo-demo", "--harness", "claude", "--yes")
	if second.code != 0 || !strings.Contains(second.stdout, "already up to date") {
		t.Errorf("second = %+v", second)
	}
	entries, _ := os.ReadDir(filepath.Dir(filepath.Dir(unrelated)))
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != ".cli-helpers-skills-sync.json,openvaultdb-todo-demo,specscore" {
		t.Errorf("claude skills = %v", names)
	}
	if data, _ := os.ReadFile(unrelated); string(data) != "specscore" {
		t.Error("the unrelated skill changed")
	}

	list := e.run("skills", "list", "--json")
	var listed skills.Document
	if err := json.Unmarshal([]byte(list.stdout), &listed); err != nil || strings.Join(listed.Skills[1].InstalledFor, ",") != "claude" || strings.Join(listed.Skills[0].InstalledFor, ",") != "codex" {
		t.Errorf("list = %s", list.stdout)
	}
	human := e.run("skills", "list")
	if !strings.Contains(human.stdout, "TODO AI skill (todo-demo)") || !strings.Contains(human.stdout, "installed") {
		t.Errorf("list = %s", human.stdout)
	}
}
