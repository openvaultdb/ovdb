// Package tui is ovdb's terminal UI: one root Model with named screens
// (Home, OVDB server, Settings, Result, Problem) that call
// internal/client.Local exactly as the CLI does — client.Local is the single
// place that decides server-vs-files and mismatch rules, and every screen
// here goes through it rather than duplicating that logic. See
// spec/features/first-run-onboarding (Home IA, sizes, the problem pattern)
// and spec/features/configuration-parity (copy catalogue, capability
// registry, REQ:increments-keep-parity).
package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/browser"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// Screen ids named by the capability registry (internal/parity); dropping
// one of these from the registered set fails that package's existence test
// naming the row and "TUI".
const (
	ScreenHome      = "home"
	ScreenServer    = "server"
	ScreenSettings  = "settings"
	ScreenResult    = "result"
	ScreenProblem   = "problem"
	ScreenCreate    = "create"
	ScreenDatabases = "databases"
	ScreenBrowse    = "browse"
	ScreenDemo      = "demo"
	ScreenConnect   = "connect"
	ScreenSkills    = "skills"
)

// ScreenIDs lists every screen id the TUI registers.
func ScreenIDs() []string {
	return []string{ScreenHome, ScreenServer, ScreenSettings, ScreenResult, ScreenProblem, ScreenCreate, ScreenDatabases, ScreenBrowse, ScreenDemo, ScreenConnect, ScreenSkills}
}

// minWidth and minHeight are first-run-onboarding#REQ:tui-keyboard-and-size's
// floor: below them the TUI shows "Make the window a little bigger" instead
// of a screen that would have to truncate or overflow to fit.
const (
	minWidth  = 60
	minHeight = 20
)

// Model is the root bubbletea model. It owns the current screen and the one
// *client.Local every screen calls.
type Model struct {
	ctx         context.Context
	local       *client.Local
	notices     *noticeBuffer
	openBrowser func(url string) error

	width, height int
	screen        string
	showHelp      bool
	noticeLines   []string

	home      homeScreen
	server    serverScreen
	settings  settingsScreen
	result    resultScreen
	problem   problemScreen
	create    createScreen
	databases databasesScreen
	browse    browseScreen
	demo      demoScreen
	connect   connectScreen
	skills    skillsScreen
	// problemFrom is the screen whose request raised the current Problem,
	// when its remedies return there.
	problemFrom string

	busy *busyState
}

// busyState renders while an operation that takes time (start, stop,
// restart, open in browser, save) is in flight — REQ:tui-keyboard-and-size's
// neighbour requirement that such operations show progress.
type busyState struct {
	label string
	frame int
}

var spinnerFrames = []string{"|", "/", "-", "\\"}

func (b busyState) render() string {
	return itemStyle.Render(spinnerFrames[b.frame%len(spinnerFrames)] + " " + b.label)
}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// New creates the root model on Home. local's Notices field is redirected
// from cmd.ErrOrStderr() (internal/cli's default) to a buffer this model
// drains, because writing straight to stderr would corrupt bubbletea's
// alt-screen. openBrowser launches a URL for the Server screen's "Open in
// browser" — the same internal/browser.Opener the CLI's `ovdb open` uses —
// or a fake a caller injects for tests; nil defaults to the real opener.
func New(ctx context.Context, local *client.Local, openBrowser func(string) error, width, height int) Model {
	notices := &noticeBuffer{}
	local.Notices = notices
	if openBrowser == nil {
		openBrowser = browser.Opener{}.Open
	}
	return Model{
		ctx: ctx, local: local, notices: notices, openBrowser: openBrowser,
		width: width, height: height,
		screen: ScreenHome,
	}
}

func (m Model) Init() tea.Cmd {
	return m.loadHomeCmd()
}

// pullNotices replaces noticeLines with whatever internal/client logged for
// the action that just finished — including clearing it to nothing — so a
// warning from a previous, unrelated action never lingers on screen.
func (m *Model) pullNotices() {
	m.noticeLines = m.notices.take()
}

