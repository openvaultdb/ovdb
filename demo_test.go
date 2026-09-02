package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/openvaultdb/openvaultdb-go/demo"
	"github.com/spf13/cobra"
	"github.com/strongo/deviceauth"
)

type demoMemoryStore struct{ credential deviceauth.Credential }

func (s *demoMemoryStore) Save(c deviceauth.Credential) error   { s.credential = c; return nil }
func (s *demoMemoryStore) Load() (deviceauth.Credential, error) { return s.credential, nil }
func (s *demoMemoryStore) Delete() error                        { return nil }

type demoFakeClient struct {
	request  demo.CreateSessionRequest
	ended    int
	lifetime time.Duration
}

func (c *demoFakeClient) CreateSession(_ context.Context, _ string, r demo.CreateSessionRequest) (demo.Session, error) {
	c.request = r
	lifetime := c.lifetime
	if lifetime == 0 {
		lifetime = time.Minute
	}
	return demo.Session{SessionID: "session", OwnerUserID: "owner", SpaceID: "space", SpaceType: "group", DatabaseID: demoDatabaseID, ExpiresAt: time.Now().Add(lifetime), ProxyURL: "https://proxy.example", AppURL: "https://listus.app", TunnelToken: "synthetic-tunnel-token"}, nil
}
func (c *demoFakeClient) EndSession(context.Context, string, string) error { c.ended++; return nil }

func TestDemoConnectorHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[1] != "-test.run=TestDemoConnectorHelper" {
		return
	}
	var metrics string
	ready := true
	for i, a := range os.Args {
		if a == "--demo-test-not-ready" {
			ready = false
		}
		if a == "--metrics" && i+1 < len(os.Args) {
			metrics = os.Args[i+1]
		}
	}
	if metrics == "" {
		os.Exit(2)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			if !ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})}
	l, err := net.Listen("tcp", metrics)
	if err != nil {
		os.Exit(3)
	}
	_ = s.Serve(l)
}

func TestRunListusDemoHarness(t *testing.T) {
	dir := t.TempDir()
	writeDemoFixture(t, dir)
	before, err := os.ReadFile(filepath.Join(dir, "lists", "lists.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	store := &demoMemoryStore{credential: deviceauth.Credential{AccessToken: "synthetic-cloud", AccountID: "owner", Scopes: []string{"demo:write"}}}
	client := &demoFakeClient{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var captured *exec.Cmd
	var output bytes.Buffer
	deps := defaultDemoDependencies()
	deps.selectStore = func(*url.URL, bool) (cloudStoreSelection, error) { return cloudStoreSelection{store: store}, nil }
	deps.client = func(string, ...demo.Option) (demoSessionClient, error) { return client, nil }
	deps.execLookPath = func(string) (string, error) { return "cloudflared", nil }
	deps.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		captured = exec.CommandContext(ctx, os.Args[0], "-test.run=TestDemoConnectorHelper", "--")
		captured.Args = append(captured.Args, args...)
		return captured
	}
	deps.openBrowser = func(string) error {
		tokenPath := captured.Args[len(captured.Args)-1]
		info, err := os.Stat(tokenPath)
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("token mode %o", info.Mode().Perm())
		}
		parent, err := os.Stat(filepath.Dir(tokenPath))
		if err != nil {
			return err
		}
		if parent.Mode().Perm() != 0o700 {
			return fmt.Errorf("token parent mode %o", parent.Mode().Perm())
		}
		port := client.request.LocalPort
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		for _, token := range []string{"", "wrong", client.request.OriginToken} {
			req, _ := http.NewRequest(http.MethodGet, base+"/v1/databases/"+demoDatabaseID+"/records/lists/do:demo", nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, e := http.DefaultClient.Do(req)
			if e != nil {
				return e
			}
			want := http.StatusUnauthorized
			if token == client.request.OriginToken {
				want = http.StatusOK
			}
			if resp.StatusCode != want {
				return fmt.Errorf("token %q got %d", token, resp.StatusCode)
			}
			_ = resp.Body.Close()
		}
		resp, e := http.Get(base + "/v1/databases")
		if e != nil {
			return e
		}
		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("admin %d", resp.StatusCode)
		}
		_ = resp.Body.Close()
		cancel()
		return nil
	}
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err = runListusDemo(ctx, cmd, deps, dir, "127.0.0.1:0", "http://127.0.0.1", false, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error %v", err)
	}
	if client.ended != 1 {
		t.Fatalf("EndSession=%d", client.ended)
	}
	if captured == nil {
		t.Fatal("connector not started")
	}
	args := strings.Join(captured.Args, " ")
	if strings.Contains(args, "synthetic-tunnel-token") || strings.Contains(strings.Join(captured.Env, " "), "synthetic-tunnel-token") {
		t.Fatal("tunnel token leaked")
	}
	if strings.Contains(output.String(), "synthetic-") {
		t.Fatalf("credential leaked in output %q", output.String())
	}
	tokenPath := captured.Args[len(captured.Args)-1]
	if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token file remains: %v", err)
	}
	if err := syscall.Kill(captured.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("connector is still live: %v", err)
	}
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/databases", client.request.LocalPort)); err == nil {
		t.Fatal("listener remains reachable")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "lists", "lists.yaml"))
	if string(before) != string(after) {
		t.Fatal("seed changed")
	}
}

