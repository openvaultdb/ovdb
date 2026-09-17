package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// databasesReloadCmd loads a database again from its manifest (after editing
// a SQLite schema, restoring storage or fixing a connection variable), or
// with --all every manifest in the registry folder, including ones put there
// by hand — without restarting the server.
func (a *App) databasesReloadCmd() *cobra.Command {
	var all, noStart, jsonOut bool
	cmd := &cobra.Command{
		Use:   "reload [<name> | --all]",
		Short: "Load a database again from its manifest file",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return noArgs(cmd, args)
			}
			return exactArgs(1)(cmd, args)
		},
		RunE: run(func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			p := printer{cmd: cmd, json: jsonOut}
			if all {
				body, err := local.ReloadAllDatabases(cmd.Context(), noStart)
				if err != nil {
					return err
				}
				var document setup.DatabasesDocument
				if err := json.Unmarshal(body, &document); err != nil {
					return err
				}
				p.document(body, func(w io.Writer) { writeDatabases(w, document) })
				return nil
			}
			body, err := local.ReloadDatabase(cmd.Context(), args[0], noStart)
			if err != nil {
				return err
			}
			var result setup.DatabaseResult
			if err := json.Unmarshal(body, &result); err != nil {
				return err
			}
			p.document(body, func(w io.Writer) {
				db := result.Database
				say(w, uicopy.T("database.reloaded.title", map[string]string{"name": db.ID})+" · "+StateLabel(db.State))
				if db.Reason != "" {
					say(w, uicopy.T("problem.why", map[string]string{"reason": db.Reason}))
				}
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				writeNext(w, result.Next)
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "reload every database, including manifests added by hand")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}