func (m Model) tooSmall() bool {
	return (m.width > 0 && m.width < minWidth) || (m.height > 0 && m.height < minHeight)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		if m.busy != nil {
			m.busy.frame++
			return m, tickCmd()
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg.String())

	case homeLoadedMsg:
		m.home.loaded = true
		m.home.document = msg.document
		m.home.err = msg.err
		return m, nil

	case serverLoadedMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.server.loaded = true
		m.server.server = msg.document.Server
		m.server.next = msg.document.Next
		m.server.cursor = 0
		return m, nil

	case serverActionMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.server.loaded = true
		m.server.server = msg.document.Server
		m.server.next = msg.document.Next
		m.server.cursor = 0
		m.server.linkShown = false
		m.screen = ScreenServer
		return m, nil

	case stopResultMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.result = newStopResult(msg.wasRunning)
		m.screen = ScreenResult
		return m, nil

	case loginLinkMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.server.linkShown = true
		m.server.link = msg.link
		m.server.browserFailed = msg.openErr != nil
		return m, nil

	case configLoadedMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.settings.loaded = true
		m.settings.document = msg.document
		return m, nil

	case enginesLoadedMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.create.loaded = true
		m.create.engines = msg.document.Engines
		return m, nil

	case databasesLoadedMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.databases.loaded = true
		m.databases.document = msg.document
		m.databases.cursor = min(m.databases.cursor, len(msg.document.Databases))
		return m, nil

	case browseDatabasesMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.browse.loaded = true
		m.browse.databases, m.browse.currentDB = msg.databases, msg.current
		for i, db := range msg.databases {
			if db.ID == msg.current {
				m.browse.cursor = i
			}
		}
		return m, nil

	case browseLoadedMsg:
		return m.updateBrowseLoaded(msg)

	case demoLoadedMsg, demoInstalledMsg, demoOpenedMsg:
		return m.updateDemoMsg(msg)

	case connectEnginesMsg, connectResultMsg:
		return m.updateConnectMsg(msg)

	case skillsLoadedMsg, skillInstalledMsg:
		return m.updateSkillsMsg(msg)

	case contextSetMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.result = resultScreen{title: msg.document.Message, next: msg.next}
		m.screen = ScreenResult
		return m, nil

	case databaseResultMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		switch {
		case msg.removed:
			m.result = newRemovedResult(msg.result)
		case msg.reloaded:
			m.result = newReloadedResult(msg.result)
		default:
			m.result = newCreatedResult(msg.result)
			m.create = createScreen{step: createChoose}
		}
		m.screen = ScreenResult
		return m, nil

	case configSavedMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.settings.loaded = true
		m.settings.document = msg.document
		m.settings.editing = false
		m.settings.input = ""
		m.settings.invalid = false
		port := strconv.Itoa(msg.document.Config.Server.Port)
		if msg.document.Changed != nil && !*msg.document.Changed {
			m.settings.savedMessage = uicopy.T("settings.port.unchanged", map[string]string{"port": port})
		} else {
			m.settings.savedMessage = uicopy.T("settings.port.saved", map[string]string{"port": port})
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.tooSmall() {
		return m, nil
	}
	if m.busy != nil {
		return m, nil
	}
	// "?" is text while typing a filter, name or location.
	typing := m.screen == ScreenCreate && m.create.step != createManifest || m.screen == ScreenBrowse && m.browse.typing || m.screen == ScreenConnect
	if key == "?" && !typing {
		m.showHelp = !m.showHelp
		return m, nil
	}
	switch m.screen {
	case ScreenHome:
		return m.updateHome(key)
	case ScreenServer:
		return m.updateServer(key)
	case ScreenSettings:
		return m.updateSettings(key)
	case ScreenResult:
		return m.updateResult(key)
	case ScreenProblem:
		return m.updateProblem(key)
	case ScreenCreate:
		return m.updateCreate(key)
	case ScreenDatabases:
		return m.updateDatabases(key)
	case ScreenBrowse:
		return m.updateBrowse(key)
	case ScreenDemo:
		return m.updateDemo(key)
	case ScreenConnect:
		return m.updateConnect(key)
	case ScreenSkills:
		return m.updateSkills(key)
	}
	return m, nil
}

// backHome returns to Home and reloads it, since most screens change what
// it shows.
func (m Model) backHome() (tea.Model, tea.Cmd) {
	m.screen = ScreenHome
	m.problemFrom = ""
	m.home.loaded = false
	return m, m.loadHomeCmd()
}

func (m Model) updateHome(key string) (tea.Model, tea.Cmd) {
	options := m.home.document.Options
	switch key {
	case "up", "k":
		// Disabled options are skipped.
		for i := m.home.cursor - 1; i >= 0; i-- {
			if !options[i].Disabled {
				m.home.cursor = i
				break
			}
		}
	case "down", "j":
		for i := m.home.cursor + 1; i < len(options); i++ {
			if !options[i].Disabled {
				m.home.cursor = i
				break
			}
		}
	case "q":
		return m, tea.Quit
	case "enter":
		if !m.home.loaded || m.home.cursor >= len(options) || options[m.home.cursor].Disabled {
			return m, nil
		}
		target := screenFor(options[m.home.cursor].ID)
		m.screen = target
		switch target {
		case ScreenDemo:
			return m.enterDemo()
		case ScreenCreate:
			return m.enterCreate()
		case ScreenConnect:
			return m.enterConnect()
		case ScreenSkills:
			return m.enterSkills()
		case ScreenDatabases:
			return m.enterDatabases()
		case ScreenBrowse:
			return m.enterBrowse()
		case ScreenServer:
			m.server.loaded = false
			m.server.linkShown = false
			return m, m.loadServerCmd()
		case ScreenSettings:
			m.settings.loaded = false
			m.settings.editing = false
			m.settings.savedMessage = ""
			return m, m.loadConfigCmd()
		}
	}
	return m, nil
}

func (m Model) updateServer(key string) (tea.Model, tea.Cmd) {
	items := m.server.menu()
	switch key {
	case "up", "k":
		if m.server.cursor > 0 {
			m.server.cursor--
		}
	case "down", "j":
		if m.server.cursor < len(items)-1 {
			m.server.cursor++
		}
	case "enter":
		if m.server.cursor >= len(items) {
			return m, nil
		}
		switch items[m.server.cursor].action {
		case actionStart:
			m.busy = &busyState{label: uicopy.T("server.starting", nil)}
			return m, tea.Batch(m.startCmd(), tickCmd())
		case actionRestart:
			m.busy = &busyState{label: uicopy.T("server.restarting", nil)}
			return m, tea.Batch(m.restartCmd(), tickCmd())
		case actionStop:
			m.busy = &busyState{label: uicopy.T("server.stopping", nil)}
			return m, tea.Batch(m.stopCmd(), tickCmd())
		case actionOpenBrowser:
			m.busy = &busyState{label: uicopy.T("open.opened", nil)}
			return m, tea.Batch(m.openBrowserCmd(), tickCmd())
		}
	case "esc", "backspace":
		m.screen = ScreenHome
		m.home.loaded = false
		return m, m.loadHomeCmd()
	}
	return m, nil
}

func (m Model) updateSettings(key string) (tea.Model, tea.Cmd) {
	if m.settings.editing {
		switch key {
		case "enter":
			port, err := strconv.Atoi(m.settings.input)
			if err != nil || !setup.ValidPort(port) {
				m.settings.invalid = true
				return m, nil
			}
			m.settings.invalid = false
			m.busy = &busyState{label: uicopy.T("settings.saving", nil)}
			return m, tea.Batch(m.saveConfigCmd(port), tickCmd())
		case "esc":
			m.settings.editing = false
			m.settings.input = ""
			m.settings.invalid = false
			return m, nil
		case "backspace":
			if len(m.settings.input) > 0 {
				m.settings.input = m.settings.input[:len(m.settings.input)-1]
			}
			return m, nil
		default:
			if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && len(m.settings.input) < 5 {
				m.settings.input += key
			}
			return m, nil
		}
	}
	switch key {
	case "enter":
		m.settings.editing = true
		m.settings.input = ""
		m.settings.invalid = false
		m.settings.savedMessage = ""
	case "esc", "backspace":
		m.screen = ScreenHome
		m.home.loaded = false
		return m, m.loadHomeCmd()
	}
	return m, nil
}

func (m Model) updateResult(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "o":
		for _, n := range m.result.next {
			if n.Action == demo.ActionOpenApp {
				m.busy = &busyState{label: uicopy.T("demo.opening", nil)}
				return m, tea.Batch(m.openDemoCmd(), tickCmd())
			}
		}
	case "s", "a":
		// "Install TODO AI skill" opens its consent step; "Connect an app or
		// AI assistant" opens AI agent skills.
		for _, n := range m.result.next {
			switch {
			case key == "s" && n.Action == skills.ActionInstall:
				fields := strings.Fields(n.Command)
				return m.offerSkill(fields[len(fields)-1])
			case key == "a" && n.Action == skills.ActionSkills:
				return m.enterSkills()
			}
		}
	case "b", "d", "u":
		// "Browse data", "See your databases" and "Use it in this project",
		// when the result offers them.
		for _, n := range m.result.next {
			switch {
			case key == "b" && n.Action == setup.ActionBrowse:
				fields := strings.Fields(n.Command)
				return m.browseDatabase(fields[len(fields)-1])
			case key == "d" && n.Action == setup.ActionDatabases:
				return m.enterDatabases()
			case key == "u" && n.Action == setup.ActionUse:
				return m.useInProject(strings.TrimPrefix(n.Command, "ovdb use "))
			}
		}
	case "enter", "esc", "backspace":
		return m.backHome()
	}
	return m, nil
}

