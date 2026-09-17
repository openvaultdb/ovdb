package tui

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
)

// Browse views.
const (
	browseDatabases = "databases" // choose a database
	browseList      = "list"      // collections at the root, or a collection's records
	browseRecord    = "record"    // one record as formatted JSON
)

// browsePage is how many records a page shows.
const browsePage = 50

// browseScreen is Browse data (capability 15,
// database-context-navigation#REQ:browse-data-read-only): collections →
// records, 50 at a time → one record as formatted JSON, through the same
// data API the CLI uses. It is read-only (parity E5) and has no cd (E4):
// the path is where the person has browsed to, shown with the equivalent
// `ovdb list` or `ovdb get` command.
type browseScreen struct {
	view      string
	loaded    bool
	databases []setup.Database
	currentDB string // the database that applies where the TUI started
	cursor    int

	db   string
	path datapath.Path
	// items are the current page's collections or records.
	items []browseItem
	page  int
	more  bool
	// record is the record's JSON lines; missing when nothing is there.
	record  []string
	missing bool
	scroll  int
	// typing a collection name under a record.
	typing  bool
	input   string
	invalid bool
}

type browseItem struct {
	path   datapath.Path
	detail string
}

func (m Model) enterBrowse() (Model, tea.Cmd) {
	m.screen = ScreenBrowse
	m.browse = browseScreen{view: browseDatabases}
	return m, m.loadBrowseDatabasesCmd()
}

// browseDatabase opens Browse data at a database's root.
func (m Model) browseDatabase(id string) (Model, tea.Cmd) {
	m.screen = ScreenBrowse
	m.browse = browseScreen{view: browseList, db: id}
	return m, m.loadBrowseCmd(id, datapath.Path{}, 0)
}

func (m Model) browseTo(path datapath.Path, page int) (Model, tea.Cmd) {
	m.browse.loaded = false
	m.browse.path, m.browse.page, m.browse.cursor, m.browse.scroll = path, page, 0, 0
	m.browse.typing, m.browse.input, m.browse.invalid = false, "", false
	if path.Kind() == datapath.Record {
		m.browse.view = browseRecord
	} else {
		m.browse.view = browseList
	}
	return m, m.loadBrowseCmd(m.browse.db, path, page)
}

type browseDatabasesMsg struct {
	databases []setup.Database
	current   string
	err       error
}

func (m Model) loadBrowseDatabasesCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Databases(ctx)
		if err != nil {
			return browseDatabasesMsg{err: err}
		}
		var document setup.DatabasesDocument
		if err := json.Unmarshal(body, &document); err != nil {
			return browseDatabasesMsg{err: err}
		}
		msg := browseDatabasesMsg{databases: document.Databases}
		if body, err := local.Context(ctx); err == nil {
			var context dbcontext.Document
			if json.Unmarshal(body, &context) == nil && context.Context != nil {
				msg.current = context.Context.Database
			}
		}
		return msg
	}
}

type browseLoadedMsg struct {
	path    datapath.Path
	items   []browseItem
	more    bool
	record  []string
	missing bool
	err     error
}

// loadBrowseCmd reads one page, starting the server when needed. A page asks
// for one record more than it shows, to know whether there is a next page.
func (m Model) loadBrowseCmd(db string, path datapath.Path, page int) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		op := client.DataOp{Verb: "list", Database: db, Path: path, Suffix: " --db " + db}
		msg := browseLoadedMsg{path: path}
		switch path.Kind() {
		case datapath.Root:
			body, err := local.Collections(ctx, op, false)
			if err != nil {
				return browseLoadedMsg{path: path, err: err}
			}
			var info client.DatabaseInfo
			if err := json.Unmarshal(body, &info); err != nil {
				return browseLoadedMsg{path: path, err: err}
			}
			for _, name := range info.Collections {
				msg.items = append(msg.items, browseItem{path: path.Child(name)})
			}
		case datapath.Collection:
			body, err := local.Query(ctx, op, (page+1)*browsePage+1, false)
			if err != nil {
				return browseLoadedMsg{path: path, err: err}
			}
			var records client.Records
			if err := json.Unmarshal(body, &records); err != nil {
				return browseLoadedMsg{path: path, err: err}
			}
			first := min(page*browsePage, len(records.Records))
			last := min(first+browsePage, len(records.Records))
			msg.more = len(records.Records) > last
			for _, record := range records.Records[first:last] {
				msg.items = append(msg.items, browseItem{path: path.Child(client.KeyID(record.Key)), detail: oneLineJSON(record.Data)})
			}
		default:
			op.Verb = "get"
			body, err := local.Get(ctx, op, false)
			if client.MissingRecord(err) {
				return browseLoadedMsg{path: path, missing: true}
			}
			if err != nil {
				return browseLoadedMsg{path: path, err: err}
			}
			var record client.Record
			if err := json.Unmarshal(body, &record); err != nil {
				return browseLoadedMsg{path: path, err: err}
			}
			msg.record = strings.Split(prettyJSON(record.Data), "\n")
		}
		return msg
	}
}

