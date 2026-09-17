package cli

import (
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
)

// where is this process's place in the context ladder: --db (flagDB), then
// OVDB_DATABASE with OVDB_PATH, then the directories a project context is
// looked up in (decision 0008).
func (a *App) where(flagDB string) dbcontext.Request {
	lookup := a.lookup()
	request := dbcontext.Request{Dirs: lookup.Dirs, Root: lookup.Root}
	switch {
	case flagDB != "":
		request.Database, request.Scope = flagDB, dbcontext.ScopeFlag
	case a.getenv(dbcontext.EnvDatabase) != "":
		request.Database, request.Scope = a.getenv(dbcontext.EnvDatabase), dbcontext.ScopeEnvironment
		request.Path = a.getenv(dbcontext.EnvPath)
	}
	return request
}

func (a *App) lookup() dbcontext.Lookup {
	getwd := a.Getwd
	if getwd == nil {
		getwd = os.Getwd
	}
	cwd, err := getwd()
	if err != nil {
		return dbcontext.Lookup{}
	}
	return dbcontext.Find(cwd)
}

// contextFor resolves the context for one command and fails with
// not_found, listing the databases, when no rung supplies one
// (AC:no-context-error). OVDB_PATH without OVDB_DATABASE is ignored with a
// warning on stderr.
func (a *App) contextFor(cmd *cobra.Command, local *client.Local, flagDB string) (dbcontext.Document, error) {
	local.Where = a.where(flagDB)
	if flagDB == "" && a.getenv(dbcontext.EnvDatabase) == "" && a.getenv(dbcontext.EnvPath) != "" {
		say(cmd.ErrOrStderr(), uicopy.T("context.path_ignored", nil))
	}
	body, err := local.Context(cmd.Context())
	if err != nil {
		return dbcontext.Document{}, err
	}
	var document dbcontext.Document
	if err := json.Unmarshal(body, &document); err != nil {
		return document, err
	}
	if document.Context == nil {
		return document, dbcontext.NoContext(document.Databases)
	}
	return document, nil
}

// contextLine is `todo:/lists (project context from /p/a)`.
func contextLine(c dbcontext.Context) string {
	return c.Database + ":" + c.Path + " (" + dbcontext.Source(c) + ")"
}

func (a *App) useCmd() *cobra.Command {
	var global, clear, noStart, jsonOut bool
	cmd := &cobra.Command{
		Use:   "use [database]",
		Short: "Choose the database this project works with (or show it)",
		Long: "Choose the database commands use in this project: the Git working tree you are in, " +
			"or this directory outside Git. --global sets the default for all projects. " +
			"Without a database it shows which one applies here and where that came from.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exactArgs(1)(cmd, args)
			}
			return nil
		},
		RunE: run(func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			out := printer{cmd: cmd, json: jsonOut}
			if len(args) == 0 && !clear {
				body, err := local.Context(cmd.Context())
				if err != nil {
					return err
				}
				var document dbcontext.Document
				if err := json.Unmarshal(body, &document); err != nil {
					return err
				}
				out.document(body, func(w io.Writer) {
					if document.Context != nil {
						say(w, contextLine(*document.Context))
						return
					}
					none := dbcontext.NoContext(document.Databases)
					say(w, none.Message+". "+none.Reason)
					say(w, "")
					say(w, uicopy.T("problem.what_you_can_do", nil))
					writeNext(w, none.Next)
				})
				return nil
			}
			change := dbcontext.Change{Scope: dbcontext.ScopeProject, Dir: a.lookup().Root, Path: "/", Clear: clear}
			if global {
				change.Scope, change.Dir = dbcontext.ScopeGlobal, ""
			}
			if len(args) == 1 {
				if clear {
					return usageError(cmd, uicopy.T("context.clear_takes_no_database", nil))
				}
				change.Database = args[0]
			}
			body, err := local.SetContext(cmd.Context(), change, noStart)
			if err != nil {
				return err
			}
			var document dbcontext.Document
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			out.document(body, func(w io.Writer) { say(w, document.Message) })
			return nil
		}),
	}
	cmd.Flags().BoolVar(&global, "global", false, "the default for all projects instead of this project")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the project context (or, with --global, the default)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) pwdCmd() *cobra.Command {
	var jsonOut bool
	var db string
	cmd := &cobra.Command{
		Use:   "pwd",
		Short: "Print the current database, path and where they came from",
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
			out := printer{cmd: cmd, json: jsonOut}
			out.document(envelope.Marshal(document), func(w io.Writer) { say(w, contextLine(*document.Context)) })
			return nil
		}),
	}
	cmd.Flags().StringVar(&db, "db", "", "the database, instead of the current one")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) cdCmd() *cobra.Command {
	var noStart, jsonOut bool
	cmd := &cobra.Command{
		Use:   "cd [path]",
		Short: "Move to a path inside the current database",
		Long: "Move to a collection or record path inside the current database. A leading / is absolute; " +
			".. goes up. Collections that don't exist yet are fine: they appear once they hold records.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exactArgs(1)(cmd, args)
			}
			return nil
		},
		RunE: run(func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			document, err := a.contextFor(cmd, local, "")
			if err != nil {
				return err
			}
			current := *document.Context
			input := "/"
			if len(args) == 1 {
				input = args[0]
			}
			base, _ := datapath.Parse(current.Path)
			target, err := datapath.Resolve(base, input)
			if err != nil {
				return err
			}
			change := dbcontext.Change{Database: current.Database, Path: target.String()}
			switch current.Scope {
			case dbcontext.ScopeFlag, dbcontext.ScopeEnvironment:
				return envelope.New(envelope.InvalidArgument, uicopy.T("context.cd_environment", nil)).
					WithNext(envelope.Next{Label: uicopy.T("next.set_ovdb_path", nil), Command: "OVDB_PATH=" + target.String()},
						envelope.Next{Label: uicopy.T("next.use_absolute_paths", nil), Command: "ovdb list " + target.String()})
			case dbcontext.ScopeProject:
				change.Scope, change.Dir = dbcontext.ScopeProject, current.Dir
			case dbcontext.ScopeGlobal:
				change.Scope = dbcontext.ScopeGlobal
			default: // the only database: remember it for this project
				change.Scope, change.Dir = dbcontext.ScopeProject, a.lookup().Root
			}
			op := client.DataOp{Verb: "list", Database: current.Database, Path: target}
			empty, err := isEmpty(cmd, local, op, noStart)
			if err != nil {
				return err
			}
			body, err := local.SetContext(cmd.Context(), change, noStart)
			if err != nil {
				return err
			}
			var changed dbcontext.Document
			if err := json.Unmarshal(body, &changed); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, contextLine(*changed.Context))
				if empty {
					say(w, uicopy.T("data.nothing_here", nil))
				}
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}
