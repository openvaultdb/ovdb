package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/joinexec"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
)

// postgresPreviewHelp is the one statement of what a PostgreSQL or MySQL mount
// does with a structured query. `ovdb serve --help`, `ovdb init --help` and the
// README carry it word for word (a test holds them equal). The switch is read by
// openvaultdb-go when a mount opens, in the environment of the server process
// that mounts the database: ovdb serve, or the local server that
// `ovdb databases connect` starts. ovdb never sets it.
const postgresPreviewHelp = "PostgreSQL queries are a preview, and the preview is off by default. A manifest mount with engine: postgres answers structured queries (/query and /dtql) only when the environment of the server that mounts it (ovdb serve, or the local server that ovdb databases connect uses) holds " +
	core.PreviewPostgresQueriesEnv + "=1 when the mount opens; without it the mount refuses them with 501 query_unsupported and the driver is not called, and so do ovdb list and the web console's browse, which read records through those routes. " +
	"Key reads and writes are unaffected. With the switch on, a relational document is still refused on a PostgreSQL mount with 422 join_engine_unsupported, because postgres is not among the join engines, which neither server lists: every document on /v1/dtql is relational, and on /v1/databases/{db}/dtql so is a join, grouping, aggregate, alias or subquery. " +
	"On /v1/databases/{db}/dtql a document of one plain collection is answered as before. MySQL mounts refuse structured queries whatever the switch says."

// queryLimitsHelp names the limits the server applies to a query. Every number
// in it is read from openvaultdb-go's own values, so a release of the library
// that moves one moves the text (and the README, which a test holds equal).
var queryLimitsHelp = queryLimitsText(server.DefaultQueryLimits(), server.DefaultSnapshotLimits(), core.RelationalBounds())

// queryLimitsText states the limits a server built without limit options
// applies. ovdb serve and the local server run openvaultdb-go with its
// defaults, and ovdb has no flag for any of them.
func queryLimitsText(q server.QueryLimits, snap server.SnapshotLimits, b core.ProfileBounds) string {
	return fmt.Sprintf("Query limits: a relational query (join, grouping, aggregate, subquery) reads at most %d sources with %d levels of subquery, takes limit up to %s and offset up to %s, and answers at most %s rows and %s. "+
		"A request runs for at most %d seconds and reads at most %s rows and %s from its sources; a join read in memory holds at most %s rows and %s, and a grouping %s groups. "+
		"At most %d in-memory and %d database-side queries run at once, and a paged /dtql snapshot is at most %s and %s rows, %d at a time. "+
		"GET /.well-known/openvaultdb lists the per-request limits as query.limits; the concurrency and snapshot limits are not listed there. "+
		"ovdb serve and the local server run with these defaults and have no flag to change them.",
		b.MaxSources, b.MaxSubqueryDepth, grouped(int64(b.MaxLimit)), grouped(int64(b.MaxOffset)), grouped(int64(joinexec.MaxResultRows)), mebibytes(joinexec.MaxResultBytes),
		int(q.Timeout.Seconds()), grouped(int64(q.MaxSourceRows)), mebibytes(q.MaxSourceBytes), grouped(int64(joinexec.MaxInMemoryJoinRows)), mebibytes(joinexec.MaxInMemoryJoinBytes), grouped(int64(joinexec.MaxInMemoryGroups)),
		q.InMemory, q.Database, mebibytes(snap.Bytes), grouped(int64(snap.Rows)), snap.Slots)
}

// grouped writes n with a comma between each group of three digits (1,000).
func grouped(n int64) string {
	digits := strconv.FormatInt(n, 10)
	var out []byte
	for i := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digits[i])
	}
	return string(out)
}

// mebibytes writes a byte count as MiB, as the library's limits are all whole
// numbers of them.
func mebibytes(n int64) string { return grouped(n>>20) + " MiB" }

// serveDeps are the two parts of `ovdb serve` that a test replaces: how one
// manifest file becomes a mounted database, and what runs the assembled server.
type serveDeps struct {
	mountFile func(path string) (*core.Database, error)
	run       func(cmd *cobra.Command, srv *http.Server) error
	// handler is a synthetic lifecycle seam. Production always uses the
	// checked data server's real handler when this is nil.
	handler func(*server.Server) http.Handler
	// drainTimeout bounds forced handler settlement after run returns.
	// Zero uses five seconds; tests inject a short deterministic deadline.
	drainTimeout time.Duration
}

