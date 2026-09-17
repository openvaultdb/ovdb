// Package localserver is the HTTP side of the local OVDB server: the
// hardening middleware chain, the local API (/api/local/v1/…), the landing
// page, and the openvaultdb-go data API (/v1/…) mounted behind them. It
// exists only in local mode; legacy `ovdb serve` never uses it
// (REQ:local-mode-only-for-new-surfaces).
//
// The registry arrives in a later increment; until then the data API serves
// an empty database map.
package localserver

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/web"
)

// AuthStoreFile holds scoped tokens, hashed, in OVDB home.
const AuthStoreFile = "auth.json"

// Options configure one local server.
type Options struct {
	Dirs   paths.Dirs
	Record runtime.Record // this run's server.json content
	Secret string
	// RequestShutdown is called once the shutdown response is written.
	RequestShutdown func()
	Now             func() time.Time // time.Now when nil
	// ErrorLog receives recovered panics; they are redacted before writing.
	ErrorLog io.Writer
	// Console serves the web console and apps to signed-in browsers;
	// web.Handler() when nil.
	Console http.Handler
	// Databases is the data API's database map; empty when nil. Tests use it
	// until the registry increment mounts databases/.
	Databases map[string]*core.Database
}

type localServer struct {
	opts     Options
	data     http.Handler
	console  http.Handler
	logins   *loginLinks
	sessions *sessions
}

// Handler is the local-mode handler with its full middleware chain.
type Handler struct {
	http.Handler
	server *localServer
}

// Flush writes pending session renewals; call it when the server stops.
func (h *Handler) Flush() error { return h.server.sessions.flush() }

// New builds the local-mode handler. It reads server.cors from config.yaml
// once: like the port, a change applies at the next start.
func New(opts Options) (*Handler, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Console == nil {
		opts.Console = web.Handler()
	}
	if opts.Databases == nil {
		opts.Databases = map[string]*core.Database{}
	}
	store, err := auth.OpenStore(filepath.Join(opts.Dirs.Home, AuthStoreFile))
	if err != nil {
		return nil, err
	}
	config, err := setup.LoadConfig(opts.Dirs.Home)
	if err != nil {
		return nil, err
	}
	data := server.New(opts.Record.Version, opts.Databases,
		server.WithAuth(&auth.Config{OwnerToken: opts.Secret, Store: store})).Handler()
	s := &localServer{
		opts: opts, data: data, console: opts.Console,
		logins: newLoginLinks(opts.Now), sessions: newSessions(opts.Dirs.Runtime, opts.Now),
	}

	var h http.Handler = http.HandlerFunc(s.route)
	h = cors(config.Server.CORS)(h)
	h = crossOrigin(h)
	h = s.authenticate(store)(h)
	h = hostAllowlist(opts.Record.Port)(h)
	h = recoverPanics(opts.ErrorLog)(h)
	h = securityHeaders(h)
	return &Handler{Handler: h, server: s}, nil
}

// LocalAPIPrefix is the versioned local API root.
const LocalAPIPrefix = "/api/local/v1/"

type endpoint struct {
	method, path string
	access       access
	handle       func(*localServer, http.ResponseWriter, *http.Request)
}

// endpoints is the local API. Endpoints() exposes it to the parity registry.
var endpoints = []endpoint{
	{http.MethodGet, runtime.WhoamiPath, accessOwner, (*localServer).whoami},
	{http.MethodGet, "/api/local/v1/status", accessOwner, (*localServer).status},
	{http.MethodGet, "/api/local/v1/server", accessOwner, (*localServer).serverInfo},
	{http.MethodPost, runtime.ShutdownPath, accessInstanceSecret, (*localServer).shutdown},
	{http.MethodPost, "/api/local/v1/login-links", accessInstanceSecret, (*localServer).loginLink},
	{http.MethodGet, "/api/local/v1/config", accessOwner, (*localServer).getConfig},
	{http.MethodPut, "/api/local/v1/config", accessOwner, (*localServer).putConfig},
}

// Endpoints lists the local API as "METHOD path".
func Endpoints() []string {
	out := make([]string, len(endpoints))
	for i, e := range endpoints {
		out[i] = e.method + " " + e.path
	}
	return out
}

