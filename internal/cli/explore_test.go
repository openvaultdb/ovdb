package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
)

// explore-data-handoff#AC:menu-asks-intent-first: the bare command names the
// database and presents both tools before any file is written, for a
// non-demo database (the AC's own example, `notes`), with real "what works
// today" copy — not the option's own label repeated as its description
// (review-inc-7.md F3: the original version of this test enshrined that bug
// by asserting the label key).
func TestExploreMenuNamesCurrentDatabase(t *testing.T) {
	e, _, _ := dataEnv(t)
	r := e.ok("explore", "--db", "notes", "--json")
	var menu explore.Menu
	if err := json.Unmarshal([]byte(r.stdout), &menu); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if menu.Database != "notes" || menu.IsDemo {
		t.Errorf("menu = %+v", menu)
	}
	if menu.DataTugCLIKey != "explore.menu.datatug_cli_help" {
		t.Errorf("datatug_cli_description_key = %q, want the help key, not the label", menu.DataTugCLIKey)
	}
	if menu.DataTugAppKey != "explore.menu.datatug_app_help" {
		t.Errorf("datatug_app_description_key = %q, want the help key, not the label", menu.DataTugAppKey)
	}
	if !strings.Contains(r.stdout, `"database":"notes"`) {
		t.Errorf("stdout = %s", r.stdout)
	}
	human := e.ok("explore", "--db", "notes")
	if strings.Contains(human.stdout, "In the terminal with DataTug CLI\n    In the terminal with DataTug CLI") {
		t.Errorf("the option's description repeats its own label:\n%s", human.stdout)
	}
	if !strings.Contains(human.stdout, "Read-only DTQL queries on root collections") {
		t.Errorf("human output lacks the real DataTug CLI description:\n%s", human.stdout)
	}
}

// F8 (review-inc-7.md): an unregistered database is a clean not_found in
// --json too, exit 1 — not a menu for a database that does not exist
// (`ovdb explore --db nope --json` used to exit 0 with a full menu).
func TestExploreMenuUnknownDatabaseIsNotFound(t *testing.T) {
	e, _, _ := dataEnv(t)
	failure := decodeError(t, e.fails("explore", "--db", "nope", "--json"), envelope.NotFound)
	if !strings.Contains(failure.Reason, "nope") {
		t.Errorf("reason = %q, want it to name nope", failure.Reason)
	}
	if len(failure.Next) == 0 || failure.Next[0].Command != "ovdb databases" {
		t.Errorf("next = %+v, want ovdb databases", failure.Next)
	}
	human := e.fails("explore", "--db", "nope")
	if !strings.Contains(human.stderr, "nope") {
		t.Errorf("human stderr = %q, want it to name nope", human.stderr)
	}
}

// AC:demo-explore-copy: the demo database's DataTug CLI option states that
// items are not shown yet and names the TODO app, Browse data and the
// ovdb list command.
func TestExploreMenuDemoCopy(t *testing.T) {
	e := previewEnv(t)
	e.in(t.TempDir())
	e.ok("demo", "install", "--yes")
	r := e.ok("explore", "--db", "todo", "--json")
	var menu explore.Menu
	if err := json.Unmarshal([]byte(r.stdout), &menu); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if !menu.IsDemo || menu.DataTugCLIKey != "explore.menu.datatug_cli_demo" {
		t.Errorf("menu = %+v", menu)
	}
	human := e.ok("explore", "--db", "todo")
	for _, want := range []string{"TODO app", "Browse data", "ovdb list /lists/to-buy/items --db todo"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stdout)
		}
	}
}

// AC:descriptor-and-command: the descriptor has exactly the four keys and no
// token, and the JSON lists the variables, the token command and the
// datatug query run command (spike S4's findings, including --no-policies).
func TestExploreDataTugCLIWritesFourKeyDescriptor(t *testing.T) {
	e, _, _ := dataEnv(t)
	r := e.ok("explore", "datatug-cli", "--db", "notes", "--collection", "items", "--json")
	var document explore.DataTugCLI
	if err := json.Unmarshal([]byte(r.stdout), &document); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if document.Descriptor.DatabaseID != "notes" || document.Descriptor.TokenEnv != "OVDB_DATATUG_TOKEN" ||
		document.Descriptor.PrincipalID != "local-owner" || document.Descriptor.BaseURL == "" {
		t.Errorf("descriptor = %+v", document.Descriptor)
	}
	if document.Collection != "items" {
		t.Errorf("collection = %q", document.Collection)
	}
	if strings.Contains(r.stdout, `"token"`) {
		t.Errorf("stdout carries a token key: %s", r.stdout)
	}
	if !strings.Contains(document.QueryCommand, "--no-policies") {
		t.Errorf("query command lacks --no-policies: %q", document.QueryCommand)
	}
	if document.TokenCommand != "ovdb token create --db notes --scope read-only" {
		t.Errorf("token command = %q", document.TokenCommand)
	}
	if len(document.EnvLines) != 3 {
		t.Fatalf("env lines = %v", document.EnvLines)
	}
	human := e.ok("explore", "datatug-cli", "--db", "notes", "--collection", "items")
	for _, want := range []string{document.DescriptorPath, document.TokenCommand, "--no-policies"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stdout)
		}
	}
	// F11 (review-inc-7.md): the token command comes first — the env vars
	// block's own token line points at it — then the env vars, then the
	// query command.
	tokenAt := strings.Index(human.stdout, "Create a read-only token first:")
	envAt := strings.Index(human.stdout, "Set these environment variables:")
	queryAt := strings.Index(human.stdout, "Then run:")
	if tokenAt < 0 || envAt < 0 || queryAt < 0 {
		t.Fatalf("human output is missing a section:\n%s", human.stdout)
	}
	if tokenAt >= envAt || envAt >= queryAt {
		t.Errorf("wrong order (token=%d, env=%d, query=%d):\n%s", tokenAt, envAt, queryAt, human.stdout)
	}
}

