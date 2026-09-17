package tui

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/localserver"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
)

// demoScreen is Try a demo (capability 18): it shows where the TODO demo's
// lists will be stored before anything is written
// (todo-demo#REQ:demo-install-idempotent), installs on Enter, and hands over
// to the Result, whose Open TODO app (capability 19) signs the browser in.
type demoScreen struct {
	loaded   bool
	document demo.Document
}

type demoLoadedMsg struct {
	document demo.Document
	err      error
}

type demoInstalledMsg struct {
	document demo.Document
	err      error
}

type demoOpenedMsg struct {
	link    localserver.LoginLink
	openErr error
	err     error
}

// enterDemo reads the demo document (a pure read: it starts nothing). An
// installed demo goes straight to its Result.
func (m Model) enterDemo() (Model, tea.Cmd) {
	m.screen = ScreenDemo
	m.demo = demoScreen{}
	local, ctx := m.local, m.ctx
	return m, func() tea.Msg {
		body, err := local.Demo(ctx)
		if err != nil {
			return demoLoadedMsg{err: err}
		}
		var document demo.Document
		err = json.Unmarshal(body, &document)
		return demoLoadedMsg{document: document, err: err}
	}
}

func (m Model) installDemoCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.InstallDemo(ctx, demo.InstallRequest{}, false)
		if err != nil {
			return demoInstalledMsg{err: err}
		}
		var document demo.Document
		err = json.Unmarshal(body, &document)
		return demoInstalledMsg{document: document, err: err}
	}
}

// openDemoCmd creates a login link that lands on the TODO app and opens it,
// the same as `ovdb demo open`.
func (m Model) openDemoCmd() tea.Cmd {
	local, ctx, openBrowser := m.local, m.ctx, m.openBrowser
	return func() tea.Msg {
		body, err := local.DemoLink(ctx, false)
		if err != nil {
			return demoOpenedMsg{err: err}
		}
		var link localserver.LoginLink
		if err := json.Unmarshal(body, &link); err != nil {
			return demoOpenedMsg{err: err}
		}
		return demoOpenedMsg{link: link, openErr: openBrowser(link.URL)}
	}
}

func (m Model) updateDemoMsg(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case demoLoadedMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		if msg.document.Installed {
			msg.document.AlreadyInstalled = true
			m.result = newDemoResult(msg.document)
			m.screen = ScreenResult
			return m, nil
		}
		m.demo = demoScreen{loaded: true, document: msg.document}
	case demoInstalledMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.result = newDemoResult(msg.document)
		m.screen = ScreenResult
	case demoOpenedMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		intro := uicopy.T("demo.open.if_not_opened", nil)
		if msg.openErr != nil {
			intro = uicopy.T("demo.open.intro", nil)
		}
		m.result.opened = []string{intro, "  " + msg.link.URL, uicopy.T("open.fallback", nil), "  " + msg.link.FallbackURL}
		m.screen = ScreenResult
	}
	return m, nil
}

func (m Model) updateDemo(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		if !m.demo.loaded {
			return m, nil
		}
		m.busy = &busyState{label: uicopy.T("demo.installing", nil)}
		return m, tea.Batch(m.installDemoCmd(), tickCmd())
	case "esc", "backspace":
		return m.backHome()
	}
	return m, nil
}

// newDemoResult is "The TODO demo is ready" (or already installed) with its
// next actions (todo-demo#REQ:demo-next-actions).
func newDemoResult(document demo.Document) resultScreen {
	title := uicopy.T("demo.ready.title", nil)
	if document.AlreadyInstalled {
		title = uicopy.T("demo.already.title", nil)
	}
	return resultScreen{
		title: title,
		lines: []string{uicopy.T("demo.ready.stored", map[string]string{"path": document.Location}), uicopy.T("demo.ready.shared", nil)},
		next:  document.Next,
	}
}

func (m Model) viewDemo() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("demo.title", nil)))
	b.WriteString("\n\n")
	if !m.demo.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	b.WriteString(wordWrap(uicopy.T("demo.intro", nil), width))
	b.WriteString("\n\n")
	b.WriteString(wordWrap(uicopy.T("demo.will_store", map[string]string{"path": m.demo.document.Location}), width))
	b.WriteString("\n")
	return b.String()
}
