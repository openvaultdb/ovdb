package tui

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// resultScreen is the generic "what happened" screen
// (first-run-onboarding#REQ:after-action-result). Increment 1c uses it for
// stopping the server, whose exact copy AC:stop-copy fixes for both the CLI
// and the TUI.
type resultScreen struct {
	title string
	lines []string
	// next are the server's next actions, each with its command; creating
	// and removing databases fill it.
	next []envelope.Next
	// opened is the TODO app's sign-in links once Open TODO app ran.
	opened []string
}

// newStopResult builds the Result screen for `ovdb server stop`, reusing
// the same copy keys internal/cli's server.go prints
// (local-server-and-web-console#REQ:stopped-server-copy).
func newStopResult(wasRunning bool) resultScreen {
	title := uicopy.T("server.stop.not_running", nil)
	if wasRunning {
		title = uicopy.T("server.stopped", nil)
	}
	return resultScreen{title: title, lines: []string{uicopy.T("server.stopped_copy", nil)}}
}

func (m Model) viewResult() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.result.title))
	b.WriteString("\n\n")
	for _, line := range m.result.lines {
		b.WriteString(wordWrap(line, width))
		b.WriteString("\n")
	}
	if len(m.result.opened) > 0 {
		b.WriteString("\n")
		for _, line := range m.result.opened {
			if link, ok := strings.CutPrefix(line, "  "); ok {
				line = indentWrap("  ", link, width)
			} else {
				line = wordWrap(line, width)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if next := resultNext(m.result.next); len(next) > 0 {
		b.WriteString("\n")
		b.WriteString(uicopy.T("home.what_next", nil))
		b.WriteString("\n")
		for _, line := range nextLines(width, next, nil, 0) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}
