package cli

import (
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"

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
		Short: "Show a setting (server.port, server.cors)",
		Args:  exactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if !slices.Contains(setup.Keys, args[0]) {
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
				value, isDefault := configValue(document.Config, args[0])
				params := map[string]string{"key": args[0], "value": value}
				if isDefault {
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

// configValue renders one key's value, and whether it is the default.
func configValue(config setup.Config, key string) (value string, isDefault bool) {
	if key == setup.KeyServerCORS {
		if len(config.Server.CORS) == 0 {
			return uicopy.T("config.cors_none", nil), true
		}
		return strings.Join(config.Server.CORS, ","), false
	}
	if config.Server.Port == 0 {
		return strconv.Itoa(runtime.DefaultPort), true
	}
	return strconv.Itoa(config.Server.Port), false
}

func (a *App) configSetCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a setting (server.port, server.cors)",
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
				value, _ := configValue(document.Config, change.Key)
				params := map[string]string{"key": change.Key, "value": value}
				if document.Changed != nil && !*document.Changed {
					say(w, uicopy.T("config.unchanged", params))
					return
				}
				say(w, uicopy.T("config.saved", params))
				writeNext(w, document.Next)
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}
