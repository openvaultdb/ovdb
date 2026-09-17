package tui

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// homeScreen renders the server-computed Home document exactly — status
// line, question and options, in order, are all server decisions
// (first-run-onboarding#REQ:home-menu-options, REQ:home-status-line). This
// screen builds no menu of its own: a running server's "server" option
// always carries a real running badge instead of a stale "Start the OVDB
// server" label, because the label and the badge both come from the one
// document the server just computed from its own state.
type homeScreen struct {
	loaded   bool
	document setup.HomeDocument
	err      error
	cursor   int
}

// screenFor maps a Home option id to the TUI screen it opens.
func screenFor(id string) string {
	switch id {
	case "demo":
		return ScreenDemo
	case "server":
		return ScreenServer
	case "settings":
		return ScreenSettings
	case "create":
		return ScreenCreate
	case "connect":
		return ScreenConnect
	case "databases":
		return ScreenDatabases
	case "browse":
		return ScreenBrowse
	case "skills":
		return ScreenSkills
	default:
		return ScreenHome
	}
}

// statusLine renders every part of the document's status line, joined the
// same way the web console joins them (" · "), wrapped to width so a long
// address never overflows the window (review: Home status line must wrap
// like every other screen's).
func (s homeScreen) statusLine() string {
	switch {
	case !s.loaded:
		return uicopy.T("console.loading", nil)
	case s.err != nil:
		return uicopy.T("home.status_unavailable", nil)
	}
	parts := make([]string, len(s.document.StatusLine))
	for i, ref := range s.document.StatusLine {
		parts[i] = uicopy.T(ref.Key, ref.Params)
	}
	return strings.Join(parts, " · ")
}

func (m Model) viewHome() string {
	width := m.width
	var b strings.Builder
	// The persistent header already shows "OpenVaultDB"; Home's body starts
	// at the tagline so the name is not shown twice.
	b.WriteString(wordWrap(uicopy.T("home.tagline", nil), width))
	b.WriteString("\n\n")
	b.WriteString(wordWrap(m.home.statusLine(), width))
	b.WriteString("\n\n")
	if !m.home.loaded {
		return b.String()
	}
	questionKey := m.home.document.QuestionKey
	if questionKey == "" {
		questionKey = "home.question"
	}
	b.WriteString(uicopy.T(questionKey, nil))
	b.WriteString("\n\n")
	for i, option := range m.home.document.Options {
		cursor := "  "
		style := itemStyle
		if i == m.home.cursor {
			cursor = "> "
			style = selectedItemStyle
		}
		label := uicopy.T(option.LabelKey, nil)
		if option.Disabled {
			style = mutedStyle
		}
		if option.Badge != nil {
			label += "  [" + uicopy.T(option.Badge.LabelKey, nil) + "]"
		}
		b.WriteString(style.Render(cursor + label))
		b.WriteString("\n")
		if option.DescriptionKey != "" {
			b.WriteString(mutedStyle.Render(indentWrap("    ", uicopy.T(option.DescriptionKey, nil), width)))
			b.WriteString("\n")
		}
	}
	return b.String()
}
