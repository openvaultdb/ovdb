package tui

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Connect steps.
const (
	connectChoose   = "choose"   // filterable storage picker, then "Connect with a manifest file"
	connectForm     = "form"     // an inGitDB folder or SQLite file, and its name
	connectManifest = "manifest" // a manifest file's path, with a manifest-only engine's steps
)

// connectScreen is "Connect an existing database" (capability rows 10 and
// 10a). Like Create it picks storage from the server's catalogue; an inGitDB
// folder or SQLite file is named after itself until the person names it,
// and any engine connects with a manifest file. Connection details are never
// asked for (database-setup-and-providers#REQ:manifest-only-engines-are-honest):
// every check and refusal comes from the server.
type connectScreen struct {
	loaded  bool
	engines []setup.Engine
	step    string
	filter  string
	cursor  int
	chosen  setup.Engine // empty for "Connect with a manifest file"

	location   string
	name       string
	nameEdited bool
	field      int // 0 location, 1 name
	manifest   string
}

// visible is the filtered catalogue; the manifest choice follows it at
// index len(visible).
func (s connectScreen) visible() []setup.Engine {
	return setup.FilterEngines(s.engines, s.filter)
}

func (m Model) enterConnect() (Model, tea.Cmd) {
	m.screen = ScreenConnect
	m.connect = connectScreen{step: connectChoose}
	return m, m.loadConnectEnginesCmd()
}

// enterConnectManifest opens Connect with a manifest file directly, as the
// next step of a manifest-only engine's setup.
func (m Model) enterConnectManifest(engine setup.Engine) (Model, tea.Cmd) {
	m, cmd := m.enterConnect()
	m.connect.step, m.connect.chosen = connectManifest, engine
	return m, cmd
}

type connectEnginesMsg struct {
	document setup.EnginesDocument
	err      error
}

func (m Model) loadConnectEnginesCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Engines(ctx)
		if err != nil {
			return connectEnginesMsg{err: err}
		}
		var document setup.EnginesDocument
		err = json.Unmarshal(body, &document)
		return connectEnginesMsg{document: document, err: err}
	}
}

type connectResultMsg struct {
	result setup.DatabaseResult
	err    error
}

func (m Model) connectCmd(request setup.ConnectRequest) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.ConnectDatabase(ctx, request, false)
		if err != nil {
			return connectResultMsg{err: err}
		}
		var result setup.DatabaseResult
		err = json.Unmarshal(body, &result)
		return connectResultMsg{result: result, err: err}
	}
}

func (m Model) updateConnectMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.busy = nil
	m.pullNotices()
	switch msg := msg.(type) {
	case connectEnginesMsg:
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.connect.loaded = true
		m.connect.engines = msg.document.Engines
	case connectResultMsg:
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.problemFrom = ScreenConnect
			m.screen = ScreenProblem
			return m, nil
		}
		m.result = newConnectedResult(msg.result)
		m.connect = connectScreen{step: connectChoose}
		m.screen = ScreenResult
	}
	return m, nil
}

// nameFrom is the database name a location suggests: its file or folder
// name without an extension, when that is a valid name.
func nameFrom(location string) string {
	base := filepath.Base(filepath.Clean(strings.TrimSpace(location)))
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if !validName.MatchString(name) {
		return ""
	}
	return name
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func (m Model) updateConnect(key string) (tea.Model, tea.Cmd) {
	switch m.connect.step {
	case connectForm:
		return m.updateConnectForm(key)
	case connectManifest:
		return m.updateConnectManifest(key)
	}
	visible := m.connect.visible()
	switch key {
	case "up":
		if m.connect.cursor > 0 {
			m.connect.cursor--
		}
	case "down", "tab":
		if m.connect.cursor < len(visible) {
			m.connect.cursor++
		}
	case "esc":
		return m.backHome()
	case "backspace":
		if m.connect.filter == "" {
			return m.backHome()
		}
		m.connect.filter = dropLast(m.connect.filter)
		m.connect.cursor = 0
	case "enter":
		if !m.connect.loaded {
			return m, nil
		}
		if m.connect.cursor >= len(visible) {
			m.connect.chosen = setup.Engine{}
			m.connect.step = connectManifest
			return m, nil
		}
		m.connect.chosen = visible[m.connect.cursor]
		if m.connect.chosen.Setup == setup.SetupManifest {
			m.connect.step = connectManifest
			return m, nil
		}
		m.connect.step = connectForm
		m.connect.field = 0
	default:
		if text := typed(key); text != "" && text != " " {
			m.connect.filter += text
			m.connect.cursor = 0
		}
	}
	return m, nil
}

func (m Model) updateConnectForm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.connect.step = connectChoose
		return m, nil
	case "tab", "down":
		m.connect.field = 1
		return m, nil
	case "shift+tab", "up":
		m.connect.field = 0
		return m, nil
	case "enter":
		if m.connect.field == 0 {
			m.connect.field = 1
			return m, nil
		}
		request := setup.ConnectRequest{ID: m.connect.name, Engine: m.connect.chosen.ID, Path: strings.TrimSpace(m.connect.location)}
		m.busy = &busyState{label: uicopy.T("connect.connecting", nil)}
		return m, tea.Batch(m.connectCmd(request), tickCmd())
	case "backspace":
		if m.connect.field == 0 {
			m.connect.location = dropLast(m.connect.location)
		} else {
			m.connect.name = dropLast(m.connect.name)
			m.connect.nameEdited = m.connect.name != ""
		}
	default:
		text := typed(key)
		if text == "" {
			return m, nil
		}
		if m.connect.field == 0 {
			m.connect.location += text
		} else if text != " " {
			m.connect.name += text
			m.connect.nameEdited = true
		}
	}
	// The name follows the folder or file until the person names it.
	if m.connect.field == 0 && !m.connect.nameEdited {
		m.connect.name = nameFrom(m.connect.location)
	}
	return m, nil
}

