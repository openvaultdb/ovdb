package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// recorder is this process's telemetry recorder: channel cli, or agent
// under a known agent harness; the CLI never buffers
// (telemetry-consent#REQ:pre-consent-buffer).
func (a *App) recorder(home string) *telemetry.Recorder {
	if a.telemetry == nil || a.telemetry.Home != home {
		a.telemetry = &telemetry.Recorder{
			Home: home, Version: a.Version, Getenv: a.getenv, Environ: a.Environ,
			Key: a.TelemetryKey, Endpoint: a.TelemetryEndpoint,
		}
	}
	return a.telemetry
}

// FlushTelemetry sends this command's events in one batch, bounded by
// telemetry.Timeout; main calls it after the command, success or failure,
// so output and exit code never depend on it
// (REQ:bounded-synchronous-sender).
func (a *App) FlushTelemetry(ctx context.Context) {
	a.telemetry.Flush(ctx)
}

// telemetryCmd is `ovdb telemetry status|enable|disable` (capabilities 23
// and 24).
func (a *App) telemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Show or change anonymous usage statistics (off unless you turn them on)",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(a.telemetryStatusCmd(), a.telemetryEnableCmd(), a.telemetryDisableCmd())
	for _, sub := range cmd.Commands() {
		sub.SetFlagErrorFunc(flagError)
	}
	return cmd
}

func (a *App) telemetryStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether usage statistics are sent, and what they contain",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			document := a.local(cmd, t).TelemetryStatus()
			printer{cmd: cmd, json: jsonOut}.document(envelope.Marshal(document), func(w io.Writer) {
				writeTelemetry(w, document.Telemetry)
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				writeNext(w, document.Next)
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// writeTelemetry is the shared explanation (telemetry-consent Example copy).
func writeTelemetry(w io.Writer, status telemetry.Status) {
	say(w, uicopy.T("telemetry.title", nil))
	say(w, "")
	say(w, uicopy.T("telemetry.status_line", map[string]string{"state": telemetryStateLabel(status.State)}))
	if status.ReasonText != "" {
		say(w, status.ReasonText)
	}
	say(w, "")
	say(w, uicopy.T("telemetry.intro", nil))
	writeCollected(w, status)
	say(w, "")
	say(w, uicopy.T("telemetry.change_any_time", nil))
}

func writeCollected(w io.Writer, status telemetry.Status) {
	say(w, "")
	say(w, uicopy.T("telemetry.collected.title", nil))
	for _, line := range status.Collected {
		say(w, "  • "+line)
	}
	say(w, "")
	say(w, uicopy.T("telemetry.never.title", nil))
	for _, line := range status.NeverCollected {
		say(w, "  • "+line)
	}
}

func telemetryStateLabel(state string) string {
	switch state {
	case telemetry.StateEnabled:
		return uicopy.T("telemetry.state.enabled", nil)
	case telemetry.StateDisabled:
		return uicopy.T("telemetry.state.disabled", nil)
	default:
		return uicopy.T("telemetry.state.not_asked", nil)
	}
}

// telemetryEnableCmd turns telemetry on only as a person's decision
// (REQ:enable-requires-a-person): in a terminal it shows what is collected
// and asks; anywhere else it needs --confirmed-by-user, which an agent may
// pass only to relay the person's own yes (parity exception E6).
func (a *App) telemetryEnableCmd() *cobra.Command {
	var confirmed, jsonOut bool
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Turn on anonymous usage statistics (asks first)",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			if !confirmed {
				out := cmd.OutOrStdout()
				if jsonOut {
					out = cmd.ErrOrStderr()
				}
				status := local.TelemetryStatus().Telemetry
				say(out, uicopy.T("telemetry.intro", nil))
				writeCollected(out, status)
				say(out, "")
				in, isFile := cmd.InOrStdin().(*os.File)
				if jsonOut || a.getenv(EnvNonInteractive) != "" || !isFile || !a.isTerminal(in.Fd()) {
					return setup.TelemetryConfirmationRequired()
				}
				_, _ = io.WriteString(cmd.ErrOrStderr(), uicopy.T("telemetry.enable.confirm", nil))
				answer, _ := bufio.NewReader(in).ReadString('\n')
				if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
					say(cmd.OutOrStdout(), uicopy.T("telemetry.enable.cancelled", nil))
					return nil
				}
			}
			document, err := local.SetTelemetry(cmd.Context(), telemetry.Change{State: telemetry.StateEnabled, ConfirmedByUser: true})
			if err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(envelope.Marshal(document), func(w io.Writer) {
				if document.Changed != nil && !*document.Changed {
					say(w, uicopy.T("telemetry.already_enabled", nil))
				} else {
					say(w, uicopy.T("telemetry.enabled", nil))
				}
				if document.Telemetry.ReasonText != "" {
					say(w, document.Telemetry.ReasonText)
				}
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&confirmed, "confirmed-by-user", false, "the person has agreed; agents pass it only to relay their yes")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) telemetryDisableCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Turn off usage statistics and remove the install id",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			document, err := a.local(cmd, t).SetTelemetry(cmd.Context(), telemetry.Change{State: telemetry.StateDisabled})
			if err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(envelope.Marshal(document), func(w io.Writer) {
				if document.Changed != nil && !*document.Changed {
					say(w, uicopy.T("telemetry.already_disabled", nil))
					return
				}
				say(w, uicopy.T("telemetry.disabled", nil))
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}
