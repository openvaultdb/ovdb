package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
)

// exploreCmd is Explore data's intent-first menu (explore-data-handoff
// REQ:intent-first-menu): DataTug CLI or DataTug.app, for the current
// database or --db, naming it, before any file is written.
func (a *App) exploreCmd() *cobra.Command {
	var db string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "explore",
		Short: "Choose where to explore your data: DataTug CLI or DataTug.app",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			document, err := a.contextFor(cmd, local, db)
			if err != nil {
				return err
			}
			database := document.Context.Database
			body, err := local.ExploreMenu(cmd.Context(), database)
			if err != nil {
				return err
			}
			var menu explore.Menu
			if err := json.Unmarshal(body, &menu); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, uicopy.T("explore.question", map[string]string{"database": database}))
				say(w, "")
				say(w, "  "+uicopy.T("explore.menu.datatug_cli", nil))
				say(w, "    "+uicopy.T(menu.DataTugCLIKey, nil))
				say(w, "  "+uicopy.T("explore.menu.datatug_app", nil))
				say(w, "    "+uicopy.T(menu.DataTugAppKey, nil))
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				say(w, "  • "+uicopy.T("explore.menu.datatug_cli", nil)+"   ovdb explore datatug-cli --db "+database)
				say(w, "  • "+uicopy.T("explore.menu.datatug_app", nil)+"   ovdb explore datatug-app --db "+database)
			})
			return nil
		}),
	}
	cmd.Flags().StringVar(&db, "db", "", "the database, instead of the current one")
	jsonFlag(cmd, &jsonOut)
	cmd.AddCommand(a.exploreDataTugCLICmd(), a.exploreDataTugAppCmd())
	return cmd
}

// exploreDataTugCLICmd chooses DataTug CLI
// (REQ:prepare-datatug-cli-connection): checks datatug on PATH, writes the
// four-key descriptor, and prints the environment variables, the token
// command and the exact `datatug query run` command — never a token value.
func (a *App) exploreDataTugCLICmd() *cobra.Command {
	var db, collection string
	var jsonOut, noStart bool
	cmd := &cobra.Command{
		Use:   "datatug-cli",
		Short: "Prepare a DataTug CLI connection to this database",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			ctxDocument, err := a.contextFor(cmd, local, db)
			if err != nil {
				return err
			}
			database := ctxDocument.Context.Database
			body, err := local.PrepareDataTugCLI(cmd.Context(), database, collection, noStart)
			if err != nil {
				return err
			}
			var document explore.DataTugCLI
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				if document.OnPath {
					say(w, uicopy.T("explore.datatug_cli.ready", nil))
				} else {
					say(w, uicopy.T("explore.datatug_cli.missing", nil))
					say(w, "")
					for _, install := range document.InstallCommands {
						say(w, "  "+install)
					}
				}
				say(w, "")
				say(w, uicopy.T("explore.datatug_cli.descriptor_saved", map[string]string{"database": database, "path": document.DescriptorPath}))
				say(w, "")
				// F11 (review-inc-7.md): the token command first — the env
				// vars block's own token line points at it — then the env
				// vars, then the query command.
				say(w, uicopy.T("explore.datatug_cli.token_intro", nil))
				say(w, "  "+document.TokenCommand)
				say(w, "")
				say(w, uicopy.T("explore.datatug_cli.env_vars", nil))
				say(w, document.ShellText)
				say(w, "")
				say(w, uicopy.T("explore.datatug_cli.query_intro", nil))
				say(w, document.QueryCommand)
			})
			return nil
		}),
	}
	cmd.Flags().StringVar(&db, "db", "", "the database, instead of the current one")
	cmd.Flags().StringVar(&collection, "collection", "", "the root collection to query (default: lists)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// exploreDataTugAppCmd chooses DataTug.app (REQ:honest-datatug-app-state): a
// pure local document, no server call, opening DataTug.app in the browser
// unless --print-url.
func (a *App) exploreDataTugAppCmd() *cobra.Command {
	var db string
	var jsonOut, printURL bool
	cmd := &cobra.Command{
		Use:   "datatug-app",
		Short: "Show DataTug.app's current limitation for OVDB databases",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			ctxDocument, err := a.contextFor(cmd, local, db)
			if err != nil {
				return err
			}
			body := local.ExploreDataTugApp(ctxDocument.Context.Database)
			var document explore.DataTugApp
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			opened := !printURL && a.openBrowser(document.URL) == nil
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, uicopy.T("explore.datatug_app.honesty", nil))
				say(w, "")
				if opened {
					say(w, uicopy.T("explore.datatug_app.opened", nil))
				} else {
					say(w, uicopy.T("explore.datatug_app.url_intro", nil))
				}
				say(w, "  "+document.URL)
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				writeNext(w, document.Next)
			})
			return nil
		}),
	}
	cmd.Flags().StringVar(&db, "db", "", "the database, instead of the current one")
	cmd.Flags().BoolVar(&printURL, "print-url", false, "print the URL instead of opening a browser")
	jsonFlag(cmd, &jsonOut)
	return cmd
}
