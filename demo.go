package main

// The demo command deliberately owns a small, bounded composition layer.  The
// public demo SDK owns the control-plane protocol; this package owns local
// files, process lifetime, and the least-privilege local HTTP surface.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/openvaultdb/openvaultdb-go/demo"
	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
	"github.com/spf13/cobra"
	"github.com/strongo/deviceauth"
)

const demoApp = "listus"
const demoDatabaseID = "demo-sneat-space"
const demoDefaultDir = "~/ovdb/sneat/demo-space"

type demoSessionClient interface {
	CreateSession(context.Context, string, demo.CreateSessionRequest) (demo.Session, error)
	EndSession(context.Context, string, string) error
}

type demoDependencies struct {
	client       func(string, ...demo.Option) (demoSessionClient, error)
	execLookPath func(string) (string, error)
	execCommand  func(context.Context, string, ...string) *exec.Cmd
	openBrowser  func(string) error
	now          func() time.Time
	clone        func(context.Context, string, string) error
	selectStore  func(*url.URL, bool) (cloudStoreSelection, error)
}

func defaultDemoDependencies() demoDependencies {
	return demoDependencies{
		client: func(base string, opts ...demo.Option) (demoSessionClient, error) {
			return demo.NewClient(base, opts...)
		},
		execLookPath: exec.LookPath, execCommand: exec.CommandContext,
		openBrowser: deviceauth.OpenBrowser, now: time.Now,
		clone: cloneDemoSeed,
		selectStore: func(base *url.URL, insecure bool) (cloudStoreSelection, error) {
			return selectCloudStore(base, insecure, os.UserConfigDir)
		},
	}
}

func newDemoCmd() *cobra.Command { return newDemoCmdWithDependencies(defaultDemoDependencies()) }

func newDemoCmdWithDependencies(deps demoDependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "demo", Short: "Run bounded OpenVaultDB application demos"}
	cmd.AddCommand(newDemoServeCmd(deps))
	return cmd
}

func newDemoServeCmd(deps demoDependencies) *cobra.Command {
	var app, dir, addr, host string
	var noOpen, insecure bool
	cmd := &cobra.Command{Use: "serve", Short: "Serve the Listus seed through a time-limited tunnel", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app != demoApp {
				return fmt.Errorf("--app must be %q", demoApp)
			}
			return runListusDemo(cmd.Context(), cmd, deps, dir, addr, host, insecure, noOpen)
		}}
	cmd.Flags().StringVar(&app, "app", demoApp, "application demo to run (only listus is available)")
	cmd.Flags().StringVar(&dir, "dir", demoDefaultDir, "local seed directory (created only when absent)")
	cmd.Flags().StringVar(&addr, "addr", DefaultAddr, "loopback listen address")
	cmd.Flags().StringVar(&host, "host", defaultCloudURL, "OpenVaultDB Cloud base URL")
	cmd.Flags().BoolVar(&insecure, "insecure-storage", false, "use explicitly requested plaintext cloud credential storage")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open Listus after the tunnel is ready")
	return cmd
}

