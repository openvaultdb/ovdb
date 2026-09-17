package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

// callWith calls the running server with bearer and returns the status.
func (e *env) callWith(bearer, method, path, body string) (int, string) {
	e.t.Helper()
	state, err := runtime.Inspect(context.Background(), e.dirs.Runtime)
	if err != nil || !state.Running {
		e.t.Fatalf("server not running: %+v %v", state, err)
	}
	request, _ := http.NewRequest(method, runtime.BaseURL(state.Record.Port)+path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(answer)
}

// AC:token-against-local-server through the CLI
// (REQ:tokens-against-local-server): with the gate and no --addr or
// --owner-token, `ovdb token` manages <OVDB home>/auth.json through the
// local server, starting it. The secret is printed once and appears nowhere
// else. It writes no records, so it runs on every OS, Windows included,
// where it also checks auth.json's ACL.
func TestTokensThroughLocalServer(t *testing.T) {
	e := previewEnv(t)
	e.ok("databases", "create", "todo")
	e.ok("server", "stop")

	// create starts the server.
	created := e.ok("token", "create", "--db", "todo", "--scope", "read-only", "--label", "reader", "--json")
	var token struct {
		ID           string   `json:"id"`
		Token        string   `json:"token"`
		DatabaseID   string   `json:"databaseId"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(created.stdout), &token); err != nil || token.Token == "" || token.ID == "" || token.DatabaseID != "todo" {
		t.Fatalf("token create --json = %q (%v)", created.stdout, err)
	}
	e.waitMounted()

	if status, body := e.callWith(token.Token, http.MethodGet, "/v1/databases/todo", ""); status != http.StatusOK {
		t.Errorf("read = %d %s", status, body)
	}
	if status, body := e.callWith(token.Token, http.MethodPut, "/v1/databases/todo/records/lists/x", `{"data":{"title":"x"}}`); status != http.StatusForbidden {
		t.Errorf("write = %d %s", status, body)
	}
	status, body := e.callWith(token.Token, http.MethodGet, "/api/local/v1/status", "")
	if e := envelope.Decode([]byte(body)); status != http.StatusForbidden || e == nil || e.Code != envelope.Forbidden {
		t.Errorf("local API = %d %s", status, body)
	}

	authFile := filepath.Join(e.dirs.Home, "auth.json")
	if err := daemonlifecycle.ValidateOwnerOnly(authFile); err != nil {
		t.Errorf("auth.json under OVDB home is not owner-only: %v", err)
	}

	// Human create shows the secret once, on stdout.
	human := e.ok("token", "create", "--db", "todo", "--scope", "read-write")
	if !strings.Contains(human.stdout, "Save this token now. OVDB shows it only once:") || !strings.Contains(human.stdout, "records:write") {
		t.Errorf("human create = %q", human.stdout)
	}

	// The secret is in no list, status, log or state file.
	list := e.ok("token", "list")
	listJSON := e.ok("token", "list", "--json")
	statusJSON := e.api(http.MethodGet, "/api/local/v1/status")
	serverLog, _ := os.ReadFile(filepath.Join(e.dirs.Runtime, "server.log"))
	stored, _ := os.ReadFile(authFile)
	for name, text := range map[string]string{"list": list.stdout + list.stderr, "list --json": listJSON.stdout, "status": statusJSON,
		"server.log": string(serverLog), "auth.json": string(stored), "create stderr": created.stderr} {
		if strings.Contains(text, token.Token) {
			t.Errorf("%s contains the token secret", name)
		}
	}
	if !strings.Contains(list.stdout, token.ID) || !strings.Contains(list.stdout, "reader") || !strings.Contains(listJSON.stdout, token.ID) {
		t.Errorf("list = %q / %q", list.stdout, listJSON.stdout)
	}

	// Revoke takes effect at once.
	if r := e.ok("token", "revoke", token.ID); !strings.Contains(r.stdout, "Revoked token "+token.ID) {
		t.Errorf("revoke = %q", r.stdout)
	}
	if status, body := e.callWith(token.Token, http.MethodGet, "/v1/databases/todo", ""); status != http.StatusUnauthorized {
		t.Errorf("revoked read = %d %s", status, body)
	}
	if err := daemonlifecycle.ValidateOwnerOnly(authFile); err != nil {
		t.Errorf("auth.json after revoke: %v", err)
	}

	// Failures are envelopes: an unknown id, a missing --db, a bad scope,
	// --no-start with the server stopped.
	// With --json a /v1 failure prints the /v1 error body, as data commands do.
	if r := e.fails("token", "revoke", "nope", "--json"); !strings.HasPrefix(r.stdout, `{"error":{"code":"not_found"`) {
		t.Errorf("unknown revoke --json = %q", r.stdout)
	}
	if r := e.fails("token", "revoke", "nope"); !strings.Contains(r.stderr, "Couldn't revoke the token") || !strings.Contains(r.stderr, "ovdb token list") {
		t.Errorf("unknown revoke = %q", r.stderr)
	}
	_ = decodeError(t, e.run("token", "create", "--json"), envelope.InvalidArgument)
	_ = decodeError(t, e.run("token", "create", "--db", "todo", "--scope", "admin", "--json"), envelope.InvalidArgument)
	_ = decodeError(t, e.run("token", "list", "extra", "--json"), envelope.InvalidArgument)
	e.ok("server", "stop")
	_ = decodeError(t, e.run("token", "list", "--no-start", "--json"), envelope.ServerNotRunning)
}

// --addr and --owner-token keep today's remote-server path.
func TestLegacyTokenPaths(t *testing.T) {
	for _, args := range [][]string{
		{"token", "create", "--db", "todo", "--addr", "http://127.0.0.1:1"},
		{"token", "list", "--owner-token", "x"},
		{"token", "revoke", "abc", "--addr", "http://127.0.0.1:1"},
	} {
		e := previewEnv(t)
		root := newRoot(e.app)
		root.SetArgs(args)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		if err := root.Execute(); err == nil || err.Error() != "legacy token "+args[1] {
			t.Errorf("%v = %v, want the legacy path", args, err)
		}
	}
}
