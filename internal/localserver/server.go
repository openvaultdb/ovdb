// Package localserver is the HTTP side of the local OVDB server: the
// hardening middleware chain, the local API (/api/local/v1/…), the landing
// page, and the openvaultdb-go data API (/v1/…) mounted behind them. It
// exists only in local mode; legacy `ovdb serve` never uses it
// (REQ:local-mode-only-for-new-surfaces).
//
// The data API serves the databases registered in <OVDB home>/databases,
// mounted by the setup.Registry this server owns.
package localserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openvaultdb/openvaultdb-go/pkg/auth"
	"github.com/openvaultdb/openvaultdb-go/pkg/core"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
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
	// Databases are mounted next to the registry's (tests).
	Databases map[string]*core.Database
	// MountTimeout bounds each registered database's mount;
	// setup.DefaultMountTimeout when zero.
	MountTimeout time.Duration
}

type localServer struct {
	opts     Options
	data     http.Handler
	console  http.Handler
	logins   *loginLinks
	sessions *sessions
	registry *setup.Registry
	demo     *demo.Service
	// contextMu serialises context writes.
	contextMu sync.Mutex
}

// Handler is the local-mode handler with its full middleware chain.
type Handler struct {
	http.Handler
	server *localServer
}

// Flush writes pending session renewals; call it when the server stops.
func (h *Handler) Flush() error { return h.server.sessions.flush() }

// Close unmounts every registered database, releasing engine resources, and
// removes mounts.json. Call it after the HTTP server has shut down.
func (h *Handler) Close() { h.server.registry.Close() }

// MountDatabases mounts the registered databases, each within its deadline,
// returning when all are settled or ctx ends. Run calls it in the background
// once the server listens, so no storage can keep the server from starting
// or stopping.
func (h *Handler) MountDatabases(ctx context.Context) { h.server.registry.MountAll(ctx) }

// logf writes one redacted, timestamped line to w (server.log).
func logf(w io.Writer, now func() time.Time) func(format string, args ...any) {
	return func(format string, args ...any) {
		if w == nil {
			return
		}
		line := redact.String(fmt.Sprintf(format, args...))
		_, _ = fmt.Fprintf(w, "%s %s\n", now().UTC().Format(time.RFC3339), line)
	}
}

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
	dataServer := server.New(opts.Record.Version, opts.Databases,
		server.WithAuth(&auth.Config{OwnerToken: opts.Secret, Store: store}))
	registry, err := setup.OpenRegistry(opts.Dirs, dataServer, logf(opts.ErrorLog, opts.Now), setup.RegistryOptions{MountTimeout: opts.MountTimeout})
	if err != nil {
		return nil, err
	}
	s := &localServer{
		opts: opts, data: dataServer.Handler(), console: opts.Console,
		logins: newLoginLinks(opts.Now), sessions: newSessions(opts.Dirs.Runtime, opts.Now),
		registry: registry,
	}
	s.protectAuthStore()
	s.demo = &demo.Service{Dirs: opts.Dirs, Registry: registry, Seed: s.seedData, Now: opts.Now, Logf: logf(opts.ErrorLog, opts.Now)}

	var h http.Handler = http.HandlerFunc(s.route)
	h = cors(setup.NormalizeOrigins(config.Server.CORS))(h)
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
	{http.MethodGet, "/api/local/v1/home", accessOwner, (*localServer).home},
	{http.MethodGet, "/api/local/v1/server", accessOwner, (*localServer).serverInfo},
	{http.MethodPost, runtime.ShutdownPath, accessInstanceSecret, (*localServer).shutdown},
	{http.MethodPost, "/api/local/v1/login-links", accessInstanceSecret, (*localServer).loginLink},
	{http.MethodGet, "/api/local/v1/config", accessOwner, (*localServer).getConfig},
	{http.MethodPut, "/api/local/v1/config", accessOwner, (*localServer).putConfig},
	{http.MethodGet, "/api/local/v1/engines", accessOwner, (*localServer).engines},
	{http.MethodGet, "/api/local/v1/databases", accessOwner, (*localServer).databases},
	{http.MethodPost, "/api/local/v1/databases", accessOwner, (*localServer).createDatabase},
	{http.MethodPost, "/api/local/v1/databases/{id}/reload", accessOwner, (*localServer).reloadDatabase},
	{http.MethodPost, "/api/local/v1/databases/reload", accessOwner, (*localServer).reloadAll},
	{http.MethodDelete, "/api/local/v1/databases/{id}", accessOwner, (*localServer).removeDatabase},
	{http.MethodGet, "/api/local/v1/context", accessOwner, (*localServer).getContext},
	{http.MethodPut, "/api/local/v1/context", accessOwner, (*localServer).putContext},
	{http.MethodGet, "/api/local/v1/demo", accessOwner, (*localServer).getDemo},
	{http.MethodPost, "/api/local/v1/demo/install", accessOwner, (*localServer).installDemo},
}

