package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/cli"
	"github.com/openvaultdb/ovdb/internal/preview"
)

func fetchJSON(url string) (map[string]any, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, string(body))
	}
	var out map[string]any
	if err = json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func newStatusCmd(app *cli.App) *cobra.Command {
	var url string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of a running OpenVaultDB server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// With the preview gate, status reports the whole local setup;
			// --url keeps today's behaviour (first-run-onboarding#REQ:status-command).
			if preview.On() && !cmd.Flags().Changed("url") {
				return app.Status(cmd, jsonOut)
			}
			status, err := fetchJSON(url + "/v1/status")
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(status, "", "  ")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "http://"+DefaultAddr, "server base URL")
	if preview.On() {
		// Registered only behind the gate, so `ovdb status --help` and a
		// stray --json behave exactly as before without it.
		cmd.Flags().BoolVar(&jsonOut, "json", false, "print the local setup status as JSON")
		cmd.Short = "Show the local OVDB setup: server, databases, demo, skills and usage statistics (starts nothing)"
		cmd.Flags().Lookup("url").Usage = "query this running server's legacy /v1/status instead"
	}
	return cmd
}
