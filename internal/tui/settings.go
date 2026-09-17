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
}

func (s settingsScreen) portLine() string {
	port := s.document.Config.Server.Port
	if port == 0 {
		return uicopy.T("settings.port.label_default", map[string]string{"value": strconv.Itoa(runtime.DefaultPort)})
	}
	return uicopy.T("settings.port.label", map[string]string{"value": strconv.Itoa(port)})
}

func (m Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(uicopy.T("settings.title", nil)))
	b.WriteString("\n\n")
	if !m.settings.loaded {
		b.WriteString(uicopy.T("home.loading", nil))
		return b.String()
	}
	switch {
	case m.settings.editing:
		b.WriteString(selectedItemStyle.Render(uicopy.T("settings.port.input", map[string]string{"value": m.settings.input})))
		if m.settings.invalid {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render(uicopy.T("settings.port.invalid", nil)))
		}
	default:
		b.WriteString(itemStyle.Render(m.settings.portLine()))
	}
	return b.String()
}
