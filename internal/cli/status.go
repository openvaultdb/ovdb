package cli

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Status is the preview `ovdb status`: a pure read that never starts a
// server (first-run-onboarding#REQ:status-command).
func (a *App) Status(cmd *cobra.Command, jsonOut bool) error {
	return run(func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).Status(cmd.Context())
		if err != nil {
			return err
		}
		var status setup.Status
		if err := json.Unmarshal(body, &status); err != nil {
			return err
		}
		printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
			say(w, uicopy.T("status.title", map[string]string{"version": status.Version}))
			say(w, "")
			writeServer(w, status.Server)
			say(w, "")
			say(w, uicopy.T("status.home", map[string]string{"dir": status.Locations.Home}))
			say(w, uicopy.T("status.runtime", map[string]string{"dir": status.Locations.Runtime}))
			say(w, uicopy.T("status.data", map[string]string{"dir": status.Locations.Data}))
			say(w, "")
			if len(status.Databases) == 0 {
				say(w, uicopy.T("home.status.databases_none", nil))
			} else {
				say(w, uicopy.T("databases.title", nil)+":")
				for _, db := range status.Databases {
					line := "  " + db.ID + " · " + EngineName(db.Engine) + " · " + StateLabel(db.State)
					if db.Reason != "" {
						line += " · " + db.Reason
					}
					say(w, line)
				}
			}
			say(w, "")
			say(w, uicopy.T("problem.what_you_can_do", nil))
			writeNext(w, status.Next)
		})
		return nil
	})(cmd, nil)
}
