package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// usageState is Settings → Usage statistics and the one consent prompt
// (telemetry-consent#REQ:consent-prompt-placement, first-run-onboarding
// #REQ:telemetry-asked-after-first-success). The TUI process sends its own
// events: buffered in memory while not_asked, released only by this
// session's Turn on, dropped on No thanks, dismissal or exit.
type usageState struct {
	// asked is set once the prompt was shown in this session: it never
	// appears again, whatever the answer.
	asked bool
	// prompt is whether the current Result shows it.
	prompt bool
	// details shows What's collected (prompt and Settings).
	details bool
	// message is the answer's confirmation, on the Result or in Settings.
	message string
	// status is Settings' telemetry document.
	status telemetry.Status
}

type usageSetMsg struct {
	document telemetry.Document
	state    string
	err      error
}

// Update is update plus telemetry bookkeeping: the step's events, the
// prompt on the first successful Result, and a flush at each step's end.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m
	updated, cmd := m.update(msg)
	next, ok := updated.(Model)
	if !ok {
		return updated, cmd
	}
	if _, isKey := msg.(tea.KeyPressMsg); isKey {
		next.recordChoice(before)
		return next, cmd
	}
	switch msg.(type) {
	case tickMsg, tea.WindowSizeMsg, homeLoadedMsg, configLoadedMsg, usageSetMsg:
		return next, cmd
	}
	if next.busy != nil || (next.screen != ScreenResult && next.screen != ScreenProblem) {
		return next, cmd
	}
	if next.screen == ScreenResult {
		next = next.offerUsagePrompt()
	}
	return next, tea.Batch(cmd, next.flushCmd())
}

// recordChoice records the Home option or storage type a key just chose.
func (m Model) recordChoice(before Model) {
	switch {
	case before.screen == ScreenHome && m.screen != ScreenHome && before.home.cursor < len(before.home.document.Options):
		m.local.Telemetry.Record(telemetry.NewOptionSelected(before.home.document.Options[before.home.cursor].ID))
	case before.screen == ScreenCreate && before.create.step == createChoose && m.create.step != createChoose && m.create.chosen.ID != "":
		m.local.Telemetry.Record(telemetry.NewEngineSelected(m.create.chosen.ID))
	case before.screen == ScreenConnect && before.connect.step == connectChoose && m.connect.step != connectChoose && m.connect.chosen.ID != "":
		m.local.Telemetry.Record(telemetry.NewEngineSelected(m.connect.chosen.ID))
	}
}

// offerUsagePrompt shows the prompt on this Result when it was never shown
// in this session, the person hasn't decided and this process isn't forced
// off.
func (m Model) offerUsagePrompt() Model {
	if m.usage.asked || m.local.Telemetry == nil {
		return m
	}
	status := m.local.TelemetryStatus().Telemetry
	if status.State != telemetry.StateNotAsked || forcedOff(status.Reason) {
		return m
	}
	m.usage.asked, m.usage.prompt, m.usage.details, m.usage.message = true, true, false, ""
	return m
}

func forcedOff(reason string) bool {
	return reason == telemetry.ReasonEnvOVDB || reason == telemetry.ReasonDoNotTrack || reason == telemetry.ReasonCI
}

// flushCmd sends what may be sent, off the UI goroutine, within the 2 s
// bound.
func (m Model) flushCmd() tea.Cmd {
	recorder, ctx := m.local.Telemetry, m.ctx
	if recorder == nil {
		return nil
	}
	return func() tea.Msg {
		recorder.Flush(ctx)
		return nil
	}
}

func (m Model) setUsageCmd(state string) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		document, err := local.SetTelemetry(ctx, telemetry.Change{State: state, ConfirmedByUser: state == telemetry.StateEnabled})
		if err == nil {
			local.Telemetry.Flush(ctx)
		}
		return usageSetMsg{document: document, state: state, err: err}
	}
}

// usageKey handles the prompt's and Settings' keys.
func (m Model) usageKey(key string) (bool, Model, tea.Cmd) {
	switch {
	case m.screen == ScreenResult && m.usage.prompt:
		switch key {
		case "t":
			m.usage.prompt = false
			return true, m, m.setUsageCmd(telemetry.StateEnabled)
		case "n":
			m.usage.prompt = false
			m.local.Telemetry.Discard()
			return true, m, m.setUsageCmd(telemetry.StateDisabled)
		case "w":
			m.usage.details = !m.usage.details
			return true, m, nil
		case "esc", "backspace":
			if m.usage.details {
				m.usage.details = false
				return true, m, nil
			}
			// Decide later: nothing buffered is kept, it isn't asked again,
			// and the Result stays.
			m.usage.prompt = false
			m.local.Telemetry.Discard()
			m.usage.message = uicopy.T("telemetry.prompt.later", nil)
			return true, m, nil
		case "enter":
			// Enter never answers or dismisses it by accident (review L5).
			return true, m, nil
		}
	case m.screen == ScreenSettings && m.settings.loaded && !m.settings.editing:
		switch key {
		case "t":
			if m.usage.status.State != telemetry.StateEnabled {
				return true, m, m.setUsageCmd(telemetry.StateEnabled)
			}
			return true, m, nil
		case "x":
			if m.usage.status.State != telemetry.StateDisabled {
				m.local.Telemetry.Discard()
				return true, m, m.setUsageCmd(telemetry.StateDisabled)
			}
			return true, m, nil
		case "w":
			m.usage.details = !m.usage.details
			return true, m, nil
		}
	}
	return false, m, nil
}

