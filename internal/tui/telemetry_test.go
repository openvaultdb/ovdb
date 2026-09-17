package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/openvaultdb/ovdb/internal/telemetry"
)

type posthogRecorder struct {
	mu     sync.Mutex
	events []string
}

func (p *posthogRecorder) received() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

// withTelemetry gives m the TUI's buffering recorder, with a key, a
// recording endpoint and an environment that forces nothing off.
func withTelemetry(t *testing.T, m Model, key string) (Model, *posthogRecorder) {
	t.Helper()
	rec := &posthogRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body struct {
			Batch []struct {
				Event      string         `json:"event"`
				Properties map[string]any `json:"properties"`
			} `json:"batch"`
		}
		_ = json.Unmarshal(data, &body)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, e := range body.Batch {
			if e.Properties["channel"] != "tui" {
				t.Errorf("event %s channel %v", e.Event, e.Properties["channel"])
			}
			rec.events = append(rec.events, e.Event)
		}
	}))
	t.Cleanup(server.Close)
	m.local.Telemetry = &telemetry.Recorder{Channel: telemetry.ChannelTUI, Home: m.local.Dirs.Home, Version: "1.0.0",
		Getenv: m.local.Getenv, Buffer: true, Key: key, Endpoint: server.URL}
	// A real session records onboarding_started at start.
	return drain(t, m, m.Init()), rec
}

func installDemo(t *testing.T, m Model) Model {
	t.Helper()
	m = send(t, m, key("enter")) // Try a demo
	m = send(t, m, key("enter")) // install
	if m.screen != ScreenResult {
		t.Fatalf("screen %s, problem %+v", m.screen, m.problem.err)
	}
	return m
}

const promptText = "Help improve OpenVaultDB? Help improve OpenVaultDB by sending usage statistics"

// AC:telemetry-question-is-late-and-once and AC:prompt-once-equal-choices:
// the prompt is on the demo's Result only, with Turn on and No thanks in one
// line and a What's collected? key; after dismissal it is not asked again.
func TestUsagePromptLateAndOnce(t *testing.T) {
	m, rec := withTelemetry(t, realModel(t, freePort(t)), "phc_test")
	if view := flat(m.View().Content); strings.Contains(view, "Help improve") {
		t.Fatalf("prompt before any action:\n%s", view)
	}
	m = installDemo(t, m)
	view := flat(m.View().Content)
	if !strings.Contains(view, promptText) || !strings.Contains(view, "t Turn on · n No thanks · w What's collected?") {
		t.Fatalf("demo Result lacks the prompt:\n%s", view)
	}
	m = send(t, m, key("w"))
	if view := flat(m.View().Content); !strings.Contains(view, "What's collected") || !strings.Contains(view, "Never collected") {
		t.Errorf("What's collected? shows nothing:\n%s", view)
	}
	m = send(t, m, key("esc")) // dismiss
	if m.screen != ScreenHome {
		t.Fatalf("dismiss went to %s", m.screen)
	}
	m = openCreate(t, m)
	m = send(t, m, key("enter"))
	m = typeText(t, m, "notes")
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	if view := flat(m.View().Content); m.screen != ScreenResult || strings.Contains(view, "Help improve") {
		t.Fatalf("second Result (%s) shows the prompt:\n%s", m.screen, view)
	}
	if got := rec.received(); len(got) != 0 {
		t.Fatalf("sent while not_asked: %v", got)
	}
	if status := m.local.TelemetryStatus().Telemetry; status.State != telemetry.StateNotAsked {
		t.Errorf("dismissal decided: %+v", status)
	}
}

// AC:buffer-flushed-or-dropped: one session turns it on and sends its
// buffer; another says No thanks and sends nothing and writes no telemetry
// file.
func TestUsagePromptTurnOnSendsBufferNoThanksDropsIt(t *testing.T) {
	m, rec := withTelemetry(t, realModel(t, freePort(t)), "phc_test")
	m = installDemo(t, m)
	m = send(t, m, key("t"))
	want := "onboarding_started,onboarding_option_selected,demo_installed,telemetry_consent_changed"
	if got := strings.Join(rec.received(), ","); got != want {
		t.Fatalf("Turn on sent %s, want %s", got, want)
	}
	if view := flat(m.View().Content); !strings.Contains(view, "Usage statistics are on. Thank you.") || strings.Contains(view, "Help improve") {
		t.Errorf("after Turn on:\n%s", view)
	}

	other, rec2 := withTelemetry(t, realModel(t, freePort(t)), "phc_test")
	other = installDemo(t, other)
	other = send(t, other, key("n"))
	if got := rec2.received(); len(got) != 0 {
		t.Fatalf("No thanks sent %v", got)
	}
	status := other.local.TelemetryStatus().Telemetry
	if status.State != telemetry.StateDisabled || status.HasInstallID {
		t.Errorf("No thanks: %+v", status)
	}
	entries, _ := os.ReadDir(other.local.Dirs.Home)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "telemetry") {
			t.Errorf("telemetry file %s", entry.Name())
		}
	}
}

func openSettings(t *testing.T, m Model) Model {
	t.Helper()
	for m.home.document.Options[m.home.cursor].ID != "settings" {
		m = send(t, m, key("down"))
	}
	m = send(t, m, key("enter"))
	if m.screen != ScreenSettings || !m.settings.loaded {
		t.Fatalf("screen %s", m.screen)
	}
	return m
}

// Settings → Usage statistics (rows 23 and 24): the same state and
// explanation as the CLI, turned on and off here; a build without a key says
// it is unavailable (AC:unavailable-build, TUI half).
func TestSettingsUsageStatistics(t *testing.T) {
	t.Parallel()
	m, rec := withTelemetry(t, testModel(t, 80, 24), "")
	m = openSettings(t, m)
	view := flat(m.View().Content)
	for _, want := range []string{"Usage statistics", "Status: Off (you haven't decided yet)", "unavailable in this build", "PostHog (EU)", "t turn on usage statistics"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings lacks %q:\n%s", want, view)
		}
	}
	m = send(t, m, key("t"))
	status := m.local.TelemetryStatus().Telemetry
	if view := flat(m.View().Content); status.State != telemetry.StateEnabled || status.Channel != "tui" || !strings.Contains(view, "Status: On") {
		t.Fatalf("turn on: %+v\n%s", status, view)
	}
	m = send(t, m, key("x"))
	status = m.local.TelemetryStatus().Telemetry
	if view := flat(m.View().Content); status.State != telemetry.StateDisabled || status.HasInstallID || !strings.Contains(view, "Status: Off") {
		t.Fatalf("turn off: %+v\n%s", status, view)
	}
	if got := rec.received(); len(got) != 0 {
		t.Errorf("key-less build sent %v", got)
	}
}
