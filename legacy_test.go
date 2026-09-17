package main

// legacy_test.go is a black-box safety net for Task 9 ("Legacy safety net and
// openvaultdb-go bump"): it exercises the built `ovdb` binary over loopback
// HTTP exactly the way a real caller would, so that bumping the
// openvaultdb-go dependency (a separate commit) cannot silently change
// today's observable behavior for legacy manifests (no access policies, no
// OVDB_PREVIEW gate).
//
// It verifies:
//   - local-server-and-web-console#ac:legacy-serve-unchanged
//   - database-setup-and-providers#ac:legacy-create-still-works
//   - first-run-onboarding#ac:status-unchanged-without-gate
//
// All tests build the binary once (TestMain) and run every server under a
// random loopback port, polling /v1/status for readiness and always killing
// the process in t.Cleanup.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ovdbBinPath is the path of the `ovdb` binary built once in TestMain and
// shared read-only by every test in this package.
var ovdbBinPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ovdb-legacy-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "legacy_test: mkdir temp:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ovdbBinPath = filepath.Join(dir, "ovdb")
	if runtime.GOOS == "windows" {
		ovdbBinPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", ovdbBinPath, ".")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "legacy_test: build ovdb:", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// freeLoopbackAddr reserves a free TCP port on 127.0.0.1 by binding and
// immediately closing a listener, then returns the address for a caller
// (typically a separately-started process) to bind next. This is
// best-effort (there is a race between close and the child process's own
// bind) but is the standard approach for test harnesses that must pick a
// port for another process's --addr/-l flag.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close port probe listener: %v", err)
	}
	return addr
}

// syncBuffer is an io.Writer safe for concurrent use by the child process's
// stdout/stderr pump and the test goroutine reading it for diagnostics.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runningServer is a started `ovdb serve` (or similar long-running command)
// under test.
type runningServer struct {
	baseURL string
	output  *syncBuffer
}

// startServer starts the ovdb binary with args in dir (with additional env
// vars appended to the current environment), waits for /v1/status to answer
// on addr, and arranges for the process to be killed in t.Cleanup. addr must
// be the loopback address passed to the child via one of its --addr/--url
// flags in args. readyBearer is the bearer token used while polling for
// readiness — required when the server is started with --auth, since
// /v1/status is not a public path once auth is enabled (empty for a server
// started without --auth).
func startServer(t *testing.T, dir, addr string, args []string, env []string, readyBearer string) *runningServer {
	t.Helper()
	cmd := exec.Command(ovdbBinPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ovdb %v: %v", args, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	baseURL := "http://" + addr
	waitForReady(t, baseURL, readyBearer, out)
	return &runningServer{baseURL: baseURL, output: out}
}

// waitForReady polls GET /v1/status (with bearer, when non-empty) until it
// answers 200 or the deadline passes, in which case the test fails with the
// process's captured output for diagnostics.
func waitForReady(t *testing.T, baseURL, bearer string, out *syncBuffer) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/status", nil)
		if err != nil {
			t.Fatalf("build readiness request: %v", err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready; output so far:\n%s", baseURL, out.String())
}

// doJSON performs an HTTP request with an optional bearer token and JSON
// body, returning the status code and raw response body.
func doJSON(t *testing.T, method, url, bearer string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, b
}

// writeManifest writes a manifest YAML file at dir/name.
func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest %s: %v", path, err)
	}
	return path
}

