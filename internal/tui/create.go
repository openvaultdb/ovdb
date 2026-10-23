package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Create steps.
const (
	createChoose   = "choose"   // filterable storage picker
	createManifest = "manifest" // a manifest-only engine's steps
	createForm     = "form"     // name and location
)

// createScreen is "Create a database" (capability rows 8 and 9). The
// catalogue, its order and every refusal come from the server through
// client.Local; the screen only filters (setup.FilterEngines, the rule the
// web console also uses) and collects a name and a location, which follows
// the name under the data home this client resolved until the person edits
// it (database-setup-and-providers#REQ:create-new-database).
type createScreen struct {
	loaded  bool
	engines []setup.Engine
	step    string
	filter  string
	cursor  int
	chosen  setup.Engine

	name           string
	location       string
	locationEdited bool
	field          int // 0 name, 1 location
}

func (s createScreen) visible() []setup.Engine {
	return setup.FilterEngines(s.engines, s.filter)
}

// enterCreate resets the screen and loads the catalogue.
func (m Model) enterCreate() (Model, tea.Cmd) {
	m.screen = ScreenCreate
	m.create = createScreen{step: createChoose}
	return m, m.loadEnginesCmd()
}

func (m Model) defaultLocation() string {
	if m.create.name == "" {
		return ""
	}
	return setup.DefaultPath(m.local.Dirs.Data, m.create.chosen.ID, m.create.name)
}

// typed is the text a key press adds to an input, or "" for any other key.
func typed(key string) string {
	if key == "space" {
		return " "
	}
	if len([]rune(key)) == 1 {
		return key
	}
	return ""
}

func dropLast(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

func (m Model) updateCreate(key string) (tea.Model, tea.Cmd) {
	switch m.create.step {
	case createManifest:
		switch key {
		case "esc", "backspace":
			m.create.step = createChoose
		case "enter":
			// The last step: connect the edited manifest file.
			return m.enterConnectManifest(m.create.chosen)
		}
		return m, nil
	case createForm:
		return m.updateCreateForm(key)
	}
	visible := m.create.visible()
	switch key {
	case "up":
		if m.create.cursor > 0 {
			m.create.cursor--
		}
	case "down", "tab":
		if m.create.cursor < len(visible)-1 {
			m.create.cursor++
		}
	case "esc":
		return m.backHome()
	case "backspace":
		if m.create.filter == "" {
			return m.backHome()
		}
		m.create.filter = dropLast(m.create.filter)
		m.create.cursor = 0
	case "enter":
		if m.create.cursor >= len(visible) {
			return m, nil
		}
		m.create.chosen = visible[m.create.cursor]
		if m.create.chosen.Setup == setup.SetupManifest {
			m.create.step = createManifest
			return m, nil
		}
		m.create.step = createForm
		m.create.field = 0
		if !m.create.locationEdited {
			m.create.location = m.defaultLocation()
		}
	default:
		if text := typed(key); text != "" && text != " " {
			m.create.filter += text
			m.create.cursor = 0
		}
	}
	return m, nil
}

func (m Model) updateCreateForm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.create.step = createChoose
		return m, nil
	case "tab", "down":
		m.create.field = 1
		return m, nil
	case "shift+tab", "up":
		m.create.field = 0
		return m, nil
	case "enter":
		if m.create.field == 0 {
			m.create.field = 1
			return m, nil
		}
		location := m.create.location
		if location == "" {
			location = m.defaultLocation()
		}
		request := setup.CreateRequest{ID: m.create.name, Engine: m.create.chosen.ID, Path: location}
		m.busy = &busyState{label: uicopy.T("create.creating", nil)}
		return m, tea.Batch(m.createCmd(request), tickCmd())
	case "backspace":
		if m.create.field == 0 {
			m.create.name = dropLast(m.create.name)
		} else {
			m.create.location = dropLast(m.create.location)
			m.create.locationEdited = m.create.location != ""
		}
	default:
		text := typed(key)
		if text == "" {
			return m, nil
		}
		if m.create.field == 0 {
			if text != " " {
				m.create.name += text
			}
		} else {
			m.create.location += text
			m.create.locationEdited = true
		}
	}
	if m.create.field == 0 && !m.create.locationEdited {
		m.create.location = m.defaultLocation()
	}
	return m, nil
}

// createRemedy handles a Problem screen next action that returns to the
// form: choose another name or location.
func (m Model) createRemedy(next envelope.Next) Model {
	m.screen = ScreenCreate
	m.create.step = createForm
	if next.Action == setup.ActionEditLocation {
		m.create.field = 1
		return m
	}
	m.create.field = 0
	// "Use the name notes-2 instead" uses notes-2.
	if name := setup.SuggestedName(next); name != "" {
		m.create.name = name
		if !m.create.locationEdited {
			m.create.location = m.defaultLocation()
		}
	}
	return m
}

