package tui

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
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
	result   setup.DatabaseResult
	removed  bool
	reloaded bool
	err      error
}

// reloadCmd loads a database again from its manifest.
func (m Model) reloadCmd(id string) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.ReloadDatabase(ctx, id, false)
		if err != nil {
			return databaseResultMsg{err: err, reloaded: true}
		}
		var result setup.DatabaseResult
		err = json.Unmarshal(body, &result)
		return databaseResultMsg{result: result, reloaded: true, err: err}
	}
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

type contextSetMsg struct {
	document dbcontext.Document
	next     []envelope.Next
	err      error
}

// useInProject makes id the database for the project the TUI was started
// in (capability 13, REQ:select-database-in-tui-and-web): the Git working
// tree, or the starting directory outside Git.
func (m Model) useInProject(id string) (Model, tea.Cmd) {
	local, ctx := m.local, m.ctx
	m.busy = &busyState{label: uicopy.T("context.saving", nil)}
	return m, tea.Batch(func() tea.Msg {
		change := dbcontext.Change{Scope: dbcontext.ScopeProject, Dir: local.Where.Root, Database: id, Path: "/"}
		body, err := local.SetContext(ctx, change, false)
		if err != nil {
			return contextSetMsg{err: err}
		}
		var document dbcontext.Document
		err = json.Unmarshal(body, &document)
		next := []envelope.Next{
			{Label: uicopy.T("home.menu.browse", nil), Command: "ovdb list /"},
			{Label: uicopy.T("next.done", nil), Action: setup.ActionDone},
		}
		return contextSetMsg{document: document, next: next, err: err}
	}, tickCmd())
}
