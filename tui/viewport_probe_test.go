// Reproduction probe for the "extra newlines while scrolling streamed
// thinking output" report: drive the bubbles v2 viewport the same way the
// TUI does (SoftWrap on, growing content, GotoBottom every frame) and
// inspect the rendered frames for row-count anomalies.
package main

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func newVP() viewport.Model {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SoftWrap = true
	return vp
}

// countRows returns the number of physical rows the viewport emits and
// flags any row that is not exactly the expected width-ish content.
func countRows(t *testing.T, vp viewport.Model) (rows int, view string) {
	t.Helper()
	view = vp.View()
	rows = strings.Count(strings.TrimSuffix(view, "\n"), "\n") + 1
	return rows, view
}

// TestViewportPlainASCII: long ASCII lines wrapping at 80 — the estimate
// and the real wrap must agree so GotoBottom shows the true last row.
func TestViewportPlainASCII(t *testing.T) {
	vp := newVP()
	long := strings.Repeat("a", 200)
	text := strings.Repeat(long+"\n", 10)
	vp.SetContent(text)
	vp.GotoBottom()
	rows, view := countRows(t, vp)
	if rows != 20 {
		t.Fatalf("want 20 rows at bottom, got %d", rows)
	}
	// The true bottom is the tail of the last line; it must be visible.
	if !strings.Contains(view, strings.Repeat("a", 80)) {
		t.Fatalf("bottom rows missing:\n%q", view)
	}
}

// TestViewportTrailingCR: streamed deltas often carry \r\n; SetContent
// splits on \n so lines end with a bare \r. The width estimator and the
// real wrap must agree on its zero width.
func TestViewportTrailingCR(t *testing.T) {
	vp := newVP()
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString(strings.Repeat("a", 100) + "\r\n")
	}
	vp.SetContent(b.String())
	vp.GotoBottom()
	rows, view := countRows(t, vp)
	if rows != 20 {
		t.Fatalf("want 20 rows at bottom, got %d (view %q)", rows, view)
	}
	if strings.Contains(view, "\r") {
		t.Fatalf("raw CR leaked into the rendered view:\n%q", view)
	}
}

// TestViewportStyledMultiLine: lipgloss styles a multi-line thinking block;
// per-line escape pairs must not skew width accounting.
func TestViewportStyledMultiLine(t *testing.T) {
	vp := newVP()
	style := lipgloss.NewStyle().Italic(true).Faint(true)
	var parts []string
	for i := 0; i < 10; i++ {
		parts = append(parts, style.Render(strings.Repeat("x", 130)))
	}
	vp.SetContent(strings.Join(parts, "\n"))
	vp.GotoBottom()
	rows, view := countRows(t, vp)
	if rows != 20 {
		t.Fatalf("want 20 rows at bottom, got %d", rows)
	}
	// True bottom: the tail of the last wrapped line (130 = 80 + 50).
	if !strings.Contains(ansi.Strip(view), strings.Repeat("x", 50)) {
		t.Fatalf("true bottom row not visible:\n%q", ansi.Strip(view))
	}
}

// TestViewportWideRunes: CJK runes straddle the wrap boundary; ansi.Cut
// then produces more rows than ceil(width/maxWidth) estimates, and the
// scroll window misaligns — duplicated/blank rows near the bottom.
func TestViewportWideRunes(t *testing.T) {
	vp := newVP()
	// 90 cells of CJK content per line (45 runes), 10 lines.
	line := strings.Repeat("漢", 45)
	vp.SetContent(strings.Repeat(line+"\n", 10))
	vp.GotoBottom()
	rows, view := countRows(t, vp)
	if rows != 20 {
		t.Fatalf("want 20 rows at bottom, got %d", rows)
	}
	// The last wrapped row holds the tail of the final line: cells 81..90.
	if !strings.Contains(ansi.Strip(view), strings.Repeat("漢", 5)) {
		t.Fatalf("true bottom row not visible:\n%q", ansi.Strip(view))
	}
	fmt.Println(view)
}
