package tui

import (
	"strconv"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// settingsScreen is capability row 7 (change server port); telemetry joins
// it in a later increment.
type settingsScreen struct {
	loaded   bool
	document setup.ConfigDocument
	editing  bool
	input    string
	invalid  bool
	// savedMessage is the confirmation shown after a save (config.saved or
	// config.unchanged, matching the web console's own wording); cleared
	// when editing starts again.
	savedMessage string
}

func (s settingsScreen) currentPort() int {
	if port := s.document.Config.Server.Port; port != 0 {
		return port
	}
	return runtime.DefaultPort
}

func (m Model) viewSettings() string {
	width := m.width
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("settings.title", nil)))
	b.WriteString("\n\n")
	if !m.settings.loaded {
		b.WriteString(uicopy.T("console.loading", nil))
		return b.String()
	}
	if m.settings.editing {
		b.WriteString(selectedItemStyle.Render(uicopy.T("settings.port.input", map[string]string{"value": m.settings.input})))
		if m.settings.invalid {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render(uicopy.T("settings.port.invalid", nil)))
		}
		return b.String()
	}
	b.WriteString(itemStyle.Render(uicopy.T("settings.port.label", nil)))
	b.WriteString("\n")
	port := strconv.Itoa(m.settings.currentPort())
	b.WriteString(mutedStyle.Render(wordWrap(uicopy.T("settings.port.help", map[string]string{"port": port}), width)))
	if m.settings.savedMessage != "" {
		b.WriteString("\n\n")
		b.WriteString(wordWrap(m.settings.savedMessage, width))
	}
	return b.String()
}
