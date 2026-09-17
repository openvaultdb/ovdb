package cli

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Status is the preview `ovdb status`: a pure read that never starts a
// server. With this home's server running it prints GET /api/local/v1/status
// as is; otherwise it builds the same document from state files
// (first-run-onboarding#REQ:status-command).
func (a *App) Status(cmd *cobra.Command, jsonOut bool) error {
	return run(func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		state, err := runtime.Inspect(cmd.Context(), t.dirs.Runtime)
		if err != nil {
			return err
		}
		var body []byte
		if state.Running && state.Record.Home == t.dirs.Home {
			response, err := client.New(state).Do(cmd.Context(), http.MethodGet, statusPath, nil)
			if err != nil {
				return err
			}
			body = response.Body
		} else {
			server := setup.StoppedServer(t.port, t.dirs)
			if state.Running {
				server = setup.RunningServer(state.Record, t.dirs)
			}
			body = envelope.Marshal(setup.NewStatus(a.Version, t.dirs, server))
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
			say(w, uicopy.T("problem.what_you_can_do", nil))
			writeNext(w, status.Next)
		})
		return nil
	})(cmd, nil)
}
