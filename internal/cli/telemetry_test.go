package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/preview"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// posthog is a recording PostHog stand-in.
type posthog struct {
	mu     sync.Mutex
	events []map[string]any // properties plus "event"
}

func newPosthog(t *testing.T) (*posthog, string) {
	p := &posthog{}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body struct {
			Batch []struct {
				Event      string         `json:"event"`
				Properties map[string]any `json:"properties"`
			} `json:"batch"`
		}
		_ = json.Unmarshal(data, &body)
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, e := range body.Batch {
			e.Properties["event"] = e.Event
			p.events = append(p.events, e.Properties)
		}
	}))
	t.Cleanup(server.Close)
	return p, server.URL
}

func (p *posthog) received() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.events...)
}

// telemetryEnv is newEnv with a build key and a recording endpoint.
func telemetryEnv(t *testing.T, endpoint string) *env {
	e := previewEnv(t)
	e.app.TelemetryKey, e.app.TelemetryEndpoint = "phc_test", endpoint
	e.app.Environ = func() []string { return nil }
	return e
}

// runCommand is one ovdb process: the command, then main's flush.
func (e *env) runCommand(args ...string) result {
	e.t.Helper()
	r := e.run(args...)
	e.app.FlushTelemetry(context.Background())
	return r
}

func (e *env) telemetryStatus() telemetry.Document {
	e.t.Helper()
	r := e.run("telemetry", "status", "--json")
	var document telemetry.Document
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &document) != nil {
		e.t.Fatalf("telemetry status: %+v", r)
	}
	return document
}

// AC:nothing-sent-by-default.
func TestTelemetryNothingSentByDefault(t *testing.T) {
	recorder, endpoint := newPosthog(t)
	e := telemetryEnv(t, endpoint)
	for _, args := range [][]string{{"demo", "install", "--yes"}, {"databases", "create", "notes"}, {"server", "start"}} {
		if r := e.runCommand(args...); r.code != 0 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	if got := recorder.received(); len(got) != 0 {
		t.Fatalf("sent before consent: %v", got)
	}
	status := e.telemetryStatus().Telemetry
	if status.State != telemetry.StateNotAsked || status.HasInstallID || status.Sending || len(status.Collected) != 4 {
		t.Fatalf("status = %+v", status)
	}
}

// AC:non-tty-enable-needs-confirmation, AC:channel-derived (CLI half),
// AC:events-delivered-within-bound (first half) and disable removing the
// install id.
func TestTelemetryEnableNeedsAPersonThenDelivers(t *testing.T) {
	recorder, endpoint := newPosthog(t)
	e := telemetryEnv(t, endpoint)
	refused := e.runCommand("telemetry", "enable")
	if refused.code != 1 || !strings.Contains(refused.stdout, "What's collected") || !strings.Contains(refused.stderr, "ovdb telemetry enable") {
		t.Fatalf("enable without a terminal: %+v", refused)
	}
	_ = decodeError(t, e.runCommand("telemetry", "enable", "--json"), envelope.ConfirmationRequired)
	if state := e.telemetryStatus().Telemetry.State; state != telemetry.StateNotAsked {
		t.Fatalf("state after refusal = %s", state)
	}
	e.vars["CLAUDECODE"] = "1"
	enabled := e.runCommand("telemetry", "enable", "--confirmed-by-user", "--json")
	var document telemetry.Document
	if enabled.code != 0 || json.Unmarshal([]byte(enabled.stdout), &document) != nil ||
		document.Telemetry.State != telemetry.StateEnabled || document.Telemetry.Channel != "agent" || !document.Telemetry.HasInstallID {
		t.Fatalf("enable --confirmed-by-user: %+v", enabled)
	}
	if r := e.runCommand("databases", "create", "notes", "--json"); r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	got := recorder.received()
	names := []string{}
	for _, event := range got {
		names = append(names, event["event"].(string))
		if event["channel"] != "agent" || !telemetry.ValidInstallID(event["install_id"].(string)) {
			t.Errorf("event %v", event)
		}
		for _, secret := range []string{"notes", e.dirs.Home, e.dirs.Data} {
			if data, _ := json.Marshal(event); strings.Contains(string(data), secret) {
				t.Errorf("event carries %q: %s", secret, data)
			}
		}
	}
	// server_started is the auto-start's: the process started the server.
	if strings.Join(names, ",") != "telemetry_consent_changed,database_created" {
		t.Fatalf("events = %v", names)
	}
	if got[1]["engine"] != "ingitdb" || got[1]["success"] != true {
		t.Fatalf("database_created = %v", got[1])
	}
	disabled := e.runCommand("telemetry", "disable")
	if disabled.code != 0 {
		t.Fatalf("disable: %+v", disabled)
	}
	status := e.telemetryStatus().Telemetry
	config, _ := os.ReadFile(filepath.Join(e.dirs.Home, "config.yaml"))
	if status.State != telemetry.StateDisabled || status.HasInstallID || strings.Contains(string(config), "install_id") {
		t.Fatalf("after disable: %+v\n%s", status, config)
	}
}

// AC:client-env-wins: an enabled home and a server started without
// DO_NOT_TRACK; the CLI's own DO_NOT_TRACK stops its events and is named.
func TestTelemetryClientDoNotTrackWins(t *testing.T) {
	recorder, endpoint := newPosthog(t)
	e := telemetryEnv(t, endpoint)
	if r := e.runCommand("server", "start"); r.code != 0 {
		t.Fatalf("start: %+v", r)
	}
	if r := e.runCommand("telemetry", "enable", "--confirmed-by-user"); r.code != 0 {
		t.Fatalf("enable: %+v", r)
	}
	before := len(recorder.received())
	e.vars["DO_NOT_TRACK"] = "1"
	if r := e.runCommand("demo", "install", "--yes"); r.code != 0 {
		t.Fatalf("demo install: %+v", r)
	}
	if got := recorder.received(); len(got) != before {
		t.Fatalf("sent with DO_NOT_TRACK: %v", got[before:])
	}
	status := e.telemetryStatus().Telemetry
	if status.State != telemetry.StateEnabled || status.Reason != "DO_NOT_TRACK" || status.Sending {
		t.Fatalf("status = %+v", status)
	}
	human := e.run("telemetry", "status")
	if !strings.Contains(human.stdout, "DO_NOT_TRACK") {
		t.Fatalf("human status does not name DO_NOT_TRACK:\n%s", human.stdout)
	}
}

// AC:events-delivered-within-bound, second half: an endpoint that never
// answers adds at most 2 s and changes neither output nor exit code.
func TestTelemetryUnresponsiveEndpointIsBounded(t *testing.T) {
	release := make(chan struct{})
	silent := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); silent.Close() })
	e := telemetryEnv(t, silent.URL)
	if r := e.run("telemetry", "enable", "--confirmed-by-user"); r.code != 0 {
		t.Fatalf("enable: %+v", r)
	}
	e.app.FlushTelemetry(context.Background())
	r := e.run("databases", "create", "notes", "--no-start", "--json")
	start := time.Now()
	e.app.FlushTelemetry(context.Background())
	if elapsed := time.Since(start); elapsed > telemetry.Timeout+500*time.Millisecond {
		t.Fatalf("flush took %s", elapsed)
	}
	_ = decodeError(t, r, envelope.ServerNotRunning)
}

