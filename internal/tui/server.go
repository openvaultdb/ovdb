package tui

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// serverAction names what Enter does for one serverScreen menu item.
type serverAction int

const (
	actionStart serverAction = iota
	actionOpenBrowser
	actionRestart
	actionStop
)

type serverMenuItem struct {
	label  string
	action serverAction
}

// serverScreen is the "OVDB server" screen: capability rows 4 (server
// status), 5 (stop/restart) and 6 (open in browser, via matrix TUI cell
// "Open in browser"). Row 3 (start server)'s TUI cell is the same "Start the
// OVDB server" action, offered here whenever the server is not running.
type serverScreen struct {
	loaded bool
	server setup.Server
	cursor int

	linkShown     bool
	link          localserver.LoginLink
	browserFailed bool
}

func (s serverScreen) menu() []serverMenuItem {
	if s.loaded && s.server.State == setup.StateRunning {
		return []serverMenuItem{
			{label: uicopy.T("server.menu.open_browser", nil), action: actionOpenBrowser},
			{label: uicopy.T("server.menu.restart", nil), action: actionRestart},
			{label: uicopy.T("server.menu.stop", nil), action: actionStop},
		}
	}
	return []serverMenuItem{{label: uicopy.T("home.menu.start_server", nil), action: actionStart}}
}

func (m Model) viewServer() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("server.screen.title", nil)))
	b.WriteString("\n\n")
	if !m.server.loaded {
		b.WriteString(uicopy.T("home.loading", nil))
		return b.String()
	}
	if m.server.server.State == setup.StateRunning {
		b.WriteString(uicopy.T("server.status.running", nil))
		b.WriteString("\n")
		b.WriteString(wordWrap(uicopy.T("server.status.address", map[string]string{
			"address": m.server.server.Address, "fallback": m.server.server.FallbackAddress,
		}), width))
		b.WriteString("\n")
		b.WriteString(uicopy.T("server.status.version", map[string]string{"version": m.server.server.Version}))
	} else {
		b.WriteString(uicopy.T("server.status.not_running", nil))
	}
	b.WriteString("\n\n")
	for i, item := range m.server.menu() {
		cursor := "  "
		style := itemStyle
		if i == m.server.cursor {
			cursor = "> "
			style = selectedItemStyle
		}
		b.WriteString(style.Render(cursor + item.label))
		b.WriteString("\n")
	}
	if m.server.linkShown {
		b.WriteString("\n")
		b.WriteString(wordWrap(uicopy.T("open.intro", nil), width))
		b.WriteString("\n  ")
		b.WriteString(m.server.link.URL)
		b.WriteString("\n")
		b.WriteString(uicopy.T("open.fallback", nil))
		b.WriteString("\n  ")
		b.WriteString(m.server.link.FallbackURL)
	}
	return b.String()
}
