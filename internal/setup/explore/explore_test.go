package explore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// TestDescriptorHasExactlyFourKeysAndNoToken is
// explore-data-handoff#AC:descriptor-and-command's core assertion (spike
// S4): the descriptor is exactly baseUrl, databaseId, tokenEnv, principalId.
func TestDescriptorHasExactlyFourKeysAndNoToken(t *testing.T) {
	descriptor := NewDescriptor("http://127.0.0.1:6832", "todo")
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Fatalf("descriptor has %d keys, want 4: %v", len(raw), raw)
	}
	for _, key := range []string{"baseUrl", "databaseId", "tokenEnv", "principalId"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("descriptor missing key %q", key)
		}
	}
	if _, ok := raw["token"]; ok {
		t.Error("descriptor must never carry a token key")
	}
	if descriptor.TokenEnv != "OVDB_DATATUG_TOKEN" || descriptor.PrincipalID != "local-owner" {
		t.Errorf("descriptor = %+v", descriptor)
	}
}

func TestWriteDescriptorWritesOwnerOnlyFileAndDirs(t *testing.T) {
	home := t.TempDir()
	path := DescriptorPath(home, "todo")
	if want := filepath.Join(home, "explore", "datatug", "todo.json"); path != want {
		t.Fatalf("DescriptorPath = %q, want %q", path, want)
	}
	if err := WriteDescriptor(path, NewDescriptor("http://127.0.0.1:6832", "todo")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Fatalf("written descriptor has %d keys: %v", len(raw), raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("descriptor file mode = %v, want owner-only", info.Mode())
	}
}

func TestOnPathReflectsLookPath(t *testing.T) {
	if !OnPath(func(string) (string, error) { return "/usr/bin/datatug", nil }) {
		t.Error("OnPath = false, want true")
	}
	if OnPath(func(string) (string, error) { return "", errors.New("not found") }) {
		t.Error("OnPath = true, want false")
	}
}

// TestEnvLinesNeverCarryTheRealToken: the token line is always a placeholder
// naming the command that mints it, never a value.
func TestEnvLinesNeverCarryTheRealToken(t *testing.T) {
	descriptor := NewDescriptor("http://127.0.0.1:6832", "todo")
	lines := EnvLines(descriptor, TokenCommand("todo"))
	if len(lines) != 3 {
		t.Fatalf("EnvLines returned %d lines, want 3", len(lines))
	}
	if lines[0].Name != "OVDB_DATATUG_TOKEN" || lines[0].Value != "<paste the token from: ovdb token create --db todo --scope read-only>" {
		t.Errorf("token line = %+v", lines[0])
	}
	if lines[1].Name != "OVDB_DATATUG_TOKEN_BASE_URL" || lines[1].Value != "http://127.0.0.1:6832" {
		t.Errorf("base url line = %+v", lines[1])
	}
	if lines[2].Name != "OVDB_DATATUG_TOKEN_PRINCIPAL_ID" || lines[2].Value != "local-owner" {
		t.Errorf("principal line = %+v", lines[2])
	}
}

func TestShellTextPOSIXAndPowerShell(t *testing.T) {
	descriptor := NewDescriptor("http://127.0.0.1:6832", "todo")
	lines := EnvLines(descriptor, TokenCommand("todo"))
	sh := ShellText("linux", lines)
	if want := "export OVDB_DATATUG_TOKEN=<paste the token from: ovdb token create --db todo --scope read-only>\n" +
		`export OVDB_DATATUG_TOKEN_BASE_URL="http://127.0.0.1:6832"` + "\n" +
		`export OVDB_DATATUG_TOKEN_PRINCIPAL_ID="local-owner"`; sh != want {
		t.Errorf("sh =\n%s\nwant\n%s", sh, want)
	}
	ps := ShellText("windows", lines)
	if want := `$env:OVDB_DATATUG_TOKEN = "<paste the token from: ovdb token create --db todo --scope read-only>"` + "\n" +
		`$env:OVDB_DATATUG_TOKEN_BASE_URL = "http://127.0.0.1:6832"` + "\n" +
		`$env:OVDB_DATATUG_TOKEN_PRINCIPAL_ID = "local-owner"`; ps != want {
		t.Errorf("powershell =\n%s\nwant\n%s", ps, want)
	}
}

// TestQueryCommandAlwaysHasNoPolicies is spike S4's central finding: a fresh
// DataTug home has no access policies, so the printed command must always
// include --no-policies or every newcomer's first run fails.
func TestQueryCommandAlwaysHasNoPolicies(t *testing.T) {
	sh := QueryCommand("linux", "/home/a/.ovdb/explore/datatug/todo.json", "lists")
	if want := "datatug query run --db \"openvaultdb:///home/a/.ovdb/explore/datatug/todo.json\" \\\n" +
		"  --from lists --as local-owner --no-policies --format json"; sh != want {
		t.Errorf("sh query =\n%s\nwant\n%s", sh, want)
	}
	ps := QueryCommand("windows", `C:\Users\alex\.ovdb\explore\datatug\todo.json`, "lists")
	if want := "datatug query run --db \"openvaultdb://C:\\Users\\alex\\.ovdb\\explore\\datatug\\todo.json\" `\n" +
		"  --from lists --as local-owner --no-policies --format json"; ps != want {
		t.Errorf("powershell query =\n%s\nwant\n%s", ps, want)
	}
}

func TestPrepareOnPathTrue(t *testing.T) {
	home := t.TempDir()
	result, err := Prepare(func(string) (string, error) { return "/usr/bin/datatug", nil }, home, "http://127.0.0.1:6832", "todo", "lists")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OnPath || len(result.InstallCommands) != 0 {
		t.Errorf("on path result = %+v", result)
	}
	if result.Descriptor.DatabaseID != "todo" || result.Descriptor.BaseURL != "http://127.0.0.1:6832" {
		t.Errorf("descriptor = %+v", result.Descriptor)
	}
	if result.TokenCommand != "ovdb token create --db todo --scope read-only" {
		t.Errorf("token command = %q", result.TokenCommand)
	}
	if _, err := os.Stat(result.DescriptorPath); err != nil {
		t.Errorf("descriptor not written: %v", err)
	}
}

// TestPrepareDataTugMissingShowsInstallCommands is
// explore-data-handoff#AC:datatug-missing.
func TestPrepareDataTugMissingShowsInstallCommands(t *testing.T) {
	home := t.TempDir()
	result, err := Prepare(func(string) (string, error) { return "", errors.New("not found") }, home, "http://127.0.0.1:6832", "todo", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.OnPath {
		t.Error("OnPath = true, want false")
	}
	if len(result.InstallCommands) != 2 || result.InstallCommands[0] != InstallCommands[0] {
		t.Errorf("install commands = %v", result.InstallCommands)
	}
	// The prepared command is still shown, for afterwards.
	if result.QueryCommand == "" {
		t.Error("query command empty even though datatug is missing")
	}
	// An empty collection defaults to the demo's own root collection.
	if result.Collection != DefaultCollection {
		t.Errorf("collection = %q, want %q", result.Collection, DefaultCollection)
	}
}

// F3 (review-inc-7.md): both description keys must be real "what works
// today" copy — the *_help keys — never an option's own label key.
func TestNewMenuDemoCopy(t *testing.T) {
	menu := NewMenu("todo", true)
	if menu.DataTugCLIKey != "explore.menu.datatug_cli_demo" || menu.DataTugAppKey != "explore.menu.datatug_app_help" || !menu.IsDemo {
		t.Errorf("demo menu = %+v", menu)
	}
	generic := NewMenu("notes", false)
	if generic.DataTugCLIKey != "explore.menu.datatug_cli_help" || generic.DataTugAppKey != "explore.menu.datatug_app_help" || generic.IsDemo {
		t.Errorf("generic menu = %+v", generic)
	}
	// Neither key may equal the option's own label key.
	for _, key := range []string{menu.DataTugCLIKey, menu.DataTugAppKey, generic.DataTugCLIKey, generic.DataTugAppKey} {
		if key == "explore.menu.datatug_cli" || key == "explore.menu.datatug_app" {
			t.Errorf("description key %q is a label key, not a description", key)
		}
	}
}

// F5 (review-inc-7.md): the demo always defaults to "lists"; any other
// database with exactly one root collection defaults to it; with several
// (or none), --collection is required, naming the choices; a value with a
// "/" is refused (datatug-cli reads root collections only — datatug-cli#256).
func TestResolveCollection(t *testing.T) {
	if got, err := ResolveCollection(true, "", nil, "todo"); err != nil || got != "lists" {
		t.Errorf("demo default = %q, %v", got, err)
	}
	if got, err := ResolveCollection(false, "", []string{"customers"}, "notes"); err != nil || got != "customers" {
		t.Errorf("sole collection default = %q, %v", got, err)
	}
	if got, err := ResolveCollection(false, "orders", []string{"customers", "orders"}, "notes"); err != nil || got != "orders" {
		t.Errorf("explicit choice = %q, %v", got, err)
	}
	_, err := ResolveCollection(false, "", []string{"customers", "orders"}, "notes")
	if err == nil || err.Code != envelope.InvalidArgument {
		t.Fatalf("ambiguous collections: err = %v, want invalid_argument", err)
	}
	if len(err.Next) != 2 {
		t.Errorf("ambiguous collections next = %+v, want one per root collection", err.Next)
	}
	for _, name := range []string{"customers", "orders"} {
		found := false
		for _, n := range err.Next {
			if strings.Contains(n.Command, "--collection "+name) {
				found = true
			}
		}
		if !found {
			t.Errorf("next lacks a choice for %q: %+v", name, err.Next)
		}
	}
	_, err = ResolveCollection(false, "", nil, "notes")
	if err == nil || err.Code != envelope.InvalidArgument {
		t.Fatalf("no collections: err = %v, want invalid_argument", err)
	}

	_, err = ResolveCollection(true, "lists/to-buy/items", nil, "todo")
	if err == nil || err.Code != envelope.InvalidArgument {
		t.Fatalf("nested collection: err = %v, want invalid_argument", err)
	}
	if !strings.Contains(err.Reason, "root collections only") {
		t.Errorf("nested collection reason = %q, want it to say root collections only", err.Reason)
	}
}
