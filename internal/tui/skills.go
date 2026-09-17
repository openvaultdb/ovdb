package tui

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// skillsScreen is AI agent skills (capabilities 20 and 21): both skills and
// the AI agents they are installed for, and the consent step
// (ai-agent-skills#REQ:explicit-consent-to-install) that shows a skill's
// purpose, each agent with its exact directory, and Install skill / Not now
// with neither preselected. Nothing is written until Install skill is
// chosen. Directories are this TUI's own (REQ:client-values-and-mismatch).
type skillsScreen struct {
	loaded   bool
	document skills.Document
	cursor   int
	// consent is the skill being offered; nil on the list.
	consent *skills.Skill
	// selected marks the chosen targets by harness id; only found agents
	// can be chosen.
	selected map[string]bool
	// back is where Not now and Esc return: the list, or the Result that
	// offered the skill.
	back string
	// result is the Result to return to.
	result resultScreen
}

type skillsLoadedMsg struct {
	document skills.Document
	offer    string // skill id whose consent step opens once loaded
	err      error
}

type skillInstalledMsg struct {
	document skills.InstallDocument
	err      error
}

// enterSkills opens AI agent skills.
func (m Model) enterSkills() (Model, tea.Cmd) {
	m.screen = ScreenSkills
	m.skills = skillsScreen{back: ScreenHome}
	return m, m.loadSkillsCmd("")
}

// offerSkill opens the consent step for skill id from the Result that
// offered it; Not now returns there.
func (m Model) offerSkill(id string) (Model, tea.Cmd) {
	result := m.result
	m.screen = ScreenSkills
	m.skills = skillsScreen{back: ScreenResult, result: result}
	return m, m.loadSkillsCmd(id)
}

func (m Model) loadSkillsCmd(offer string) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		body, err := local.Skills(ctx)
		if err != nil {
			return skillsLoadedMsg{err: err}
		}
		var document skills.Document
		err = json.Unmarshal(body, &document)
		return skillsLoadedMsg{document: document, offer: offer, err: err}
	}
}

func (m Model) installSkillCmd(id string, harnesses []string) tea.Cmd {
	local, ctx := m.local, m.ctx
	return func() tea.Msg {
		plan, err := local.PlanSkill(skills.InstallRequest{Skill: id, Harnesses: harnesses})
		if err != nil {
			return skillInstalledMsg{err: err}
		}
		body, err := local.InstallSkill(ctx, plan, false)
		if err != nil {
			return skillInstalledMsg{err: err}
		}
		var document skills.InstallDocument
		err = json.Unmarshal(body, &document)
		return skillInstalledMsg{document: document, err: err}
	}
}

func (m Model) updateSkillsMsg(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case skillsLoadedMsg:
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.skills.loaded, m.skills.document = true, msg.document
		if msg.offer != "" {
			m.skills.openConsent(msg.offer)
		}
	case skillInstalledMsg:
		m.busy = nil
		m.pullNotices()
		if msg.err != nil {
			m.problem.setError(asProblem(msg.err))
			m.screen = ScreenProblem
			return m, nil
		}
		m.result = newSkillResult(msg.document)
		m.screen = ScreenResult
	}
	return m, nil
}

// openConsent starts the consent step for skill id, with the found agents
// chosen and the cursor on the first agent, not on Install skill.
func (s *skillsScreen) openConsent(id string) {
	for i := range s.document.Skills {
		if s.document.Skills[i].ID != id {
			continue
		}
		skill := s.document.Skills[i]
		s.consent, s.cursor, s.selected = &skill, 0, map[string]bool{}
		for _, target := range skill.Targets {
			s.selected[target.Harness] = target.Detected
		}
	}
}

// consentItems are the consent step's cursor stops: each found agent, then
// Install skill and Not now.
func (s skillsScreen) consentItems() []string {
	var items []string
	for _, target := range s.consent.Targets {
		if target.Detected {
			items = append(items, target.Harness)
		}
	}
	return append(items, itemInstall, itemNotNow)
}

const (
	itemInstall = "\x00install"
	itemNotNow  = "\x00not-now"
)

func (s skillsScreen) chosen() []string {
	var harnesses []string
	for _, target := range s.consent.Targets {
		if target.Detected && s.selected[target.Harness] {
			harnesses = append(harnesses, target.Harness)
		}
	}
	return harnesses
}

