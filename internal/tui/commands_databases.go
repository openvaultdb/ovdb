package tui

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"github.com/openvaultdb/ovdb/internal/setup"
)

type enginesLoadedMsg struct {
	document setup.EnginesDocument
	err      error
}

// loadEnginesCmd reads the storage catalogue (a pure read: it starts
// nothing).
func (m Model) loadEnginesCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Engines(ctx)
		if err != nil {
			return enginesLoadedMsg{err: err}
		}
		var document setup.EnginesDocument
		err = json.Unmarshal(body, &document)
		return enginesLoadedMsg{document: document, err: err}
	}
}

type databaseResultMsg struct {
	result  setup.DatabaseResult
	removed bool
	err     error
}

// createCmd creates a database through the server, starting it when needed
// (REQ:auto-start); the start notice reaches the screen through Notices.
func (m Model) createCmd(request setup.CreateRequest) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.CreateDatabase(ctx, request, false)
		if err != nil {
			return databaseResultMsg{err: err}
		}
		var result setup.DatabaseResult
		err = json.Unmarshal(body, &result)
		return databaseResultMsg{result: result, err: err}
	}
}

func (m Model) removeCmd(id string) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.RemoveDatabase(ctx, id, false)
		if err != nil {
			return databaseResultMsg{err: err, removed: true}
		}
		var result setup.DatabaseResult
		err = json.Unmarshal(body, &result)
		return databaseResultMsg{result: result, removed: true, err: err}
	}
}

type databasesLoadedMsg struct {
	document setup.DatabasesDocument
	err      error
}

// loadDatabasesCmd lists databases: from the running server, or from the
// registry with state "unknown" when none runs.
func (m Model) loadDatabasesCmd() tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Databases(ctx)
		if err != nil {
			return databasesLoadedMsg{err: err}
		}
		var document setup.DatabasesDocument
		err = json.Unmarshal(body, &document)
		return databasesLoadedMsg{document: document, err: err}
	}
}
