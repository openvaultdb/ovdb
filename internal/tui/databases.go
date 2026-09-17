package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// Databases screen views.
const (
	databasesList    = "list"
	databasesDetails = "details"
	databasesConfirm = "confirm"
)

// Actions on a database's details view, in order.
const (
	detailBrowse = iota
	detailUse
	detailReload
	detailRemove
	detailBack
)

// databasesScreen is "Databases" (capability rows 11, 12 and reload): the
// registered databases with storage, location and state as the server (or,
// without one, the registry files) reports them. Enter opens a database's
// details, where Reload and Remove live; Remove asks first and says where
// the data stays (database-setup-and-providers#REQ:list-and-remove).
type databasesScreen struct {
	loaded   bool
	document setup.DatabasesDocument
	cursor   int // row in the list; len(databases) is "Create a database"
	view     string
	action   int  // selected action in details
	keep     bool // in the confirmation, "Keep it" is selected
}

func (m Model) enterDatabases() (Model, tea.Cmd) {
	m.screen = ScreenDatabases
	m.databases = databasesScreen{view: databasesList}
	return m, m.loadDatabasesCmd()
}

func (m Model) updateDatabases(key string) (tea.Model, tea.Cmd) {
	list := m.databases.document.Databases
	switch m.databases.view {
	case databasesConfirm:
		switch key {
		case "up", "down", "k", "j", "tab":
			m.databases.keep = !m.databases.keep
		case "esc", "backspace", "n":
			m.databases.view = databasesDetails
		case "y":
			return m.removeSelected()
		case "enter":
			if m.databases.keep {
				m.databases.view = databasesDetails
				return m, nil
			}
			return m.removeSelected()
		}
		return m, nil
	case databasesDetails:
		switch key {
		case "up", "k":
			if m.databases.action > detailBrowse {
				m.databases.action--
			}
		case "down", "j":
			if m.databases.action < detailBack {
				m.databases.action++
			}
		case "esc", "backspace":
			m.databases.view = databasesList
		case "enter":
			switch m.databases.action {
			case detailBrowse:
				if db, ok := m.selectedDatabase(); ok {
					return m.browseDatabase(db.ID)
				}
			case detailUse:
				if db, ok := m.selectedDatabase(); ok {
					return m.useInProject(db.ID)
				}
			case detailReload:
				return m.reloadSelected()
			case detailRemove:
				m.databases.view = databasesConfirm
				m.databases.keep = true
			default:
				m.databases.view = databasesList
			}
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
		m.databases.view = databasesDetails
		m.databases.action = detailBrowse
	case "esc", "backspace":
		return m.backHome()
	}
	return m, nil
}

func (m Model) selectedDatabase() (setup.Database, bool) {
	list := m.databases.document.Databases
	if m.databases.cursor >= len(list) {
		return setup.Database{}, false
	}
	return list[m.databases.cursor], true
}

func (m Model) removeSelected() (tea.Model, tea.Cmd) {
	db, ok := m.selectedDatabase()
	if !ok {
		return m, nil
	}
	m.databases.view = databasesList
	m.busy = &busyState{label: uicopy.T("databases.removing", nil)}
	return m, tea.Batch(m.removeCmd(db.ID), tickCmd())
}

func (m Model) reloadSelected() (tea.Model, tea.Cmd) {
	db, ok := m.selectedDatabase()
	if !ok {
		return m, nil
	}
	m.databases.view = databasesList
	m.busy = &busyState{label: uicopy.T("databases.reloading", nil)}
	return m, tea.Batch(m.reloadCmd(db.ID), tickCmd())
}

// bodyHeight is how many lines a screen body may use: the window minus the
// header, the footer and any notices.
func (m Model) bodyHeight() int {
	if m.height <= 0 {
		return 1 << 30
	}
	// Header and footer, plus the line break a body's last line ends with.
	return max(m.height-3-len(m.noticeLines), 3)
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
	if db, ok := m.selectedDatabase(); ok && m.databases.view != databasesList {
		b.WriteString(m.viewDatabaseDetails(db))
		return b.String()
	}
	list := m.databases.document.Databases
	if len(list) == 0 {
		b.WriteString(wordWrap(uicopy.T("databases.empty", nil), width))
		b.WriteString("\n\n")
	}

	// Each database is two or three lines; the list scrolls so the selected
	// row (and "Create a database" at the end) stays in view.
	var rows [][]string
	for i, db := range list {
		cursor, style := "  ", itemStyle
		if i == m.databases.cursor {
			cursor, style = "> ", selectedItemStyle
		}
		row := []string{style.Render(truncateEnd(cursor+db.ID+"  ["+stateLabel(db.State)+"]", width))}
		details := engineName(db.Engine)
		if db.Location != "" {
			details += " · " + db.Location
		}
		row = append(row, mutedStyle.Render("    "+truncateStart(details, width-4)))
		if db.Reason != "" {
			row = append(row, "    "+truncateEnd(uicopy.T("problem.why", map[string]string{"reason": db.Reason}), width-4))
		}
		rows = append(rows, row)
	}
	cursor, style := "  ", itemStyle
	if m.databases.cursor == len(list) {
		cursor, style = "> ", selectedItemStyle
	}
	rows = append(rows, []string{style.Render(cursor + uicopy.T("home.menu.create_database", nil))})

	available := m.bodyHeight() - 2 // title and blank line
	first := scrollStart(rows, m.databases.cursor, available)
	used := 0
	if first > 0 {
		b.WriteString(mutedStyle.Render("  ↑"))
		b.WriteString("\n")
		used++
	}
	for i := first; i < len(rows); i++ {
		room := available - used
		if i < len(rows)-1 {
			room-- // keep a line for the "more below" marker
		}
		if len(rows[i]) > room {
			b.WriteString(mutedStyle.Render("  ↓"))
			b.WriteString("\n")
			break
		}
		for _, line := range rows[i] {
			b.WriteString(line)
			b.WriteString("\n")
		}
		used += len(rows[i])
	}
	return b.String()
}

// scrollStart is the first row to draw so that row cursor fits in height
// lines, leaving a line for each scroll marker.
func scrollStart(rows [][]string, cursor, height int) int {
	first := 0
	for {
		lines := 0
		if first > 0 {
			lines++
		}
		for i := first; i <= cursor && i < len(rows); i++ {
			lines += len(rows[i])
		}
		if cursor < len(rows)-1 {
			lines++
		}
		if lines <= height || first >= cursor {
			return first
		}
		first++
	}
}

func (m Model) viewDatabaseDetails(db setup.Database) string {
	width := m.width
	var b strings.Builder
	if m.databases.view == databasesConfirm {
		location := db.Location
		if location == "" {
			location = uicopy.T("database.removed.data_kept_elsewhere", nil)
		}
		b.WriteString(errorStyle.Render(wordWrap(uicopy.T("database.remove.confirm_title", map[string]string{"name": db.ID}), width)))
		b.WriteString("\n\n")
		b.WriteString(wordWrap(uicopy.T("database.remove.confirm_body", map[string]string{"location": location}), width))
		b.WriteString("\n\n")
		b.WriteString(choice(m.databases.keep, uicopy.T("database.remove.cancel", nil)))
		b.WriteString(choice(!m.databases.keep, uicopy.T("database.remove.button", nil)))
		return b.String()
	}
	b.WriteString(itemStyle.Render(wordWrap(db.ID+"  ["+stateLabel(db.State)+"]", width)))
	b.WriteString("\n")
	detail := func(labelKey, value string) {
		if value != "" {
			b.WriteString(mutedStyle.Render(uicopy.T(labelKey, nil) + ":"))
			b.WriteString("\n")
			b.WriteString(indentWrap("  ", value, width))
			b.WriteString("\n")
		}
	}
	detail("databases.detail.engine", engineName(db.Engine))
	detail("databases.detail.location", db.Location)
	detail("databases.detail.manifest", db.Manifest)
	if db.Reason != "" {
		b.WriteString(wordWrap(uicopy.T("problem.why", map[string]string{"reason": db.Reason}), width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(choice(m.databases.action == detailBrowse, uicopy.T("home.menu.browse", nil)))
	b.WriteString(choice(m.databases.action == detailUse, uicopy.T("next.use_in_project", nil)))
	b.WriteString(choice(m.databases.action == detailReload, uicopy.T("next.reload_database", nil)))
	b.WriteString(choice(m.databases.action == detailRemove, uicopy.T("database.remove.button", nil)))
	b.WriteString(choice(m.databases.action == detailBack, uicopy.T("databases.back", nil)))
	return b.String()
}

func choice(selected bool, label string) string {
	if selected {
		return selectedItemStyle.Render("> "+label) + "\n"
	}
	return itemStyle.Render("  "+label) + "\n"
}

// truncateEnd cuts s to width runes, ending with "…".
func truncateEnd(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	return string(r[:max(width-1, 0)]) + "…"
}

// truncateStart cuts s to width runes, keeping its end (a path's name).
func truncateStart(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	return "…" + string(r[len(r)-max(width-1, 0):])
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
	case setup.MountMounting:
		return uicopy.T("databases.state.mounting", nil)
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

// newReloadedResult is the Result after reloading a database.
func newReloadedResult(result setup.DatabaseResult) resultScreen {
	db := result.Database
	lines := []string{stateLabel(db.State)}
	if db.Reason != "" {
		lines = append(lines, uicopy.T("problem.why", map[string]string{"reason": db.Reason}))
	}
	return resultScreen{
		title: uicopy.T("database.reloaded.title", map[string]string{"name": db.ID}),
		lines: lines,
		next:  result.Next,
	}
}