func (s *localServer) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, LocalAPIPrefix):
		s.localAPI(w, r)
	case path == "/.well-known/openvaultdb" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.wellKnown(w)
	case path == "/v1" || strings.HasPrefix(path, "/v1/"):
		if credentialOf(r) == credentialSession {
			// The console acts as the owner: openvaultdb-go sees exactly
			// what the CLI's instance secret would send.
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+s.opts.Secret)
		}
		s.data.ServeHTTP(w, r)
	case path == loginPath:
		s.login(w, r)
	case path == pageCSSPath || path == submitJSPath:
		pageAsset(w, r)
	case path == "/authorize" || path == "/token":
		// The connect flow needs a console session to approve anything;
		// until increment 6 adds that check it is not offered at all.
		// The body uses the /v1 error shape these routes belong to.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(envelope.Marshal(v1Error{Error: v1ErrorDetail{
			Code: "not_supported", Message: uicopy.T("api.connect_not_supported", nil),
		}}))
	case credentialOf(r) == credentialSession:
		s.console.ServeHTTP(w, r)
	default:
		s.landing(w, r)
	}
}

func (s *localServer) localAPI(w http.ResponseWriter, r *http.Request) {
	credential := credentialOf(r)
	pathKnown := false
	for _, e := range endpoints {
		if e.path != r.URL.Path {
			continue
		}
		pathKnown = true
		if e.method != r.Method {
			continue
		}
		if !allow(w, credential, e.access) {
			return
		}
		if r.Method != http.MethodGet && !isJSON(r) {
			envelope.WriteStatus(w, http.StatusUnsupportedMediaType,
				envelope.New(envelope.InvalidArgument, uicopy.T("api.json_required", nil)))
			return
		}
		e.handle(s, w, r)
		return
	}
	// Unauthenticated callers learn nothing about which paths exist.
	if !allow(w, credential, accessOwner) {
		return
	}
	if pathKnown {
		envelope.WriteStatus(w, http.StatusMethodNotAllowed,
			envelope.New(envelope.InvalidArgument, uicopy.T("api.method_not_allowed", map[string]string{"method": r.Method})))
		return
	}
	envelope.Write(w, envelope.New(envelope.NotFound, uicopy.T("api.not_found", nil)))
}

// isJSON enforces the application/json body rule for non-GET requests, which
// also keeps plain HTML forms on other sites from reaching the local API.
func isJSON(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// wellKnown is openvaultdb-go's discovery document without the connect
// endpoints, which local mode does not serve until increment 6.
func (s *localServer) wellKnown(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(envelope.Marshal(map[string]any{
		"name": "OpenVaultDB", "protocol": "openvaultdb/0.1", "version": s.opts.Record.Version, "authEnabled": true,
	}))
}

func (s *localServer) whoami(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, runtime.Whoami{
		Schema: envelope.Schema, InstanceID: s.opts.Record.InstanceID, Version: s.opts.Record.Version,
	})
}

func (s *localServer) server() setup.Server {
	return setup.RunningServer(&s.opts.Record, s.opts.Dirs)
}

func (s *localServer) status(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, setup.NewStatus(s.opts.Record.Version, s.opts.Dirs, s.server()))
}

func (s *localServer) serverInfo(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, setup.NewServerDocument(s.server()))
}

func (s *localServer) shutdown(w http.ResponseWriter, _ *http.Request) {
	info := s.server()
	info.State = setup.StateStopping
	envelope.WriteJSON(w, http.StatusOK, setup.NewServerDocument(info))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if s.opts.RequestShutdown != nil {
		s.opts.RequestShutdown()
	}
}

func (s *localServer) getConfig(w http.ResponseWriter, _ *http.Request) {
	config, err := setup.LoadConfig(s.opts.Dirs.Home)
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, setup.NewConfigDocument(config, false))
}

func (s *localServer) putConfig(w http.ResponseWriter, r *http.Request) {
	var change setup.ConfigChange
	if !decodeBody(w, r, &change) {
		return
	}
	document, err := setup.ApplyConfigChange(s.opts.Dirs, change, true)
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, document)
}

// decodeBody reads a small JSON body into v, writing invalid_argument on
// failure. An empty body decodes as {}.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		err = json.Unmarshal(data, v)
	}
	if err != nil {
		envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("api.bad_json", nil)).WithReason(err.Error()))
		return false
	}
	return true
}

// writeError sends an envelope error as is and anything else as internal,
// without the raw text (REQ:redacted-errors).
func writeError(w http.ResponseWriter, err error) {
	if e := envelope.As(err); e != nil {
		envelope.Write(w, e)
		return
	}
	envelope.Write(w, envelope.New(envelope.Internal, uicopy.T("api.internal", nil)))
}

// v1Error is openvaultdb-go's /v1 error body.
type v1Error struct {
	Error v1ErrorDetail `json:"error"`
}

type v1ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
