package tui

import "charm.land/lipgloss/v2"

var (
	colorPrimary = lipgloss.Color("#7C3AED")
	colorMuted   = lipgloss.Color("#6B7280")
	colorText    = lipgloss.Color("#F9FAFB")
	colorAccent  = lipgloss.Color("#A78BFA")
	colorError   = lipgloss.Color("#F87171")
	colorWhite   = lipgloss.Color("#FFFFFF")
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(colorPrimary).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary)

	itemStyle = lipgloss.NewStyle().
			Foreground(colorText)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorError)
)
