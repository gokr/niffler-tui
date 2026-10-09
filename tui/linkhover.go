package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Hover uses the same screen token and classifier as Ctrl-click. All-motion
// reporting is required; terminals report modifiers only when the mouse moves.
func (m model) applyLinkHover(content string) string {
	if !m.mouse || !m.hoverCtrl || m.selection.pressed {
		return content
	}
	token := screenTokenAt(content, m.hover.y, m.hover.x)
	if _, _, ok := tokenTarget(token); !ok {
		return content
	}
	lines := strings.Split(content, "\n")
	line := lines[m.hover.y]
	plain := ansi.Strip(line)
	start := graphemeStart(plain, m.hover.x)
	end := graphemeEnd(plain, m.hover.x)
	for start > 0 {
		prev := graphemeStart(plain, start-1)
		if isSpaceCells(plain, prev, start) {
			break
		}
		start = prev
	}
	width := ansi.StringWidth(plain)
	for end < width {
		next := graphemeEnd(plain, end)
		if isSpaceCells(plain, end, next) {
			break
		}
		end = next
	}
	// Reapply underline after embedded SGR resets, preserving existing colors.
	selected := ansi.Cut(line, start, end)
	selected = strings.ReplaceAll(selected, "\x1b[0m", "\x1b[0m\x1b[4m")
	selected = strings.ReplaceAll(selected, "\x1b[m", "\x1b[m\x1b[4m")
	lines[m.hover.y] = ansi.Cut(line, 0, start) + "\x1b[4m" + selected + "\x1b[24m" + ansi.Cut(line, end, width)
	return strings.Join(lines, "\n")
}