func TestRunListusDemoExpiresWhileConnectorIsNotReady(t *testing.T) {
	dir := t.TempDir()
	writeDemoFixture(t, dir)
	store := &demoMemoryStore{credential: deviceauth.Credential{AccessToken: "synthetic-cloud", AccountID: "owner", Scopes: []string{"demo:write"}}}
	client := &demoFakeClient{lifetime: 400 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deps := defaultDemoDependencies()
	deps.selectStore = func(*url.URL, bool) (cloudStoreSelection, error) { return cloudStoreSelection{store: store}, nil }
	deps.client = func(string, ...demo.Option) (demoSessionClient, error) { return client, nil }
	deps.execLookPath = func(string) (string, error) { return "cloudflared", nil }
	var connector *exec.Cmd
	deps.execCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		connector = exec.CommandContext(ctx, os.Args[0], "-test.run=TestDemoConnectorHelper", "--", "--demo-test-not-ready")
		connector.Args = append(connector.Args, args...)
		return connector
	}
	opened := false
	deps.openBrowser = func(string) error { opened = true; return nil }
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := runListusDemo(ctx, cmd, deps, dir, "127.0.0.1:0", "http://127.0.0.1", false, false)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("session deadline did not stop readiness: %v (parent: %v)", err, ctx.Err())
	}
	if opened || strings.Contains(output.String(), "demo ready") {
		t.Fatal("unready session opened Listus or announced readiness")
	}
	if client.ended != 1 {
		t.Fatalf("EndSession=%d", client.ended)
	}
	if connector == nil || connector.ProcessState == nil {
		t.Fatal("connector was not started and reaped")
	}
	tokenPath := connector.Args[len(connector.Args)-1]
	if _, err := os.Stat(filepath.Dir(tokenPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token directory remains: %v", err)
	}
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", client.request.LocalPort), time.Second)
	if err == nil {
		_ = connection.Close()
		t.Fatal("local listener remains reachable")
	}
	for _, secret := range []string{"synthetic-cloud", "synthetic-tunnel-token", client.request.OriginToken} {
		if secret != "" && strings.Contains(output.String(), secret) {
			t.Fatal("credential appeared in output")
		}
	}
}

func TestDemoDeadlineClampsActiveHandlerAtEquality(t *testing.T) {
	now := time.Now()
	var expiry atomic.Int64
	expiry.Store(now.Add(time.Hour).UnixNano())
	h := withDemoDeadline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), &expiry, func() time.Time { return now })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("active status %d", w.Code)
	}
	expiry.Store(now.UnixNano())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired status %d", w.Code)
	}
}