func encodeJSON(v map[string]any, indent string) string {
	if v == nil {
		v = map[string]any{}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	_ = encoder.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func oneLineJSON(v map[string]any) string { return encodeJSON(v, "") }

func prettyJSON(v map[string]any) string { return encodeJSON(v, "  ") }

func (m Model) updateBrowseLoaded(msg browseLoadedMsg) (tea.Model, tea.Cmd) {
	m.pullNotices()
	if msg.err != nil {
		m.problem.setError(asProblem(msg.err))
		m.screen = ScreenProblem
		return m, nil
	}
	if msg.path.String() != m.browse.path.String() {
		return m, nil // a page the person already left
	}
	m.browse.loaded = true
	m.browse.items, m.browse.more = msg.items, msg.more
	m.browse.record, m.browse.missing = msg.record, msg.missing
	return m, nil
}

func (m Model) updateBrowse(key string) (tea.Model, tea.Cmd) {
	b := &m.browse
	switch {
	case b.view == browseDatabases:
		switch key {
		case "up", "k":
			b.cursor = max(b.cursor-1, 0)
		case "down", "j":
			b.cursor = min(b.cursor+1, max(len(b.databases)-1, 0))
		case "enter":
			if b.loaded && b.cursor < len(b.databases) {
				return m.browseDatabase(b.databases[b.cursor].ID)
			}
		case "esc", "backspace":
			return m.backHome()
		}
	case b.typing:
		switch key {
		case "enter":
			next, err := datapath.Resolve(b.path, b.input)
			if err != nil || b.input == "" || strings.Contains(b.input, "/") || next.Kind() != datapath.Collection {
				b.invalid = true
				return m, nil
			}
			return m.browseTo(next, 0)
		case "esc":
			b.typing, b.input, b.invalid = false, "", false
		case "backspace":
			b.input, b.invalid = dropLast(b.input), false
		default:
			b.input += typed(key)
			b.invalid = false
		}
	case b.view == browseRecord:
		switch key {
		case "up", "k":
			b.scroll = max(b.scroll-1, 0)
		case "down", "j":
			b.scroll = min(b.scroll+1, max(len(b.record)-1, 0))
		case "c":
			b.typing = true
		case "esc", "backspace":
			return m.browseTo(b.path.Parent(), 0)
		}
	default:
		switch key {
		case "up", "k":
			b.cursor = max(b.cursor-1, 0)
		case "down", "j":
			b.cursor = min(b.cursor+1, max(len(b.items)-1, 0))
		case "n", "right", "pgdown":
			if b.loaded && b.more {
				return m.browseTo(b.path, b.page+1)
			}
		case "p", "left", "pgup":
			if b.loaded && b.page > 0 {
				return m.browseTo(b.path, b.page-1)
			}
		case "enter":
			if b.loaded && b.cursor < len(b.items) {
				return m.browseTo(b.items[b.cursor].path, 0)
			}
		case "esc", "backspace":
			if b.path.Kind() == datapath.Root {
				return m.enterBrowse()
			}
			return m.browseTo(b.path.Parent(), 0)
		}
	}
	return m, nil
}

// browseCommand is the CLI command showing the same thing.
func (b browseScreen) command() string {
	if b.path.Kind() == datapath.Record {
		return "ovdb get " + b.path.String() + " --db " + b.db
	}
	return "ovdb list " + b.path.String() + " --db " + b.db
}

func (m Model) viewBrowse() string {
	width := m.width
	b := m.browse
	var out strings.Builder
	out.WriteString(titleStyle.Render(uicopy.T("home.menu.browse", nil)))
	out.WriteString("\n")
	lines := []string{}
	if b.view == browseDatabases {
		out.WriteString("\n")
		if !b.loaded {
			return out.String() + uicopy.T("console.loading", nil)
		}
		if len(b.databases) == 0 {
			return out.String() + wordWrap(uicopy.T("databases.empty", nil), width)
		}
		out.WriteString(wordWrap(uicopy.T("browse.choose", nil), width))
		out.WriteString("\n\n")
		for i, db := range b.databases {
			label := db.ID
			if db.ID == m.browse.current() {
				label += "  (" + uicopy.T("browse.current", nil) + ")"
			}
			cursor, style := "  ", itemStyle
			if i == b.cursor {
				cursor, style = "> ", selectedItemStyle
			}
			lines = append(lines, style.Render(truncateEnd(cursor+label, width)))
		}
		return out.String() + strings.Join(window(lines, b.cursor, m.bodyHeight()-4), "\n")
	}

	out.WriteString(itemStyle.Render(truncateStart(b.db+":"+b.path.String(), width)))
	out.WriteString("\n")
	out.WriteString(mutedStyle.Render(truncateStart(b.command(), width)))
	out.WriteString("\n\n")
	if !b.loaded {
		return out.String() + uicopy.T("console.loading", nil)
	}
	available := m.bodyHeight() - 5
	if b.view == browseRecord {
		switch {
		case b.missing:
			out.WriteString(uicopy.T("data.nothing_here", nil))
			out.WriteString("\n")
		default:
			end := min(b.scroll+max(available-2, 1), len(b.record))
			for _, line := range b.record[b.scroll:end] {
				out.WriteString(truncateEnd(line, width))
				out.WriteString("\n")
			}
		}
		out.WriteString("\n")
		if b.typing {
			out.WriteString(truncateStart(uicopy.T("browse.collection_input", map[string]string{"value": b.input + "_"}), width))
			if b.invalid {
				out.WriteString("\n")
				out.WriteString(errorStyle.Render(wordWrap(uicopy.T("browse.collection_invalid", nil), width)))
			}
		}
		return out.String()
	}
	if len(b.items) == 0 {
		return out.String() + uicopy.T("data.nothing_here", nil)
	}
	for i, item := range b.items {
		cursor, style := "  ", itemStyle
		if i == b.cursor {
			cursor, style = "> ", selectedItemStyle
		}
		label := itemLabel(item.path)
		line := style.Render(truncateEnd(cursor+label, width))
		if item.detail != "" && width-len([]rune(cursor+label))-2 > 8 {
			line = style.Render(cursor+label) + "  " + mutedStyle.Render(truncateEnd(item.detail, width-len([]rune(cursor+label))-2))
		}
		lines = append(lines, line)
	}
	out.WriteString(strings.Join(window(lines, b.cursor, available-1), "\n"))
	out.WriteString("\n")
	if b.path.Kind() == datapath.Collection {
		first := b.page*browsePage + 1
		params := map[string]string{"first": strconv.Itoa(first), "last": strconv.Itoa(first + len(b.items) - 1)}
		status := uicopy.T("browse.page", params)
		if b.page > 0 {
			status += " · " + uicopy.T("browse.page_previous", nil)
		}
		if b.more {
			status += " · " + uicopy.T("browse.page_next", nil)
		}
		out.WriteString(mutedStyle.Render(truncateEnd(status, width)))
	}
	return out.String()
}

// itemLabel is how a list shows an item: the collection name at the root, the
// escaped id in a collection.
func itemLabel(path datapath.Path) string {
	segments := strings.Split(path.String(), "/")
	return segments[len(segments)-1]
}

func (b browseScreen) current() string { return b.currentDB }

// window is the part of lines that fits in height with the cursor's line
// in view, with a marker where lines are cut.
func window(lines []string, cursor, height int) []string {
	height = max(height, 3)
	if len(lines) <= height {
		return lines
	}
	rows := make([][]string, len(lines))
	for i, line := range lines {
		rows[i] = []string{line}
	}
	first := scrollStart(rows, cursor, height)
	var shown []string
	if first > 0 {
		shown = append(shown, mutedStyle.Render("  ↑"))
	}
	for i := first; i < len(lines); i++ {
		room := height - len(shown)
		if i < len(lines)-1 {
			room--
		}
		if room <= 0 {
			shown = append(shown, mutedStyle.Render("  ↓"))
			break
		}
		shown = append(shown, lines[i])
	}
	return shown
}

func (m Model) browseFooter() string {
	switch {
	case m.browse.typing:
		return uicopy.T("browse.hint.typing", nil)
	case m.browse.view == browseRecord:
		return uicopy.T("browse.hint.record", nil)
	case m.browse.view == browseList:
		return uicopy.T("browse.hint.list", nil)
	default:
		return uicopy.T("tui.footer.back", nil)
	}
}
