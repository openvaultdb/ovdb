package tui

import (
	"strings"
	"testing"
)

// Review F10: Settings says the port the server uses now — OVDB_PORT (the
// client's resolved port) or the running server's — not the default.
func TestSettingsShowsTheResolvedPort(t *testing.T) {
	t.Parallel()
	m := testModel(t, 80, 24)
	m.local.Port, m.local.ExplicitPort = 18933, true
	m.screen = ScreenSettings
	m = drain(t, m, m.loadConfigCmd())
	if view := flat(m.View().Content); !strings.Contains(view, "The OVDB server is using port 18933 now.") {
		t.Errorf("settings:\n%s", view)
	}
}

func TestSettingsShowsTheRunningServersPort(t *testing.T) {
	port := freePort(t)
	m := realModel(t, port)
	m = drain(t, m, m.startCmd())
	m.local.Port, m.local.ExplicitPort = port+1, false
	m.screen = ScreenSettings
	m = drain(t, m, m.loadConfigCmd())
	if view := flat(m.View().Content); !strings.Contains(view, "using port "+itoa(port)+" now") {
		t.Errorf("settings:\n%s", view)
	}
}
