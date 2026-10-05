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
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/mount"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
)

// postgresPreviewHelp is the one statement of what a PostgreSQL or MySQL mount
// does with a structured query. `ovdb serve --help`, `ovdb init --help` and the
// README carry it word for word (a test holds them equal). The switch is read by
// openvaultdb-go when a mount opens; ovdb never sets it.
const postgresPreviewHelp = "PostgreSQL queries are a preview, and the preview is off by default. A manifest mount with engine: postgres answers structured queries (/query and /dtql) only when the environment of ovdb serve holds " +
	core.PreviewPostgresQueriesEnv + "=1 when the mount opens; without it the mount refuses them with 501 query_unsupported and the driver is not called. " +
	"Key reads and writes are unaffected. Joins, grouping and aggregates also need postgres among the join engines, which ovdb serve does not list, so they answer 422 join_engine_unsupported on a PostgreSQL mount. MySQL mounts refuse structured queries whatever the switch says."

// queryLimitsHelp names the limits the server applies to a query. ovdb serve
// runs openvaultdb-go with its defaults and has no flag for any of them.
const queryLimitsHelp = "Query limits: a relational query (join, grouping, aggregate, subquery) reads at most 8 sources with 4 levels of subquery, and answers at most limit 1000 rows (offset 10000, 8 MiB). " +
	"A request runs for at most 10 seconds and reads at most 100,000 rows and 64 MiB from its sources; a join read in memory holds at most 10,000 rows and 16 MiB, and a grouping 100,000 groups. " +
	"At most 2 in-memory and 4 database-side queries run at once, and a paged /dtql snapshot is at most 512 MiB and 1,000,000 rows, 2 at a time. " +
	"GET /.well-known/openvaultdb lists them as query.limits. ovdb serve runs with these defaults and has no flag to change them."

// serveDeps are the two parts of `ovdb serve` that a test replaces: how one
// manifest file becomes a mounted database, and what runs the assembled server.
type serveDeps struct {
	mountFile func(path string) (*core.Database, error)
	run       func(cmd *cobra.Command, srv *http.Server) error
}

// defaultServeDeps is what the binary runs with: the real mount and a listener.
func defaultServeDeps() serveDeps {
	return serveDeps{mountFile: mount.File, run: serveUntilSignal}
}

func newServeCmd() *cobra.Command { return newServeCmdWith(defaultServeDeps()) }

func newServeCmdWith(deps serveDeps) *cobra.Command {
	var addr, dir, dataDir, publicURL string
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
			if publicURL != "" {
				parsed, err := url.Parse(publicURL)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
					return fmt.Errorf("--public-url must be an HTTP(S) origin without credentials, path, query, or fragment")
				}
				publicURL = strings.TrimRight(publicURL, "/")
			}
			dbs := map[string]*core.Database{}
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
				for id, db := range created {
					if _, dup := dbs[id]; dup {
						return fmt.Errorf("%s: duplicate database id %q (also in --data-dir)", dataDir, id)
					}
					dbs[id] = db
				}
			}
			if len(dbs) == 0 && dataDir == "" {
				return fmt.Errorf("no databases mounted: provide --manifest files, a --dir with *.yaml manifests, or a --data-dir for runtime-created databases")
			}
			defer func() {
				for _, db := range dbs {
					_ = db.Close()
				}
			}()

			for _, db := range dbs {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mounted %q (engine=%s, schema_mode=%s)\n",
					db.ID(), db.Manifest.Storage.Engine, db.Manifest.Database.SchemaMode)
			}

			var opts []server.Option
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

			srv := &http.Server{
				Addr:              addr,
				Handler:           server.New(appVersion, dbs, opts...).Handler(),
				ReadHeaderTimeout: 10 * time.Second,
			}
			return deps.run(cmd, srv)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", DefaultAddr, "listen address")
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
	return serveUntil(cmd.OutOrStdout(), srv.Addr, srv, stop)
}

// stoppableServer is the part of *http.Server that serveUntil uses.
type stoppableServer interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
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
			return err
		}
		return nil
	}
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
