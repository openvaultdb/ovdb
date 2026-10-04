package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	embedded "github.com/openvaultdb/ovdb/skills"
)

// userHome is the client's HOME in e.
// It is the canonical path the skills service resolves (Windows short
// names and macOS /var are spelled out).
func (e *env) userHome() string { return skills.Canonical(e.vars["HOME"]) }

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
	if !strings.Contains(problem.Reason, want) || len(problem.Next) != 1 || problem.Next[0].Command != "ovdb skills install todo-demo --yes" ||
		problem.Next[0].Label != "Install it (ask the person first)" {
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
	if !strings.Contains(status.stdout, `{"label":"Install TODO AI skill (ask the person first)","command":"ovdb skills install todo-demo","action":"install_skill"}`) {
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

// Review F7 through the CLI: an edited skill is listed as changed since
// install, installing over it fails with already_exists, and
// --replace-changed replaces it only with the person's yes.
func TestSkillChangedSinceInstall(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	e.ok("skills", "install", "todo-demo", "--harness", "claude", "--yes")
	edited := filepath.Join(e.userHome(), ".claude", "skills", "openvaultdb-todo-demo", "SKILL.md")
	if err := os.WriteFile(edited, []byte("my notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list := e.ok("skills", "list"); !strings.Contains(list.stdout, "changed since install") {
		t.Errorf("list = %s", list.stdout)
	}
	problem := decodeError(t, e.run("skills", "install", "todo-demo", "--harness", "claude", "--yes", "--json"), envelope.AlreadyExists)
	if problem.Next[0].Command != "ovdb skills install todo-demo --replace-changed" {
		t.Errorf("next = %+v", problem.Next)
	}
	consent := decodeError(t, e.run("skills", "install", "todo-demo", "--harness", "claude", "--replace-changed", "--json"), envelope.ConfirmationRequired)
	if !strings.Contains(consent.Reason, "replaces the changes") && strings.Contains(consent.Next[0].Command, "--replace-changed --yes") {
		t.Errorf("consent = %+v", consent)
	}
	if data, _ := os.ReadFile(edited); string(data) != "my notes" {
		t.Fatal("overwritten without consent")
	}
	e.ok("skills", "install", "todo-demo", "--harness", "claude", "--replace-changed", "--yes")
	if data, _ := os.ReadFile(edited); string(data) == "my notes" {
		t.Error("not replaced")
	}
}

// Review F3: status and skills list agree, both from the client's
// environment, even when the running server was started from a shell with
// another home where the skill is installed.
func TestStatusSkillsFromClientEnvironment(t *testing.T) {
	e := previewEnv(t)
	serverHome := e.userHome()
	e.app.ChildEnv = append(e.app.ChildEnv, "HOME="+serverHome, "USERPROFILE="+serverHome, "CLAUDE_CONFIG_DIR=")
	e.ok("server", "start")
	e.ok("skills", "install", "openvaultdb", "--harness", "claude", "--yes")

	clientHome := filepath.Join(t.TempDir(), "client")
	e.vars["HOME"], e.vars["USERPROFILE"] = clientHome, clientHome
	statusCmd := &cobra.Command{Use: "status"}
	var out bytes.Buffer
	statusCmd.SetOut(&out)
	statusCmd.SetContext(context.Background())
	if err := e.app.Status(statusCmd, true); err != nil {
		t.Fatal(err)
	}
	var status setup.Status
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Skills) != 2 || len(status.Skills[0].InstalledFor) != 0 {
		t.Errorf("status skills = %+v", status.Skills)
	}
	if last := status.Next[len(status.Next)-1]; last.Command != "ovdb skills install openvaultdb --yes" {
		t.Errorf("status next lacks the skill: %+v", status.Next)
	}
	var listed skills.Document
	if err := json.Unmarshal([]byte(e.ok("skills", "list", "--json").stdout), &listed); err != nil || len(listed.Skills[0].InstalledFor) != 0 {
		t.Errorf("list = %+v", listed)
	}

	// Back in the server's home both say installed.
	e.vars["HOME"], e.vars["USERPROFILE"] = serverHome, serverHome
	out.Reset()
	if err := e.app.Status(statusCmd, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `{"id":"openvaultdb","installed_for":["claude"]`) {
		t.Errorf("status in the server's home = %s", out.String())
	}
}

// Review L10: installing for an agent that isn't found says so, instead of
// implying it is there.
func TestSkillInstallNamesAgentsNotFound(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	problem := decodeError(t, e.run("skills", "install", "todo-demo", "--json"), envelope.ConfirmationRequired)
	if !strings.Contains(problem.Reason, "Claude Code wasn't found on this computer") {
		t.Errorf("reason = %q", problem.Reason)
	}
}

// bundledSkill is the text OVDB ships for skill dir.
func bundledSkill(t *testing.T, dir string) string {
	t.Helper()
	data, err := fs.ReadFile(embedded.FS, dir+"/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// skillsync v0.26.0 adopts a folder that is already the bundled skill instead
// of refusing it, and reports it as "adopted". Every way of asking for that
// prints it: the person is told before they agree, the dry run and the real
// install say what happens and where the copy that was there is kept, --json
// carries the same as backup_path, and list says the folder is already here.
// Before the text existed the human forms of the dry run and the install
// panicked with a missing copy key (exit 2 in the binary).
func TestSkillAdoptsAnExistingCopy(t *testing.T) {
	e := previewEnv(t)
	e.vars[cli.EnvNonInteractive] = "1"
	claudeDir := filepath.Join(e.userHome(), ".claude", "skills", "openvaultdb")
	cursorDir := filepath.Join(e.userHome(), ".cursor", "skills", "openvaultdb")
	bundled := bundledSkill(t, "openvaultdb")
	edited := bundled + "\nMy own note.\n"
	for dir, text := range map[string]string{claudeDir: bundled, cursorDir: edited} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	filesBefore := e.skillFiles()

	// list: the folder is already here and not managed yet, not "not installed".
	list := e.ok("skills", "list")
	if !strings.Contains(list.stdout, "not managed yet") {
		t.Errorf("list = %s", list.stdout)
	}
	var listed skills.Document
	if err := json.Unmarshal([]byte(e.ok("skills", "list", "--json").stdout), &listed); err != nil {
		t.Fatal(err)
	}
	for _, target := range listed.Skills[0].Targets {
		if (target.Harness == "claude" || target.Harness == "cursor") && (target.State != skills.StateAdoptable || target.Installed) {
			t.Errorf("list target = %+v, want adoptable and not installed", target)
		}
	}

	// Asking first says the copy is taken over (human reads it from stderr's
	// error, an agent from the JSON reason).
	needed := decodeError(t, e.run("skills", "install", "openvaultdb", "--harness", "claude", "--json"), envelope.ConfirmationRequired)
	if !strings.Contains(needed.Reason, "takes over the copy already in: "+claudeDir) || !strings.Contains(needed.Reason, "keeps a backup") {
		t.Errorf("confirmation reason = %q", needed.Reason)
	}
	if human := e.run("skills", "install", "openvaultdb", "--harness", "claude"); human.code != 1 || !strings.Contains(human.stdout+human.stderr, "takes over the copy already in: "+claudeDir) {
		t.Errorf("human confirmation = %+v", human)
	}

	// Dry run, human and JSON: no crash, nothing written, no server started.
	dry := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--dry-run")
	for _, want := range []string{"Installing the OpenVaultDB skill would change:", claudeDir + "  (already there, now managed by OVDB)", "    A backup of your copy would be kept."} {
		if dry.code != 0 || !strings.Contains(dry.stdout, want) {
			t.Errorf("dry run lacks %q: %+v", want, dry)
		}
	}
	var dryDocument skills.InstallDocument
	if r := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--dry-run", "--json"); r.code != 0 || json.Unmarshal([]byte(r.stdout), &dryDocument) != nil ||
		dryDocument.Outcomes[0].Result != "adopted" || dryDocument.Outcomes[0].BackupPath != "" || !dryDocument.DryRun {
		t.Errorf("dry run JSON = %+v", r)
	}
	if got := e.skillFiles(); len(got) != len(filesBefore) {
		t.Errorf("a dry run changed the files: %v -> %v", filesBefore, got)
	}
	if _, err := os.Stat(filepath.Join(e.dirs.Runtime, "server.json")); !os.IsNotExist(err) {
		t.Errorf("a dry run started the server: %v", err)
	}

	// The real install, human: says what happened and where the copy is kept.
	real := e.run("skills", "install", "openvaultdb", "--harness", "claude", "--yes")
	if real.code != 0 || !strings.HasPrefix(real.stdout, "Installed the OpenVaultDB skill\n") || !strings.Contains(real.stdout, claudeDir+"  (already there, now managed by OVDB)") {
		t.Fatalf("install = %+v", real)
	}
	const keptAt = "    Your copy is kept at "
	_, rest, found := strings.Cut(real.stdout, keptAt)
	backup := strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
	if !found || !strings.HasPrefix(backup, filepath.Dir(claudeDir)+string(filepath.Separator)) {
		t.Fatalf("install output names no backup under the skills folder:\n%s", real.stdout)
	}
	if kept, err := os.ReadFile(filepath.Join(backup, "SKILL.md")); err != nil || string(kept) != bundled {
		t.Errorf("backup = %q, %v", kept, err)
	}

	// The real install, JSON: the same facts, and the edited copy is what is kept.
	var document skills.InstallDocument
	r := e.run("skills", "install", "openvaultdb", "--harness", "cursor", "--yes", "--json")
	if err := json.Unmarshal([]byte(r.stdout), &document); r.code != 0 || err != nil || document.Outcomes[0].Result != "adopted" || document.AlreadyUpToDate {
		t.Fatalf("install --json = %+v", r)
	}
	if kept, err := os.ReadFile(filepath.Join(document.Outcomes[0].BackupPath, "SKILL.md")); err != nil || string(kept) != edited {
		t.Errorf("backup of the edited copy = %q, %v", kept, err)
	}
	if now, _ := os.ReadFile(filepath.Join(cursorDir, "SKILL.md")); string(now) != bundled {
		t.Errorf("the skill was not put in place: %q", now)
	}

	// Managed from now on: listed as installed, and installing again changes nothing.
	for _, target := range func() []skills.Target {
		var after skills.Document
		_ = json.Unmarshal([]byte(e.ok("skills", "list", "--json").stdout), &after)
		return after.Skills[0].Targets
	}() {
		if (target.Harness == "claude" || target.Harness == "cursor") && target.State != skills.StateInstalled {
			t.Errorf("after the install %s is %s", target.Harness, target.State)
		}
	}
	if again := e.ok("skills", "install", "openvaultdb", "--harness", "claude", "--yes"); !strings.Contains(again.stdout, "already up to date") {
		t.Errorf("second install = %s", again.stdout)
	}
}
