package localserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

type posthogStub struct {
	mu     sync.Mutex
	events []map[string]any
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
	if got := f.drained(t, stub); len(got) != 0 {
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
	for _, event := range f.drained(t, stub) {
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
	if got := f.drained(t, stub); got[5]["engine"] != "sqlite" || got[5]["success"] != true || got[6]["success"] != false || got[7]["error_code"] != "already_exists" || got[4]["engine"] != "other" {
		t.Fatalf("event properties = %v", got[4:])
	}

	// The owner's local processes declare their channel with the instance
	// secret (review M1): cli, tui or agent is stored; web or anything else
	// is refused, and a session's body channel is ignored.
	for _, declared := range []string{"tui", "agent", "cli"} {
		off := telemetryDocument(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"disabled","channel":"` + declared + `"}`, bearer: testSecret}))
		if off.Telemetry.State != telemetry.StateDisabled || off.Telemetry.HasInstallID || off.Telemetry.Channel != declared {
			t.Fatalf("turn off declaring %s = %+v", declared, off)
		}
		_ = f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"enabled","confirmed_by_user":true,"channel":"` + declared + `"}`, bearer: testSecret})
		if consent, _ := telemetry.LoadConsent(f.dirs.Home); consent.Channel != declared || consent.State != telemetry.StateEnabled {
			t.Fatalf("enable declaring %s recorded %+v", declared, consent)
		}
	}
	for _, bad := range []string{"web", "robot"} {
		assertEnvelope(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"disabled","channel":"` + bad + `"}`, bearer: testSecret}),
			http.StatusBadRequest, envelope.InvalidArgument)
	}
	web := telemetryDocument(t, f.do(t, request{method: http.MethodPut, path: "/api/local/v1/telemetry", body: `{"state":"disabled","channel":"agent"}`, cookie: session, header: same}))
	if web.Telemetry.Channel != "web" {
		t.Fatalf("session declaring agent = %+v", web.Telemetry)
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
	if got := f.drained(t, stub); len(got) != 0 {
		t.Fatalf("sent with DO_NOT_TRACK: %v", got)
	}
}

// Review M5: with telemetry on, a console action returns without waiting
// for PostHog; the batch still arrives, sent in the background.
func TestConsoleActionsDoNotWaitForTheSend(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		action request
	}{
		{"posted events", request{method: http.MethodPost, path: "/api/local/v1/telemetry/events", body: `{"events":[{"event":"onboarding_started"}]}`}},
		{"observed action", request{method: http.MethodPost, path: "/api/local/v1/demo/install", body: `{}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arrived := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				arrived <- struct{}{}
				<-release
				return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
			})}
			t.Cleanup(unblock)

			f := newFixture(t, func(o *Options) {
				o.Telemetry = &telemetry.Recorder{Channel: telemetry.ChannelWeb, Home: o.Dirs.Home, Version: o.Record.Version,
					Getenv: func(string) string { return "" }, Key: "phc_test", Endpoint: "http://telemetry.invalid", Client: client}
			})
			if _, err := setup.ChangeTelemetry(f.dirs, telemetry.Change{State: telemetry.StateEnabled, ConfirmedByUser: true}, telemetry.ChannelWeb, f.now); err != nil {
				t.Fatal(err)
			}
			if decision := f.handler.server.opts.Telemetry.Decide(); !decision.Sending {
				t.Fatalf("telemetry decision = %+v, want sending", decision)
			}
			action := tc.action
			action.cookie = f.signIn(t)
			action.header = sameOrigin(testHost)
			if !json.Valid([]byte(action.body)) {
				t.Fatalf("invalid test request body: %q", action.body)
			}

			returned := make(chan *httptest.ResponseRecorder, 1)
			req := httptest.NewRequest(action.method, action.path, strings.NewReader(action.body))
			req.Host = testHost
			req.AddCookie(&http.Cookie{Name: SessionCookieName(testPort), Value: action.cookie})
			for name, value := range action.header {
				req.Header.Set(name, value)
			}
			req.Header.Set("Content-Type", "application/json")
			go func() {
				rec := httptest.NewRecorder()
				f.handler.ServeHTTP(rec, req)
				returned <- rec
			}()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			responseC, arrivedC := returned, arrived
			var response *httptest.ResponseRecorder
			for responseC != nil || arrivedC != nil {
				select {
				case response = <-responseC:
					responseC = nil
				case <-arrivedC:
					arrivedC = nil
				case <-deadline.C:
					if response == nil {
						t.Fatalf("response returned=false, telemetry endpoint entered=%v", arrivedC == nil)
					}
					t.Fatalf("response returned=true (%d %s), telemetry endpoint entered=%v", response.Code, response.Body, arrivedC == nil)
				}
			}
			if response.Code >= 400 {
				t.Errorf("%s = %d: %s", action.path, response.Code, response.Body)
			}
			// The response and endpoint arrival were both observed while the
			// endpoint remained blocked. A synchronous send would deadlock above.
			unblock()
		})
	}
}

// drained waits for the background sender, then returns what arrived.
func (f *fixture) drained(t *testing.T, stub *posthogStub) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.handler.server.opts.Telemetry.Drain(ctx)
	return stub.received()
}