func (m Model) updateProblem(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.problem.moveCursor(-1)
	case "down", "j":
		m.problem.moveCursor(1)
	case "enter":
		n := m.problem.selected()
		switch {
		case n == nil:
		case n.Action == "use_port":
			if port, ok := parsePort(n.Command); ok {
				m.busy = &busyState{label: uicopy.T("server.starting", nil)}
				return m, tea.Batch(m.portRemedyCmd(port), tickCmd())
			}
		case m.problemFrom == ScreenConnect && (n.Action == setup.ActionEditName || n.Action == setup.ActionEditLocation || n.Action == setup.ActionEditManifest):
			m.problemFrom = ""
			return m.connectRemedy(*n), nil
		case n.Action == setup.ActionEditManifest:
			return m.enterConnectManifest(m.create.chosen)
		case (n.Action == setup.ActionEditName || n.Action == setup.ActionEditLocation) && m.create.chosen.ID != "":
			return m.createRemedy(*n), nil
		case n.Action == setup.ActionDatabases:
			return m.enterDatabases()
		}
	case "esc", "backspace":
		return m.backHome()
	}
	return m, nil
}

func (m Model) View() tea.View {
	if m.tooSmall() {
		v := tea.NewView(mutedStyle.Render(uicopy.T("tui.too_small", nil)))
		v.AltScreen = true
		return v
	}
	header := headerStyle.Width(max(m.width, 1)).Render(uicopy.T("app.name", nil))
	var body string
	switch {
	case m.busy != nil:
		body = m.busy.render()
	case m.screen == ScreenHome:
		body = m.viewHome()
	case m.screen == ScreenServer:
		body = m.viewServer()
	case m.screen == ScreenSettings:
		body = m.viewSettings()
	case m.screen == ScreenResult:
		body = m.viewResult()
	case m.screen == ScreenProblem:
		body = m.viewProblem()
	case m.screen == ScreenCreate:
		body = m.viewCreate()
	case m.screen == ScreenDatabases:
		body = m.viewDatabases()
	case m.screen == ScreenBrowse:
		body = m.viewBrowse()
	case m.screen == ScreenDemo:
		body = m.viewDemo()
	case m.screen == ScreenConnect:
		body = m.viewConnect()
	case m.screen == ScreenSkills:
		body = m.viewSkills()
	}
	sections := []string{header, body}
	if len(m.noticeLines) > 0 {
		sections = append(sections, mutedStyle.Render(strings.Join(m.noticeLines, "\n")))
	}
	sections = append(sections, m.footer())
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) footer() string {
	switch {
	case m.showHelp:
		return helpStyle.Render(uicopy.T("tui.help.body", nil))
	case m.screen == ScreenHome:
		return helpStyle.Render(uicopy.T("tui.footer.home", nil))
	case m.screen == ScreenSettings && m.settings.editing:
		return helpStyle.Render(uicopy.T("settings.hint.edit", nil))
	case m.screen == ScreenSettings:
		return helpStyle.Render(uicopy.T("settings.hint.view", nil))
	case m.screen == ScreenCreate && m.create.step == createChoose:
		return helpStyle.Render(wordWrap(uicopy.T("create.hint.choose", nil), m.width))
	case m.screen == ScreenCreate && m.create.step == createForm:
		return helpStyle.Render(wordWrap(uicopy.T("create.hint.form", nil), m.width))
	case m.screen == ScreenCreate && m.create.step == createManifest:
		return helpStyle.Render(wordWrap(uicopy.T("create.hint.manifest", nil), m.width))
	case m.screen == ScreenConnect && m.connect.step == connectChoose:
		return helpStyle.Render(wordWrap(uicopy.T("create.hint.choose", nil), m.width))
	case m.screen == ScreenConnect && m.connect.step == connectForm:
		return helpStyle.Render(wordWrap(uicopy.T("connect.hint.form", nil), m.width))
	case m.screen == ScreenConnect:
		return helpStyle.Render(wordWrap(uicopy.T("connect.hint.manifest", nil), m.width))
	case m.screen == ScreenBrowse:
		return helpStyle.Render(wordWrap(m.browseFooter(), m.width))
	case m.screen == ScreenDemo:
		return helpStyle.Render(wordWrap(uicopy.T("demo.hint.install", nil), m.width))
	case m.screen == ScreenSkills && m.skills.consent != nil:
		return helpStyle.Render(wordWrap(uicopy.T("skills.hint.consent", nil), m.width))
	case m.screen == ScreenSkills:
		return helpStyle.Render(wordWrap(uicopy.T("skills.hint.list", nil), m.width))
	case m.screen == ScreenResult && len(m.result.next) > 0:
		for _, n := range m.result.next {
			if n.Action == skills.ActionInstall {
				return helpStyle.Render(wordWrap(uicopy.T("result.hint.demo", nil), m.width))
			}
		}
		for _, n := range m.result.next {
			if n.Action == demo.ActionOpenApp {
				return helpStyle.Render(wordWrap(uicopy.T("result.hint.open_app", nil), m.width))
			}
		}
		for _, n := range m.result.next {
			if n.Action == skills.ActionSkills {
				return helpStyle.Render(wordWrap(uicopy.T("result.hint.created_skills", nil), m.width))
			}
		}
		for _, n := range m.result.next {
			if n.Action == setup.ActionUse {
				return helpStyle.Render(wordWrap(uicopy.T("result.hint.created", nil), m.width))
			}
		}
		for _, n := range m.result.next {
			if n.Action == setup.ActionDatabases {
				return helpStyle.Render(wordWrap(uicopy.T("result.hint.databases", nil), m.width))
			}
		}
		return helpStyle.Render(uicopy.T("tui.footer.back", nil))
	default:
		return helpStyle.Render(uicopy.T("tui.footer.back", nil))
	}
}