func (m Model) updateSkills(key string) (tea.Model, tea.Cmd) {
	if !m.skills.loaded {
		if key == "esc" || key == "backspace" {
			return m.skillsBack()
		}
		return m, nil
	}
	if m.skills.consent == nil {
		switch key {
		case "up", "k":
			m.skills.cursor = max(m.skills.cursor-1, 0)
		case "down", "j":
			m.skills.cursor = min(m.skills.cursor+1, len(m.skills.document.Skills)-1)
		case "enter":
			m.skills.openConsent(m.skills.document.Skills[m.skills.cursor].ID)
		case "esc", "backspace":
			return m.skillsBack()
		}
		return m, nil
	}
	items := m.skills.consentItems()
	switch key {
	case "up", "k", "shift+tab":
		m.skills.cursor = max(m.skills.cursor-1, 0)
	case "down", "j", "tab":
		m.skills.cursor = min(m.skills.cursor+1, len(items)-1)
	case "space", " ", "enter":
		switch item := items[m.skills.cursor]; item {
		case itemInstall:
			if key != "enter" {
				return m, nil
			}
			harnesses := m.skills.chosen()
			if len(harnesses) == 0 {
				return m, nil
			}
			m.busy = &busyState{label: uicopy.T("skills.installing", map[string]string{"name": m.skills.consent.Name})}
			return m, tea.Batch(m.installSkillCmd(m.skills.consent.ID, harnesses), tickCmd())
		case itemNotNow:
			if key != "enter" {
				return m, nil
			}
			return m.skillsBack()
		default:
			m.skills.selected[item] = !m.skills.selected[item]
		}
	case "esc", "backspace":
		return m.skillsBack()
	}
	return m, nil
}

// skillsBack leaves the consent step for where it was opened from, or the
// list for Home.
func (m Model) skillsBack() (tea.Model, tea.Cmd) {
	switch {
	case m.skills.consent != nil && m.skills.back == ScreenResult:
		m.result = m.skills.result
		m.screen = ScreenResult
		return m, nil
	case m.skills.consent != nil:
		m.skills.consent = nil
		m.skills.cursor = 0
		return m, nil
	}
	return m.backHome()
}

// newSkillResult is "Installed the TODO AI skill" (or already up to date)
// with where it went and what to try.
func newSkillResult(document skills.InstallDocument) resultScreen {
	title := "skills.installed.title"
	if document.AlreadyUpToDate {
		title = "skills.up_to_date.title"
	}
	var lines []string
	for _, outcome := range document.Outcomes {
		lines = append(lines, uicopy.T("skills.result.line", map[string]string{"name": outcome.Name, "path": outcome.Dir}))
	}
	return resultScreen{title: uicopy.T(title, map[string]string{"name": document.Name}), lines: lines, next: document.Next}
}

func (m Model) viewSkills() string {
	width := m.width
	var b strings.Builder
	if !m.skills.loaded {
		b.WriteString(titleStyle.Render(uicopy.T("skills.title", nil)))
		b.WriteString("\n\n")
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	if m.skills.consent != nil {
		return m.viewConsent()
	}
	b.WriteString(titleStyle.Render(uicopy.T("skills.title", nil)))
	b.WriteString("\n\n")
	b.WriteString(wordWrap(uicopy.T("skills.intro", nil), width))
	b.WriteString("\n\n")
	for i, skill := range m.skills.document.Skills {
		cursor, style := "  ", itemStyle
		if i == m.skills.cursor {
			cursor, style = "> ", selectedItemStyle
		}
		b.WriteString(style.Render(cursor + skill.Name))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(indentWrap("    ", skill.Purpose, width)))
		b.WriteString("\n")
		installed := uicopy.T("skills.list.not_installed", nil)
		var names, updates []string
		for _, target := range skill.Targets {
			switch target.State {
			case skills.StateInstalled:
				names = append(names, target.Name)
			case skills.StateNotInstalled:
			default:
				updates = append(updates, target.Name+" ("+uicopy.T("skills.state."+target.State, nil)+")")
			}
		}
		if all := append(names, updates...); len(all) > 0 {
			installed = uicopy.T("skills.list.installed_for", map[string]string{"agents": strings.Join(all, ", ")})
		}
		b.WriteString(mutedStyle.Render(indentWrap("    ", installed, width)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) viewConsent() string {
	width, s := m.width, m.skills
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("skills.consent.question", map[string]string{"name": s.consent.Name})))
	b.WriteString("\n\n")
	b.WriteString(wordWrap(s.consent.Purpose, width))
	b.WriteString("\n")
	b.WriteString(wordWrap(s.consent.Example, width))
	b.WriteString("\n\n")
	b.WriteString(uicopy.T("skills.consent.install_for", nil))
	b.WriteString("\n")
	items := s.consentItems()
	for _, target := range s.consent.Targets {
		if !target.Detected {
			b.WriteString(mutedStyle.Render(hangingWrap("      ", target.Name+" — "+uicopy.T("skills.state.not_found", nil), width)))
			b.WriteString("\n")
			continue
		}
		cursor, style := "  ", itemStyle
		if items[s.cursor] == target.Harness {
			cursor, style = "> ", selectedItemStyle
		}
		box := "[ ] "
		if s.selected[target.Harness] {
			box = "[x] "
		}
		b.WriteString(style.Render(cursor + box + target.Name))
		b.WriteString("\n")
		b.WriteString(indentWrap("      ", target.Dir, width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for _, item := range []string{itemInstall, itemNotNow} {
		label := uicopy.T("skills.consent.install", nil)
		if item == itemNotNow {
			label = uicopy.T("skills.consent.not_now", nil)
		}
		cursor, style := "  ", itemStyle
		if items[s.cursor] == item {
			cursor, style = "> ", selectedItemStyle
		}
		if item == itemInstall && len(s.chosen()) == 0 {
			style = mutedStyle
		}
		b.WriteString(style.Render(cursor + label))
		b.WriteString("\n")
	}
	return b.String()
}
