package localserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// getTelemetry is capability 23 as the server process sees it: the forced-off
// conditions are this process's, which is what applies to web console events
// (telemetry-consent#REQ:sender-process-decides).
func (s *localServer) getTelemetry(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, telemetry.NewDocument(s.opts.Telemetry.Decide(), s.opts.Telemetry.Available()))
}

// putTelemetry is capability 24. A console session may turn telemetry on:
// the person clicked Turn on (AC:enable-then-disable). A session's deciding
// channel is always web, whatever the body says. Instance-secret callers
// are the owner's local processes (CLI, TUI, an agent harness running the
// CLI), which declare cli, tui or agent; anything else is refused (review
// M1). Enabling always needs confirmed_by_user
// (REQ:enable-requires-a-person).
func (s *localServer) putTelemetry(w http.ResponseWriter, r *http.Request) {
	var change telemetry.Change
	if !decodeBody(w, r, &change) {
		return
	}
	session := credentialOf(r) == credentialSession
	channel := telemetry.ChannelWeb
	if !session {
		switch declared := telemetry.Channel(change.Channel); declared {
		case "":
			channel = telemetry.ChannelCLI
		case telemetry.ChannelCLI, telemetry.ChannelTUI, telemetry.ChannelAgent:
			channel = declared
		default:
			envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("telemetry.failed", nil)).
				WithReason(uicopy.T("telemetry.invalid_channel", map[string]string{"channel": change.Channel})))
			return
		}
	}
	outcome, err := setup.ChangeTelemetry(s.opts.Dirs, change, channel, s.opts.Now())
	if err != nil {
		writeError(w, err)
		return
	}
	changed := outcome.Changed
	document := telemetry.NewDocument(s.opts.Telemetry.Decide(), s.opts.Telemetry.Available())
	document.Changed, document.Backup = &changed, outcome.Backup
	envelope.WriteJSON(w, http.StatusOK, document)
	if session && changed && change.State == telemetry.StateEnabled {
		s.sendAfterResponse(w, r, telemetry.NewConsentEnabled())
	}
}

// TelemetryEvents is the body of POST /api/local/v1/telemetry/events: the
// web page's pre-consent buffer, posted only after Turn on.
type TelemetryEvents struct {
	Events []telemetry.Wire `json:"events"`
}

// TelemetryEventsResult says how many events were accepted into the closed
// set and whether this process sent them.
type TelemetryEventsResult struct {
	Schema   int  `json:"schema"`
	Accepted int  `json:"accepted"`
	Sent     bool `json:"sent"`
}

// postTelemetryEvents rebuilds each posted event through the closed
// constructors (unknown events are dropped) and sends them as web events
// when this process may send; otherwise they are dropped, never kept — the
// server does not buffer (REQ:pre-consent-buffer).
func (s *localServer) postTelemetryEvents(w http.ResponseWriter, r *http.Request) {
	var body TelemetryEvents
	if !decodeBody(w, r, &body) {
		return
	}
	events := []telemetry.Event{}
	for _, wire := range body.Events {
		if len(events) == telemetry.MaxBuffered {
			break
		}
		if e, ok := telemetry.FromWire(wire); ok {
			events = append(events, e)
		}
	}
	sending := s.opts.Telemetry.Decide().Sending
	envelope.WriteJSON(w, http.StatusOK, TelemetryEventsResult{Schema: envelope.Schema, Accepted: len(events), Sent: sending && len(events) > 0})
	if sending {
		s.sendAfterResponse(w, r, events...)
	}
}

// sendAfterResponse records events and hands them to the background
// sender, so a slow endpoint never delays the response (review M5).
func (s *localServer) sendAfterResponse(_ http.ResponseWriter, _ *http.Request, events ...telemetry.Event) {
	s.opts.Telemetry.Record(events...)
	s.opts.Telemetry.FlushInBackground()
}

// captured keeps a handler's status and a bounded copy of its body, for the
// event describing a console action.
type captured struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captured) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *captured) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.body.Len() < 1<<16 {
		c.body.Write(p)
	}
	return c.ResponseWriter.Write(p)
}

func (c *captured) Flush() {
	if flusher, ok := c.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// observed are the console actions the server reports, by endpoint, with
// the onboarding step their errors belong to.
var observed = map[string]string{
	http.MethodPost + " /api/local/v1/databases":         "create",
	http.MethodPost + " /api/local/v1/databases/connect": "connect",
	http.MethodPost + " /api/local/v1/demo/install":      "demo",
	http.MethodPost + " /api/local/v1/explore/datatug":   "explore",
	http.MethodPost + " /api/local/v1/skills/install":    "skills",
}

// observe runs e's handler and, for a console session's onboarding action,
// sends its event from this process afterwards (REQ:sender-process-decides:
// the server sends web console events; CLI and TUI requests, made with the
// instance secret, are reported by their own process).
func (s *localServer) observe(e endpoint, w http.ResponseWriter, r *http.Request) {
	step, ok := observed[e.method+" "+e.path]
	if !ok || credentialOf(r) != credentialSession || !s.opts.Telemetry.Decide().Sending {
		e.handle(s, w, r)
		return
	}
	request, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(request))
	start := s.opts.Now()
	c := &captured{ResponseWriter: w}
	e.handle(s, c, r)
	success := c.status > 0 && c.status < 400
	elapsed := s.opts.Now().Sub(start)
	if step == "skills" {
		var body skills.InstallRequest
		_ = json.Unmarshal(request, &body)
		var failure error
		if !success {
			failure = envelope.Decode(c.body.Bytes())
		}
		s.sendAfterResponse(c, r, skills.TelemetryEvents(body, c.body.Bytes(), failure)...)
		return
	}
	events := []telemetry.Event{consoleEvent(step, request, c.body.Bytes(), success, elapsed)}
	if !success {
		events = append(events, telemetry.NewOnboardingError(step, envelope.Decode(c.body.Bytes())))
	}
	s.sendAfterResponse(c, r, events...)
}

// consoleEvent reads only enum-bearing fields from the request and result.
func consoleEvent(step string, request, response []byte, success bool, elapsed time.Duration) telemetry.Event {
	switch step {
	case "create":
		var body setup.CreateRequest
		_ = json.Unmarshal(request, &body)
		if body.Engine == "" {
			body.Engine = setup.EngineInGitDB
		}
		return telemetry.NewDatabaseCreated(body.Engine, success, elapsed)
	case "connect":
		var body setup.ConnectRequest
		_ = json.Unmarshal(request, &body)
		var result setup.DatabaseResult
		if json.Unmarshal(response, &result) == nil && result.Database.Engine != "" {
			body.Engine = result.Database.Engine
		}
		return telemetry.NewDatabaseConnected(body.Engine, success, elapsed)
	case "demo":
		var document demo.Document
		return telemetry.NewDemoInstalled(success, success && json.Unmarshal(response, &document) == nil && document.AlreadyInstalled)
	default:
		var document explore.DataTugCLI
		return telemetry.NewExploreDataSelected("datatug_cli", success && json.Unmarshal(response, &document) == nil && document.OnPath)
	}
}
