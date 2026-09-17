package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup"
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

func decodeServer(body []byte) (setup.Server, error) {
	var document setup.ServerDocument
	err := json.Unmarshal(body, &document)
	return document.Server, err
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
			outcome, err := a.local(cmd, t).Start(cmd.Context())
			if err != nil {
				return err
			}
			return printStarted(printer{cmd: cmd, json: jsonOut}, outcome.Body, outcome.AlreadyRunning)
		}),
	}
	portFlag(cmd, &port)
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func printStarted(p printer, body []byte, alreadyRunning bool) error {
	server, err := decodeServer(body)
	if err != nil {
		return err
	}
	p.document(body, func(w io.Writer) {
		params := map[string]string{"address": server.Address}
		if alreadyRunning {
			say(w, uicopy.T("server.already_running", params))
		} else {
			say(w, uicopy.T("server.running", params))
		}
		say(w, uicopy.T("server.also_at", map[string]string{"address": server.FallbackAddress}))
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
			body, err := a.local(cmd, t).Server(cmd.Context())
			if err != nil {
				return err
			}
			server, err := decodeServer(body)
			if err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				writeServer(w, server)
				if server.State != setup.StateRunning {
					writeNext(w, []envelope.Next{{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"}})
				}
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
			outcome, err := a.local(cmd, t).Stop(cmd.Context())
			if err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(outcome.Body, func(w io.Writer) {
				if outcome.WasRunning {
					say(w, uicopy.T("server.stopped", nil))
				} else {
					say(w, uicopy.T("server.stop.not_running", nil))
				}
				say(w, uicopy.T("server.stopped_copy", nil))
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
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
			outcome, err := a.local(cmd, t).Restart(cmd.Context())
			if err != nil {
				return err
			}
			return printStarted(printer{cmd: cmd, json: jsonOut}, outcome.Body, false)
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
