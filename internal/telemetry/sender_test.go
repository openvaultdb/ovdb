package telemetry_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// recorder is a PostHog stand-in that keeps every batch it receives.
type recorder struct {
	mu      sync.Mutex
	batches []map[string]any
}

func newRecorder(t *testing.T) (*recorder, string) {
	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/batch/" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		rec.mu.Lock()
		rec.batches = append(rec.batches, body)
		rec.mu.Unlock()
	}))
	t.Cleanup(server.Close)
	return rec, server.URL
}

func (r *recorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, b := range r.batches {
		for _, e := range b["batch"].([]any) {
			names = append(names, e.(map[string]any)["event"].(string))
		}
	}
	return names
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func newTestRecorder(t *testing.T, endpoint string, buffer bool, vars map[string]string) (*telemetry.Recorder, paths.Dirs) {
	home := t.TempDir()
	return &telemetry.Recorder{Channel: telemetry.ChannelCLI, Home: home, Version: "1.0.0", Getenv: env(vars),
		Buffer: buffer, Key: "phc_test", Endpoint: endpoint}, paths.Dirs{Home: home}
}

func decide(t *testing.T, dirs paths.Dirs, state string) {
	t.Helper()
	_, _, err := setup.ApplyTelemetryChange(dirs, telemetry.Change{State: state, ConfirmedByUser: true}, telemetry.ChannelCLI, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

// AC:nothing-sent-by-default: not_asked sends nothing and has no install id.
func TestNothingSentByDefault(t *testing.T) {
	rec, endpoint := newRecorder(t)
	r, dirs := newTestRecorder(t, endpoint, false, nil)
	r.Record(telemetry.NewDemoInstalled(true, false), telemetry.NewDatabaseCreated("ingitdb", true, time.Second), telemetry.NewServerStarted(true, true, time.Second))
	r.Flush(context.Background())
	if rec.count() != 0 || r.Pending() != 0 {
		t.Fatalf("sent %d batches, %d pending", rec.count(), r.Pending())
	}
	d := r.Decide()
	if d.State != telemetry.StateNotAsked || d.Consent.InstallID != "" {
		t.Fatalf("decision = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dirs.Home, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config.yaml written: %v", err)
	}
}

// AC:events-delivered-within-bound, first half: enabled sends one batch
// before Flush returns, with the install id.
func TestEnabledFlushSendsOneBatch(t *testing.T) {
	rec, endpoint := newRecorder(t)
	r, dirs := newTestRecorder(t, endpoint, false, nil)
	decide(t, dirs, telemetry.StateEnabled)
	r.Record(telemetry.NewDatabaseCreated("sqlite", true, 3*time.Millisecond), telemetry.NewOnboardingError("create", envelope.New(envelope.AlreadyExists, "notes exists")))
	r.Flush(context.Background())
	if got := rec.events(); len(got) != 2 || got[0] != "database_created" || got[1] != "onboarding_error" || rec.count() != 1 {
		t.Fatalf("events = %v in %d batches", got, rec.count())
	}
	consent, _ := telemetry.LoadConsent(dirs.Home)
	properties := rec.batches[0]["batch"].([]any)[0].(map[string]any)["properties"].(map[string]any)
	if rec.batches[0]["api_key"] != "phc_test" || properties["install_id"] != consent.InstallID || properties["distinct_id"] != consent.InstallID {
		t.Fatalf("batch = %v, install id %q", rec.batches[0], consent.InstallID)
	}
}

// AC:buffer-flushed-or-dropped: a buffering process keeps events while
// not_asked, sends them after Turn on, and sends nothing after No thanks.
func TestBufferFlushedAfterTurnOn(t *testing.T) {
	rec, endpoint := newRecorder(t)
	r, dirs := newTestRecorder(t, endpoint, true, nil)
	r.Channel = telemetry.ChannelTUI
	r.Record(telemetry.NewOnboardingStarted(), telemetry.NewOptionSelected("demo"), telemetry.NewDemoInstalled(true, false))
	r.Flush(context.Background())
	if rec.count() != 0 || r.Pending() != 3 {
		t.Fatalf("before consent: %d batches, %d pending", rec.count(), r.Pending())
	}
	decide(t, dirs, telemetry.StateEnabled)
	r.Consented() // this session's Turn on
	r.Record(telemetry.NewConsentEnabled())
	r.Flush(context.Background())
	want := []string{"onboarding_started", "onboarding_option_selected", "demo_installed", "telemetry_consent_changed"}
	if got := rec.events(); len(got) != len(want) || got[0] != want[0] || got[3] != want[3] {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestBufferDroppedAfterNoThanks(t *testing.T) {
	rec, endpoint := newRecorder(t)
	r, dirs := newTestRecorder(t, endpoint, true, nil)
	for range telemetry.MaxBuffered + 20 {
		r.Record(telemetry.NewOnboardingStarted())
	}
	if r.Pending() != telemetry.MaxBuffered {
		t.Fatalf("buffered %d, cap %d", r.Pending(), telemetry.MaxBuffered)
	}
	decide(t, dirs, telemetry.StateDisabled) // No thanks
	r.Record(telemetry.NewDemoInstalled(true, false))
	r.Flush(context.Background())
	r.Discard()
	if rec.count() != 0 || r.Pending() != 0 {
		t.Fatalf("after No thanks: %d batches, %d pending", rec.count(), r.Pending())
	}
	entries, _ := os.ReadDir(dirs.Home)
	for _, entry := range entries {
		if entry.Name() != "config.yaml" {
			t.Errorf("unexpected file %s", entry.Name())
		}
	}
	consent, _ := telemetry.LoadConsent(dirs.Home)
	if consent.InstallID != "" {
		t.Fatalf("install id kept after disable: %+v", consent)
	}
}

// AC:client-env-wins: the sending process's DO_NOT_TRACK stops an enabled
// home from sending, and is named as the reason.
func TestClientDoNotTrackWins(t *testing.T) {
	for _, vars := range []map[string]string{{"DO_NOT_TRACK": "1"}, {"DO_NOT_TRACK": "yes"}, {"OVDB_TELEMETRY": "0"}, {"CI": "true"}} {
		rec, endpoint := newRecorder(t)
		r, dirs := newTestRecorder(t, endpoint, false, vars)
		decide(t, dirs, telemetry.StateEnabled)
		r.Record(telemetry.NewDemoInstalled(true, false))
		r.Flush(context.Background())
		d := r.Decide()
		if rec.count() != 0 || d.Sending || d.Reason == "" || telemetry.ReasonText(d.Reason) == "" {
			t.Fatalf("%v: sent %d, decision %+v", vars, rec.count(), d)
		}
	}
	if got := telemetry.ForcedOff(env(map[string]string{"DO_NOT_TRACK": "1"})); got != "DO_NOT_TRACK" {
		t.Fatalf("reason = %q", got)
	}
	for _, off := range []string{"0", "false", "FALSE", ""} {
		if got := telemetry.ForcedOff(env(map[string]string{"DO_NOT_TRACK": off, "CI": "0", "OVDB_TELEMETRY": "1"})); got != "" {
			t.Errorf("DO_NOT_TRACK=%q forced %q", off, got)
		}
	}
}

// AC:unavailable-build: enabled without a key sends nothing and says so.
func TestUnavailableBuildSendsNothing(t *testing.T) {
	rec, endpoint := newRecorder(t)
	r, dirs := newTestRecorder(t, endpoint, true, nil)
	r.Key = ""
	decide(t, dirs, telemetry.StateEnabled)
	r.Record(telemetry.NewConsentEnabled())
	r.Flush(context.Background())
	d := r.Decide()
	if telemetry.Available() {
		t.Skip("test binary built with a key")
	}
	document := telemetry.NewDocument(d, telemetry.Available())
	if rec.count() != 0 || d.State != telemetry.StateEnabled || d.Reason != telemetry.ReasonUnavailable || document.Telemetry.ReasonText == "" {
		t.Fatalf("sent %d, decision %+v, document %+v", rec.count(), d, document.Telemetry)
	}
}

// AC:events-delivered-within-bound, second half: an endpoint that never
// answers adds at most the 2 s bound.
func TestFlushBoundedByTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); server.Close() })
	r, dirs := newTestRecorder(t, server.URL, false, nil)
	decide(t, dirs, telemetry.StateEnabled)
	r.Record(telemetry.NewDatabaseCreated("ingitdb", true, time.Second))
	start := time.Now()
	r.Flush(context.Background())
	if elapsed := time.Since(start); elapsed > telemetry.Timeout+500*time.Millisecond {
		t.Fatalf("flush took %s", elapsed)
	}
}

func TestChannelDetection(t *testing.T) {
	none := func() []string { return nil }
	if got := telemetry.DetectChannel(env(nil), func() []string { return []string{"PATH=/bin", "HOME=/home/ann"} }); got != telemetry.ChannelCLI {
		t.Errorf("plain shell = %s", got)
	}
	if got := telemetry.DetectChannel(env(map[string]string{"CLAUDECODE": "1"}), none); got != telemetry.ChannelAgent {
		t.Errorf("CLAUDECODE = %s", got)
	}
	if got := telemetry.DetectChannel(env(nil), func() []string { return []string{"CODEX_SANDBOX=seatbelt"} }); got != telemetry.ChannelAgent {
		t.Errorf("CODEX_* = %s", got)
	}
}

func TestApplyTelemetryChange(t *testing.T) {
	dirs := paths.Dirs{Home: t.TempDir()}
	if _, _, err := setup.ApplyTelemetryChange(dirs, telemetry.Change{State: telemetry.StateEnabled}, telemetry.ChannelAgent, time.Now()); envelope.As(err) == nil || envelope.As(err).Code != envelope.ConfirmationRequired {
		t.Fatalf("enable without confirmation: %v", err)
	}
	if _, _, err := setup.ApplyTelemetryChange(dirs, telemetry.Change{State: "maybe"}, telemetry.ChannelCLI, time.Now()); envelope.As(err) == nil || envelope.As(err).Code != envelope.InvalidArgument {
		t.Fatalf("bad state: %v", err)
	}
	if consent, _ := telemetry.LoadConsent(dirs.Home); consent.EffectiveState() != telemetry.StateNotAsked {
		t.Fatalf("state changed by refused requests: %+v", consent)
	}
	// server.port survives, and telemetry survives a config change.
	if _, err := setup.ApplyConfigChange(dirs, setup.ConfigChange{Key: setup.KeyServerPort, Value: "7000"}, false); err != nil {
		t.Fatal(err)
	}
	enabled, changed, err := setup.ApplyTelemetryChange(dirs, telemetry.Change{State: telemetry.StateEnabled, ConfirmedByUser: true}, telemetry.ChannelWeb, time.Now())
	if err != nil || !changed || !telemetry.ValidInstallID(enabled.InstallID) || enabled.Channel != "web" || enabled.DecidedAt == "" {
		t.Fatalf("enable = %+v %v %v", enabled, changed, err)
	}
	again, changed, _ := setup.ApplyTelemetryChange(dirs, telemetry.Change{State: telemetry.StateEnabled, ConfirmedByUser: true}, telemetry.ChannelCLI, time.Now())
	if changed || again.InstallID != enabled.InstallID {
		t.Fatalf("second enable changed the id: %+v", again)
	}
	if _, err := setup.ApplyConfigChange(dirs, setup.ConfigChange{Key: setup.KeyServerPort, Value: "7001"}, false); err != nil {
		t.Fatal(err)
	}
	config, _ := setup.LoadConfig(dirs.Home)
	if config.Server.Port != 7001 || config.Telemetry.InstallID != enabled.InstallID {
		t.Fatalf("config = %+v", config)
	}
	disabled, changed, _ := setup.ApplyTelemetryChange(dirs, telemetry.Change{State: telemetry.StateDisabled}, telemetry.ChannelTUI, time.Now())
	if !changed || disabled.InstallID != "" || disabled.State != telemetry.StateDisabled {
		t.Fatalf("disable = %+v", disabled)
	}
	data, _ := os.ReadFile(filepath.Join(dirs.Home, "config.yaml"))
	if string(data) == "" || containsID(string(data), enabled.InstallID) {
		t.Fatalf("config.yaml after disable:\n%s", data)
	}
}

func containsID(s, id string) bool { return id != "" && strings.Contains(s, id) }

// Review F1: events buffered while not_asked are never sent because another
// process (an agent relaying consent) turned telemetry on; only this
// session's own Turn on sends them.
func TestBufferNotSentWhenAnotherProcessEnables(t *testing.T) {
	rec, endpoint := newRecorder(t)
	r, dirs := newTestRecorder(t, endpoint, true, nil)
	r.Channel = telemetry.ChannelTUI
	r.Record(telemetry.NewOnboardingStarted(), telemetry.NewDemoInstalled(true, false))
	decide(t, dirs, telemetry.StateEnabled) // out of band
	r.Flush(context.Background())
	r.Record(telemetry.NewDatabaseCreated("sqlite", true, time.Millisecond))
	r.Exit(context.Background())
	if got := rec.events(); len(got) != 1 || got[0] != "database_created" {
		t.Fatalf("events = %v, want only the post-consent database_created", got)
	}
}

// Review F2: the request identifies nothing about the machine beyond what
// HTTP needs, and every event asks PostHog to record a placeholder instead
// of the connection's IP address.
func TestRequestCarriesNoIdentifyingHeaders(t *testing.T) {
	var headers http.Header
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(server.Close)
	r, dirs := newTestRecorder(t, server.URL, false, nil)
	decide(t, dirs, telemetry.StateEnabled)
	r.Record(telemetry.NewOnboardingStarted())
	r.Flush(context.Background())
	allowed := map[string]bool{"Content-Type": true, "Content-Length": true, "User-Agent": true, "Accept-Encoding": true}
	for name := range headers {
		if !allowed[name] {
			t.Errorf("header %s: %v", name, headers[name])
		}
	}
	host, _ := os.Hostname()
	if ua := headers.Get("User-Agent"); ua != telemetry.UserAgent || (host != "" && strings.Contains(ua, host)) {
		t.Errorf("User-Agent = %q", ua)
	}
	var batch struct {
		Batch []struct {
			Properties map[string]any `json:"properties"`
		} `json:"batch"`
	}
	if err := json.Unmarshal(body, &batch); err != nil || len(batch.Batch) != 1 || batch.Batch[0].Properties["$ip"] != telemetry.IPPlaceholder {
		t.Fatalf("body = %s", body)
	}
}

// Review F2: the copy says what reaches PostHog, not "anonymous".
func TestCopyStatesTheNetworkAddress(t *testing.T) {
	all := strings.Join(append(telemetry.Collected(), telemetry.NeverCollected()...), "\n")
	if !strings.Contains(all, "IP address") {
		t.Errorf("collected lists do not mention the IP address:\n%s", all)
	}
	// Review L6: with $ip set, PostHog stores the placeholder, not the
	// connection address (posthog.com tutorials/web-redact-properties).
	if !strings.Contains(all, "PostHog stores "+telemetry.IPPlaceholder+" in its place") || strings.Contains(all, "only if the project") {
		t.Errorf("IP copy overstates or understates what is stored:\n%s", all)
	}
	for _, line := range telemetry.NeverCollected() {
		if strings.Contains(line, "addresses") && !strings.Contains(line, "database") {
			t.Errorf("never-collected line claims addresses in general: %q", line)
		}
	}
	document := telemetry.NewDocument(telemetry.Decision{State: telemetry.StateNotAsked}, true)
	if data, _ := json.Marshal(document); strings.Contains(strings.ToLower(string(data)), "anonymous") {
		t.Errorf("document says anonymous: %s", data)
	}
}
