package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/setup"
)

func (a *App) enginesCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "engines",
		Short: "List where OVDB can keep your data",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).Engines(cmd.Context())
			if err != nil {
				return err
			}
			var document setup.EnginesDocument
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, uicopy.T("engines.title", nil))
				say(w, "")
				writeEngines(w, document.Engines)
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// writeEngines prints the catalogue: pinned engines, a divider, the rest
// with their manifest steps.
func writeEngines(w io.Writer, engines []setup.Engine) {
	width := 0
	for _, engine := range engines {
		width = max(width, len([]rune(engine.Name)))
	}
	pinned := true
	for _, engine := range engines {
		if pinned && !engine.Pinned {
			pinned = false
			say(w, "")
			say(w, uicopy.T("engines.more", nil)+":")
		}
		say(w, "  "+engine.Name+strings.Repeat(" ", width-len([]rune(engine.Name)))+"   "+engine.Description)
		if engine.Note != "" {
			say(w, "  "+strings.Repeat(" ", width)+"   "+engine.Note)
		}
	}
	for _, engine := range engines {
		if engine.Setup == setup.SetupManifest {
			say(w, "")
			say(w, uicopy.T("engine.manifest.title", nil)+" ("+engine.Name+"):")
			writeNext(w, engine.ManifestSteps)
			return // the steps are the same for every manifest engine but the id
		}
	}
}

// DatabasesPreview adds the preview behaviour to the legacy `ovdb
// databases` and `ovdb databases create` commands: with OVDB_PREVIEW=1 they
// use the local OVDB server, unless --url (list) or --addr (create) asks
// for today's behaviour (database-setup-and-providers#REQ:list-and-remove,
// REQ:legacy-create-compatible). Without the gate nothing changes, not even
// the flags help shows.
func (a *App) DatabasesPreview(list, create *cobra.Command) {
	if !preview.On() {
		return
	}
	var listJSON bool
	legacyList := list.RunE
	list.Flags().BoolVar(&listJSON, "json", false, "print the databases as JSON")
	list.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("url") {
			return legacyList(cmd, args)
		}
		return a.listDatabases(cmd, listJSON)
	}

	var engine, path string
	var noStart bool
	legacyCreate := create.RunE
	create.Flags().StringVar(&engine, "engine", setup.EngineInGitDB, "storage: ingitdb or sqlite (see ovdb engines)")
	create.Flags().StringVar(&path, "path", "", "absolute location (default: under OVDB_DATA_HOME, or ~/ovdb)")
	create.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	create.Args = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("addr") {
			return cobra.ExactArgs(1)(cmd, args)
		}
		return exactArgs(1)(cmd, args)
	}
	create.SetFlagErrorFunc(flagError)
	create.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("addr") {
			return legacyCreate(cmd, args)
		}
		jsonOut, _ := cmd.Flags().GetBool("json")
		return a.createDatabase(cmd, setup.CreateRequest{ID: args[0], Engine: engine, Path: path}, noStart, jsonOut)
	}
}

func (a *App) listDatabases(cmd *cobra.Command, jsonOut bool) error {
	return run(func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).Databases(cmd.Context())
		if err != nil {
			return err
		}
		var document setup.DatabasesDocument
		if err := json.Unmarshal(body, &document); err != nil {
			return err
		}
		printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
			writeDatabases(w, document)
		})
		return nil
	})(cmd, nil)
}

func writeDatabases(w io.Writer, document setup.DatabasesDocument) {
	if len(document.Databases) == 0 {
		say(w, uicopy.T("databases.empty", nil))
	} else {
		say(w, uicopy.T("databases.title", nil))
		say(w, "")
		idWidth, engineWidth, locationWidth := 0, 0, 0
		for _, db := range document.Databases {
			idWidth = max(idWidth, len([]rune(db.ID)))
			engineWidth = max(engineWidth, len([]rune(EngineName(db.Engine))))
			locationWidth = max(locationWidth, len([]rune(db.Location)))
		}
		pad := func(s string, width int) string { return s + strings.Repeat(" ", width-len([]rune(s))) }
		for _, db := range document.Databases {
			say(w, "  "+pad(db.ID, idWidth)+"   "+pad(EngineName(db.Engine), engineWidth)+"   "+pad(db.Location, locationWidth)+"   "+StateLabel(db.State))
			if db.Reason != "" {
				say(w, "  "+strings.Repeat(" ", idWidth)+"   "+uicopy.T("problem.why", map[string]string{"reason": db.Reason}))
			}
		}
	}
	say(w, "")
	say(w, uicopy.T("problem.what_you_can_do", nil))
	writeNext(w, document.Next)
}

