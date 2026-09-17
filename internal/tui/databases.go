package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// databasesScreen is "Databases" (capability rows 11 and 12): the registered
// databases with storage, location and state as the server (or, without
// one, the registry files) reports them, and Remove after a confirmation
// that says where the data stays
// (database-setup-and-providers#REQ:list-and-remove).
type databasesScreen struct {
	loaded    bool
	document  setup.DatabasesDocument
	cursor    int
	confirm   bool // asking whether to remove the selected database
	keepFocus bool // in the confirmation, "Keep it" is selected
}

func (m Model) enterDatabases() (Model, tea.Cmd) {
	m.screen = ScreenDatabases
	m.databases = databasesScreen{}
	return m, m.loadDatabasesCmd()
}

func (m Model) updateDatabases(key string) (tea.Model, tea.Cmd) {
	list := m.databases.document.Databases
	if m.databases.confirm {
		switch key {
		case "up", "down", "k", "j", "tab":
			m.databases.keepFocus = !m.databases.keepFocus
		case "esc", "backspace", "n":
			m.databases.confirm = false
		case "y":
			m.databases.keepFocus = false
			return m.removeSelected()
		case "enter":
			if m.databases.keepFocus {
				m.databases.confirm = false
				return m, nil
			}
			return m.removeSelected()
		}
		return m, nil
	}
	switch key {
	case "up", "k":
		if m.databases.cursor > 0 {
			m.databases.cursor--
		}
	case "down", "j":
		if m.databases.cursor < len(list) {
			m.databases.cursor++
		}
	case "enter":
		if !m.databases.loaded {
			return m, nil
		}
		if m.databases.cursor == len(list) {
			return m.enterCreate()
		}
		m.databases.confirm = true
		m.databases.keepFocus = true
	case "esc", "backspace":
		return m.backHome()
	}
	return m, nil
}

func (m Model) removeSelected() (tea.Model, tea.Cmd) {
	list := m.databases.document.Databases
	if m.databases.cursor >= len(list) {
		return m, nil
	}
	m.databases.confirm = false
	m.busy = &busyState{label: uicopy.T("databases.removing", nil)}
	return m, tea.Batch(m.removeCmd(list[m.databases.cursor].ID), tickCmd())
}

func (m Model) viewDatabases() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("databases.title", nil)))
	b.WriteString("\n\n")
	if !m.databases.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	list := m.databases.document.Databases
	if m.databases.confirm && m.databases.cursor < len(list) {
		db := list[m.databases.cursor]
		location := db.Location
		if location == "" {
			location = uicopy.T("database.removed.data_kept_elsewhere", nil)
		}
		b.WriteString(errorStyle.Render(wordWrap(uicopy.T("database.remove.confirm_title", map[string]string{"name": db.ID}), width)))
		b.WriteString("\n\n")
		b.WriteString(wordWrap(uicopy.T("database.remove.confirm_body", map[string]string{"location": location}), width))
		b.WriteString("\n\n")
		keep, remove := "  ", "> "
		keepStyle, removeStyle := itemStyle, selectedItemStyle
		if m.databases.keepFocus {
			keep, remove = "> ", "  "
			keepStyle, removeStyle = selectedItemStyle, itemStyle
		}
		b.WriteString(keepStyle.Render(keep + uicopy.T("database.remove.cancel", nil)))
		b.WriteString("\n")
		b.WriteString(removeStyle.Render(remove + uicopy.T("database.remove.button", nil)))
		return b.String()
	}
	if len(list) == 0 {
		b.WriteString(wordWrap(uicopy.T("databases.empty", nil), width))
		b.WriteString("\n\n")
	}
	for i, db := range list {
		cursor, style := "  ", itemStyle
		if i == m.databases.cursor {
			cursor, style = "> ", selectedItemStyle
		}
		b.WriteString(style.Render(wordWrap(cursor+db.ID+"  ["+stateLabel(db.State)+"]", width)))
		b.WriteString("\n")
		details := engineName(db.Engine)
		if db.Location != "" {
			details += " · " + db.Location
		}
		b.WriteString(mutedStyle.Render(indentWrap("    ", details, width)))
		b.WriteString("\n")
		if db.Reason != "" {
			b.WriteString(indentWrap("    ", uicopy.T("problem.why", map[string]string{"reason": db.Reason}), width))
			b.WriteString("\n")
		}
	}
	cursor, style := "  ", itemStyle
	if m.databases.cursor == len(list) {
		cursor, style = "> ", selectedItemStyle
	}
	b.WriteString(style.Render(cursor + uicopy.T("home.menu.create_database", nil)))
	b.WriteString("\n")
	return b.String()
}

func engineName(id string) string {
	if engine, ok := setup.FindEngine(id); ok {
		return engine.Name
	}
	return id
}

func stateLabel(state string) string {
	switch state {
	case setup.MountMounted:
		return uicopy.T("databases.state.mounted", nil)
	case setup.MountNeedsAttention:
		return uicopy.T("databases.state.needs_attention", nil)
	default:
		return uicopy.T("databases.state.unknown", nil)
	}
}

// newRemovedResult is the Result after removing a database.
func newRemovedResult(result setup.DatabaseResult) resultScreen {
	db := result.Database
	kept := uicopy.T("database.removed.data_kept_elsewhere", nil)
	if db.Location != "" {
		kept = uicopy.T("database.removed.data_kept", map[string]string{"location": db.Location})
	}
	return resultScreen{
		title: uicopy.T("database.removed.title", map[string]string{"name": db.ID}),
		lines: []string{kept},
		next:  result.Next,
	}
}