// defaultServeDeps is what the binary runs with: the real mount and a listener.
func defaultServeDeps() serveDeps {
	return serveDeps{mountFile: mount.File, run: serveUntilSignal}
}

func newServeCmd() *cobra.Command { return newServeCmdWith(defaultServeDeps()) }

func newServeCmdWith(deps serveDeps) *cobra.Command {
	var addr, dir, dataDir, publicURL, serverID, providerProfilesPath string
	var manifests []string
	var authEnabled, readOnly bool
	var ownerToken, authStorePath string
	var corsOrigins []string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the OpenVaultDB API server",
		Long: `Run the OpenVaultDB HTTP API server over databases described by manifests.

Databases are mounted from --manifest files and/or every *.yaml manifest in --dir.
With --data-dir, databases can also be created at runtime (POST /v1/databases);
created databases persist as manifests in the data-dir and are remounted on restart.

` + postgresPreviewHelp + `

` + queryLimitsHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("server-id") {
				if strings.TrimSpace(serverID) == "" || len(serverID) > 256 || !utf8.ValidString(serverID) || strings.ContainsFunc(serverID, unicode.IsControl) {
					return fmt.Errorf("--server-id must be a nonblank identity of at most 256 UTF-8 bytes without control characters")
				}
			}
			var providerProfiles map[string]server.ProviderReadProfile
			if cmd.Flags().Changed("provider-read-profiles") {
				if strings.TrimSpace(providerProfilesPath) == "" {
					return fmt.Errorf("--provider-read-profiles requires an operator-supplied admission JSON file")
				}
				var err error
				providerProfiles, err = loadProviderReadProfiles(providerProfilesPath)
				if err != nil {
					return err
				}
			}
			if publicURL != "" {
				parsed, err := url.Parse(publicURL)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
					return fmt.Errorf("--public-url must be an HTTP(S) origin without credentials, path, query, or fragment")
				}
				publicURL = strings.TrimRight(publicURL, "/")
			}
			dbs := map[string]*core.Database{}
			var dataServer *server.Server
			var lifetimeSignals chan os.Signal
			requests := newServeRequests()
			defer requests.cleanup(func() {
				if dataServer != nil {
					dataServer.CloseSnapshots()
				}
				for _, db := range dbs {
					_ = db.Close()
				}
				if lifetimeSignals != nil {
					signal.Stop(lifetimeSignals)
				}
			})
			if dir != "" {
				mounted, err := mount.Dir(dir)
				if err != nil {
					return err
				}
				dbs = mounted
			}
			for _, path := range manifests {
				db, err := deps.mountFile(path)
				if err != nil {
					return err
				}
				if _, dup := dbs[db.ID()]; dup {
					_ = db.Close()
					return fmt.Errorf("%s: duplicate database id %q", path, db.ID())
				}
				dbs[db.ID()] = db
			}
			if dataDir != "" {
				// Rescan previously created databases (each persisted as a
				// manifest YAML in the data-dir by POST /v1/databases).
				if err := os.MkdirAll(dataDir, 0o755); err != nil {
					return fmt.Errorf("failed to create data dir %s: %w", dataDir, err)
				}
				created, err := mount.Dir(dataDir)
				if err != nil {
					return err
				}
				for id := range created {
					if _, dup := dbs[id]; dup {
						for _, db := range created {
							_ = db.Close()
						}
						return fmt.Errorf("%s: duplicate database id %q (also in --data-dir)", dataDir, id)
					}
				}
				for id, db := range created {
					dbs[id] = db
				}
			}
			if len(dbs) == 0 && dataDir == "" {
				return fmt.Errorf("no databases mounted: provide --manifest files, a --dir with *.yaml manifests, or a --data-dir for runtime-created databases")
			}

			for _, db := range dbs {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mounted %q (engine=%s, schema_mode=%s)\n",
					db.ID(), db.Manifest.Storage.Engine, db.Manifest.Database.SchemaMode)
			}

			var opts []server.Option
			if serverID != "" {
				opts = append(opts, server.WithSourceRights(serverID, nil))
			}
			if publicURL != "" {
				opts = append(opts, server.WithPublicOrigin(publicURL))
			}
			if authEnabled {
				if ownerToken == "" {
					ownerToken = os.Getenv("OVDB_OWNER_TOKEN")
				}
				if ownerToken == "" {
					generated, err := auth.NewToken()
					if err != nil {
						return err
					}
					ownerToken = generated
					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"auth enabled; generated owner token (set --owner-token or OVDB_OWNER_TOKEN to pin):\n  %s\n", ownerToken)
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "auth enabled")
				}
				store, err := auth.OpenStore(authStorePath)
				if err != nil {
					return err
				}
				opts = append(opts, server.WithAuth(&auth.Config{OwnerToken: ownerToken, Store: store}))
			}
			if dataDir != "" {
				opts = append(opts, server.WithDataDir(dataDir))
			}
			if readOnly {
				opts = append(opts, server.WithReadOnly(true))
			}
			if len(corsOrigins) > 0 {
				corsCfg := server.ParseCORSOrigins(corsOrigins)
				if corsCfg != nil {
					opts = append(opts, server.WithCORS(corsCfg))
					// Warn when --cors * is combined with --auth=false on a non-loopback addr.
					if !authEnabled && isNonLoopback(addr) {
						_, _ = fmt.Fprintln(cmd.OutOrStdout(),
							"WARNING: --cors * with auth disabled on a non-loopback address allows any browser origin to read and write all data without credentials")
					}
				}
			}

			var err error
			if providerProfiles != nil {
				opts = append(opts, server.WithProviderReadProfiles(providerProfiles))
			}
			dataServer, err = server.NewChecked(appVersion, dbs, opts...)
			if err != nil {
				return err
			}
			handler := dataServer.Handler()
			if deps.handler != nil {
				handler = deps.handler(dataServer)
			}
			srv := &http.Server{
				Addr:              addr,
				Handler:           requests.wrap(handler),
				ReadHeaderTimeout: 10 * time.Second,
			}
			// Keep repeated interrupt/termination signals from killing the
			// cleanup owner after serveUntilSignal returns its timeout. The
			// ordinary listener subscription still initiates shutdown; this
			// lifetime guard is released only after resources are settled.
			lifetimeSignals = make(chan os.Signal, 1)
			signal.Notify(lifetimeSignals, os.Interrupt, syscall.SIGTERM)
			runErr := deps.run(cmd, srv)
			drainTimeout := deps.drainTimeout
			if drainTimeout <= 0 {
				drainTimeout = 5 * time.Second
			}
			return errors.Join(runErr, requests.drain(drainTimeout))
		},
	}
	cmd.Flags().StringVar(&addr, "addr", DefaultAddr, "listen address")
	cmd.Flags().StringVar(&serverID, "server-id", "", "stable operator-supplied server identity for source-rights evidence (required when manifests declare terms)")
	cmd.Flags().StringVar(&providerProfilesPath, "provider-read-profiles", "", "opt in mounted HTTP instances using an externally admitted JSON map of database id to collection, binding and sourceRight notices; requires aware consumers and --server-id, does not verify artifacts or authorize activation")
	cmd.Flags().StringVar(&publicURL, "public-url", "", "externally reachable HTTP(S) origin for database connection URLs (set behind a reverse proxy)")
	cmd.Flags().StringVar(&dir, "dir", "", "directory with database manifest *.yaml files")
	cmd.Flags().StringArrayVar(&manifests, "manifest", nil, "database manifest file (repeatable)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "directory for runtime-created databases (enables POST /v1/databases)")
	cmd.Flags().BoolVar(&authEnabled, "auth", false, "require bearer tokens (owner token + app connect flow)")
	cmd.Flags().StringVar(&ownerToken, "owner-token", "", "owner bearer token (default: $OVDB_OWNER_TOKEN, else generated)")
	cmd.Flags().StringVar(&authStorePath, "auth-store", "ovdb-auth.json", "path of the persisted app-grants file (tokens stored hashed)")
	cmd.Flags().StringArrayVar(&corsOrigins, "cors", nil,
		"allowed CORS origin (repeatable; comma-separated values accepted).\n"+
			"Examples: --cors https://sneat.app --cors http://localhost:4200\n"+
			"Use --cors '*' to allow any origin (development only — use with caution on public addresses)")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "reject all database and token mutations, including owner-token writes")
	return cmd
}

