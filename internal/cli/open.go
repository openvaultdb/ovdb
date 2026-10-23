package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/browser"
	"github.com/openvaultdb/ovdb/internal/localserver"
)

// Hosts `ovdb open --host` accepts: the primary address and the fallback
// for systems where *.localhost does not resolve.
const (
	hostPrimary  = "ovdb.localhost"
	hostFallback = "127.0.0.1"
)

// openCmd creates a login link, opens it in the browser and always prints
// both links. With --print-url, or when no browser can be launched here (SSH,
// containers, agents), it only prints them and still exits 0
// (REQ:login-links).
func (a *App) openCmd() *cobra.Command {
	var jsonOut, noStart, printURL bool
	var host string
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Sign in to the OVDB web console",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			if host != hostPrimary && host != hostFallback {
				return usageError(cmd, "--host must be "+hostPrimary+" or "+hostFallback+", not "+host)
			}
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
			opened := false
			if !printURL {
				target := link.URL
				if host == hostFallback {
					target = link.FallbackURL
				}
				opened = a.openBrowser(target) == nil
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				if opened {
					say(w, uicopy.T("open.opened", nil))
					say(w, uicopy.T("open.if_not_opened", nil))
				} else {
					say(w, uicopy.T("open.intro", nil))
				}
				say(w, "  "+link.URL)
				say(w, uicopy.T("open.fallback", nil))
				say(w, "  "+link.FallbackURL)
			})
			return nil
		}),
	}
	cmd.Flags().BoolVar(&printURL, "print-url", false, "print the sign-in links without opening a browser")
	cmd.Flags().StringVar(&host, "host", hostPrimary, "address to open: "+hostPrimary+" or "+hostFallback)
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

func (a *App) openBrowser(url string) error {
	if a.OpenBrowser != nil {
		return a.OpenBrowser(url)
	}
	return browser.Opener{}.Open(url)
}
