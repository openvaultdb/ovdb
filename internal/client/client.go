// Package client is how every presentation (CLI now, TUI in 1c, and the
// shared rules the web console relies on) reaches the local OVDB server.
//
// Local is the single place that decides, for each capability, whether to
// ask the running server or to read state files, and it applies one rule
// set on the way: a running server started for a different OVDB home — or
// on a port other than an explicitly requested one — is a
// server_config_mismatch; a version difference is a one-line notice; a
// command that needs the server starts it unless told not to. Every method
// returns the schema-1 document bytes a presentation renders or prints as
// --json.
//
// See decision 0006 and spec/features/local-server-and-web-console
// (REQ:client-values-and-mismatch, REQ:auto-start, REQ:version-mismatch-notice).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Local API paths.
const (
	ServerPath     = "/api/local/v1/server"
	StatusPath     = "/api/local/v1/status"
	HomePath       = "/api/local/v1/home"
	LoginLinksPath = "/api/local/v1/login-links"
	ConfigPath     = "/api/local/v1/config"
)

// Local is one presentation's view of this home's local server.
type Local struct {
	Dirs         paths.Dirs
	Version      string // this client's version
	Port         int    // resolved with setup.ResolvePort
	ExplicitPort bool   // --port or OVDB_PORT
	// Command builds the detached server process for a port.
	Command func(port int) *exec.Cmd
	// Notices receives one-line notices: auto-start, version mismatch,
	// directory warnings, unreadable runtime files. Never stdout with --json.
	Notices io.Writer
}

func (l *Local) notice(line string) {
	if l.Notices != nil {
		_, _ = fmt.Fprintln(l.Notices, line)
	}
}

// inspect reads the runtime state and applies the rule set. checkPort is
// false for restart, whose whole point may be a different port.
func (l *Local) inspect(ctx context.Context, checkPort bool) (runtime.State, error) {
	state, err := runtime.Inspect(ctx, l.Dirs.Runtime)
	if err != nil {
		return state, err
	}
	if state.Unreadable != nil {
		l.notice(uicopy.T("server.record_unreadable", map[string]string{"error": redact.String(state.Unreadable.Error())}))
	}
	if !state.Running {
		return state, nil
	}
	if mismatch := runtime.CheckMatch(state.Record, l.Dirs, l.Port, l.ExplicitPort && checkPort); mismatch != nil {
		return state, mismatch
	}
	if state.Whoami.Version != l.Version {
		l.notice(VersionNotice(state.Whoami.Version, l.Version))
	}
	return state, nil
}

func (l *Local) startOptions() runtime.StartOptions {
	return runtime.StartOptions{Dirs: l.Dirs, Port: l.Port, ExplicitPort: l.ExplicitPort, Command: l.Command}
}

// StartOutcome is a start's server document.
type StartOutcome struct {
	Body           []byte // GET /api/local/v1/server
	AlreadyRunning bool
}

// Start starts the server, or reports the one already running.
func (l *Local) Start(ctx context.Context) (StartOutcome, error) {
	result, err := runtime.Start(ctx, l.startOptions())
	for _, warning := range result.Warnings {
		l.notice(warning)
	}
	if err != nil {
		return StartOutcome{}, err
	}
	if result.AlreadyRunning && result.State.Whoami.Version != l.Version {
		l.notice(VersionNotice(result.State.Whoami.Version, l.Version))
	}
	response, err := newClient(result.State).Do(ctx, http.MethodGet, ServerPath, nil)
	return StartOutcome{Body: response.Body, AlreadyRunning: result.AlreadyRunning}, err
}

// StopOutcome is a stop's server document.
type StopOutcome struct {
	Body       []byte // the not-running server document
	WasRunning bool
}

// Stop stops this home's server. A record naming a process that is gone or
// was reused is "not running", not a failure.
func (l *Local) Stop(ctx context.Context) (StopOutcome, error) {
	return l.stop(ctx, true)
}

func (l *Local) stop(ctx context.Context, checkPort bool) (StopOutcome, error) {
	if _, err := l.inspect(ctx, checkPort); err != nil {
		return StopOutcome{}, err
	}
	result, err := runtime.Stop(ctx, l.Dirs.Runtime, 0)
	if err != nil {
		return StopOutcome{}, err
	}
	port := l.Port
	if result.Record != nil && !l.ExplicitPort {
		port = result.Record.Port
	}
	body := envelope.Marshal(setup.NewServerDocument(setup.StoppedServer(port, l.Dirs)))
	return StopOutcome{Body: body, WasRunning: result.WasRunning}, nil
}

// Restart stops the server, treating an unconfirmable stale process as not
// running, and starts it again on the resolved port.
func (l *Local) Restart(ctx context.Context) (StartOutcome, error) {
	if _, err := l.stop(ctx, false); err != nil && !runtime.IsUnconfirmedProcess(err) {
		return StartOutcome{}, err
	}
	return l.Start(ctx)
}

// Server is the server document: from the server when it runs, otherwise
// built from files (a pure read that never starts anything).
func (l *Local) Server(ctx context.Context) ([]byte, error) {
	return l.read(ctx, ServerPath, func() any {
		return setup.NewServerDocument(setup.StoppedServer(l.Port, l.Dirs))
	})
}