func writeDemoFixture(t *testing.T, dir string) {
	t.Helper()
	for p, b := range map[string]string{"ovdb.yaml": "database:\n  id: demo-sneat-space\n  schema_mode: schemaless\nstorage:\n  engine: ingitdb\n  path: .\n  ingitdb:\n    push: none\n", ".ingitdb/root-collections.yaml": "lists: lists\n", "lists/.collection/definition.yaml": "record_file:\n  name: lists.yaml\n  format: yaml\n  type: map[$record_id]map[$field_name]any\ncolumns:\n  title:\n    type: string\n    required: true\n  type:\n    type: string\n    required: true\n", "lists/lists.yaml": "do:demo:\n  title: To do\n  type: do\n"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWaitDemoTunnelReadyWaitsForReady(t *testing.T) {
	ready := make(chan struct{})
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Listener = l
	s.Start()
	defer s.Close()
	go func() { time.Sleep(150 * time.Millisecond); close(ready) }()
	exit := make(chan error)
	if err := waitDemoTunnelReady(context.Background(), s.Listener.Addr().String(), exit); err != nil {
		t.Fatal(err)
	}
}

func TestWaitDemoTunnelReadyReturnsConnectorExitAndCancellation(t *testing.T) {
	exit := make(chan error, 1)
	exit <- errors.New("connector failed")
	if err := waitDemoTunnelReady(context.Background(), "127.0.0.1:1", exit); err == nil {
		t.Fatal("connector exit was ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitDemoTunnelReady(ctx, "127.0.0.1:1", make(chan error)); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestValidateDemoSessionRejectsWrongOwnerAndUnsafeProxy(t *testing.T) {
	s := demo.Session{SessionID: "s", OwnerUserID: "owner", SpaceID: "space", SpaceType: "group", DatabaseID: demoDatabaseID, ExpiresAt: time.Now().Add(time.Minute), ProxyURL: "https://proxy.example", AppURL: "https://listus.app", TunnelToken: "token"}
	if err := validateDemoSession(s, "other"); err == nil {
		t.Fatal("wrong owner accepted")
	}
	s.OwnerUserID = "owner"
	s.ProxyURL = "http://proxy.example"
	if err := validateDemoSession(s, "owner"); err == nil {
		t.Fatal("insecure proxy accepted")
	}
}

func TestValidateDemoManifestRejectsUnsafeBackendsBeforeMount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ovdb.yaml")
	if err := os.WriteFile(path, []byte("database:\n  id: demo-sneat-space\n  schema_mode: schemaless\nstorage:\n  engine: firestore\n  firestore:\n    project: should-not-connect\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDemoManifest(path); err == nil {
		t.Fatal("remote engine accepted")
	}
	if err := os.Symlink(path, filepath.Join(dir, "linked.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := validateDemoManifest(filepath.Join(dir, "linked.yaml")); err == nil {
		t.Fatal("manifest symlink accepted")
	}
}

func TestValidateDemoAddrRejectsPublicAndMalformedAddresses(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:6832", "example.com:6832", "6832", "127.0.0.1"} {
		if err := validateDemoAddr(addr); err == nil {
			t.Errorf("validateDemoAddr(%q) unexpectedly succeeded", addr)
		}
	}
	if err := validateDemoAddr("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
}

func TestListusDemoURLDoesNotAcceptControlCharacters(t *testing.T) {
	if _, err := listusDemoURL("space\nsecret"); err == nil {
		t.Fatal("control character accepted")
	}
	got, err := listusDemoURL("space_1")
	if err != nil || got != "https://listus.app/space/group/space_1/lists" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRestrictedDemoHandlerDoesNotExposeAdminOrAuth(t *testing.T) {
	h := restrictedDemoHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/authorize", "/token", "/v1/tokens", "/v1/databases", "/v1/databases/other/records/x", "/v1/databases/demo-sneat-space/query", "/v1/databases/demo-sneat-space/batch"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/databases/demo-sneat-space/records/lists/do:demo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("allowed record route got %d", w.Code)
	}
}
