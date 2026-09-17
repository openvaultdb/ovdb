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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// Local API paths.
const (
	ServerPath     = "/api/local/v1/server"
	StatusPath     = "/api/local/v1/status"
	HomePath       = "/api/local/v1/home"
	LoginLinksPath = "/api/local/v1/login-links"
	ConfigPath     = "/api/local/v1/config"
	EnginesPath    = "/api/local/v1/engines"
	DatabasesPath  = "/api/local/v1/databases"
	ConnectPath    = "/api/local/v1/databases/connect"
	ContextPath    = "/api/local/v1/context"
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
	// Telemetry records this process's capability events (CLI or TUI);
	// nil records nothing (telemetry-consent#REQ:sender-process-decides).
	Telemetry *telemetry.Recorder
	// Where is where this client runs, for resolving its database context:
	// the walk-up directories and any --db or OVDB_DATABASE. The web console
	// has no such thing; CLI and TUI send it with every context read.
	Where dbcontext.Request
	// ConsoleBuilt reports whether this binary embeds the web console and
	// TODO app; web.Built when nil.
	ConsoleBuilt func() bool
	// Getenv resolves this client's environment-dependent values, such as AI
	// agent skill directories; os.Getenv when nil.
	Getenv func(string) string
	// DataTugLookPath resolves whether datatug is on this process's own
	// PATH for Explore data (capability row 22); exec.LookPath when nil.
	// The CLI and TUI run in this process, so PrepareDataTugCLI uses it to
	// override whatever the server itself saw (review-inc-7.md F2).
	DataTugLookPath explore.LookPath
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
func (l *Local) Start(ctx context.Context) (outcome StartOutcome, err error) {
	defer l.trackStart(time.Now(), &err)
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
	response, err := l.newClient(result.State).Do(ctx, http.MethodGet, ServerPath, nil)
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
//
// The skills field group is always this client's: the server's copy is
// replaced with the skills resolved from the client's environment.
func (l *Local) Status(ctx context.Context) ([]byte, error) {
	body, err := l.readErr(ctx, l.withWhere(StatusPath), func() (any, error) {
		databases, err := setup.ListDatabases(l.Dirs.Home, nil)
		context := dbcontext.Resolve(l.Dirs.Home, setup.DatabaseIDs(databases), l.Where).Context
		return setup.NewStatus(l.Version, l.Dirs, setup.StoppedServer(l.Port, l.Dirs), databases, context, l.installedSkills()), err
	})
	if err != nil {
		return nil, err
	}
	var status setup.Status
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, err
	}
	status.SetSkills(l.installedSkills())
	return envelope.Marshal(status), nil
}

// Home is the Home menu document the TUI and web console render
// (first-run-onboarding#REQ:home-menu-options): from the server when it
// runs, otherwise built for a stopped server without starting one.
func (l *Local) Home(ctx context.Context) ([]byte, error) {
	return l.readErr(ctx, l.withWhere(HomePath), func() (any, error) {
		databases, err := setup.ListDatabases(l.Dirs.Home, nil)
		context := dbcontext.Resolve(l.Dirs.Home, setup.DatabaseIDs(databases), l.Where).Context
		return setup.NewHome(setup.StoppedServer(l.Port, l.Dirs), databases, context), err
	})
}

func (l *Local) withWhere(path string) string {
	if query := l.Where.Query().Encode(); query != "" {
		return path + "?" + query
	}
	return path
}

// Context is the context document for where this client runs (capability
// 14): from the server when it runs, otherwise from the files, starting
// nothing.
func (l *Local) Context(ctx context.Context) ([]byte, error) {
	return l.readErr(ctx, l.withWhere(ContextPath), func() (any, error) {
		databases, err := setup.ListDatabases(l.Dirs.Home, nil)
		return dbcontext.Resolve(l.Dirs.Home, setup.DatabaseIDs(databases), l.Where), err
	})
}

// SetContext stores or clears a project context or the global default
// through the server, starting it unless noStart (capability 13).
func (l *Local) SetContext(ctx context.Context, change dbcontext.Change, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPut, ContextPath, change)
	return response.Body, err
}

// Engines is the storage catalogue (capability 8). It is the same data in
// every binary, so without a server it is built here.
func (l *Local) Engines(ctx context.Context) ([]byte, error) {
	return l.read(ctx, EnginesPath, func() any { return setup.NewEnginesDocument() })
}

// Databases lists registered databases (capability 11): from the running
// server, or from the registry with mount state "unknown" when none runs
// (database-setup-and-providers#REQ:list-and-remove).
func (l *Local) Databases(ctx context.Context) ([]byte, error) {
	return l.readErr(ctx, DatabasesPath, func() (any, error) {
		databases, err := setup.ListDatabases(l.Dirs.Home, nil)
		return setup.NewDatabasesDocument(databases), err
	})
}