// initGitRepo git-inits dir with a local identity, so the inGitDB engine
// (which commits every write) can build a commit without relying on any
// ambient git identity of the test host.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "config", "user.email", "legacy-test@example.com"},
		{"-C", dir, "config", "user.name", "legacy-test"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

const ingitdbManifest = `database:
  id: todo
  schema_mode: schemaless

storage:
  engine: ingitdb
  path: ./data
`

const sqliteManifest = `database:
  id: crm
  schema_mode: strict

storage:
  engine: sqlite
  path: ./crm.sqlite

schemas:
  collections:
    items:
      fields:
        title: {type: string, required: true}
`

// TestLegacyServeIngitdbCRUD is the AC:legacy-serve-unchanged happy path
// against a schemaless inGitDB-backed database: serve --manifest, then
// create/read/delete a record over the documented /v1/databases/{db}/records
// API, exactly as today.
func TestLegacyServeIngitdbCRUD(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeManifest(t, dir, "todo.yaml", ingitdbManifest)

	addr := freeLoopbackAddr(t)
	srv := startServer(t, dir, addr, []string{"serve", "--manifest", "todo.yaml", "--addr", addr}, nil, "")

	if status, body := doJSON(t, http.MethodPut, srv.baseURL+"/v1/databases/todo/records/items/1", "",
		map[string]any{"data": map[string]any{"title": "buy milk"}}); status != http.StatusNoContent {
		t.Fatalf("PUT record: status=%d body=%s", status, body)
	}
	status, body := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/todo/records/items/1", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET record: status=%d body=%s", status, body)
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal GET response: %v", err)
	}
	if got.Data["title"] != "buy milk" {
		t.Errorf("record title = %v, want %q", got.Data["title"], "buy milk")
	}

	if status, body := doJSON(t, http.MethodPatch, srv.baseURL+"/v1/databases/todo/records/items/1", "",
		map[string]any{"updates": []map[string]any{{"fieldName": "title", "value": "buy bread"}}}); status != http.StatusNoContent {
		t.Fatalf("PATCH record: status=%d body=%s", status, body)
	}
	if status, body := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/todo/records/items/1", "", nil); status != http.StatusOK || !strings.Contains(string(body), "buy bread") {
		t.Fatalf("GET after PATCH: status=%d body=%s", status, body)
	}

	if status, body := doJSON(t, http.MethodDelete, srv.baseURL+"/v1/databases/todo/records/items/1", "", nil); status != http.StatusNoContent {
		t.Fatalf("DELETE record: status=%d body=%s", status, body)
	}
	if status, _ := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/todo/records/items/1", "", nil); status != http.StatusNotFound {
		t.Fatalf("GET after DELETE: status=%d, want 404", status)
	}
}

// TestLegacyServeSQLiteCRUD is the AC:legacy-serve-unchanged happy path
// against a strict SQLite-backed database with a declared collection schema.
func TestLegacyServeSQLiteCRUD(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "crm.yaml", sqliteManifest)

	addr := freeLoopbackAddr(t)
	srv := startServer(t, dir, addr, []string{"serve", "--manifest", "crm.yaml", "--addr", addr}, nil, "")

	if status, body := doJSON(t, http.MethodPost, srv.baseURL+"/v1/databases/crm/records/items/1", "",
		map[string]any{"data": map[string]any{"title": "buy milk"}}); status != http.StatusCreated {
		t.Fatalf("POST insert: status=%d body=%s", status, body)
	}
	status, body := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/crm/records/items/1", "", nil)
	if status != http.StatusOK || !strings.Contains(string(body), "buy milk") {
		t.Fatalf("GET record: status=%d body=%s", status, body)
	}

	if status, body := doJSON(t, http.MethodPatch, srv.baseURL+"/v1/databases/crm/records/items/1", "",
		map[string]any{"updates": []map[string]any{{"fieldName": "title", "value": "buy bread"}}}); status != http.StatusNoContent {
		t.Fatalf("PATCH record: status=%d body=%s", status, body)
	}
	if status, body := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/crm/records/items/1", "", nil); status != http.StatusOK || !strings.Contains(string(body), "buy bread") {
		t.Fatalf("GET after PATCH: status=%d body=%s", status, body)
	}

	if status, body := doJSON(t, http.MethodDelete, srv.baseURL+"/v1/databases/crm/records/items/1", "", nil); status != http.StatusNoContent {
		t.Fatalf("DELETE record: status=%d body=%s", status, body)
	}
}

