package tui

import (
	"strconv"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// homeItem is one entry of the Home menu, in the founder's order
// (first-run-onboarding#REQ:home-menu-options). Increment 1c implements
// only "Start the OVDB server" (row 3) and "Settings" (row 7); the rest of
// the founder's order arrives with the capabilities that add them — "Interim
// Home screens are therefore subsets, by design" (plan.md Approach).
type homeItem struct {
	label  string
	help   string
	target string
}

// homeScreen is capability rows 1 and 2 (Guided first run, Whole-setup
// status): a pure read of the local server's status, never a start.
type homeScreen struct {
	loaded bool
	status setup.Status
	err    error
	cursor int
}

// menu returns the two implemented Home options with the running or
// would-start port substituted into the first one's help line.
func (s homeScreen) menu() []homeItem {
	port := strconv.Itoa(s.status.Server.Port)
	return []homeItem{
		{
			label:  uicopy.T("home.menu.start_server", nil),
			help:   uicopy.T("home.menu.start_server.help", map[string]string{"port": port}),
			target: ScreenServer,
		},
		{
			label:  uicopy.T("home.menu.settings", nil),
			help:   uicopy.T("home.menu.settings.help", nil),
			target: ScreenSettings,
		},
	}
}

// statusLine is REQ:home-status-line's subset implemented so far: server
// state and address. Databases and current-database join it once increment
// 2 implements them.
func (s homeScreen) statusLine() string {
	switch {
	case !s.loaded:
		return uicopy.T("home.loading", nil)
	case s.err != nil:
		return uicopy.T("home.status_unavailable", nil)
	case s.status.Server.State == setup.StateRunning:
		return uicopy.T("server.status.running", nil) + " — " +
			uicopy.T("server.status.address", map[string]string{
				"address": s.status.Server.Address, "fallback": s.status.Server.FallbackAddress,
			})
	default:
		return uicopy.T("server.status.not_running", nil)
	}
}

func (m Model) viewHome() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("home.title", nil)))
	b.WriteString("\n")
	b.WriteString(wordWrap(uicopy.T("home.tagline", nil), width))
	b.WriteString("\n\n")
	b.WriteString(m.home.statusLine())
	b.WriteString("\n\n")
	b.WriteString(uicopy.T("home.question", nil))
	b.WriteString("\n\n")
	for i, item := range m.home.menu() {
		cursor := "  "
		style := itemStyle
		if i == m.home.cursor {
			cursor = "> "
			style = selectedItemStyle
		}
		b.WriteString(style.Render(cursor + item.label))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(indentWrap("    ", item.help, width)))
		b.WriteString("\n")
	}
	return b.String()
}
