package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/localserver"
)

// openCmd prints a login link for the console. Launching the browser comes
// with the console in increment 1b; until then it always takes the
// print-only path REQ:login-links allows when no browser can be launched.
func (a *App) openCmd() *cobra.Command {
	var jsonOut, noStart, printURL bool
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Sign in to the OVDB web console",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).LoginLink(cmd.Context(), noStart)
			if err != nil {
				return err
			}
			var link localserver.LoginLink
			if err := json.Unmarshal(body, &link); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, uicopy.T("open.intro", nil))
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
