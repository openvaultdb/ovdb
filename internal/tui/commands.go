package tui

import (
	"encoding/json"
	"strconv"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Every screen's data comes from internal/client.Local — the single place
// that decides server-vs-files and mismatch rules (see internal/client's
// doc comment). Commands here only call it, decode the byte-for-byte
// document it returns, and report the result as a message; they hold no
// business logic of their own.

func decodeHome(body []byte) (setup.HomeDocument, error) {
	var document setup.HomeDocument
	err := json.Unmarshal(body, &document)
	return document, err
}

func decodeServer(body []byte) (setup.ServerDocument, error) {
	var document setup.ServerDocument
	err := json.Unmarshal(body, &document)
	return document, err
}

func decodeConfig(body []byte) (setup.ConfigDocument, error) {
	var document setup.ConfigDocument
	err := json.Unmarshal(body, &document)
	return document, err
}

// asProblem turns any error into the *envelope.Error the Problem screen
// renders: services already return envelope errors, so this only builds a
// fallback for the rare decode failure that is not one.
func asProblem(err error) *envelope.Error {
	if e := envelope.As(err); e != nil {
		return e
	}
	return envelope.New(envelope.Internal, uicopy.T("api.internal", nil)).WithReason(err.Error())
}

type homeLoadedMsg struct {
	document setup.HomeDocument
	err      error
}

// loadHomeCmd loads GET /api/local/v1/home — the server's own status line,
// question and options (first-run-onboarding#REQ:home-menu-options); Home
// renders it as given rather than building a menu itself.
func (m Model) loadHomeCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Home(ctx)
		if err != nil {
			return homeLoadedMsg{err: err}
		}
		document, decErr := decodeHome(body)
		return homeLoadedMsg{document: document, err: decErr}
	}
}

type serverLoadedMsg struct {
	document setup.ServerDocument
	err      error
}

func (m Model) loadServerCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Server(ctx)
		if err != nil {
			return serverLoadedMsg{err: err}
		}
		document, decErr := decodeServer(body)
		return serverLoadedMsg{document: document, err: decErr}
	}
}

// serverActionMsg is the result of starting or restarting the server; both
// return the same document shape (internal/client.StartOutcome).
type serverActionMsg struct {
	document setup.ServerDocument
	err      error
}

func (m Model) startCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		outcome, err := local.Start(ctx)
		if err != nil {
			return serverActionMsg{err: err}
		}
		document, decErr := decodeServer(outcome.Body)
		return serverActionMsg{document: document, err: decErr}
	}
}

func (m Model) restartCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		outcome, err := local.Restart(ctx)
		if err != nil {
			return serverActionMsg{err: err}
		}
		document, decErr := decodeServer(outcome.Body)
		return serverActionMsg{document: document, err: decErr}
	}
}

// portRemedyCmd is the TUI's "Use port <N+1> instead" action
// (local-server-and-web-console#REQ:deterministic-port-conflict): it
// persists the offered port, exactly like `ovdb config set server.port`,
// then retries the start on it. Either step's failure lands back on the
// Problem screen through the same serverActionMsg the plain start uses.
func (m Model) portRemedyCmd(port int) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		if _, err := local.SetConfig(ctx, setup.ConfigChange{Key: setup.KeyServerPort, Value: strconv.Itoa(port)}); err != nil {
			return serverActionMsg{err: err}
		}
		local.Port = port
		local.ExplicitPort = true
		outcome, err := local.Start(ctx)
		if err != nil {
			return serverActionMsg{err: err}
		}
		document, decErr := decodeServer(outcome.Body)
		return serverActionMsg{document: document, err: decErr}
	}
}

type stopResultMsg struct {
	wasRunning bool
	body       []byte
	err        error
}

func (m Model) stopCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		outcome, err := local.Stop(ctx)
		if err != nil {
			return stopResultMsg{err: err}
		}
		return stopResultMsg{wasRunning: outcome.WasRunning, body: outcome.Body}
	}
}

type loginLinkMsg struct {
	link    localserver.LoginLink
	openErr error
	err     error
}

// openBrowserCmd creates a login link and hands it to m.openBrowser (the
// same internal/browser.Opener the CLI's `ovdb open` uses, or a fake
// injected for tests). openErr — not err — carries a launch failure: the
// link was created fine, so the Server screen still shows it, just with the
// "couldn't open a browser" wording instead of the success one (review: a
// failed launch must not look identical to a successful one).
func (m Model) openBrowserCmd() tea.Cmd {
	local, ctx, openBrowser := m.local, m.ctx, m.openBrowser
	return func() tea.Msg {
		body, err := local.LoginLink(ctx, false)
		if err != nil {
			return loginLinkMsg{err: err}
		}
		var link localserver.LoginLink
		if decErr := json.Unmarshal(body, &link); decErr != nil {
			return loginLinkMsg{err: decErr}
		}
		return loginLinkMsg{link: link, openErr: openBrowser(link.URL)}
	}
}

type configLoadedMsg struct {
	document setup.ConfigDocument
	err      error
}

func (m Model) loadConfigCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Config(ctx)
		if err != nil {
			return configLoadedMsg{err: err}
		}
		document, decErr := decodeConfig(body)
		return configLoadedMsg{document: document, err: decErr}
	}
}

type configSavedMsg struct {
	document setup.ConfigDocument
	err      error
}

func (m Model) saveConfigCmd(port int) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.SetConfig(ctx, setup.ConfigChange{Key: setup.KeyServerPort, Value: strconv.Itoa(port)})
		if err != nil {
			return configSavedMsg{err: err}
		}
		document, decErr := decodeConfig(body)
		return configSavedMsg{document: document, err: decErr}
	}
}
