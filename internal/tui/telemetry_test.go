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
	if !strings.Contains(view, promptText) || !strings.Contains(view, "t turn on · n no thanks · w what's collected? · Esc decide later") {
		t.Fatalf("demo Result lacks the prompt:\n%s", view)
	}
	m = send(t, m, key("w"))
	if view := flat(m.View().Content); !strings.Contains(view, "What's collected") || !strings.Contains(view, "Never collected") {
		t.Errorf("What's collected? shows nothing:\n%s", view)
	}
	m = send(t, m, key("esc")) // back from the lists
	m = send(t, m, key("esc")) // decide later
	if view := flat(m.View().Content); m.screen != ScreenResult || strings.Contains(view, "Help improve") || !strings.Contains(view, "decide about usage statistics any time in Settings") {
		t.Fatalf("decide later (%s):\n%s", m.screen, view)
	}
	m = send(t, m, key("esc"))
	if m.screen != ScreenHome {
		t.Fatalf("esc went to %s", m.screen)
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
	if view := flat(m.View().Content); !strings.Contains(view, "Usage statistics: Off (you haven't decided yet)") || !strings.Contains(view, "u usage statistics") {
		t.Errorf("settings:\n%s", view)
	}
	// Review L4: its own view shows everything the CLI and web show, and the
	// footer offers only what applies, within 80×24.
	m = send(t, m, key("u"))
	fits := func(name string, wants, unwanted []string, footer string) {
		t.Helper()
		content := stripANSI(m.View().Content)
		lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
		if len(lines) > 24 {
			t.Errorf("%s: %d lines:\n%s", name, len(lines), content)
		}
		view := flat(content)
		for _, want := range wants {
			if !strings.Contains(view, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, content)
			}
		}
		for _, bad := range unwanted {
			if strings.Contains(flat(lines[len(lines)-1]), bad) {
				t.Errorf("%s footer offers %q: %s", name, bad, lines[len(lines)-1])
			}
		}
		if !strings.Contains(flat(lines[len(lines)-1]), footer) {
			t.Errorf("%s footer = %q, want %q", name, lines[len(lines)-1], footer)
		}
	}
	lists := []string{"Status: Off (you haven't decided yet)", "unavailable in this build", "PostHog (EU)", "What's collected", "Never collected", "Anything you type", "Change it any time"}
	fits("not asked", lists, nil, "t turn on · x keep off · Esc back")
	m = send(t, m, key("t"))
	status := m.local.TelemetryStatus().Telemetry
	if status.State != telemetry.StateEnabled || status.Channel != "tui" {
		t.Fatalf("turn on: %+v", status)
	}
	fits("on", []string{"Status: On"}, []string{"t turn on"}, "x turn off · Esc back")
	m = send(t, m, key("x"))
	status = m.local.TelemetryStatus().Telemetry
	if status.State != telemetry.StateDisabled || status.HasInstallID {
		t.Fatalf("turn off: %+v", status)
	}
	fits("off", []string{"Status: Off"}, []string{"x "}, "t turn on · Esc back")
	m = send(t, m, key("esc"))
	if m.screen != ScreenSettings || m.usage.settingsOpen {
		t.Errorf("esc: screen %s open %v", m.screen, m.usage.settingsOpen)
	}
	if got := rec.received(); len(got) != 0 {
		t.Errorf("key-less build sent %v", got)
	}
}

// Review M3: at 80×24 the prompt's choices and the key footer stay on
// screen, with What's collected? open too.
func TestUsagePromptFitsAt80x24(t *testing.T) {
	m, _ := withTelemetry(t, realModel(t, freePort(t)), "phc_test")
	m = installDemo(t, m)
	check := func(name string, wants ...string) {
		t.Helper()
		content := stripANSI(m.View().Content)
		lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
		if len(lines) > 24 {
			t.Errorf("%s: %d lines at 80x24:\n%s", name, len(lines), content)
		}
		noWiderThan(t, content, 80)
		last := lines[len(lines)-1]
		if !strings.Contains(last, "t turn on · n no thanks") {
			t.Errorf("%s: footer %q lacks the choices", name, last)
		}
		for _, want := range wants {
			if !strings.Contains(flat(content), want) {
				t.Errorf("%s lacks %q:\n%s", name, want, content)
			}
		}
	}
	check("prompt", "Help improve OpenVaultDB?", "The TODO demo is ready")
	m = send(t, m, key("w"))
	check("details", "What's collected", "Never collected", "Anything you type")
	m = send(t, m, key("w"))
	check("back", "Help improve OpenVaultDB?")
	// Enter does not silently dismiss it (review L5); Esc decides later.
	m = send(t, m, key("enter"))
	if m.screen != ScreenResult || !m.usage.prompt {
		t.Fatalf("enter: screen %s prompt %v", m.screen, m.usage.prompt)
	}
	m = send(t, m, key("esc"))
	if m.screen != ScreenResult || m.usage.prompt {
		t.Fatalf("esc: screen %s prompt %v", m.screen, m.usage.prompt)
	}
}

func TestUsagePromptOnCreateResultFitsAt80x24(t *testing.T) {
	m, _ := withTelemetry(t, realModel(t, freePort(t)), "phc_test")
	m = openCreate(t, m)
	m = send(t, m, key("enter"))
	m = typeText(t, m, "notes")
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	for _, step := range []string{"", "w"} {
		if step != "" {
			m = send(t, m, key(step))
		}
		content := stripANSI(m.View().Content)
		lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
		if len(lines) > 24 || !strings.Contains(lines[len(lines)-1], "t turn on · n no thanks") {
			t.Errorf("create result %q: %d lines:\n%s", step, len(lines), content)
		}
	}
}

// Review L1: Done on a successful Result records onboarding_completed with
// its step, and the Databases option keeps its name.
func TestUsageCompletedOnDoneAndDatabasesOption(t *testing.T) {
	m, rec := withTelemetry(t, realModel(t, freePort(t)), "phc_test")
	m = installDemo(t, m)
	m = send(t, m, key("t"))
	m = send(t, m, key("enter")) // Done
	if got := rec.received(); got[len(got)-1] != "onboarding_completed" {
		t.Fatalf("events = %v", got)
	}
	props := telemetry.NewOptionSelected("databases").Properties(telemetry.Meta{})
	if props["option"] != "databases" {
		t.Errorf("databases option = %v", props["option"])
	}
}
