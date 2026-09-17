package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change OVDB settings",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(a.configGetCmd(), a.configSetCmd())
	return cmd
}

// configGetCmd is a pure read: it asks the running server, or reads
// config.yaml through the same package when none runs.
func (a *App) configGetCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Show a setting (server.port)",
		Args:  exactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if args[0] != setup.KeyServerPort {
				return setup.UnknownConfigKey(args[0])
			}
			body, err := a.configDocument(cmd)
			if err != nil {
				return err
			}
			var document setup.ConfigDocument
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				params := map[string]string{"key": setup.KeyServerPort, "value": strconv.Itoa(document.Config.Server.Port)}
				if document.Config.Server.Port == 0 {
					params["value"] = strconv.Itoa(runtime.DefaultPort)
					say(w, uicopy.T("config.value_default", params))
					return
				}
				say(w, uicopy.T("config.value", params))
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) configDocument(cmd *cobra.Command) ([]byte, error) {
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	state, err := runtime.Inspect(cmd.Context(), dirs.Runtime)
	if err != nil {
		return nil, err
	}
	if state.Running && state.Record.Home == dirs.Home {
		response, err := client.New(state).Do(cmd.Context(), http.MethodGet, configPath, nil)
		return response.Body, err
	}
	config, err := setup.LoadConfig(dirs.Home)
	if err != nil {
		return nil, err
	}
	return envelope.Marshal(setup.NewConfigDocument(config, false)), nil
}

// configSetCmd writes through the running server. With no server running it
// takes the home lock and writes directly instead of auto-starting one: the
// fix offered for a busy port ("ovdb config set server.port N") must work
// while that port keeps the server from starting.
func (a *App) configSetCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a setting (server.port)",
		Args:  exactArgs(2),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			change := setup.ConfigChange{Key: args[0], Value: args[1]}
			body, err := a.applyConfig(cmd, change)
			if err != nil {
				return err
			}
			var document setup.ConfigDocument
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, uicopy.T("config.saved", map[string]string{"key": change.Key, "value": strconv.Itoa(document.Config.Server.Port)}))
				writeNext(w, document.Next)
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) applyConfig(cmd *cobra.Command, change setup.ConfigChange) ([]byte, error) {
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	state, err := runtime.Inspect(cmd.Context(), dirs.Runtime)
	if err != nil {
		return nil, err
	}
	if state.Running {
		if mismatch := runtime.CheckMatch(state.Record, dirs, 0, false); mismatch != nil {
			return nil, mismatch
		}
		response, err := client.New(state).Do(cmd.Context(), http.MethodPut, configPath, change)
		return response.Body, err
	}
	var document setup.ConfigDocument
	err = runtime.WithHomeLock(dirs, func() error {
		document, err = setup.ApplyConfigChange(dirs, change, false)
		return err
	})
	if errors.Is(err, runtime.ErrAlreadyRunning) {
		return nil, client.NotRunning().WithReason(err.Error())
	}
	if err != nil {
		return nil, err
	}
	return envelope.Marshal(document), nil
}
