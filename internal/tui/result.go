package tui

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
)

// resultScreen is the generic "what happened" screen
// (first-run-onboarding#REQ:after-action-result). Increment 1c uses it for
// stopping the server, whose exact copy AC:stop-copy fixes for both the CLI
// and the TUI.
type resultScreen struct {
	title string
	lines []string
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
	return b.String()
}