// updateUsage applies a decision.
func (m Model) updateUsage(msg usageSetMsg) (Model, tea.Cmd) {
	m.pullNotices()
	if msg.err != nil {
		m.problem.setError(asProblem(msg.err))
		m.screen = ScreenProblem
		return m, nil
	}
	m.usage.asked, m.usage.prompt = true, false
	m.usage.status = msg.document.Telemetry
	switch {
	case msg.state == telemetry.StateEnabled:
		m.usage.message = uicopy.T("telemetry.enabled", nil)
	case m.screen == ScreenResult:
		m.usage.message = uicopy.T("telemetry.prompt.declined", nil)
	default:
		m.usage.message = uicopy.T("telemetry.turned_off", nil)
	}
	if text := msg.document.Telemetry.ReasonText; text != "" {
		m.usage.message += " " + text
	}
	return m, nil
}

// viewResultWithUsage is the Result with the prompt under it, or, while
// What's collected? is open, the prompt and both lists in the Result's
// place, so the choices and footer always fit at 80×24 (review M3).
func (m Model) viewResultWithUsage() string {
	if m.usage.prompt && m.usage.details {
		var b strings.Builder
		b.WriteString(titleStyle.Render(uicopy.T("telemetry.prompt.title", nil)))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("telemetry.intro", nil), m.width)))
		b.WriteString(m.viewCollected())
		return b.String()
	}
	return m.viewResult() + m.viewUsagePrompt()
}

// usagePromptFooter lists the prompt's keys on one line: Turn on and No
// thanks with equal weight.
func (m Model) usagePromptFooter() string {
	if m.usage.details {
		return uicopy.T("telemetry.prompt.footer_details", nil)
	}
	return uicopy.T("telemetry.prompt.footer", nil)
}

func (m Model) viewUsagePrompt() string {
	width := m.width
	var b strings.Builder
	switch {
	case m.usage.prompt:
		b.WriteString("\n")
		b.WriteString(itemStyle.Render(uicopy.T("telemetry.prompt.title", nil)))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("telemetry.intro", nil), width)))
	case m.usage.message != "" && m.screen == ScreenResult:
		b.WriteString("\n")
		b.WriteString(wordWrap(m.usage.message, width))
	}
	return b.String()
}

func (m Model) viewCollected() string {
	width := m.width
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(itemStyle.Render(uicopy.T("telemetry.collected.title", nil)))
	for _, line := range telemetry.Collected() {
		b.WriteString("\n")
		b.WriteString(bullet(line, width))
	}
	b.WriteString("\n")
	b.WriteString(itemStyle.Render(uicopy.T("telemetry.never.title", nil)))
	for _, line := range telemetry.NeverCollected() {
		b.WriteString("\n")
		b.WriteString(bullet(line, width))
	}
	return b.String()
}

func usageStateLabel(state string) string {
	switch state {
	case telemetry.StateEnabled:
		return uicopy.T("telemetry.state.enabled", nil)
	case telemetry.StateDisabled:
		return uicopy.T("telemetry.state.disabled", nil)
	default:
		return uicopy.T("telemetry.state.not_asked", nil)
	}
}

// viewUsageSettings is Settings → Usage statistics, with the same state,
// reason, provider and lists as `ovdb telemetry status` and the web console.
func (m Model) viewUsageSettings() string {
	width := m.width
	status := m.usage.status
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(itemStyle.Render(uicopy.T("telemetry.title", nil)))
	b.WriteString("\n")
	b.WriteString(wordWrap(uicopy.T("telemetry.status_line", map[string]string{"state": usageStateLabel(status.State)}), width))
	if status.ReasonText != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(wordWrap(status.ReasonText, width)))
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("telemetry.intro", nil), width)))
	if m.usage.details {
		b.WriteString(m.viewCollected())
	}
	if m.usage.message != "" {
		b.WriteString("\n")
		b.WriteString(wordWrap(m.usage.message, width))
	}
	return b.String()
}

// bullet is "  • text", wrapped under its first word.
func bullet(text string, width int) string {
	wrap := 0
	if width > 0 {
		wrap = max(width-4, 10)
	}
	lines := strings.Split(wordWrap(text, wrap), "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = "  • " + lines[i]
		} else {
			lines[i] = "    " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