// Review F1, the reviewer's repro across processes: a TUI buffers
// demo_installed while not_asked, an agent in another process enables
// telemetry with --confirmed-by-user, and the TUI exits without its own Turn
// on: nothing the TUI buffered is sent.
func TestTUIBufferDroppedWhenAnotherProcessEnables(t *testing.T) {
	recorder, endpoint := newPosthog(t)
	e := telemetryEnv(t, endpoint)
	tui := e.app.TUIRecorder(e.dirs.Home)
	tui.Record(telemetry.NewOnboardingStarted(), telemetry.NewOptionSelected("demo"), telemetry.NewDemoInstalled(true, false))

	agent := exec.Command(os.Args[0], "telemetry", "enable", "--confirmed-by-user")
	agent.Env = append(os.Environ(), childEnv+"=1", "CLAUDECODE=1", preview.EnvVar+"=1")
	for key, value := range e.vars {
		agent.Env = append(agent.Env, key+"="+value)
	}
	if out, err := agent.CombinedOutput(); err != nil {
		t.Fatalf("agent enable: %v\n%s", err, out)
	}
	if state := e.telemetryStatus().Telemetry; state.State != telemetry.StateEnabled || state.Channel != "agent" {
		t.Fatalf("after agent enable: %+v", state)
	}
	tui.Exit(context.Background())
	if got := recorder.received(); len(got) != 0 {
		t.Fatalf("TUI buffer sent after another process enabled: %v", got)
	}
}

// Review F6: human output points people at the prompting command; the
// relay flag appears only in agent-directed JSON, stating it may be passed
// only after the person said yes, and never as a runnable next command.
func TestTelemetryRelayFlagOnlyInAgentGuidance(t *testing.T) {
	e := telemetryEnv(t, "http://127.0.0.1:9")
	const flag = "--confirmed-by-user"
	human := e.run("telemetry", "status")
	refused := e.run("telemetry", "enable")
	for _, r := range []result{human, refused} {
		if strings.Contains(r.stdout+r.stderr, flag) || !strings.Contains(r.stdout+r.stderr, "ovdb telemetry enable") {
			t.Errorf("human output advertises the flag or lacks the command:\n%s\n%s", r.stdout, r.stderr)
		}
	}
	document := e.telemetryStatus()
	if g := document.Telemetry.AgentGuidance; !strings.Contains(g, flag) || !strings.Contains(g, "only after") {
		t.Errorf("status --json agent guidance = %q", g)
	}
	failure := decodeError(t, e.run("telemetry", "enable", "--json"), envelope.ConfirmationRequired)
	if !strings.Contains(failure.Reason, flag) || !strings.Contains(failure.Reason, "only after") {
		t.Errorf("--json refusal reason = %q", failure.Reason)
	}
	for _, next := range append(document.Next, failure.Next...) {
		if strings.Contains(next.Command, flag) {
			t.Errorf("next offers %q", next.Command)
		}
	}
}
