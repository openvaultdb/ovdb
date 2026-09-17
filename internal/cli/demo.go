package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
)

// demoCmd is `ovdb demo install|open|status`, defaulting to the TODO demo so
// an `--app <name>` option can join without breaking them
// (todo-demo#REQ:demo-command-shape).
func (a *App) demoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Try the built-in TODO demo: two lists your apps and AI agents share",
		Args:  noArgs,
	}
	cmd.AddCommand(a.demoInstallCmd(), a.demoOpenCmd(), a.demoStatusCmd())
	for _, sub := range cmd.Commands() {
		sub.SetFlagErrorFunc(flagError)
	}
	return cmd
}

func (a *App) demoInstallCmd() *cobra.Command {
	var yes, noStart, jsonOut bool
	var id string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the TODO demo (never overwrites your changes)",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			request := demo.InstallRequest{ID: id}
			if !yes {
				where := demo.Location(t.dirs.Data, demo.DefaultID)
				if id != "" {
					where = demo.Location(t.dirs.Data, id)
				}
				confirmed, err := a.confirmDemo(cmd, local, where, id, jsonOut)
				if err != nil || !confirmed {
					return err
				}
			}
			body, err := local.InstallDemo(cmd.Context(), request, noStart)
			if err != nil {
				return err
			}
			var document demo.Document
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				if document.AlreadyInstalled {
					say(w, uicopy.T("demo.already.title", nil))
				} else {
					say(w, uicopy.T("demo.ready.title", nil))
				}
				say(w, "")
				say(w, uicopy.T("demo.ready.stored", map[string]string{"path": document.Location}))
				say(w, uicopy.T("demo.ready.shared", nil))
				say(w, "")
				say(w, uicopy.T("home.what_next", nil))
				writeNext(w, document.Next)
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "install without asking")
	cmd.Flags().StringVar(&id, "id", "", "install under another database id (default todo)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// confirmDemo shows where the demo will be stored and asks in a terminal;
// anywhere else it fails with confirmation_required naming --yes, having
// written and started nothing (first-run-onboarding#REQ:never-block-without-terminal).
func (a *App) confirmDemo(cmd *cobra.Command, local *client.Local, where, id string, jsonOut bool) (bool, error) {
	command := "ovdb demo install --yes"
	if id != "" {
		command = "ovdb demo install --id " + id + " --yes"
	}
	needed := envelope.New(envelope.ConfirmationRequired, uicopy.T("demo.install.failed", nil)).
		WithReason(uicopy.T("demo.install.confirm_needed", nil)).
		WithNext(envelope.Next{Label: uicopy.T("demo.install.confirm_flag", nil), Command: command})
	in, isFile := cmd.InOrStdin().(*os.File)
	if jsonOut || a.getenv(EnvNonInteractive) == "1" || !isFile || !a.isTerminal(in.Fd()) {
		return false, needed
	}
	// Installed already: installing writes nothing and says so, no question.
	if body, err := local.Demo(cmd.Context()); err == nil {
		var document demo.Document
		if json.Unmarshal(body, &document) == nil && document.Installed && (id == "" || strings.EqualFold(id, document.Database)) {
			return true, nil
		}
	}
	_, _ = io.WriteString(cmd.ErrOrStderr(), uicopy.T("demo.install.confirm", map[string]string{"path": where}))
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer == "y" || answer == "yes" {
		return true, nil
	}
	say(cmd.OutOrStdout(), uicopy.T("demo.install.cancelled", nil))
	return false, nil
}

// demoOpenCmd starts the server if needed and opens the TODO app signed in
// (todo-demo#REQ:todo-app-same-origin), printing both links like `ovdb open`.
func (a *App) demoOpenCmd() *cobra.Command {
	var printURL, noStart, jsonOut bool
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Open the TODO app in your browser, signed in",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).DemoLink(cmd.Context(), noStart)
			if err != nil {
				return err
			}
			var link localserver.LoginLink
			if err := json.Unmarshal(body, &link); err != nil {
				return err
			}
			opened := !printURL && a.openBrowser(link.URL) == nil
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				if opened {
					say(w, uicopy.T("demo.open.opened", nil))
					say(w, uicopy.T("demo.open.if_not_opened", nil))
				} else {
					say(w, uicopy.T("demo.open.intro", nil))
				}
				say(w, "  "+link.URL)
				say(w, uicopy.T("open.fallback", nil))
				say(w, "  "+link.FallbackURL)
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&printURL, "print-url", false, "print the sign-in links without opening a browser")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) demoStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the TODO demo is installed, and where",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).Demo(cmd.Context())
			if err != nil {
				return err
			}
			var document demo.Document
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				if document.Installed {
					say(w, uicopy.T("demo.status.installed", map[string]string{"database": document.Database, "path": document.Location}))
				} else {
					say(w, uicopy.T("demo.status.not_installed", map[string]string{"path": document.Location}))
				}
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
