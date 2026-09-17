package localserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

type posthogStub struct {
	mu     sync.Mutex
	events []map[string]any
}

func (p *posthogStub) received() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.events...)
}

// telemetryFixture is a server whose recorder has a key and posts to a
// recording endpoint, with env as its process environment.
func telemetryFixture(t *testing.T, env map[string]string) (*fixture, *posthogStub) {
	stub := &posthogStub{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body struct {
			Batch []struct {
				Event      string         `json:"event"`
				Properties map[string]any `json:"properties"`
			} `json:"batch"`
		}
		_ = json.Unmarshal(data, &body)
		stub.mu.Lock()
		defer stub.mu.Unlock()
		for _, e := range body.Batch {
			e.Properties["event"] = e.Event
			stub.events = append(stub.events, e.Properties)
		}
	}))
	t.Cleanup(endpoint.Close)
	f := newFixture(t, func(o *Options) {
		o.Telemetry = &telemetry.Recorder{Channel: telemetry.ChannelWeb, Home: o.Dirs.Home, Version: o.Record.Version,
			Getenv: func(key string) string { return env[key] }, Key: "phc_test", Endpoint: endpoint.URL}
	})
	return f, stub
}

func telemetryDocument(t *testing.T, rec *httptest.ResponseRecorder) telemetry.Document {
	t.Helper()
	var document telemetry.Document
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &document) != nil {
		t.Fatalf("telemetry document = %d %s", rec.Code, rec.Body)
	}
	return document
}

// The web half of AC:enable-then-disable and AC:channel-derived, and the
// server side of AC:buffer-flushed-or-dropped: nothing is sent or kept
// before Turn on; the page's buffer is sent after it, as web events.
func TestConsoleTelemetryConsentAndEvents(t *testing.T) {
	t.Parallel()
	f, stub := telemetryFixture(t, nil)
	session := f.signIn(t)
	same := sameOrigin(testHost)

	status := telemetryDocument(t, f.do(t, request{path: "/api/local/v1/telemetry", cookie: session})).Telemetry
	if status.State != telemetry.StateNotAsked || status.Sending || !status.Available || status.Provider == "" || len(status.NeverCollected) != 3 {
		t.Fatalf("initial status = %+v", status)
	}
	// Before consent: a console action and posted events send nothing.
	if rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/demo/install", body: `{}`, cookie: session, header: same}); rec.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", rec.Code, rec.Body)
	}
	posted := `{"events":[{"event":"onboarding_started"},{"event":"onboarding_option_selected","option":"demo"},{"event":"demo_installed","success":true},{"event":"database_deleted","engine":"/home/ann"},{"event":"engine_selected","engine":"/home/ann/secret-db"}]}`
	var result TelemetryEventsResult
	rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/telemetry/events", body: posted, cookie: session, header: same})
	if json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Accepted != 4 || result.Sent {
		t.Fatalf("events before consent = %d %s", rec.Code, rec.Body)
	}
	if got := stub.received(); len(got) != 0 {
		t.Fatalf("sent before consent: %v", got)
	}

	// Turn on needs the click's confirmation, then records web as the channel.
	assertEnvelope(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"enabled"}`, cookie: session, header: same}),
		http.StatusBadRequest, envelope.ConfirmationRequired)
	on := telemetryDocument(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry",
		body: `{"state":"enabled","confirmed_by_user":true,"channel":"cli"}`, cookie: session, header: same}))
	if on.Telemetry.State != telemetry.StateEnabled || on.Telemetry.Channel != "web" || !on.Telemetry.HasInstallID || on.Changed == nil || !*on.Changed {
		t.Fatalf("turn on = %+v", on)
	}
	rec = f.do(t, request{method: http.MethodPost, path: "/api/local/v1/telemetry/events", body: posted, cookie: session, header: same})
	if json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Accepted != 4 || !result.Sent {
		t.Fatalf("events after consent = %d %s", rec.Code, rec.Body)
	}
	// A console action is reported by the server; the CLI's, made with the
	// instance secret, is not.
	create, _ := json.Marshal(setup.CreateRequest{ID: "notes", Engine: "sqlite", Path: filepath.Join(f.dirs.Data, "notes.sqlite")})
	if rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/databases", body: string(create), cookie: session, header: same}); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/databases", body: string(create), cookie: session, header: same}); rec.Code < 400 {
		t.Fatalf("second create = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, request{method: http.MethodPost, path: "/api/local/v1/demo/install", body: `{}`, bearer: testSecret}); rec.Code >= 400 {
		t.Fatalf("CLI install = %d %s", rec.Code, rec.Body)
	}

	names := []string{}
	for _, event := range stub.received() {
		names = append(names, event["event"].(string))
		data, _ := json.Marshal(event)
		if event["channel"] != "web" || strings.Contains(string(data), "/home/ann") || strings.Contains(string(data), "notes") {
			t.Errorf("event %s", data)
		}
	}
	want := "telemetry_consent_changed,onboarding_started,onboarding_option_selected,demo_installed,engine_selected,database_created,database_created,onboarding_error"
	if strings.Join(names, ",") != want {
		t.Fatalf("events = %v\nwant %s", names, want)
	}
	if got := stub.received(); got[5]["engine"] != "sqlite" || got[5]["success"] != true || got[6]["success"] != false || got[7]["error_code"] != "already_exists" || got[4]["engine"] != "other" {
		t.Fatalf("event properties = %v", got[4:])
	}

	// Turning it off from the CLI removes the install id; the CLI's own
	// channel is recorded.
	off := telemetryDocument(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"disabled","channel":"tui"}`, bearer: testSecret}))
	if off.Telemetry.State != telemetry.StateDisabled || off.Telemetry.HasInstallID || off.Telemetry.Channel != "tui" {
		t.Fatalf("turn off = %+v", off)
	}
}

// REQ:sender-process-decides: the server's own environment decides for
// console events, and GET names its reason.
func TestServerEnvironmentForcesConsoleTelemetryOff(t *testing.T) {
	t.Parallel()
	f, stub := telemetryFixture(t, map[string]string{"DO_NOT_TRACK": "1"})
	session := f.signIn(t)
	same := sameOrigin(testHost)
	on := telemetryDocument(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"enabled","confirmed_by_user":true}`, cookie: session, header: same}))
	if on.Telemetry.State != telemetry.StateEnabled || on.Telemetry.Sending || on.Telemetry.Reason != "DO_NOT_TRACK" {
		t.Fatalf("status = %+v", on.Telemetry)
	}
	_ = f.do(t, request{method: http.MethodPost, path: "/api/local/v1/telemetry/events", body: `{"events":[{"event":"onboarding_started"}]}`, cookie: session, header: same})
	if got := stub.received(); len(got) != 0 {
		t.Fatalf("sent with DO_NOT_TRACK: %v", got)
	}
}