// TestLegacyServeHostHeaderAndRootAreUnchanged records today's behavior for
// AC:legacy-serve-unchanged's Host-header and "/" clauses: without
// OVDB_PREVIEW, a request carrying an arbitrary Host header (as a caller
// behind a tunnel/proxy would send) is answered exactly like one with the
// real Host — there is no Host allowlist in the legacy code path — and "/"
// is not a console (no route is registered for it, so it 404s).
func TestLegacyServeHostHeaderAndRootAreUnchanged(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeManifest(t, dir, "todo.yaml", ingitdbManifest)

	addr := freeLoopbackAddr(t)
	srv := startServer(t, dir, addr, []string{"serve", "--manifest", "todo.yaml", "--addr", addr}, nil, "")

	req, err := http.NewRequest(http.MethodGet, srv.baseURL+"/v1/databases", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "tunnel.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/databases with foreign Host: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/databases with Host: tunnel.example: status=%d body=%s (today's behavior has no Host allowlist)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"todo"`) {
		t.Errorf("databases list with foreign Host missing todo: %s", body)
	}

	status, _ := doJSON(t, http.MethodGet, srv.baseURL+"/", "", nil)
	if status != http.StatusNotFound {
		t.Errorf("GET / status = %d, want 404 (no console route registered for legacy serve)", status)
	}
}

// TestLegacyServeBareNoDatabasesError is AC:legacy-serve-unchanged's third
// clause: bare `ovdb serve` (no --manifest/--dir/--data-dir) fails with
// today's exact error message and a nonzero exit code.
func TestLegacyServeBareNoDatabasesError(t *testing.T) {
	dir := t.TempDir()
	addr := freeLoopbackAddr(t)
	cmd := exec.Command(ovdbBinPath, "serve", "--addr", addr)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bare `ovdb serve` succeeded; output:\n%s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("bare `ovdb serve` failed to start: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
	}
	// fang renders the error inside a box word-wrapped to the terminal
	// width; since this runs with stdout/stderr piped (no TTY) that wrap
	// still happens at a fixed width and can break the message mid-word, so
	// whitespace (including the line break) is collapsed before comparing.
	const want = "no databases mounted: provide --manifest files, a --dir with *.yaml manifests, or a --data-dir for runtime-created databases"
	normalized := strings.ToLower(strings.Join(strings.Fields(string(out)), " "))
	if !strings.Contains(normalized, want) {
		t.Errorf("output = %q (normalized: %q), want it to contain %q", out, normalized, want)
	}
}

// TestLegacyAuthTokenLifecycle is AC:legacy-serve-unchanged's auth clause:
// with --auth, a read-only token created via `ovdb token create` can read
// but not write, `token list` shows it, and after `token revoke` it is
// rejected outright.
func TestLegacyAuthTokenLifecycle(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeManifest(t, dir, "todo.yaml", ingitdbManifest)

	addr := freeLoopbackAddr(t)
	const ownerToken = "ovdb_test_owner_token_for_legacy_safety_net"
	srv := startServer(t, dir, addr,
		[]string{"serve", "--manifest", "todo.yaml", "--addr", addr, "--auth", "--owner-token", ownerToken}, nil, ownerToken)

	// Seed a record as the owner so there is something to read.
	if status, body := doJSON(t, http.MethodPut, srv.baseURL+"/v1/databases/todo/records/items/x", ownerToken,
		map[string]any{"data": map[string]any{"title": "seed"}}); status != http.StatusNoContent {
		t.Fatalf("seed record as owner: status=%d body=%s", status, body)
	}

	createOut := runCLI(t, dir, "token", "create", "--db", "todo", "--scope", "read-only",
		"--addr", srv.baseURL, "--owner-token", ownerToken, "--json")
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(createOut), &created); err != nil {
		t.Fatalf("unmarshal `token create --json` output %q: %v", createOut, err)
	}
	if created.ID == "" || created.Token == "" {
		t.Fatalf("token create returned empty id/token: %q", createOut)
	}

	listOut := runCLI(t, dir, "token", "list", "--addr", srv.baseURL, "--owner-token", ownerToken)
	if !strings.Contains(listOut, created.ID) {
		t.Errorf("`token list` output missing created token id %q:\n%s", created.ID, listOut)
	}

	if status, body := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/todo/records/items/x", created.Token, nil); status != http.StatusOK {
		t.Fatalf("read-only token GET: status=%d body=%s, want 200 (read allowed)", status, body)
	}
	if status, _ := doJSON(t, http.MethodPut, srv.baseURL+"/v1/databases/todo/records/items/x", created.Token,
		map[string]any{"data": map[string]any{"title": "should be denied"}}); status != http.StatusForbidden {
		t.Fatalf("read-only token PUT: status=%d, want 403 (write denied)", status)
	}

	revokeOut := runCLI(t, dir, "token", "revoke", created.ID, "--addr", srv.baseURL, "--owner-token", ownerToken)
	if !strings.Contains(revokeOut, "revoked") {
		t.Errorf("`token revoke` output = %q, want it to mention the token was revoked", revokeOut)
	}

	if status, _ := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases/todo/records/items/x", created.Token, nil); status != http.StatusUnauthorized {
		t.Errorf("revoked token GET: status=%d, want 401 (revoked token rejected)", status)
	}
}

