package tui

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
)

// exploreView is which part of Explore data (capability 22) is on screen.
type exploreView int

const (
	exploreMenuView exploreView = iota
	exploreCLIView
	exploreAppView
)

// exploreScreen is Explore data: the intent-first menu
// (explore-data-handoff#REQ:intent-first-menu), then either the prepared
// DataTug CLI connection or DataTug.app's honest limitation.
type exploreScreen struct {
	loaded    bool
	database  string
	menu      explore.Menu
	view      exploreView
	cursor    int
	cli       explore.DataTugCLI
	cliLoaded bool
}

type exploreMenuMsg struct {
	database string
	menu     explore.Menu
	err      error
}

type exploreCLIMsg struct {
	document explore.DataTugCLI
	err      error
}

type exploreAppOpenedMsg struct {
	openErr error
}

// enterExplore reads the current database and the intent-first menu — a
// pure read: it starts nothing and writes no file
// (AC:menu-asks-intent-first).
func (m Model) enterExplore() (Model, tea.Cmd) {
	m.screen = ScreenExplore
	m.explore = exploreScreen{}
	local, ctx := m.local, m.ctx
	return m, func() tea.Msg {
		body, err := local.Context(ctx)
		if err != nil {
			return exploreMenuMsg{err: err}
		}
		var document dbcontext.Document
		if err := json.Unmarshal(body, &document); err != nil {
			return exploreMenuMsg{err: err}
		}
		database := ""
		if document.Context != nil {
			database = document.Context.Database
		}
		menuBody, err := local.ExploreMenu(ctx, database)
		if err != nil {
			return exploreMenuMsg{err: err}
		}
		var menu explore.Menu
		if err := json.Unmarshal(menuBody, &menu); err != nil {
			return exploreMenuMsg{err: err}
		}
		return exploreMenuMsg{database: database, menu: menu}
	}
}

// loadExploreCLICmd chooses DataTug CLI: checks datatug on PATH and writes
// the descriptor (REQ:prepare-datatug-cli-connection).
func (m Model) loadExploreCLICmd() tea.Cmd {
	local, ctx, database := m.local, m.ctx, m.explore.database
	return func() tea.Msg {
		body, err := local.PrepareDataTugCLI(ctx, database, "", false)
		if err != nil {
			return exploreCLIMsg{err: err}
		}
		var document explore.DataTugCLI
		if err := json.Unmarshal(body, &document); err != nil {
			return exploreCLIMsg{err: err}
		}
		return exploreCLIMsg{document: document}
	}
}

// openDataTugAppCmd opens DataTug.app in the browser (REQ:honest-datatug-app-state).
func (m Model) openDataTugAppCmd() tea.Cmd {
	openBrowser := m.openBrowser
	return func() tea.Msg { return exploreAppOpenedMsg{openErr: openBrowser(explore.AppURL)} }
}

func (m Model) updateExploreMsg(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case exploreMenuMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.explore = exploreScreen{loaded: true, database: msg.database, menu: msg.menu, view: exploreMenuView}
	case exploreCLIMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.explore.cli, m.explore.cliLoaded, m.explore.view = msg.document, true, exploreCLIView
	case exploreAppOpenedMsg:
		m.busy = nil
		m.pullNotices()
	}
	return m, nil
}

func (m Model) updateExplore(key string) (tea.Model, tea.Cmd) {
	switch m.explore.view {
	case exploreMenuView:
		switch key {
		case "up", "k":
			if m.explore.cursor > 0 {
				m.explore.cursor--
			}
		case "down", "j":
			if m.explore.cursor < 1 {
				m.explore.cursor++
			}
		case "enter":
			if !m.explore.loaded {
				return m, nil
			}
			if m.explore.cursor == 0 {
				m.busy = &busyState{label: uicopy.T("console.loading", nil)}
				return m, tea.Batch(m.loadExploreCLICmd(), tickCmd())
			}
			m.explore.view = exploreAppView
		case "esc", "backspace":
			return m.backHome()
		}
	case exploreCLIView:
		switch key {
		case "esc", "backspace":
			m.explore.view = exploreMenuView
		}
	case exploreAppView:
		switch key {
		case "c":
			m.explore.view = exploreMenuView
			m.explore.cursor = 0
			m.busy = &busyState{label: uicopy.T("console.loading", nil)}
			return m, tea.Batch(m.loadExploreCLICmd(), tickCmd())
		case "o":
			m.busy = &busyState{label: uicopy.T("explore.datatug_app.opening", nil)}
			return m, tea.Batch(m.openDataTugAppCmd(), tickCmd())
		case "esc", "backspace":
			m.explore.view = exploreMenuView
		}
	}
	return m, nil
}

func (m Model) viewExplore() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("explore.title", nil)))
	b.WriteString("\n\n")
	if !m.explore.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	switch m.explore.view {
	case exploreMenuView:
		b.WriteString(wordWrap(uicopy.T("explore.question", map[string]string{"database": m.explore.database}), width))
		b.WriteString("\n\n")
		options := [2]struct{ label, help string }{
			{uicopy.T("explore.menu.datatug_cli", nil), uicopy.T(m.explore.menu.DataTugCLIKey, nil)},
			{uicopy.T("explore.menu.datatug_app", nil), uicopy.T("explore.menu.datatug_app_help", nil)},
		}
		for i, option := range options {
			cursor, style := "  ", itemStyle
			if i == m.explore.cursor {
				cursor, style = "> ", selectedItemStyle
			}
			b.WriteString(style.Render(cursor + option.label))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render(indentWrap("    ", option.help, width)))
			b.WriteString("\n")
		}
	case exploreCLIView:
		cli := m.explore.cli
		if cli.OnPath {
			b.WriteString(wordWrap(uicopy.T("explore.datatug_cli.ready", nil), width))
		} else {
			b.WriteString(wordWrap(uicopy.T("explore.datatug_cli.missing", nil), width))
			b.WriteString("\n")
			for _, install := range cli.InstallCommands {
				b.WriteString(indentWrap("  ", install, width))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n\n")
		b.WriteString(wordWrap(uicopy.T("explore.datatug_cli.descriptor_saved", map[string]string{"database": m.explore.database, "path": cli.DescriptorPath}), width))
		b.WriteString("\n\n")
		b.WriteString(uicopy.T("explore.datatug_cli.env_vars", nil))
		b.WriteString("\n")
		b.WriteString(wordWrap(cli.ShellText, width))
		b.WriteString("\n\n")
		b.WriteString(uicopy.T("explore.datatug_cli.token_intro", nil))
		b.WriteString("\n")
		b.WriteString(indentWrap("  ", cli.TokenCommand, width))
		b.WriteString("\n\n")
		b.WriteString(uicopy.T("explore.datatug_cli.query_intro", nil))
		b.WriteString("\n")
		b.WriteString(wordWrap(cli.QueryCommand, width))
		b.WriteString("\n")
	case exploreAppView:
		b.WriteString(wordWrap(uicopy.T("explore.datatug_app.honesty", nil), width))
		b.WriteString("\n\n")
		b.WriteString(itemStyle.Render(uicopy.T("explore.datatug_app.use_cli_instead", nil)))
		b.WriteString("\n")
		b.WriteString(itemStyle.Render(uicopy.T("explore.datatug_app.open", nil)))
		b.WriteString("\n")
	}
	return b.String()
}