// serveUntilSignal serves srv until its listener fails or the process is
// interrupted. The interrupt signals are the only part that needs a process;
// serveUntil holds the rest.
func serveUntilSignal(cmd *cobra.Command, srv *http.Server) error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	return serveUntil(cmd.OutOrStdout(), srv.Addr, srv, stop)
}

// stoppableServer is the part of *http.Server that serveUntil uses.
type stoppableServer interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
	Close() error
}

// serveUntil starts srv, says where it listens, and returns when the listener
// fails (with its error) or a value arrives on stop (after a shutdown that
// waits at most 5 seconds for requests in flight).
func serveUntil(out io.Writer, addr string, srv stoppableServer, stop <-chan os.Signal) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	_, _ = fmt.Fprintf(out, "OpenVaultDB %s serving on http://%s\n", appVersion, addr)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		_, _ = fmt.Fprintln(out, "shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Shutdown leaves active connections alive when its deadline
			// expires. Close cancels their request contexts before the command
			// drains its tracked handlers and releases mounted resources.
			return errors.Join(err, srv.Close())
		}
		return nil
	}
}

var errServeHandlersActive = errors.New("HTTP handlers did not settle before the forced drain deadline; resource cleanup is deferred until they exit")

type serveHandlersActiveError struct{ cleaned <-chan struct{} }

