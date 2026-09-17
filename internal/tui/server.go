package tui

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// serverAction names what Enter does for one serverScreen menu item.
type serverAction int

const (
	actionUnknown serverAction = iota
	actionStart
	actionOpenBrowser
	actionRestart
	actionStop
)

// actionForCommand maps one of the server document's own Next commands to
// the action the TUI runs for it, so labels and behaviour both come from
// the server rather than being duplicated here.
func actionForCommand(command string) serverAction {
	switch command {
	case "ovdb server start":
		return actionStart
	case "ovdb server restart":
		return actionRestart
	case "ovdb server stop":
		return actionStop
	default:
		return actionUnknown
	}
}

type serverMenuItem struct {
	label  string
	action serverAction
}

// serverScreen is the "OVDB server" screen: capability rows 4 (server
// status), 5 (stop/restart) and 6 (open in browser). Its state-changing
// actions come straight from the server document's Next list (label and
// command it already computed); "Open in browser" is the one TUI-only
// addition, offered whenever the server is running.
type serverScreen struct {
	loaded bool
	server setup.Server
	next   []envelope.Next
	cursor int

	linkShown     bool
	link          localserver.LoginLink
	browserFailed bool
}

func (s serverScreen) menu() []serverMenuItem {
	var items []serverMenuItem
	if s.loaded && s.server.State == setup.StateRunning {
		items = append(items, serverMenuItem{label: uicopy.T("server.menu.open_browser", nil), action: actionOpenBrowser})
	}
	for _, n := range s.next {
		if action := actionForCommand(n.Command); action != actionUnknown {
			items = append(items, serverMenuItem{label: n.Label, action: action})
		}
	}
	return items
}

func (m Model) viewServer() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("server.title", nil)))
	b.WriteString("\n\n")
	if !m.server.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
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
		if m.server.browserFailed {
			b.WriteString(wordWrap(uicopy.T("open.failed", nil), width))
		} else {
			b.WriteString(wordWrap(uicopy.T("open.if_not_opened", nil), width))
		}
		b.WriteString("\n  ")
		b.WriteString(m.server.link.URL)
		b.WriteString("\n")
		b.WriteString(uicopy.T("open.fallback", nil))
		b.WriteString("\n  ")
		b.WriteString(m.server.link.FallbackURL)
	}
	return b.String()
}
