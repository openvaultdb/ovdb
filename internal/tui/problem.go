package tui

import (
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// problemScreen renders any *envelope.Error the way CLI's Render does:
// title, "Why", "What you can do" (first-run-onboarding#REQ:problem-pattern,
// AC:problem-shows-why-and-fix). A Next entry whose Action the TUI knows how
// to run (today, only "use_port") is selectable; every other entry is shown
// with its runnable command for a person or agent to copy.
type problemScreen struct {
	err    *envelope.Error
	cursor int
}

func (s *problemScreen) setError(err *envelope.Error) {
	s.err = err
	s.cursor = 0
}

// actionable returns the indexes into err.Next this screen can run itself.
func (s problemScreen) actionable() []int {
	if s.err == nil {
		return nil
	}
	var out []int
	for i, n := range s.err.Next {
		if n.Action != "" {
			out = append(out, i)
		}
	}
	return out
}

// selected returns the currently highlighted actionable Next entry, or nil
// when none of them is one the TUI can run.
func (s *problemScreen) selected() *envelope.Next {
	act := s.actionable()
	if len(act) == 0 {
		return nil
	}
	if s.cursor < 0 || s.cursor >= len(act) {
		s.cursor = 0
	}
	return &s.err.Next[act[s.cursor]]
}

func (s *problemScreen) moveCursor(delta int) {
	act := s.actionable()
	if len(act) == 0 {
		return
	}
	s.cursor = ((s.cursor+delta)%len(act) + len(act)) % len(act)
}

func (m Model) viewProblem() string {
	width := m.width
	e := m.problem.err
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(errorStyle.Render(wordWrap(e.Message, width)))
	b.WriteString("\n\n")
	if e.Reason != "" {
		b.WriteString(wordWrap(uicopy.T("problem.why", map[string]string{"reason": e.Reason}), width))
		b.WriteString("\n\n")
	}
	if len(e.Next) > 0 {
		b.WriteString(uicopy.T("problem.what_you_can_do", nil))
		b.WriteString("\n")
		for _, line := range nextLines(width, e.Next, m.problem.actionable(), m.problem.cursor) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}