// CreateDatabase creates a database through the server, starting it unless
// noStart. An empty path is the default location under this client's data
// home, sent as an absolute path (REQ:client-values-and-mismatch).
func (l *Local) CreateDatabase(ctx context.Context, request setup.CreateRequest, noStart bool) (body []byte, err error) {
	defer l.trackCreate(time.Now(), &request, &err)
	if request.Engine == "" {
		request.Engine = setup.EngineInGitDB
	}
	if home, err := os.UserHomeDir(); err == nil && request.Path != "" {
		request.Path = paths.ExpandHome(request.Path, home)
	}
	if request.Path == "" {
		request.Path = setup.DefaultPath(l.Dirs.Data, request.Engine, request.ID)
	} else if abs, err := filepath.Abs(request.Path); err == nil {
		request.Path = abs
	}
	// Refuse what cannot work before starting a server for it.
	if err := setup.ValidateCreate(&request); err != nil {
		return nil, err
	}
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, DatabasesPath, request)
	return response.Body, err
}

// ConnectDatabase registers an existing folder, SQLite file or manifest file
// through the server, starting it unless noStart. Relative paths are made
// absolute against this client's working directory before they are sent
// (REQ:client-values-and-mismatch).
func (l *Local) ConnectDatabase(ctx context.Context, request setup.ConnectRequest, noStart bool) (body []byte, err error) {
	defer l.trackConnect(time.Now(), &request, &body, &err)
	home, _ := os.UserHomeDir()
	for _, path := range []*string{&request.Path, &request.Manifest} {
		if *path != "" && home != "" {
			*path = paths.ExpandHome(*path, home)
		}
		if *path != "" {
			if abs, err := filepath.Abs(*path); err == nil {
				*path = abs
			}
		}
	}
	// Refuse what cannot work before starting a server for it.
	if err := setup.ValidateConnect(&request); err != nil {
		return nil, err
	}
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, ConnectPath, request)
	return response.Body, err
}

// ReloadDatabase mounts database id again from its manifest through the
// server, starting it unless noStart.
func (l *Local) ReloadDatabase(ctx context.Context, id string, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, DatabasesPath+"/"+url.PathEscape(id)+"/reload", struct{}{})
	return response.Body, err
}

// ReloadAllDatabases reloads every registration and picks up manifests
// added by hand.
func (l *Local) ReloadAllDatabases(ctx context.Context, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, DatabasesPath+"/reload", struct{}{})
	return response.Body, err
}

// RemoveDatabase unregisters database id through the server, starting it
// unless noStart. The data is kept.
func (l *Local) RemoveDatabase(ctx context.Context, id string, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodDelete, DatabasesPath+"/"+url.PathEscape(id), nil)
	return response.Body, err
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
	return l.readErr(ctx, path, func() (any, error) { return fromFiles(), nil })
}

func (l *Local) readErr(ctx context.Context, path string, fromFiles func() (any, error)) ([]byte, error) {
	state, err := l.inspect(ctx, true)
	if err != nil {
		return nil, err
	}
	if state.Running {
		response, err := l.newClient(state).Do(ctx, http.MethodGet, path, nil)
		return response.Body, err
	}
	document, err := fromFiles()
	if err != nil {
		return nil, err
	}
	return envelope.Marshal(document), nil
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
			response, err := l.newClient(state).Do(ctx, http.MethodPut, ConfigPath, change)
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
		return l.newClient(state), nil
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
	return l.newClient(result.State), nil
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
	state   runtime.State
	version string // the client's version
	http    *http.Client
}

func (l *Local) newClient(state runtime.State) *Client {
	return &Client{state: state, version: l.Version, http: runtime.NewHTTPClient(30 * time.Second)}
}

// VersionMismatch is server_version_mismatch: the running server is too old
// (or new) to serve this request (REQ:version-mismatch-notice).
func VersionMismatch(serverVersion, clientVersion string) *envelope.Error {
	return envelope.New(envelope.ServerVersionMismatch, uicopy.T("server.version_mismatch.message", nil)).
		WithReason(VersionNotice(serverVersion, clientVersion)).
		WithNext(envelope.Next{Label: uicopy.T("next.restart_server", nil), Command: "ovdb server restart"})
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
		e := envelope.Decode(data)
		// A server of another version that lacks this endpoint answers 404
		// or 405 for it; say what to do instead of "nothing here".
		unknownEndpoint := response.StatusCode == http.StatusMethodNotAllowed ||
			response.StatusCode == http.StatusNotFound && (e == nil || e.Message == uicopy.T("api.not_found", nil))
		if unknownEndpoint && c.state.Whoami != nil && c.state.Whoami.Version != c.version {
			mismatch := VersionMismatch(c.state.Whoami.Version, c.version)
			return Response{Status: response.StatusCode, Body: envelope.MarshalError(mismatch)}, mismatch
		}
		if e != nil {
			return result, e
		}
		return result, envelope.New(envelope.Internal, uicopy.T("api.internal", nil)).WithReason(response.Status)
	}
	return result, nil
}
