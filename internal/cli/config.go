package cli

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
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

func decodeConfig(body []byte) (setup.ConfigDocument, error) {
	var document setup.ConfigDocument
	err := json.Unmarshal(body, &document)
	return document, err
}

// configGetCmd is a pure read: the running server answers, or config.yaml
// is read when none runs.
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
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).Config(cmd.Context())
			if err != nil {
				return err
			}
			document, err := decodeConfig(body)
			if err != nil {
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

func (a *App) configSetCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a setting (server.port)",
		Args:  exactArgs(2),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			change := setup.ConfigChange{Key: args[0], Value: args[1]}
			body, err := a.local(cmd, t).SetConfig(cmd.Context(), change)
			if err != nil {
				return err
			}
			document, err := decodeConfig(body)
			if err != nil {
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
