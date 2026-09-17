package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Local API paths the CLI calls.
const (
	serverPath     = "/api/local/v1/server"
	statusPath     = "/api/local/v1/status"
	loginLinksPath = "/api/local/v1/login-links"
	configPath     = "/api/local/v1/config"
)

func (a *App) serverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start, stop and check the local OVDB server",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(a.serverStartCmd(), a.serverStopCmd(), a.serverRestartCmd(), a.serverStatusCmd(), a.serverRunCmd())
	return cmd
}

func portFlag(cmd *cobra.Command, port *int) {
	cmd.Flags().IntVar(port, "port", 0, "port (default: OVDB_PORT, then server.port, then 6832)")
}

func jsonFlag(cmd *cobra.Command, jsonOut *bool) {
	cmd.Flags().BoolVar(jsonOut, "json", false, "print the result as JSON")
}

func (a *App) serverStartCmd() *cobra.Command {
	var port int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local OVDB server in the background",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(port)
			if err != nil {
				return err
			}
			return a.start(cmd, t, printer{cmd: cmd, json: jsonOut})
		}),
	}
	portFlag(cmd, &port)
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) start(cmd *cobra.Command, t target, p printer) error {
	result, err := runtime.Start(cmd.Context(), a.startOptions(t))
	p.notices(result.Warnings)
	if err != nil {
		return err
	}
	if version := result.State.Whoami.Version; version != a.Version {
		p.notices([]string{client.VersionNotice(version, a.Version)})
	}
	return a.printRunningServer(cmd, p, result.State, result.AlreadyRunning)
}

// printRunningServer prints GET /api/local/v1/server from the server itself.
func (a *App) printRunningServer(cmd *cobra.Command, p printer, state runtime.State, alreadyRunning bool) error {
	response, err := client.New(state).Do(cmd.Context(), http.MethodGet, serverPath, nil)
	if err != nil {
		return err
	}
	var document setup.ServerDocument
	if err := json.Unmarshal(response.Body, &document); err != nil {
		return err
	}
	p.document(response.Body, func(w io.Writer) {
		params := map[string]string{"address": document.Server.Address}
		if alreadyRunning {
			say(w, uicopy.T("server.already_running", params))
		} else {
			say(w, uicopy.T("server.running", params))
		}
		say(w, uicopy.T("server.also_at", map[string]string{"address": document.Server.FallbackAddress}))
		say(w, uicopy.T("server.sign_in_hint", nil))
	})
	return nil
}

func (a *App) serverStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the local OVDB server is running",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			p := printer{cmd: cmd, json: jsonOut}
			state, err := runtime.Inspect(cmd.Context(), t.dirs.Runtime)
			if err != nil {
				return err
			}
			if state.Running {
				if mismatch := runtime.CheckMatch(state.Record, t.dirs, t.port, t.explicit); mismatch != nil {
					return mismatch
				}
				response, err := client.New(state).Do(cmd.Context(), http.MethodGet, serverPath, nil)
				if err != nil {
					return err
				}
				var document setup.ServerDocument
				if err := json.Unmarshal(response.Body, &document); err != nil {
					return err
				}
				p.document(response.Body, func(w io.Writer) { writeServer(w, document.Server) })
				return nil
			}
			server := setup.StoppedServer(t.port, t.dirs)
			p.document(envelope.Marshal(setup.NewServerDocument(server)), func(w io.Writer) {
				writeServer(w, server)
				writeNext(w, []envelope.Next{{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"}})
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func writeServer(w io.Writer, server setup.Server) {
	if server.State != setup.StateRunning {
		say(w, uicopy.T("server.status.not_running", nil))
		return
	}
	say(w, uicopy.T("server.status.running", nil))
	say(w, uicopy.T("server.status.address", map[string]string{"address": server.Address, "fallback": server.FallbackAddress}))
	say(w, uicopy.T("server.status.version", map[string]string{"version": server.Version}))
	if server.StartedAt != nil {
		uptime := time.Since(*server.StartedAt).Truncate(time.Second).String()
		say(w, uicopy.T("server.status.uptime", map[string]string{"uptime": uptime}))
	}
	say(w, uicopy.T("server.status.log", map[string]string{"log": server.Log}))
}

func (a *App) serverStopCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the local OVDB server",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			return a.stop(cmd, t, printer{cmd: cmd, json: jsonOut}, true)
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// stop stops this home's server; print is false inside restart.
func (a *App) stop(cmd *cobra.Command, t target, p printer, print bool) error {
	state, err := runtime.Inspect(cmd.Context(), t.dirs.Runtime)
	if err != nil {
		return err
	}
	if state.Running {
		if mismatch := runtime.CheckMatch(state.Record, t.dirs, 0, false); mismatch != nil {
			return mismatch
		}
	}
	result, err := runtime.Stop(cmd.Context(), t.dirs.Runtime, 0)
	if err != nil || !print {
		return err
	}
	port := t.port
	if result.Record != nil {
		port = result.Record.Port
	}
	document := setup.NewServerDocument(setup.StoppedServer(port, t.dirs))
	p.document(envelope.Marshal(document), func(w io.Writer) {
		if result.WasRunning {
			say(w, uicopy.T("server.stopped", nil))
		} else {
			say(w, uicopy.T("server.stop.not_running", nil))
		}
		say(w, uicopy.T("server.stopped_copy", nil))
	})
	return nil
}

func (a *App) serverRestartCmd() *cobra.Command {
	var port int
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Stop and start the local OVDB server",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(port)
			if err != nil {
				return err
			}
			p := printer{cmd: cmd, json: jsonOut}
			if err := a.stop(cmd, t, p, false); err != nil {
				return err
			}
			return a.start(cmd, t, p)
		}),
	}
	portFlag(cmd, &port)
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// serverRunCmd is the detached server process itself. It is always hidden:
// people and agents use start, stop and restart.
func (a *App) serverRunCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:    "run",
		Short:  "Run the local OVDB server in the foreground (used by ovdb server start)",
		Hidden: true,
		Args:   noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(port)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return localserver.Run(ctx, localserver.RunOptions{
				Dirs: t.dirs, Port: t.port, Version: a.Version, Log: cmd.OutOrStdout(),
				FailBeforeReady: a.getenv(EnvStartFault) == "1",
			})
		}),
	}
	portFlag(cmd, &port)
	return cmd
}
