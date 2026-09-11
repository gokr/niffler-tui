package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the final cached piece and the viewport, not just the pre-wrap
// renderer: those later stages used to turn shaded rectangles into ragged rows.
func TestCardBackgroundFinalRows(t *testing.T) {
	restoreDefaultTheme(t)
	cases := []struct {
		name string
		call toolCall
	}{
		{"short", toolCall{name: "bash", args: shellArgs(t, "pwd"), result: shellResult(t, "ok")}},
		{"script", toolCall{name: "bash", args: shellArgs(t, "for file in alpha beta gamma delta epsilon zeta; do\n\tprintf '%s\\n' \"$file\"\ndone"), result: shellResult(t, "short\n\n"+strings.Repeat("long output ", 12))}},
		{"resets", toolCall{name: "bash", args: shellArgs(t, "make test"), result: shellResult(t, "\x1b[31mred\x1b[0m plain\n\x1b[49mdefault\x1b[0;32mgreen\x1b[m tail")}},
		{"wide", toolCall{name: "bash", args: shellArgs(t, "echo 漢字 e\u0301"), result: shellResult(t, strings.Repeat("漢字 e\u0301 ", 20))}},
		{"token", toolCall{name: "bash", args: shellArgs(t, strings.Repeat("x", 160)), result: shellResult(t, "ok")}},
		{"highlight", toolCall{name: "read", args: json.RawMessage(`{"path":"example.go"}`), result: json.RawMessage(`"package main\n\nfunc main() { println(\"hello from a wrapped highlighted line\") }"`)}},
		{"error", toolCall{name: "bash", args: shellArgs(t, "false"), err: strings.Repeat("failure ", 12)}},
		{"pending", toolCall{name: "bash", args: shellArgs(t, "sleep 1"), pending: true}},
	}
	for _, theme := range []string{ThemeDefault, "light"} {
		applyTheme(theme)
		for _, tc := range cases {
			for _, level := range []toolLevel{toolBrief, toolMedium, toolFull} {
				for _, grouped := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/grouped=%t", theme, tc.name, level, grouped), func(t *testing.T) {
						m := newTestModel()
						m.loc, m.toolCards, m.toolLevel = LocaleEN, true, level
						run := &toolRun{calls: []toolCall{tc.call}, collapsed: true}
						if grouped {
							run.calls = append(run.calls, toolCall{name: "files"})
						}
						m.blocks = []transcriptBlock{{kind: blockTool, run: run}}
						// Reuse the cached block across resizes, including very narrow
						// layouts and a wide layout where no wrapping is needed.
						for _, width := range []int{32, 17, 1, 80} {
							m.viewport.SetWidth(width)
							piece := m.piece(0)
							assertCardRectangle(t, piece, width)
							rows := strings.Count(piece, "\n") + 1
							m.viewport.SetHeight(min(3, rows))
							m.viewport.SetContent(piece)
							m.viewport.GotoBottom()
							assertCardRectangle(t, m.viewport.View(), width)
						}
					})
				}
			}
		}
	}
}

func assertCardRectangle(t *testing.T, text string, width int) {
	t.Helper()
	for row, line := range strings.Split(text, "\n") {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d: width %d, want %d: %q", row, got, width, line)
		}
		for col, shaded := range shadedCells(line) {
			if !shaded {
				t.Fatalf("row %d rune %d lacks background: %q", row, col, line)
			}
		}
		// Trailing unrelated content must not inherit the card background.
		cells := shadedCells(line + "x")
		if cells[len(cells)-1] {
			t.Fatalf("row %d leaks background: %q", row, line)
		}
	}
}

func TestCardBackgroundDisabledLayout(t *testing.T) {
	applyTheme(ThemeDefault)
	restoreDefaultTheme(t)
	for _, noBackground := range []bool{false, true} {
		m := newTestModel()
		m.toolCards = noBackground
		if noBackground {
			toolCardStyle = lipgloss.NewStyle()
		}
		run := cardRun(t, 1)
		want := m.renderToolRunLines(run, detailPreview)
		if got := m.renderToolRun(run, detailPreview); got != want {
			t.Fatalf("disabled background changed layout: %q != %q", got, want)
		}
	}
}
