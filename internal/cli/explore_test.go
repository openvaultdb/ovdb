package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/setup/explore"
)

// explore-data-handoff#AC:menu-asks-intent-first: the bare command names the
// database and presents both tools before any file is written.
func TestExploreMenuNamesCurrentDatabase(t *testing.T) {
	e, _, _ := dataEnv(t)
	r := e.ok("explore", "--db", "notes", "--json")
	var menu explore.Menu
	if err := json.Unmarshal([]byte(r.stdout), &menu); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	if menu.Database != "notes" || menu.IsDemo || menu.DataTugCLIKey != "explore.menu.datatug_cli" {
		t.Errorf("menu = %+v", menu)
	}
	if !strings.Contains(r.stdout, `"database":"notes"`) {
		t.Errorf("stdout = %s", r.stdout)
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
}

// AC:datatug-missing: nothing in this test environment puts a real datatug
// on PATH, so the install commands and the prepared command for afterwards
// are always shown here.
func TestExploreDataTugCLIMissingShowsInstallCommands(t *testing.T) {
	e, _, _ := dataEnv(t)
	r := e.ok("explore", "datatug-cli", "--db", "notes", "--json")
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
	human := e.ok("explore", "datatug-cli", "--db", "notes")
	for _, want := range []string{explore.InstallCommands[0], explore.InstallCommands[1], document.QueryCommand} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stdout)
		}
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