// runCLI runs a short-lived `ovdb` subcommand (not `serve`) in dir and
// returns its combined stdout+stderr, trimmed. Cobra's Println family
// writes to OutOrStderr, so callers must not assume JSON/table output is on
// stdout alone.
func runCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(ovdbBinPath, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ovdb %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestLegacyDatabasesCreateAgainstRunningServer is
// AC:legacy-create-still-works's `ovdb databases create --addr` clause: with
// the server started `--data-dir`, the CLI creates the database over
// POST /v1/databases as today.
func TestLegacyDatabasesCreateAgainstRunningServer(t *testing.T) {
	dir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, dir, addr, []string{"serve", "--data-dir", "./data", "--addr", addr}, nil, "")

	out := runCLI(t, dir, "databases", "create", "notes", "--addr", srv.baseURL, "--owner-token", "unused-because-auth-is-disabled")
	if !strings.Contains(out, "Database created successfully.") || !strings.Contains(out, "notes") {
		t.Fatalf("`databases create` output = %q", out)
	}

	if _, err := os.Stat(filepath.Join(dir, "data", "notes.yaml")); err != nil {
		t.Errorf("expected persisted manifest data/notes.yaml: %v", err)
	}

	status, body := doJSON(t, http.MethodGet, srv.baseURL+"/v1/databases", "", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"notes"`) {
		t.Fatalf("GET /v1/databases after create: status=%d body=%s", status, body)
	}
}

// TestLegacyStatusCommand is AC:status-unchanged-without-gate's `ovdb
// status` clause: without OVDB_PREVIEW, `ovdb status --url` calls the
// server exactly as today and prints its JSON status.
func TestLegacyStatusCommand(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeManifest(t, dir, "todo.yaml", ingitdbManifest)

	addr := freeLoopbackAddr(t)
	srv := startServer(t, dir, addr, []string{"serve", "--manifest", "todo.yaml", "--addr", addr}, nil, "")

	out := runCLI(t, dir, "status", "--url", srv.baseURL)
	var got struct {
		Name      string   `json:"name"`
		Databases []string `json:"databases"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal `ovdb status` output %q: %v", out, err)
	}
	if got.Name != "OpenVaultDB" {
		t.Errorf("status name = %q, want OpenVaultDB", got.Name)
	}
	if len(got.Databases) != 1 || got.Databases[0] != "todo" {
		t.Errorf("status databases = %v, want [todo]", got.Databases)
	}

	dbOut := runCLI(t, dir, "databases", "--url", srv.baseURL)
	if !strings.Contains(dbOut, `"todo"`) {
		t.Errorf("`ovdb databases` output missing todo: %s", dbOut)
	}
}