func runListusDemo(ctx context.Context, cmd *cobra.Command, deps demoDependencies, rawDir, addr, rawHost string, insecure, noOpen bool) error {
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if deps.now == nil {
		deps.now = time.Now
	}
	if err := validateDemoAddr(addr); err != nil {
		return err
	}
	base, err := normalizeCloudURL(rawHost)
	if err != nil {
		return err
	}
	dir, err := expandDemoDir(rawDir)
	if err != nil {
		return err
	}
	if err := ensureDemoSeed(ctx, deps.clone, dir); err != nil {
		return err
	}
	manifest := filepath.Join(dir, "ovdb.yaml")
	if err := validateDemoManifest(manifest); err != nil {
		return err
	}
	if _, err := deps.execLookPath("cloudflared"); err != nil {
		return errors.New("cloudflared is required for ovdb demo serve; install Cloudflare cloudflared and try again")
	}

	selection, err := deps.selectStore(base, insecure)
	if err != nil {
		return err
	}
	credential, err := selection.store.Load()
	if errors.Is(err, deviceauth.ErrCredentialNotFound) {
		return fmt.Errorf("not logged in to %s; run %s", base.Host, cloudLoginGuidance(&cloudFlags{host: base.String(), insecureStorage: insecure}))
	}
	if err != nil {
		return cloudStorageError(err)
	}
	if !containsCloudScope(credential.Scopes, "demo:write") {
		return fmt.Errorf("cloud credential lacks demo:write; run 'ovdb cloud login --host %s --scope demo:write' to grant demo access", sanitizeCloudTerminalText(base.String()))
	}
	client, err := deps.client(base.String())
	if err != nil {
		return fmt.Errorf("configure demo client: %w", err)
	}

	started := deps.now().UTC()
	localDeadline := started.Add(time.Hour)
	origin, err := randomDemoSecret()
	if err != nil {
		return err
	}
	requestID, err := randomDemoSecret()
	if err != nil {
		return err
	}
	owner, err := randomDemoSecret()
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "ovdb-demo-")
	if err != nil {
		return err
	}
	if err := os.Chmod(temp, 0o700); err != nil {
		_ = os.RemoveAll(temp)
		return err
	}
	defer os.RemoveAll(temp)
	store, err := auth.OpenStore(filepath.Join(temp, "grants.json"))
	if err != nil {
		return err
	}
	grant := &auth.Grant{PrincipalID: demoApp, DatabaseID: demoDatabaseID, Capabilities: []auth.Capability{{Action: auth.CapRecordsRead}, {Action: auth.CapRecordsWrite}, {Action: auth.CapRecordsDelete}}, ExpiresAt: localDeadline}
	if err := store.CreateGrant(grant, origin); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind demo listener %s: %w", addr, err)
	}
	db, err := mount.File(manifest)
	if err != nil {
		_ = listener.Close()
		return err
	}
	defer db.Close()
	local := restrictedDemoHandler(server.New(appVersion, map[string]*core.Database{demoDatabaseID: db}, server.WithAuth(&auth.Config{OwnerToken: owner, Store: store})).Handler())
	httpServer := &http.Server{Handler: local, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	shutdownLocal := func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(c)
	}

	session, err := client.CreateSession(ctx, credential.AccessToken, demo.CreateSessionRequest{RequestID: requestID, App: demoApp, LocalPort: listener.Addr().(*net.TCPAddr).Port, OriginToken: origin})
	if err != nil {
		shutdownLocal()
		return fmt.Errorf("provision demo session: %w", err)
	}
	if err := validateDemoSession(session, credential.AccountID); err != nil {
		shutdownLocal()
		endDemoSession(client, credential.AccessToken, session.SessionID)
		return err
	}
	deadline := session.ExpiresAt.UTC()
	if deadline.After(localDeadline) {
		deadline = localDeadline
	}
	if !deadline.After(deps.now().UTC()) {
		shutdownLocal()
		endDemoSession(client, credential.AccessToken, session.SessionID)
		return errors.New("cloud returned an expired demo session")
	}
	// No local grant can outlive the cloud session, even if the control plane
	// supplied an earlier expiry than our hard one-hour cap.
	grant.ExpiresAt = deadline
	tokenFile := filepath.Join(temp, "tunnel-token")
	if err := os.WriteFile(tokenFile, []byte(session.TunnelToken), 0o600); err != nil {
		_ = os.RemoveAll(temp)
		shutdownLocal()
		endDemoSession(client, credential.AccessToken, session.SessionID)
		return err
	}
	metricsAddr, err := reserveDemoMetricsAddr()
	if err != nil {
		shutdownLocal()
		endDemoSession(client, credential.AccessToken, session.SessionID)
		return err
	}
	// cloudflared reads a protected file. No token reaches argv, env, output, or URLs.
	// The explicit loopback metrics listener is used only for its /ready signal.
	tunnel := deps.execCommand(ctx, "cloudflared", "tunnel", "--metrics", metricsAddr, "run", "--token-file", tokenFile)
	tunnel.Env = []string{"PATH=" + os.Getenv("PATH")}
	if err := tunnel.Start(); err != nil {
		_ = os.RemoveAll(temp)
		shutdownLocal()
		endDemoSession(client, credential.AccessToken, session.SessionID)
		return fmt.Errorf("start cloudflared tunnel: %w", err)
	}
	connectorExit := make(chan error, 1)
	connectorDone := make(chan struct{})
	go func() { connectorExit <- tunnel.Wait(); close(connectorDone) }()
	cleanup := func() {
		if tunnel.Process != nil {
			_ = tunnel.Process.Signal(os.Interrupt)
			select {
			case <-connectorDone:
			case <-time.After(5 * time.Second):
				_ = tunnel.Process.Kill()
				<-connectorDone
			}
		}
		_ = os.RemoveAll(temp)
		shutdownLocal()
		endDemoSession(client, credential.AccessToken, session.SessionID)
	}
	defer cleanup()
	readyContext, cancelReady := context.WithDeadline(ctx, deadline)
	defer cancelReady()
	if err := waitDemoTunnelReady(readyContext, metricsAddr, connectorExit); err != nil {
		return err
	}
	if !deps.now().UTC().Before(deadline) {
		return errors.New("demo session expired before tunnel became ready")
	}
	if !noOpen {
		if u, e := listusDemoURL(session.SpaceID); e != nil {
			return e
		} else if e = deps.openBrowser(u); e != nil {
			return fmt.Errorf("open Listus: %w", e)
		}
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Listus demo ready. Local changes are retained when the demo ends.")
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-connectorExit:
		if err == nil {
			return errors.New("cloudflared tunnel exited unexpectedly")
		}
		return fmt.Errorf("cloudflared tunnel exited: %w", err)
	}
}

func restrictedDemoHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/databases/"+demoDatabaseID+"/records/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func validateDemoAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return errors.New("--addr must be a loopback host:port")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("--addr must bind only to loopback")
	}
	return nil
}
func expandDemoDir(dir string) (string, error) {
	if dir == demoDefaultDir {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		return filepath.Join(h, "ovdb/sneat/demo-space"), nil
	}
	return filepath.Abs(dir)
}
func ensureDemoSeed(ctx context.Context, clone func(context.Context, string, string) error, dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		return clone(ctx, "https://github.com/openvaultdb/demo-sneat-space.git", dir)
	} else if err != nil {
		return err
	}
	return validateDemoManifest(filepath.Join(dir, "ovdb.yaml"))
}
func cloneDemoSeed(ctx context.Context, repo, dir string) error {
	c := exec.CommandContext(ctx, "git", "clone", repo, dir)
	c.Env = []string{"PATH=" + os.Getenv("PATH")}
	if b, e := c.CombinedOutput(); e != nil {
		return fmt.Errorf("clone demo seed: %s", sanitizeCloudTerminalText(string(b)))
	}
	return nil
}
func validateDemoManifest(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("validate demo manifest: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("validate demo manifest parent: %w", err)
	}
	if resolved != filepath.Join(parent, filepath.Base(path)) {
		return errors.New("demo manifest must not be a symlink")
	}
	m, err := manifest.Load(path)
	if err != nil {
		return fmt.Errorf("validate demo manifest: %w", err)
	}
	if m.Database.ID != demoDatabaseID {
		return fmt.Errorf("demo manifest database id must be %q", demoDatabaseID)
	}
	if m.Storage.Engine != "ingitdb" || m.Storage.Path != "." || m.Storage.InGitDB == nil || m.Storage.InGitDB.PushMode() != "none" || m.Storage.InGitDB.GitHub != nil {
		return errors.New("demo manifest must use local inGitDB storage with push: none")
	}
	return nil
}
func randomDemoSecret() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func validateDemoSession(s demo.Session, accountID string) error {
	if s.SessionID == "" || s.OwnerUserID == "" || s.OwnerUserID != accountID || s.SpaceID == "" || s.DatabaseID != demoDatabaseID || s.SpaceType != "group" || s.ProxyURL == "" || s.AppURL == "" || s.TunnelToken == "" || s.ExpiresAt.IsZero() {
		return errors.New("cloud returned an incomplete demo session")
	}
	u, e := url.ParseRequestURI(s.ProxyURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("cloud returned an invalid proxy URL")
	}
	return nil
}
func listusDemoURL(space string) (string, error) {
	if !isCloudSpaceSelector(space) {
		return "", errors.New("cloud returned an invalid Space ID")
	}
	return "https://listus.app/space/group/" + url.PathEscape(space) + "/lists", nil
}
func endDemoSession(c demoSessionClient, token, id string) {
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.EndSession(ctx, token, id)
}

func reserveDemoMetricsAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("reserve loopback cloudflared metrics listener: %w", err)
	}
	addr := l.Addr().String()
	return addr, l.Close()
}

func waitDemoTunnelReady(ctx context.Context, metricsAddr string, connectorExit <-chan error) error {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	client := &http.Client{Timeout: time.Second}
	readyURL := "http://" + metricsAddr + "/ready"
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-connectorExit:
			return fmt.Errorf("cloudflared tunnel exited before ready: %w", err)
		case <-deadline.C:
			return errors.New("timed out waiting for cloudflared tunnel readiness")
		case <-tick.C:
		}
	}
}
