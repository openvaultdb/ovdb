package cli

import (
	"encoding/json"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// TokensPreview adds the preview behaviour to the legacy `ovdb token create`,
// `list` and `revoke`: with OVDB_PREVIEW=1 they manage the tokens in
// <OVDB home>/auth.json through the local OVDB server with the instance
// secret, starting it when needed, unless --addr or --owner-token asks for
// today's remote-server behaviour
// (local-server-and-web-console#REQ:tokens-against-local-server). Without
// the gate nothing changes, not even the flags help shows.
func (a *App) TokensPreview(token *cobra.Command) {
	if !preview.On() {
		return
	}
	token.Short = "Manage scoped access tokens for apps and tools"
	token.Long = `Manage revocable scoped access tokens.

By default the tokens live in <OVDB home>/auth.json and are managed through the
local OVDB server, which is started when needed. --addr or --owner-token
(or $OVDB_OWNER_TOKEN with --addr) manage a remote OpenVaultDB server instead.

The token secret is printed once, by create, and never again.`
	for _, command := range token.Commands() {
		switch command.Name() {
		case "create":
			a.tokenCreatePreview(command)
		case "list":
			a.tokenListPreview(command)
		case "revoke":
			a.tokenRevokePreview(command)
		}
	}
}

// legacyToken reports whether cmd asked for a remote server.
func legacyToken(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("addr") || cmd.Flags().Changed("owner-token")
}

// previewToken wires one subcommand: its flag errors and arguments become
// envelopes on the local path, and the legacy path keeps its own.
func previewToken(command *cobra.Command, noStart *bool, local func(cmd *cobra.Command, args []string) error) {
	legacyRun, legacyArgs := command.RunE, command.Args
	command.Flags().BoolVar(noStart, "no-start", false, "fail instead of starting the OVDB server")
	command.SetFlagErrorFunc(flagError)
	command.Args = func(cmd *cobra.Command, args []string) error {
		if legacyToken(cmd) {
			if legacyArgs == nil {
				return nil
			}
			return legacyArgs(cmd, args)
		}
		// Only revoke takes an argument: the token's id.
		if cmd.Name() == "revoke" {
			return exactArgs(1)(cmd, args)
		}
		return noArgs(cmd, args)
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if legacyToken(cmd) {
			return legacyRun(cmd, args)
		}
		return run(local)(cmd, args)
	}
}

func (a *App) tokenCreatePreview(command *cobra.Command) {
	var noStart bool
	previewToken(command, &noStart, func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()
		scope, _ := flags.GetString("scope")
		extra, _ := flags.GetStringArray("capability")
		jsonOut, _ := flags.GetBool("json")
		request := client.TokenRequest{}
		request.DatabaseID, _ = flags.GetString("db")
		request.Label, _ = flags.GetString("label")
		request.ExpiresIn, _ = flags.GetString("expires")
		capabilities, err := setup.ScopeCapabilities(scope)
		if err != nil {
			return usageError(cmd, err.Error())
		}
		request.Capabilities = append(capabilities, extra...)
		if request.DatabaseID == "" && !setup.HasCreateDBCapability(request.Capabilities) {
			return usageError(cmd, uicopy.T("token.db_required", nil))
		}
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).CreateToken(cmd.Context(), request, noStart)
		if err != nil {
			return err
		}
		var created tokenDocument
		if err := json.Unmarshal(body, &created); err != nil {
			return err
		}
		printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
			say(w, uicopy.T("token.created", nil))
			say(w, "")
			writeToken(w, created)
			say(w, "")
			say(w, uicopy.T("token.secret_once", nil))
			say(w, "  "+created.Token)
		})
		return nil
	})
}

func (a *App) tokenListPreview(command *cobra.Command) {
	var noStart bool
	previewToken(command, &noStart, func(cmd *cobra.Command, _ []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).Tokens(cmd.Context(), noStart)
		if err != nil {
			return err
		}
		var listed struct {
			Tokens []tokenDocument `json:"tokens"`
		}
		if err := json.Unmarshal(body, &listed); err != nil {
			return err
		}
		printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
			if len(listed.Tokens) == 0 {
				say(w, uicopy.T("token.none", nil))
				say(w, "")
				writeNext(w, []envelope.Next{{Label: uicopy.T("next.token_create", nil), Command: "ovdb token create --db <database> --scope read-only"}})
				return
			}
			table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			say(table, strings.Join([]string{uicopy.T("token.column.id", nil), uicopy.T("token.column.label", nil),
				uicopy.T("token.column.database", nil), uicopy.T("token.column.access", nil), uicopy.T("token.column.issued", nil),
				uicopy.T("token.column.expires", nil), uicopy.T("token.column.revoked", nil)}, "\t"))
			for _, token := range listed.Tokens {
				say(table, strings.Join([]string{token.ID, token.Label, token.database(), strings.Join(token.Capabilities, ","),
					token.IssuedAt.Format(time.DateOnly), token.expires(time.DateOnly), date(token.RevokedAt)}, "\t"))
			}
			_ = table.Flush()
		})
		return nil
	})
}

func (a *App) tokenRevokePreview(command *cobra.Command) {
	var noStart, jsonOut bool
	jsonFlag(command, &jsonOut)
	previewToken(command, &noStart, func(cmd *cobra.Command, args []string) error {
		t, err := a.resolve(0)
		if err != nil {
			return err
		}
		body, err := a.local(cmd, t).RevokeToken(cmd.Context(), args[0], noStart)
		if err != nil {
			return err
		}
		printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
			say(w, uicopy.T("token.revoked", map[string]string{"id": args[0]}))
		})
		return nil
	})
}

// tokenDocument is one token as openvaultdb-go's /v1/tokens returns it;
// Token is set only in the create response.
type tokenDocument struct {
	ID           string     `json:"id"`
	Token        string     `json:"token"`
	Label        string     `json:"label"`
	DatabaseID   string     `json:"databaseId"`
	Capabilities []string   `json:"capabilities"`
	IssuedAt     time.Time  `json:"issuedAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	RevokedAt    *time.Time `json:"revokedAt"`
}

func (t tokenDocument) database() string {
	if t.DatabaseID == "" {
		return uicopy.T("token.server_level", nil)
	}
	return t.DatabaseID
}

func (t tokenDocument) expires(layout string) string {
	if t.ExpiresAt == nil {
		return uicopy.T("token.never", nil)
	}
	return t.ExpiresAt.Format(layout)
}

func date(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.Format(time.DateOnly)
}

// writeToken prints a token's details, never its secret.
func writeToken(w io.Writer, token tokenDocument) {
	rows := [][2]string{{uicopy.T("token.column.id", nil), token.ID}}
	if token.Label != "" {
		rows = append(rows, [2]string{uicopy.T("token.column.label", nil), token.Label})
	}
	rows = append(rows,
		[2]string{uicopy.T("token.column.database", nil), token.database()},
		[2]string{uicopy.T("token.column.access", nil), strings.Join(token.Capabilities, ", ")},
		[2]string{uicopy.T("token.column.expires", nil), token.expires(time.RFC3339)})
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		say(table, "  "+row[0]+":\t"+row[1])
	}
	_ = table.Flush()
}