// matchPath reports whether path matches pattern, where a "{name}" segment
// matches one non-empty path segment, and returns the matched values.
func matchPath(pattern, path string) (map[string]string, bool) {
	patternParts, pathParts := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(patternParts) != len(pathParts) {
		return nil, false
	}
	var values map[string]string
	for i, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if pathParts[i] == "" {
				return nil, false
			}
			if values == nil {
				values = map[string]string{}
			}
			values[part[1:len(part)-1]] = pathParts[i]
			continue
		}
		if part != pathParts[i] {
			return nil, false
		}
	}
	return values, true
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
	if strings.HasPrefix(path, LocalAPIPrefix) || path == "/v1" || strings.HasPrefix(path, "/v1/") {
		// Status documents carry home paths and ids; never keep them.
		w.Header().Set("Cache-Control", "no-store")
	}
	switch {
	case strings.HasPrefix(path, LocalAPIPrefix):
		s.localAPI(w, r)
	case path == "/.well-known/openvaultdb" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.wellKnown(w)
	case path == "/v1" || strings.HasPrefix(path, "/v1/"):
		tokens := path == "/v1/tokens" || strings.HasPrefix(path, "/v1/tokens/")
		if credentialOf(r) == credentialSession {
			if tokens {
				// Parity E7: tokens come from the CLI, or from the connect
				// flow's consent page, never from a console session.
				envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.session_tokens_not_allowed", nil)).
					WithNext(envelope.Next{Label: uicopy.T("next.tokens_cli", nil), Command: tokensCommand(r.Method)}))
				return
			}
			// The console acts as the owner: openvaultdb-go sees exactly
			// what the CLI's instance secret would send.
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+s.opts.Secret)
		}
		if credential := credentialOf(r); credential != credentialNone && credential != credentialInvalid {
			if id, ok := databaseOf(path); ok {
				s.registry.AwaitMount(r.Context(), id)
			}
		}
		s.data.ServeHTTP(w, r)
		if tokens && r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.protectAuthStore()
		}
	case path == loginPath:
		s.login(w, r)
	case path == logoutPath:
		s.logout(w, r)
	case path == pageCSSPath || path == submitJSPath:
		pageAsset(w, r)
	case path == authorizePath:
		s.authorize(w, r)
	case path == tokenPath:
		// Exchanging a code needs no credential: the code is the credential.
		s.data.ServeHTTP(w, r)
		if r.Method == http.MethodPost {
			s.protectAuthStore()
		}
	case credentialOf(r) == credentialSession && path != signedOutPath:
		s.console.ServeHTTP(w, r)
	default:
		s.landing(w, r)
	}
}

// tokensCommand is the CLI command a console session is sent to instead of a
// /v1/tokens request with method.
func tokensCommand(method string) string {
	switch method {
	case http.MethodPost:
		return "ovdb token create --db <database> --scope read-only"
	case http.MethodDelete:
		return "ovdb token revoke <token-id>"
	default:
		return "ovdb token list"
	}
}

// databaseOf is the database id a /v1/databases/{db}/… path names.
func databaseOf(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/v1/databases/")
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	id, err := url.PathUnescape(id)
	return id, err == nil && id != ""
}