func (m Model) updateConnectManifest(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.connect.step = connectChoose
	case "enter":
		request := setup.ConnectRequest{Manifest: strings.TrimSpace(m.connect.manifest)}
		m.busy = &busyState{label: uicopy.T("connect.connecting", nil)}
		return m, tea.Batch(m.connectCmd(request), tickCmd())
	case "backspace":
		m.connect.manifest = dropLast(m.connect.manifest)
	default:
		m.connect.manifest += typed(key)
	}
	return m, nil
}

// connectRemedy handles a Problem screen next action that returns to the
// form: another name, location or manifest file.
func (m Model) connectRemedy(next envelope.Next) Model {
	m.screen = ScreenConnect
	switch {
	case next.Action == setup.ActionEditManifest:
		m.connect.step = connectManifest
	case m.connect.step == connectManifest:
	case next.Action == setup.ActionEditName:
		m.connect.step, m.connect.field = connectForm, 1
	default:
		m.connect.step, m.connect.field = connectForm, 0
	}
	return m
}

func (m Model) viewConnect() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("home.menu.connect_database", nil)))
	b.WriteString("\n\n")
	if !m.connect.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	switch m.connect.step {
	case connectForm:
		engine := m.connect.chosen
		b.WriteString(wordWrap(uicopy.T("create.chosen", map[string]string{"engine": engine.Name}), width))
		b.WriteString("\n\n")
		label, help := uicopy.T("connect.location.label.ingitdb", nil), uicopy.T("connect.location.help.ingitdb", nil)
		if engine.ID == setup.EngineSQLite {
			label, help = uicopy.T("connect.location.label.sqlite", nil), uicopy.T("connect.location.help.sqlite", nil)
		}
		b.WriteString(fieldLine(m.connect.field == 0, label, m.connect.location, width))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(indentWrap("  ", help, width)))
		b.WriteString("\n\n")
		b.WriteString(fieldLine(m.connect.field == 1, uicopy.T("create.name.label", nil), m.connect.name, width))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(indentWrap("  ", uicopy.T("create.name.help", nil), width)))
		return b.String()
	case connectManifest:
		if engine := m.connect.chosen; engine.Setup == setup.SetupManifest {
			b.WriteString(itemStyle.Render(wordWrap(uicopy.T("engine.manifest.title", nil), width)))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("engine.manifest.intro", map[string]string{"engine": engine.Name}), width)))
			b.WriteString("\n")
			for _, line := range nextLines(width, engine.ManifestSteps, nil, 0) {
				b.WriteString(line)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		} else {
			b.WriteString(itemStyle.Render(wordWrap(uicopy.T("connect.manifest_option", nil), width)))
			b.WriteString("\n\n")
		}
		b.WriteString(fieldLine(true, uicopy.T("connect.manifest.label", nil), m.connect.manifest, width))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(indentWrap("  ", uicopy.T("connect.manifest.help", nil), width)))
		return b.String()
	}

	b.WriteString(wordWrap(uicopy.T("connect.question", nil), width))
	b.WriteString("\n")
	b.WriteString(selectedItemStyle.Render(uicopy.T("create.filter.label", nil) + ": " + m.connect.filter + "_"))
	b.WriteString("\n\n")
	visible := m.connect.visible()
	if len(visible) == 0 {
		b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("create.filter.none", map[string]string{"filter": m.connect.filter}), width)))
		b.WriteString("\n")
	} else {
		// Two lines for the manifest choice below, and one for the gap.
		b.WriteString(m.engineList(visible, m.connect.cursor, 3))
	}
	b.WriteString("\n")
	cursor, style := "  ", itemStyle
	if m.connect.cursor >= len(visible) {
		cursor, style = "> ", selectedItemStyle
	}
	b.WriteString(style.Render(cursor + uicopy.T("connect.manifest_option", nil)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(truncateEnd("    "+uicopy.T("connect.manifest_option_help", nil), width)))
	return b.String()
}

// fieldLine renders one text field: its label, then its value below, so a
// long path wraps under itself.
func fieldLine(focused bool, label, value string, width int) string {
	cursor, style := "  ", itemStyle
	if focused {
		cursor, style, value = "> ", selectedItemStyle, value+"_"
	}
	return style.Render(cursor+label+":") + "\n" + style.Render(indentWrap("    ", value, width))
}

// newConnectedResult is the Result for a connected database, with the
// server's next actions (database-setup-and-providers#REQ:create-result-next-actions).
func newConnectedResult(result setup.DatabaseResult) resultScreen {
	db := result.Database
	return resultScreen{
		title: uicopy.T("database.connected.title", map[string]string{"name": db.ID}),
		lines: []string{setup.ConnectedStored(db)},
		next:  result.Next,
	}
}