// Status is the whole-setup status document (first-run-onboarding#REQ:status-command).
func (l *Local) Status(ctx context.Context) ([]byte, error) {
	return l.read(ctx, StatusPath, func() any {
		return setup.NewStatus(l.Version, l.Dirs, setup.StoppedServer(l.Port, l.Dirs))
	})
}

// Home is the Home menu document the TUI and web console render
// (first-run-onboarding#REQ:home-menu-options): from the server when it
// runs, otherwise built for a stopped server without starting one.
func (l *Local) Home(ctx context.Context) ([]byte, error) {
	return l.read(ctx, HomePath, func() any {
		return setup.NewHome(setup.StoppedServer(l.Port, l.Dirs))
	})
}

// Config is the configuration document.
func (l *Local) Config(ctx context.Context) ([]byte, error) {
	var loadErr error
	body, err := l.read(ctx, ConfigPath, func() any {
		config, err := setup.LoadConfig(l.Dirs.Home)
		loadErr = err
		return setup.NewConfigDocument(config, false)
	})
	if err == nil {
		err = loadErr
	}
	return body, err
}

func (l *Local) read(ctx context.Context, path string, fromFiles func() any) ([]byte, error) {
	state, err := l.inspect(ctx, true)
	if err != nil {
		return nil, err
	}
	if state.Running {
		response, err := newClient(state).Do(ctx, http.MethodGet, path, nil)
		return response.Body, err
	}
	return envelope.Marshal(fromFiles()), nil
}

// SetConfig changes a setting through the running server. With no server
// it writes as the home's single writer under the locks instead of starting
// one, because the fix offered for a busy port (`ovdb config set
// server.port N`) must work while that port keeps the server from starting.
func (l *Local) SetConfig(ctx context.Context, change setup.ConfigChange) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		state, err := l.inspect(ctx, false)
		if err != nil {
			return nil, err
		}
		if state.Running {
			response, err := newClient(state).Do(ctx, http.MethodPut, ConfigPath, change)
			return response.Body, err
		}
		var document setup.ConfigDocument
		warnings, err := runtime.WithHomeLock(ctx, l.Dirs, uicopy.T("config.failed", nil), func() error {
			var applyErr error
			document, applyErr = setup.ApplyConfigChange(l.Dirs, change, false)
			return applyErr
		})
		for _, warning := range warnings {
			l.notice(warning)
		}
		// A server that took the lock in between is asked on the next pass.
		if errors.Is(err, runtime.ErrAlreadyRunning) && attempt == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}
		return envelope.Marshal(document), nil
	}
}

// LoginLink creates a console login link, starting the server unless noStart.
func (l *Local) LoginLink(ctx context.Context, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, LoginLinksPath, struct{}{})
	return response.Body, err
}

// Connect returns a client for the running server, starting it when needed
// unless noStart (REQ:auto-start).
func (l *Local) Connect(ctx context.Context, noStart bool) (*Client, error) {
	state, err := l.inspect(ctx, true)
	if err != nil {
		return nil, err
	}
	if state.Running {
		return newClient(state), nil
	}
	if noStart || l.Command == nil {
		return nil, NotRunning()
	}
	result, err := runtime.Start(ctx, l.startOptions())
	for _, warning := range result.Warnings {
		l.notice(warning)
	}
	if err != nil {
		return nil, err
	}
	if !result.AlreadyRunning {
		l.notice(uicopy.T("server.auto_started", map[string]string{"address": setup.PrimaryAddress(result.State.Record.Port)}))
	} else if result.State.Whoami.Version != l.Version {
		l.notice(VersionNotice(result.State.Whoami.Version, l.Version))
	}
	return newClient(result.State), nil
}

// NotRunning is server_not_running for --no-start.
func NotRunning() *envelope.Error {
	return envelope.New(envelope.ServerNotRunning, uicopy.T("server.not_running.message", nil)).
		WithNext(envelope.Next{Label: uicopy.T("home.menu.start_server", nil), Command: "ovdb server start"})
}

// VersionNotice is the one line printed when client and server versions
// differ (REQ:version-mismatch-notice).
func VersionNotice(serverVersion, clientVersion string) string {
	return uicopy.T("server.version_mismatch", map[string]string{"server": serverVersion, "client": clientVersion})
}

// Client talks to one running local server with its instance secret.
type Client struct {
	state runtime.State
	http  *http.Client
}

func newClient(state runtime.State) *Client {
	return &Client{state: state, http: runtime.NewHTTPClient(30 * time.Second)}
}

// Response is a local API response; Body is kept byte for byte so --json
// prints exactly what the API returned.
type Response struct {
	Status int
	Body   []byte
}

// Do calls the local API. A failure status comes back as the server's
// *envelope.Error.
func (c *Client) Do(ctx context.Context, method, path string, body any) (Response, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, runtime.BaseURL(c.state.Record.Port)+path, payload)
	if err != nil {
		return Response{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.state.Secret)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return Response{}, NotRunning().WithReason(err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return Response{}, err
	}
	result := Response{Status: response.StatusCode, Body: data}
	if response.StatusCode >= http.StatusMultipleChoices {
		if e := envelope.Decode(data); e != nil {
			return result, e
		}
		return result, envelope.New(envelope.Internal, uicopy.T("api.internal", nil)).WithReason(response.Status)
	}
	return result, nil
}
