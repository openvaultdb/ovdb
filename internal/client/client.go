// Package client is the typed HTTP client CLI and TUI use to reach the local
// OVDB server. Connect finds the running server through the runtime
// directory, proves it is this home's instance, refuses a server started
// with a different home or port, prints the version-mismatch notice, and
// starts the server when needed unless told not to.
//
// See decision 0006 (one transport) and
// spec/features/local-server-and-web-console#REQ:auto-start.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Options describe how to reach the server.
type Options struct {
	Dirs         paths.Dirs
	Version      string // this client's version, for the mismatch notice
	Port         int
	ExplicitPort bool
	NoStart      bool
	// Start starts the server when it is not running; nil means never.
	Start func(context.Context) (runtime.StartResult, error)
	// Notices receives one-line notices (auto-start, version mismatch,
	// directory warnings). With --json it is stderr, never stdout.
	Notices io.Writer
}

// Client talks to one running local server.
type Client struct {
	state runtime.State
	http  *http.Client
}

// Connect returns a client for this home's running server.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	state, err := runtime.Inspect(ctx, opts.Dirs.Runtime)
	if err != nil {
		return nil, err
	}
	if state.Running {
		if mismatch := runtime.CheckMatch(state.Record, opts.Dirs, opts.Port, opts.ExplicitPort); mismatch != nil {
			return nil, mismatch
		}
	} else {
		if opts.NoStart || opts.Start == nil {
			return nil, NotRunning()
		}
		result, err := opts.Start(ctx)
		for _, warning := range result.Warnings {
			notice(opts.Notices, warning)
		}
		if err != nil {
			return nil, err
		}
		if !result.AlreadyRunning {
			notice(opts.Notices, uicopy.T("server.auto_started", map[string]string{
				"address": setup.PrimaryAddress(result.State.Record.Port),
			}))
		}
		state = result.State
	}
	if state.Whoami.Version != opts.Version {
		notice(opts.Notices, VersionNotice(state.Whoami.Version, opts.Version))
	}
	return New(state), nil
}

// New returns a client for a server already confirmed running, for pure
// reads that must not auto-start.
func New(state runtime.State) *Client {
	return &Client{state: state, http: &http.Client{Timeout: 30 * time.Second}}
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

func notice(w io.Writer, line string) {
	if w != nil {
		_, _ = fmt.Fprintln(w, line)
	}
}

// State is the running server this client talks to.
func (c *Client) State() runtime.State { return c.state }

// Response is a successful local API response; Body is kept byte for byte
// so --json can print exactly what the API returned.
type Response struct {
	Status int
	Body   []byte
}

// Do calls the local API with the instance secret. A failure status comes
// back as the server's *envelope.Error.
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
	if response.StatusCode >= http.StatusBadRequest {
		if e := envelope.Decode(data); e != nil {
			return Response{Status: response.StatusCode, Body: data}, e
		}
		return Response{Status: response.StatusCode, Body: data},
			envelope.New(envelope.Internal, uicopy.T("api.internal", nil)).WithReason(response.Status)
	}
	return Response{Status: response.StatusCode, Body: data}, nil
}
