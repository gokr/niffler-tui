package main

// Frame-cost guard for transcript scrolling and streaming. Drives the bubbles
// v2 viewport exactly as the TUI does — content pre-clamped to the viewport
// width the way clampLines guarantees, a scrollback-sized window,
// SetContent+GotoBottom per 30fps flush, wheel ticks interleaved.
//
// History: with viewport SoftWrap enabled, scroll geometry (maxYOffset,
// AtBottom, SetYOffset, GotoBottom — several calls per frame) ANSI-measured
// every line of the whole window per call: ~100ms per streaming frame and
// ~70ms per wheel tick at the 3000-row cap. That was the "choppy scrolling
// while streaming thinking" report. SoftWrap off makes geometry O(1); these
// benchmarks pin the difference so it cannot creep back.

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	benchWidth  = 119 // viewport width (terminal 120 minus reserved column)
	benchHeight = 30
	benchRows   = 3000 // the scrollback cap
)

var thinkingStyleBench = lipgloss.NewStyle().Italic(true).Faint(true)

// clampBenchLines mirrors transcript.clampLines: word-wrap then hard-truncate
// every line to width, so nothing reaches the viewport wider than it.
func clampBenchLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) <= width {
			continue
		}
		wrapped := strings.Split(ansi.Wordwrap(line, width, ""), "\n")
		for j, l := range wrapped {
			wrapped[j] = ansi.Truncate(l, width, "")
		}
		lines[i] = strings.Join(wrapped, "\n")
	}
	return strings.Join(lines, "\n")
}

// benchTranscript builds a styled transcript like a long session, pre-clamped
// to the viewport width exactly as the TUI's render pipeline guarantees.
func benchTranscript(rows int) string {
	var b strings.Builder
	words := []string{"reasoning", "about", "the", "scrollback", "window", "and", "streaming", "tokens"}
	for i := 0; i < rows; i++ {
		switch i % 4 {
		case 0: // thinking prose, styled
			b.WriteString(thinkingStyleBench.Render(strings.Repeat("thinking stream ", 6)))
		case 1: // assistant markdown with inline SGR runs
			b.WriteString("**bold** text with `code` and [links](x) plus " + strings.Repeat("word ", 12))
		case 2: // long line that the clamp wraps across several rows
			b.WriteString(strings.Repeat("supercalifragilistic", 15))
		default: // ordinary prose
			b.WriteString(strings.Join(words[:1+i%len(words)], " ") + " " + strings.Repeat("lorem ipsum dolor ", 5))
		}
		b.WriteString("\n")
	}
	return clampBenchLines(b.String(), benchWidth)
}

func benchViewport(soft bool) *viewport.Model {
	vp := viewport.New(viewport.WithWidth(benchWidth), viewport.WithHeight(benchHeight))
	vp.SoftWrap = soft
	return &vp
}

// TestSoftWrapRedundantForClampedContent pins the premise of the SoftWrap=off
// fix: for content the transcript pipeline has already clamped to the
// viewport width, soft wrap renders byte-identical output and agrees on
// scroll geometry — so disabling it costs nothing visually while making
// scroll math O(1) instead of O(whole window).
func TestSoftWrapRedundantForClampedContent(t *testing.T) {
	content := benchTranscript(benchRows)
	on := benchViewport(true)
	on.SetContent(content)
	on.SetYOffset(500)
	off := benchViewport(false)
	off.SetContent(content)
	off.SetYOffset(500)

	if got, want := on.View(), off.View(); got != want {
		t.Fatalf("SoftWrap changes the rendered view even though every line is pre-clamped:\non=%q\noff=%q", got[:400], want[:400])
	}
	for _, y := range []int{0, 1, 500, benchRows, 99999} {
		on.SetYOffset(y)
		off.SetYOffset(y)
		if on.YOffset() != off.YOffset() {
			t.Fatalf("yoffset mismatch at %d: on=%d off=%d", y, on.YOffset(), off.YOffset())
		}
		if on.AtBottom() != off.AtBottom() {
			t.Fatalf("AtBottom mismatch at %d", y)
		}
		if on.View() != off.View() {
			t.Fatalf("view mismatch at yoffset %d", y)
		}
	}
}

// TestViewportMatchesNifflerSettings checks the viewport the model actually
// builds keeps the fast settings this file benchmarks.
func TestViewportMatchesNifflerSettings(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 120, 40
	m.layout()
	if m.viewport.SoftWrap {
		t.Fatal("viewport SoftWrap must stay off — see viewport.New in newModel")
	}
}

// One 30fps streaming frame (SetContent + GotoBottom + View) on a full
// scrollback window. This is the per-flush budget; it must sit well under
// viewportFlushInterval (33ms) so streaming never starves input handling.
func BenchmarkStreamFrame(b *testing.B) {
	for _, soft := range []bool{true, false} {
		vp := benchViewport(soft)
		content := benchTranscript(benchRows)
		vp.SetContent(content)
		vp.GotoBottom()
		b.Run("softwrap="+boolLabel(soft), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				vp.SetContent(content)
				vp.GotoBottom()
				_ = vp.View()
			}
		})
	}
}

// One wheel tick pair mid-transcript — the operation users feel directly.
func BenchmarkWheelScroll(b *testing.B) {
	for _, soft := range []bool{true, false} {
		vp := benchViewport(soft)
		vp.SetContent(benchTranscript(benchRows))
		vp.SetYOffset(500)
		b.Run("softwrap="+boolLabel(soft), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				vp.ScrollUp(3)
				vp.ScrollDown(3)
			}
		})
	}
}

func boolLabel(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