func (m Model) viewCreate() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("create.title", nil)))
	b.WriteString("\n\n")
	if !m.create.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	switch m.create.step {
	case createManifest:
		engine := m.create.chosen
		b.WriteString(itemStyle.Render(uicopy.T("engine.manifest.title", nil)))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("engine.manifest.intro", map[string]string{"engine": engine.Name}), width)))
		b.WriteString("\n\n")
		for _, line := range nextLines(width, engine.ManifestSteps, nil, 0) {
			b.WriteString(line)
			b.WriteString("\n")
		}
		return b.String()
	case createForm:
		b.WriteString(wordWrap(uicopy.T("create.chosen", map[string]string{"engine": m.create.chosen.Name}), width))
		b.WriteString("\n\n")
		b.WriteString(m.inputLine(0, uicopy.T("create.name.label", nil), m.create.name))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(indentWrap("  ", uicopy.T("create.name.help", nil), width)))
		b.WriteString("\n\n")
		b.WriteString(m.inputLine(1, uicopy.T("create.location.label", nil), m.create.location))
		b.WriteString("\n")
		help := uicopy.T("create.location.help.ingitdb", nil)
		if m.create.chosen.ID == setup.EngineSQLite {
			help = uicopy.T("create.location.help.sqlite", nil)
		}
		b.WriteString(mutedStyle.Render(indentWrap("  ", help, width)))
		return b.String()
	}

	b.WriteString(wordWrap(uicopy.T("create.question", nil), width))
	b.WriteString("\n")
	b.WriteString(selectedItemStyle.Render(uicopy.T("create.filter.label", nil) + ": " + m.create.filter + "_"))
	b.WriteString("\n\n")
	visible := m.create.visible()
	if len(visible) == 0 {
		b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("create.filter.none", map[string]string{"filter": m.create.filter}), width)))
		return b.String()
	}
	b.WriteString(m.engineList(visible, m.create.cursor, 0))
	return b.String()
}

// engineList renders storage choices: pinned engines, a divider, the rest,
// with the cursor at cursor. extra is how many lines the screen shows below
// the list.
func (m Model) engineList(visible []setup.Engine, cursor, extra int) string {
	width := m.width
	var b strings.Builder
	nameWidth := 0
	for _, engine := range visible {
		nameWidth = max(nameWidth, len([]rune(engine.Name)))
	}
	// Name and description share a line when every choice fits, as in the
	// spec's example; otherwise each description wraps below its name.
	oneLine, truncate := true, false
	lines := 0
	for _, engine := range visible {
		if width > 0 && 2+nameWidth+3+len([]rune(engine.Description)) > width {
			oneLine = false
		}
		lines += 1 + len(strings.Split(indentWrap("    ", engine.Description, width), "\n"))
	}
	// In a short window, descriptions are cut to one line each instead.
	if !oneLine && 5+lines+1+extra > m.bodyHeight() {
		oneLine, truncate = true, true
	}
	dividerShown := false
	for i, engine := range visible {
		if !engine.Pinned && !dividerShown {
			dividerShown = true
			if i > 0 {
				b.WriteString(mutedStyle.Render("  " + strings.Repeat("─", min(nameWidth+2, max(width-2, 1)))))
				b.WriteString("\n")
			}
		}
		marker, style := "  ", itemStyle
		if i == cursor {
			marker, style = "> ", selectedItemStyle
		}
		padded := engine.Name + strings.Repeat(" ", nameWidth-len([]rune(engine.Name)))
		if oneLine {
			description := engine.Description
			if truncate {
				description = truncateEnd(description, width-len([]rune(marker+padded))-3)
			}
			b.WriteString(style.Render(marker+padded) + mutedStyle.Render("   "+description))
		} else {
			b.WriteString(style.Render(marker + engine.Name))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render(indentWrap("    ", engine.Description, width)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// inputLine renders one field: its label, then its value on the lines
// below, so a long location wraps under itself instead of under the label.
func (m Model) inputLine(field int, label, value string) string {
	cursor, style, value := "  ", itemStyle, value
	if m.create.field == field {
		cursor, style, value = "> ", selectedItemStyle, value+"_"
	}
	return style.Render(cursor+label+":") + "\n" + style.Render(indentWrap("    ", value, m.width))
}

// newCreatedResult is the Result for a created database, with the server's
// next actions (first-run-onboarding#REQ:after-action-result).
func newCreatedResult(result setup.DatabaseResult) resultScreen {
	db := result.Database
	stored := uicopy.T("database.created.stored.ingitdb", map[string]string{"path": db.Location + pathSeparator(db.Location)})
	if db.Engine == setup.EngineSQLite {
		stored = uicopy.T("database.created.stored.sqlite", map[string]string{"path": db.Location})
	}
	return resultScreen{
		title: uicopy.T("database.created.title", map[string]string{"name": db.ID}),
		lines: []string{stored},
		next:  result.Next,
	}
}

// pathSeparator is the separator location uses, so a folder reads as one.
func pathSeparator(location string) string {
	if strings.Contains(location, `\`) && !strings.Contains(location, "/") {
		return `\`
	}
	return "/"
}

// resultNext drops the entries the Result screen offers as keys instead.
func resultNext(next []envelope.Next) []envelope.Next {
	var out []envelope.Next
	for _, n := range next {
		if n.Action != setup.ActionDone {
			out = append(out, n)
		}
	}
	return out
}