func (s *localServer) localAPI(w http.ResponseWriter, r *http.Request) {
	credential := credentialOf(r)
	pathKnown := false
	for _, e := range endpoints {
		values, matched := matchPath(e.path, r.URL.Path)
		if !matched {
			continue
		}
		pathKnown = true
		if e.method != r.Method {
			continue
		}
		if !allow(w, credential, e.access) {
			return
		}
		// DELETE carries no body; a plain HTML form cannot send it.
		if r.Method != http.MethodGet && r.Method != http.MethodDelete && !isJSON(r) {
			envelope.WriteStatus(w, http.StatusUnsupportedMediaType,
				envelope.New(envelope.InvalidArgument, uicopy.T("api.json_required", nil)))
			return
		}
		for name, value := range values {
			r.SetPathValue(name, value)
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

// wellKnown is openvaultdb-go's discovery document, connect endpoints
// included.
func (s *localServer) wellKnown(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(envelope.Marshal(map[string]any{
		"name": "OpenVaultDB", "protocol": "openvaultdb/0.1", "version": s.opts.Record.Version, "authEnabled": true,
		"authorizeEndpoint": authorizePath, "tokenEndpoint": tokenPath,
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

func (s *localServer) status(w http.ResponseWriter, r *http.Request) {
	databases, err := s.registry.List()
	if err != nil {
		writeError(w, err)
		return
	}
	context := s.resolveContext(r, databases).Context
	envelope.WriteJSON(w, http.StatusOK, setup.NewStatus(s.opts.Record.Version, s.opts.Dirs, s.server(), databases, context))
}

func (s *localServer) home(w http.ResponseWriter, r *http.Request) {
	databases, err := s.registry.List()
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, setup.NewHome(s.server(), databases, s.resolveContext(r, databases).Context))
}

// resolveContext applies the context ladder for the directories the client
// sent (its project, walked up); the browser sends none, so it sees the
// global default or the only database.
func (s *localServer) resolveContext(r *http.Request, databases []setup.Database) dbcontext.Document {
	return dbcontext.Resolve(s.opts.Dirs.Home, setup.DatabaseIDs(databases), dbcontext.RequestFrom(r.URL.Query()))
}

func (s *localServer) getContext(w http.ResponseWriter, r *http.Request) {
	databases, err := s.registry.List()
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, s.resolveContext(r, databases))
}

// putContext stores or clears a project context or the global default
// (capability 13). Only a terminal client knows its working directory, so a
// console session may change only the global default (parity E3,
// database-context-navigation#REQ:select-database-in-tui-and-web).
func (s *localServer) putContext(w http.ResponseWriter, r *http.Request) {
	var change dbcontext.Change
	if !decodeBody(w, r, &change) {
		return
	}
	if change.Scope != dbcontext.ScopeGlobal && credentialOf(r) != credentialInstanceSecret {
		envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.session_project_context", nil)).
			WithNext(envelope.Next{Label: uicopy.T("next.use_in_project", nil), Command: "ovdb use <database>"}))
		return
	}
	databases, err := s.registry.List()
	if err != nil {
		writeError(w, err)
		return
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	document, err := dbcontext.Apply(s.opts.Dirs.Home, setup.DatabaseIDs(databases), change)
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, document)
}

func (s *localServer) engines(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, setup.NewEnginesDocument(s.opts.Dirs.Home))
}

func (s *localServer) databases(w http.ResponseWriter, _ *http.Request) {
	databases, err := s.registry.List()
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, setup.NewDatabasesDocument(databases))
}

func (s *localServer) createDatabase(w http.ResponseWriter, r *http.Request) {
	var request setup.CreateRequest
	if !decodeBody(w, r, &request) {
		return
	}
	result, err := s.registry.Create(request)
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, result)
}

func (s *localServer) reloadDatabase(w http.ResponseWriter, r *http.Request) {
	result, err := s.registry.Reload(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func (s *localServer) reloadAll(w http.ResponseWriter, r *http.Request) {
	document, err := s.registry.ReloadAll(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, document)
}

func (s *localServer) removeDatabase(w http.ResponseWriter, r *http.Request) {
	result, err := s.registry.Remove(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

// getDemo is the TODO demo document (capabilities 18 and 19); the TODO app
// finds its database here (todo-demo#REQ:todo-app-same-origin).
func (s *localServer) getDemo(w http.ResponseWriter, _ *http.Request) {
	databases, err := s.registry.List()
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, demo.Inspect(s.opts.Dirs, databases))
}

// installDemo installs the TODO demo (capability 18). A console session may
// install it too: the request passed cross-origin protection.
func (s *localServer) installDemo(w http.ResponseWriter, r *http.Request) {
	var request demo.InstallRequest
	if !decodeBody(w, r, &request) {
		return
	}
	document, err := s.demo.Install(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if document.AlreadyInstalled {
		status = http.StatusOK
	}
	envelope.WriteJSON(w, status, document)
}

// seedData writes the demo's records in one batch through the data API, as
// the owner: the files are exactly what any client writing them produces.
func (s *localServer) seedData(ctx context.Context, id string, ops []demo.Op) error {
	type op struct {
		Op   string         `json:"op"`
		Key  string         `json:"key"`
		Data map[string]any `json:"data"`
	}
	batch := struct {
		Message string `json:"message"`
		Ops     []op   `json:"ops"`
	}{Message: "Install the TODO demo"}
	for _, o := range ops {
		batch.Ops = append(batch.Ops, op{Op: "insert", Key: strings.TrimPrefix(o.Path, "/"), Data: o.Data})
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/databases/"+url.PathEscape(id)+"/batch", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.opts.Secret)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.data.ServeHTTP(recorder, request)
	if recorder.Code >= http.StatusMultipleChoices {
		var failure v1Error
		_ = json.Unmarshal(recorder.Body.Bytes(), &failure)
		if failure.Error.Message == "" {
			failure.Error.Message = http.StatusText(recorder.Code)
		}
		return fmt.Errorf("%s", failure.Error.Message)
	}
	return nil
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
	if change.Key == setup.KeyServerCORS && credentialOf(r) != credentialInstanceSecret {
		// Parity E7: browser app origins are a CLI-only developer setting.
		envelope.Write(w, envelope.New(envelope.Forbidden, uicopy.T("api.session_cors_not_allowed", nil)).
			WithNext(envelope.Next{Label: uicopy.T("next.cors_cli", nil), Command: "ovdb config set " + setup.KeyServerCORS + " <origins>"}))
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

// writeError sends an envelope error, with its reason redacted, and
// anything else as internal, without the raw text (REQ:redacted-errors).
func writeError(w http.ResponseWriter, err error) {
	if e := envelope.As(err); e != nil {
		e.Message, e.Reason = redact.String(e.Message), redact.String(e.Reason)
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
