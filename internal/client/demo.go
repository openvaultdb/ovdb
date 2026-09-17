package client

import (
	"context"
	"encoding/json"
	"net/http"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
	"github.com/openvaultdb/ovdb/web"
)

// Demo API paths.
const (
	DemoPath        = "/api/local/v1/demo"
	DemoInstallPath = "/api/local/v1/demo/install"
)

// Demo is the TODO demo document (capabilities 18 and 19): from the server
// when it runs, otherwise from the registry, starting nothing.
func (l *Local) Demo(ctx context.Context) ([]byte, error) {
	return l.readErr(ctx, DemoPath, func() (any, error) {
		databases, err := setup.ListDatabases(l.Dirs.Home, nil)
		return demo.Inspect(l.Dirs, databases), err
	})
}

// InstallDemo installs the TODO demo through the server, starting it unless
// noStart. The location is resolved under this client's data home
// (REQ:client-values-and-mismatch).
func (l *Local) InstallDemo(ctx context.Context, request demo.InstallRequest, noStart bool) (body []byte, err error) {
	defer l.trackDemoInstall(&body, &err)
	if request.Path == "" {
		id := request.ID
		if id == "" {
			id = demo.DefaultID
		}
		request.Path = demo.Location(l.Dirs.Data, id)
	}
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, DemoInstallPath, request)
	return response.Body, err
}

func (l *Local) consoleBuilt() bool {
	if l.ConsoleBuilt != nil {
		return l.ConsoleBuilt()
	}
	return web.Built()
}

// DemoLink is a login link that lands on the TODO app, starting the server
// unless noStart (todo-demo#REQ:todo-app-same-origin). A binary without the
// web console, or a demo not installed yet, fails with what to do instead of
// opening a page that cannot work
// (local-server-and-web-console#REQ:embedded-assets). Whether the demo is
// installed is read first, from the running server or the registry, so a
// demo that isn't there never starts a server.
func (l *Local) DemoLink(ctx context.Context, noStart bool) (link []byte, err error) {
	defer l.trackDemoOpen(&err)
	failed := uicopy.T("demo.open.failed", nil)
	if !l.consoleBuilt() {
		return nil, envelope.New(envelope.Unsupported, failed).
			WithReason(uicopy.T("demo.open.not_built", nil)).
			WithNext(envelope.Next{Label: uicopy.T("next.install_homebrew", nil), Command: "brew install --cask openvaultdb/tap/ovdb"},
				envelope.Next{Label: uicopy.T("next.download_release", nil)})
	}
	body, err := l.Demo(ctx)
	if err != nil {
		return nil, err
	}
	var document demo.Document
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	if !document.Installed {
		return nil, envelope.New(envelope.NotFound, failed).
			WithReason(uicopy.T("demo.open.not_installed", nil)).
			WithNext(envelope.Next{Label: uicopy.T("demo.next.install", nil), Command: "ovdb demo install --yes", Action: demo.ActionInstall})
	}
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, LoginLinksPath, localserver.LoginLinkRequest{Next: document.AppPath})
	return response.Body, err
}
