package tui

import (
	"strconv"
	"strings"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// wordWrap breaks text into lines no wider than width, breaking only at
// spaces, so a screen never emits a line the terminal must truncate. width
// <= 0 disables wrapping (used where the caller has not resolved a size
// yet).
func wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var out strings.Builder
	for lineIdx, paragraph := range strings.Split(text, "\n") {
		if lineIdx > 0 {
			out.WriteByte('\n')
		}
		words := breakLongWords(strings.Fields(paragraph), width)
		lineLen := 0
		for i, w := range words {
			wl := len([]rune(w))
			switch {
			case i == 0:
				// first word of this paragraph
			case lineLen+1+wl > width:
				out.WriteByte('\n')
				lineLen = 0
			default:
				out.WriteByte(' ')
				lineLen++
			}
			out.WriteString(w)
			lineLen += wl
		}
	}
	return out.String()
}

// breakLongWords splits any word wider than width (a long path or URL) into
// pieces that fit, so wrapping never leaves a line wider than the window.
func breakLongWords(words []string, width int) []string {
	var out []string
	for _, word := range words {
		runes := []rune(word)
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		out = append(out, string(runes))
	}
	return out
}

// truncateVisual cuts s to at most width runes for display only, marking a
// cut with a trailing ellipsis. Unlike wordWrap, it never inserts a hard
// line break inside a command: a break with no shell continuation corrupts
// it when pasted (review-inc-7.md F4). Callers that show a command this way
// must offer the untouched text some other way (a copy action, --json).
func truncateVisual(s string, width int) string {
	if width <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

// hangingWrap is indentWrap with prefix (a bullet or cursor) on the first
// line only; later lines are indented to line up under the text.
func hangingWrap(prefix, text string, width int) string {
	wrapped := indentWrap(prefix, text, width)
	indent := strings.Repeat(" ", len([]rune(prefix)))
	lines := strings.Split(wrapped, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + strings.TrimPrefix(lines[i], prefix)
	}
	return strings.Join(lines, "\n")
}

// indentWrap wraps text to width, prefixing every resulting line with
// prefix — unlike wordWrap(prefix+text, width), which folds prefix's spaces
// into strings.Fields' word splitting and loses the indent entirely.
func indentWrap(prefix, text string, width int) string {
	inner := width
	if inner > 0 {
		inner -= len([]rune(prefix))
	}
	wrapped := wordWrap(text, inner)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// nextLines renders a list of envelope.Next entries as "problem.what_you_can_do"
// bullets, one or two terminal lines per entry: label and command share a
// line when they fit within width, otherwise the command goes on its own
// indented line so neither is ever cut off. actionable marks the indexes
// selectable by the caller (Problem screen's port remedy); those get a
// "> "/"  " cursor instead of a bullet, and cursorAt names which of them is
// currently selected.
func nextLines(width int, next []envelope.Next, actionable []int, cursorAt int) []string {
	var lines []string
	// Commands line up when their labels fit beside them.
	labelWidth := 0
	for _, n := range next {
		if n.Command != "" && (width <= 0 || 4+len([]rune(n.Label))+3+len([]rune(n.Command)) <= width) {
			labelWidth = max(labelWidth, len([]rune(n.Label)))
		}
	}
	for i, n := range next {
		prefix := "  • "
		if pos := indexOf(actionable, i); pos >= 0 {
			if pos == cursorAt {
				prefix = "> "
			} else {
				prefix = "  "
			}
		}
		if n.Command == "" {
			lines = append(lines, hangingWrap(prefix, n.Label, width))
			continue
		}
		padded := n.Label + strings.Repeat(" ", max(labelWidth-len([]rune(n.Label)), 0))
		combined := prefix + padded + "   " + n.Command
		if width > 0 && len([]rune(combined)) > width {
			combined = prefix + n.Label + "   " + n.Command
		}
		if width <= 0 || len([]rune(combined)) <= width {
			lines = append(lines, combined)
			continue
		}
		lines = append(lines, hangingWrap(prefix, n.Label, width), indentWrap("      ", n.Command, width))
	}
	return lines
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// parsePort extracts the trailing port number from a Next command such as
// "ovdb server start --port 6833": internal/runtime's port-conflict fixes
// always end the use_port command with the port to switch to.
func parsePort(command string) (int, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return 0, false
	}
	port, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || !setup.ValidPort(port) {
		return 0, false
	}
	return port, true
}
