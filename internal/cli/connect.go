package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// databasesConnectCmd registers an existing inGitDB folder or SQLite file,
// or any engine through its manifest file, without changing anything in the
// storage (capability rows 10 and 10a,
// database-setup-and-providers#REQ:connect-existing-storage,
// REQ:connect-with-manifest).
func (a *App) databasesConnectCmd() *cobra.Command {
	var engine, path, manifestPath string
	var noStart, jsonOut bool
	cmd := &cobra.Command{
		Use:   "connect (<name> --engine <ingitdb or sqlite> --path <absolute path> | --manifest <absolute path>)",
		Short: "Connect an existing inGitDB folder, SQLite file or manifest file",
		Long: `Connect an existing database to OVDB without changing anything in it.

  ovdb databases connect notes --engine ingitdb --path /home/me/notes
  ovdb databases connect shop --engine sqlite --path /home/me/shop.sqlite
  ovdb databases connect --manifest /home/me/crm.yaml

A manifest file can describe any engine OVDB supports (see ovdb engines);
OVDB keeps a copy with its storage paths made absolute. Connection strings
stay in the OVDB server's environment, named by the manifest.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if manifestPath != "" {
				return noArgs(cmd, args)
			}
			return exactArgs(1)(cmd, args)
		},
		RunE: run(func(cmd *cobra.Command, args []string) error {
			request := setup.ConnectRequest{Manifest: manifestPath}
			if manifestPath == "" {
				request = setup.ConnectRequest{ID: args[0], Engine: engine, Path: path}
			} else if cmd.Flags().Changed("engine") || cmd.Flags().Changed("path") {
				return usageError(cmd, uicopy.T("database.connect.manifest_or_path", nil))
			}
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).ConnectDatabase(cmd.Context(), request, noStart)
			if err != nil {
				return err
			}
			var result setup.DatabaseResult
			if err := json.Unmarshal(body, &result); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				db := result.Database
				say(w, uicopy.T("database.connected.title", map[string]string{"name": db.ID}))
				say(w, "")
				say(w, uicopy.T("database.connected.stored", map[string]string{"location": db.Location}))
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				writeNext(w, result.Next)
			})
			return nil
		}),
	}
	cmd.Flags().StringVar(&engine, "engine", setup.EngineInGitDB, "storage: ingitdb or sqlite (other engines connect with --manifest)")
	cmd.Flags().StringVar(&path, "path", "", "absolute location of the existing folder or file")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "absolute path of a manifest file to connect instead")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}
