// Application-owned mouse selection for the alternate screen.
//
// Terminal mouse tracking is required for transcript wheel events. While it is
// enabled, terminals send drag events to the application instead of performing
// native selection, so the TUI must render and copy the selection itself.
package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type selectionPoint struct {
	x int
	y int
}

type mouseSelection struct {
	anchor  selectionPoint
	focus   selectionPoint
	pressed bool
	dragged bool
}

type selectionBounds struct {
	start selectionPoint
	end   selectionPoint
}

func mousePoint(x, y int) selectionPoint {
	return selectionPoint{x: max(0, x), y: max(0, y)}
}

func pointBefore(a, b selectionPoint) bool {
	return a.y < b.y || (a.y == b.y && a.x < b.x)
}

func (s mouseSelection) bounds() (selectionBounds, bool) {
	if s.anchor == s.focus {
		return selectionBounds{}, false
	}
	if pointBefore(s.anchor, s.focus) {
		return selectionBounds{start: s.anchor, end: s.focus}, true
	}
	return selectionBounds{start: s.focus, end: s.anchor}, true
}

// beginMouseSelection starts an application-owned selection. Cell-motion mode
// will subsequently deliver drag and release events while the left button is
// held.
func (m *model) beginMouseSelection(msg tea.MouseClickMsg) bool {
	if !m.mouse || msg.Button != tea.MouseLeft {
		return false
	}
	point := mousePoint(msg.X, msg.Y)
	m.selection = mouseSelection{anchor: point, focus: point, pressed: true}
	return true
}

func (m *model) extendMouseSelection(msg tea.MouseMotionMsg) bool {
	if !m.mouse || !m.selection.pressed {
		return false
	}
	// Cell-motion normally reports MouseLeft. MouseNone is accepted as some
	// terminals omit the held button from motion reports.
	if msg.Button != tea.MouseLeft && msg.Button != tea.MouseNone {
		return false
	}
	point := mousePoint(msg.X, msg.Y)
	m.selection.focus = point
	m.selection.dragged = m.selection.dragged || point != m.selection.anchor
	return true
}

// finishMouseSelection completes a left-button gesture. The bool reports a
// drag (and therefore suppresses the tool-card click action); a plain click
// clears the zero-width selection and remains available to click handling.
func (m *model) finishMouseSelection(msg tea.MouseReleaseMsg) (tea.Cmd, bool) {
	if !m.selection.pressed {
		return nil, false
	}
	point := mousePoint(msg.X, msg.Y)
	m.selection.focus = point
	m.selection.dragged = m.selection.dragged || point != m.selection.anchor
	m.selection.pressed = false
	if !m.selection.dragged {
		m.clearMouseSelection()
		return nil, false
	}

	text := m.selectedScreenText(m.View().Content)
	if text == "" {
		return nil, true
	}
	// OSC 52 works through the terminal (including remote sessions when the
	// terminal/tmux permits it). Set both the regular and primary clipboards
	// so drag selection behaves naturally on every supported platform.
	return tea.Batch(tea.SetClipboard(text), tea.SetPrimaryClipboard(text)), true
}

func (m *model) clearMouseSelection() {
	m.selection = mouseSelection{}
}

// boundedSelection clamps rows to the rendered screen. Columns are clamped per
// line later because each rendered line can have a different cell width.
func (m *model) boundedSelection(lines []string) (selectionBounds, bool) {
	bounds, ok := m.selection.bounds()
	if !ok || len(lines) == 0 {
		return selectionBounds{}, false
	}
	last := len(lines) - 1
	bounds.start.y = min(bounds.start.y, last)
	bounds.end.y = min(bounds.end.y, last)
	if pointBefore(bounds.end, bounds.start) || bounds.start == bounds.end {
		return selectionBounds{}, false
	}
	return bounds, true
}

// graphemeStart snaps a terminal cell column to the first cell of the
// grapheme occupying it. ansi.Cut intentionally omits a wide grapheme when a
// cut boundary bisects it, which lets us detect continuation cells.
func graphemeStart(line string, column int) int {
	column = min(max(0, column), ansi.StringWidth(line))
	for column > 0 && ansi.StringWidth(ansi.Cut(line, 0, column)) < column {
		column--
	}
	return column
}

// graphemeEnd returns the boundary after the grapheme occupying column, so
// the focus cell is included even for emoji and double-width CJK characters.
func graphemeEnd(line string, column int) int {
	width := ansi.StringWidth(line)
	if width == 0 {
		return 0
	}
	column = min(max(0, column), width-1)
	end := column + 1
	for end < width && ansi.StringWidth(ansi.Cut(line, 0, end)) <= column {
		end++
	}
	return end
}

func selectionColumns(line string, row int, bounds selectionBounds) (int, int) {
	width := ansi.StringWidth(line)
	start, end := 0, width
	if row == bounds.start.y {
		start = graphemeStart(line, min(bounds.start.x, width))
	}
	if row == bounds.end.y {
		if bounds.end.x >= width {
			end = width
		} else {
			end = graphemeEnd(line, bounds.end.x)
		}
	}
	start = min(max(0, start), width)
	end = min(max(0, end), width)
	return start, end
}

// selectedScreenText extracts plain text from the exact rendered screen. This
// naturally follows viewport soft wrapping and excludes all ANSI styling.
func (m *model) selectedScreenText(content string) string {
	lines := strings.Split(content, "\n")
	bounds, ok := m.boundedSelection(lines)
	if !ok {
		return ""
	}

	selected := make([]string, 0, bounds.end.y-bounds.start.y+1)
	for row := bounds.start.y; row <= bounds.end.y; row++ {
		start, end := selectionColumns(lines[row], row, bounds)
		if end <= start {
			selected = append(selected, "")
			continue
		}
		text := ansi.Strip(ansi.Cut(lines[row], start, end))
		selected = append(selected, strings.TrimRight(text, " \t"))
	}
	return strings.Join(selected, "\n")
}

// inverseSelection preserves embedded foreground/background styles. SGR reset
// codes inside markdown would otherwise cancel a single outer reverse-video
// code before the selected range ended.
func inverseSelection(text string) string {
	var out strings.Builder
	out.Grow(len(text) + 16)
	out.WriteString("\x1b[7m")
	for i := 0; i < len(text); {
		if text[i] == '\x1b' && i+1 < len(text) && text[i+1] == '[' {
			end := i + 2
			for end < len(text) && (text[end] < 0x40 || text[end] > 0x7e) {
				end++
			}
			if end < len(text) {
				out.WriteString(text[i : end+1])
				if text[end] == 'm' {
					out.WriteString("\x1b[7m")
				}
				i = end + 1
				continue
			}
		}
		out.WriteByte(text[i])
		i++
	}
	out.WriteString("\x1b[27m")
	return out.String()
}

// applyMouseSelection overlays reverse video on the selected screen cells.
func (m model) applyMouseSelection(content string) string {
	lines := strings.Split(content, "\n")
	bounds, ok := m.boundedSelection(lines)
	if !ok {
		return content
	}

	for row := bounds.start.y; row <= bounds.end.y; row++ {
		line := lines[row]
		start, end := selectionColumns(line, row, bounds)
		if end <= start {
			continue
		}
		width := ansi.StringWidth(line)
		before := ansi.Cut(line, 0, start)
		selected := ansi.Cut(line, start, end)
		after := ansi.Cut(line, end, width)
		lines[row] = before + inverseSelection(selected) + after
	}
	return strings.Join(lines, "\n")
}