func (*serveHandlersActiveError) Error() string     { return errServeHandlersActive.Error() }
func (*serveHandlersActiveError) Unwrap() error     { return errServeHandlersActive }
func (e *serveHandlersActiveError) WaitForCleanup() { <-e.cleaned }

// serveRequests owns admitted handlers and cleanup together. A driver that
// ignores cancellation cannot make bounded shutdown report success or make
// snapshots/databases close beneath its request. Its last exiting handler
// performs any cleanup deferred after the drain deadline.
type serveRequests struct {
	mu       sync.Mutex
	stopping bool
	next     uint64
	active   map[uint64]context.CancelFunc
	idle     chan struct{}
	cleaned  chan struct{}
	pending  []func()
}

func newServeRequests() *serveRequests {
	idle := make(chan struct{})
	close(idle)
	return &serveRequests{active: map[uint64]context.CancelFunc{}, idle: idle, cleaned: make(chan struct{})}
}

func (s *serveRequests) wrap(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "server is stopping", http.StatusServiceUnavailable)
			return
		}
		if len(s.active) == 0 {
			s.idle = make(chan struct{})
		}
		s.next++
		id := s.next
		ctx, cancel := context.WithCancel(r.Context())
		s.active[id] = cancel
		s.mu.Unlock()
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.active, id)
			var cleanup []func()
			if len(s.active) == 0 {
				cleanup, s.pending = s.pending, nil
				close(s.idle)
			}
			s.mu.Unlock()
			for _, release := range cleanup {
				release()
			}
		}()
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *serveRequests) drain(timeout time.Duration) error {
	s.mu.Lock()
	s.stopping = true
	for _, cancel := range s.active {
		cancel()
	}
	idle := s.idle
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return &serveHandlersActiveError{cleaned: s.cleaned}
	}
}

func (s *serveRequests) cleanup(release func()) {
	// Each command supplies its one resource cleanup callback. Its completion,
	// rather than handler-idle alone, permits the executable to exit.
	cleanup := func() {
		release()
		close(s.cleaned)
	}
	s.mu.Lock()
	s.stopping = true
	if len(s.active) != 0 {
		s.pending = append(s.pending, cleanup)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	cleanup()
}

// isNonLoopback reports whether addr (host:port) binds to a non-loopback
// interface.  It is a best-effort heuristic used only for the foot-gun warning.
func isNonLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	return true
}