// EngineName is an engine id as people read it.
func EngineName(id string) string {
	if engine, ok := setup.FindEngine(id); ok {
		return engine.Name
	}
	return id
}

// StateLabel is a mount state as people read it.
func StateLabel(state string) string {
	switch state {
	case setup.MountMounted:
		return uicopy.T("databases.state.mounted", nil)
	case setup.MountNeedsAttention:
		return uicopy.T("databases.state.needs_attention", nil)
	default:
		return uicopy.T("databases.state.unknown", nil)
	}
}

func (a *App) createDatabase(cmd *cobra.Command, request setup.CreateRequest, noStart, jsonOut bool) error {
	return run(func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).CreateDatabase(cmd.Context(), request, noStart)
		if err != nil {
			return err
		}
		var result setup.DatabaseResult
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
			db := result.Database
			say(w, uicopy.T("database.created.title", map[string]string{"name": db.ID}))
			say(w, "")
			if db.Engine == setup.EngineSQLite {
				say(w, uicopy.T("database.created.stored.sqlite", map[string]string{"path": db.Location}))
			} else {
				say(w, uicopy.T("database.created.stored.ingitdb", map[string]string{"path": db.Location + string(filepath.Separator)}))
			}
			say(w, "")
			say(w, uicopy.T("home.what_next", nil))
			writeNext(w, result.Next)
		})
		return nil
	})(cmd, nil)
}

func (a *App) databasesRemoveCmd() *cobra.Command {
	var yes, noStart, jsonOut bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a database from OVDB; its data stays where it is",
		Args:  exactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			if !yes {
				confirmed, err := a.confirmRemove(cmd, local.Databases, args[0], jsonOut)
				if err != nil || !confirmed {
					return err
				}
			}
			body, err := local.RemoveDatabase(cmd.Context(), args[0], noStart)
			if err != nil {
				return err
			}
			var result setup.DatabaseResult
			if err := json.Unmarshal(body, &result); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				db := result.Database
				say(w, uicopy.T("database.removed.title", map[string]string{"name": db.ID}))
				if db.Location != "" {
					say(w, uicopy.T("database.removed.data_kept", map[string]string{"location": db.Location}))
				} else {
					say(w, uicopy.T("database.removed.data_kept_elsewhere", nil))
				}
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				writeNext(w, result.Next)
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "remove without asking (the data is kept)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// confirmRemove asks in a terminal; anywhere else (a pipe, an agent, --json,
// OVDB_NON_INTERACTIVE=1) it fails with confirmation_required naming --yes.
func (a *App) confirmRemove(cmd *cobra.Command, list func(ctx context.Context) ([]byte, error), id string, jsonOut bool) (bool, error) {
	needed := envelope.New(envelope.ConfirmationRequired, uicopy.T("database.remove.failed", nil)).
		WithReason(uicopy.T("database.remove.confirm_needed", nil)).
		WithNext(envelope.Next{Label: uicopy.T("database.remove.confirm_flag", nil), Command: "ovdb databases remove " + id + " --yes"})
	in, isFile := cmd.InOrStdin().(*os.File)
	if jsonOut || a.getenv(EnvNonInteractive) == "1" || !isFile || !a.isTerminal(in.Fd()) {
		return false, needed
	}
	location := id
	if body, err := list(cmd.Context()); err == nil {
		var document setup.DatabasesDocument
		if json.Unmarshal(body, &document) == nil {
			for _, db := range document.Databases {
				if db.ID == id && db.Location != "" {
					location = db.Location
				}
			}
		}
	}
	_, _ = io.WriteString(cmd.ErrOrStderr(), uicopy.T("database.remove.confirm", map[string]string{"name": id, "location": location}))
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer == "y" || answer == "yes" {
		return true, nil
	}
	say(cmd.OutOrStdout(), uicopy.T("database.remove.kept", map[string]string{"name": id}))
	return false, nil
}