// AC:datatug-missing: nothing in this test environment puts a real datatug
// on PATH, so the install commands and the prepared command for afterwards
// are always shown here.
func TestExploreDataTugCLIMissingShowsInstallCommands(t *testing.T) {
	e, _, _ := dataEnv(t)
	r := e.ok("explore", "datatug-cli", "--db", "notes", "--collection", "items", "--json")
	var document explore.DataTugCLI
	if err := json.Unmarshal([]byte(r.stdout), &document); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if document.OnPath {
		t.Skip("datatug is on PATH in this environment")
	}
	if len(document.InstallCommands) != 2 || document.QueryCommand == "" {
		t.Errorf("missing result = %+v", document)
	}
	human := e.ok("explore", "datatug-cli", "--db", "notes", "--collection", "items")
	for _, want := range []string{explore.InstallCommands[0], explore.InstallCommands[1], document.QueryCommand} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stdout)
		}
	}
}

// stubDatatug creates an executable named datatug (datatug.exe on Windows)
// in a fresh directory and returns the directory, for putting on a PATH so
// exec.LookPath finds it without needing a real datatug build.
func stubDatatug(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "datatug"
	if goruntime.GOOS == "windows" {
		name = "datatug.exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// F2 (review-inc-7.md): the detached server's PATH is not necessarily this
// process's PATH (a fresh shell after `go install`, an agent harness with a
// minimal PATH, …). The CLI must report on_path from ITS OWN process, not
// whatever the server saw, even when they differ.
func TestExploreDataTugCLIChecksTheClientsOwnPath(t *testing.T) {
	e, _, _ := dataEnv(t)
	originalPath := os.Getenv("PATH")
	stubDir := stubDatatug(t)
	// The child (detached server) keeps the PATH this test had before the
	// stub was added — datatug is not on it. Appended last, so it wins over
	// the PATH os.Environ() already carries when the child's Env is built.
	e.app.ChildEnv = append(e.app.ChildEnv, "PATH="+originalPath)
	// This test process (standing in for the CLI/TUI) gets the stub — same
	// process explore.OnPath(nil) checks from.
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+originalPath)

	r := e.ok("explore", "datatug-cli", "--db", "notes", "--collection", "items", "--json")
	var document explore.DataTugCLI
	if err := json.Unmarshal([]byte(r.stdout), &document); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if !document.OnPath {
		t.Errorf("on_path = false, want true (client's own PATH has datatug): %+v", document)
	}
	if len(document.InstallCommands) != 0 {
		t.Errorf("install_commands = %v, want none once the client sees datatug", document.InstallCommands)
	}
	human := e.ok("explore", "datatug-cli", "--db", "notes", "--collection", "items")
	if strings.Contains(human.stdout, "isn't on your PATH") {
		t.Errorf("human output still claims datatug is missing:\n%s", human.stdout)
	}
	if !strings.Contains(human.stdout, "datatug is on your PATH.") {
		t.Errorf("human output lacks the ready copy:\n%s", human.stdout)
	}
}

// AC:datatug-app-is-honest: the page states the limitation, offers Use
// DataTug CLI instead and Open DataTug.app, with no control implying OVDB
// can open a database there.
func TestExploreDataTugAppIsHonest(t *testing.T) {
	e, _, _ := dataEnv(t)
	r := e.ok("explore", "datatug-app", "--db", "notes", "--print-url", "--json")
	var document explore.DataTugApp
	if err := json.Unmarshal([]byte(r.stdout), &document); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if document.URL != "https://datatug.app" || document.Database != "notes" {
		t.Errorf("document = %+v", document)
	}
	if len(document.Next) != 2 || document.Next[0].Action != explore.ActionDataTugCLI || document.Next[1].Action != explore.ActionDataTugApp {
		t.Errorf("next = %+v", document.Next)
	}
	human := e.ok("explore", "datatug-app", "--db", "notes", "--print-url")
	if !strings.Contains(human.stdout, "DataTug.app can't open an OpenVaultDB database directly yet.") ||
		!strings.Contains(human.stdout, "https://datatug.app") {
		t.Errorf("human output = %s", human.stdout)
	}
}

// F1 (review-inc-7.md): DataTug.app is not the TODO demo — its own honesty
// copy must never borrow the demo's sign-in-link wording, with or without
// --print-url, and for both a demo and a non-demo database.
func TestExploreDataTugAppNeverBorrowsDemoSignInCopy(t *testing.T) {
	demoEnv := previewEnv(t)
	demoEnv.in(t.TempDir())
	demoEnv.ok("demo", "install", "--yes")
	dataEnvironment, _, _ := dataEnv(t)

	for _, tc := range []struct {
		name string
		e    *env
		db   string
	}{
		{"demo database", demoEnv, "todo"},
		{"non-demo database", dataEnvironment, "notes"},
	} {
		for _, args := range [][]string{
			{"explore", "datatug-app", "--db", tc.db, "--print-url"},
			{"explore", "datatug-app", "--db", tc.db},
		} {
			human := tc.e.ok(args...)
			for _, forbidden := range []string{"TODO app", "sign-in", "single use"} {
				if strings.Contains(human.stdout, forbidden) {
					t.Errorf("%s %v output leaks demo copy %q:\n%s", tc.name, args, forbidden, human.stdout)
				}
			}
			if !strings.Contains(human.stdout, "https://datatug.app") {
				t.Errorf("%s %v output lacks the URL:\n%s", tc.name, args, human.stdout)
			}
		}
	}
}
