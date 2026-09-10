// Tool-run cards — grouped rendering of consecutive tool calls.
//
// Instead of one intrusive "tool> name args" line per call, consecutive tool
// calls in a turn are folded into a single card that is collapsed to a
// one-line summary by default (glyph + call count + a few name chips),
// mirroring the web UI's ToolRun card. Expanding reveals every call with its
// args and result/error. A card is toggled with a click (when mouse tracking
// is on, see /mouse) or globally with Ctrl+T.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
)

const (
	// maxToolChips caps how many name chips a collapsed card shows before
	// collapsing the rest into a "+N" suffix.
	maxToolChips = 8
	// toolPreviewLen is the truncation for a call's inline args preview.
	toolPreviewLen = 160
)

// toolCall is one invocation of a tool with its final outcome. Core emits a
// two-phase toolcall protocol: a start event (args, no result) opens the
// call and a done event (result/error) completes it. A call stays pending
// between the two; legacy single-phase events arrive already complete.
type toolCall struct {
	name       string
	callID     string
	args       json.RawMessage
	result     json.RawMessage
	err        string
	pending    bool
	durationMs int // tool execution time, from the done event telemetry
}

// toolRun is a group of consecutive tool calls rendered as one card.
type toolRun struct {
	calls     []toolCall
	collapsed bool // a freshly started card is collapsed by default
}

func runGlyph(run *toolRun) string {
	for i := range run.calls {
		if run.calls[i].pending {
			return "⚙ "
		}
	}
	for i := range run.calls {
		if run.calls[i].err != "" {
			return "⚠ "
		}
	}
	return "✓ "
}

// completeToolCall fills in the outcome of the pending call opened by the
// start phase of core's two-phase toolcall protocol. Blocks are walked from
// the end so the pending entry is found even if other rounds have appended
// blocks; matching is by call id (by name for legacy events without one).
// Returns false when nothing pending matches — the caller appends the
// finished call directly instead.
func (m *model) completeToolCall(callID, name string, args, result json.RawMessage, err string, durationMs int) bool {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := &m.blocks[i]
		if b.kind != blockTool || b.run == nil {
			continue
		}
		for j := range b.run.calls {
			c := &b.run.calls[j]
			if !c.pending {
				continue
			}
			if c.callID != "" && callID != "" && c.callID != callID {
				continue
			}
			if callID == "" && c.name != name {
				continue
			}
			c.pending = false
			c.callID = callID
			if len(args) > 0 {
				c.args = args
			}
			c.result = result
			c.err = err
			c.durationMs = durationMs
			b.pieceOK = false // the card changed with the same text
			m.markTranscriptDirty()
			return true
		}
	}
	return false
}

// appendToolCall folds a call into a card already in progress when the last
// transcript block is an open tool-run card; otherwise it starts a new
// (collapsed) card. Consecutive tool calls in one turn — the common shape —
// share a single card; intervening assistant text starts a new one.
func (m *model) appendToolCall(call toolCall) {
	n := len(m.blocks)
	if n > 0 && m.blocks[n-1].kind == blockTool && m.blocks[n-1].run != nil {
		m.blocks[n-1].run.calls = append(m.blocks[n-1].run.calls, call)
		m.blocks[n-1].pieceOK = false // the card changed with the same text
		m.markTranscriptDirty()
		return
	}
	m.blocks = append(m.blocks, transcriptBlock{
		kind: blockTool,
		run:  &toolRun{calls: []toolCall{call}, collapsed: true},
	})
	m.markTranscriptDirty()
}

// toolLevel controls how tool-run cards render (ctrl+e cycles). Display
// only: cards always arrive, the level decides how much of them shows.
type toolLevel int

const (
	toolBrief  toolLevel = iota // one summary line per run (quietest)
	toolMedium                  // per-tool previews: bash tail, edit diff, read head
	toolFull                    // every call fully expanded
	toolOff                     // hide tool cards entirely
)

func (l toolLevel) String() string {
	switch l {
	case toolMedium:
		return "medium"
	case toolFull:
		return "full"
	case toolOff:
		return "off"
	}
	return "brief"
}

// toolDetail is the resolved amount of per-call output a card shows; it is
// the tool level with the card's own click-to-expand state folded in.
type toolDetail int

const (
	detailBrief   toolDetail = iota // summary line only
	detailPreview                   // per-tool preview (bash tail, edit diff, …)
	detailFull                      // full per-call output
)

// toolDetail resolves the active display detail for the global tool level;
// the per-card click-to-expand bump happens in renderToolRun.
func (m model) toolDetail() (toolDetail, bool) {
	switch m.toolLevel {
	case toolMedium:
		return detailPreview, true
	case toolFull:
		return detailFull, true
	case toolOff:
		return 0, false
	}
	return detailBrief, true
}

// cycleToolVisibility advances the tool-card display level
// (brief → medium → full → off → brief). Per-card clicks still toggle the
// collapse state, which bumps the level one step for that card.
func (m *model) cycleToolVisibility() {
	m.toolLevel = (m.toolLevel + 1) % 4
	m.invalidatePieces()
}

// renderToolRun renders one card at the given detail. brief shows the
// summary line only; preview and full add per-tool call/result bodies (see
// renderToolPreview) — the growing part of the transcript is cached as one
// piece either way. Card lines are wrapped in the theme's card style so the
// run reads as one block; cardStyle is applied only when the theme sets a
// background (see applyTheme), so background-less themes are unchanged.
func (m model) renderToolRun(run *toolRun, detail toolDetail) string {
	return m.cardStyle().Render(m.renderToolRunLines(run, detail))
}

// cardStyle returns the style backing tool runs: a background when the
// active theme defines one, an empty style otherwise.
func (m model) cardStyle() lipgloss.Style {
	if !m.toolCards {
		return lipgloss.NewStyle()
	}
	return toolCardStyle
}

func (m model) renderToolRunLines(run *toolRun, detail toolDetail) string {
	var b strings.Builder
	// A card the user expanded by click shows one level more than the
	// global setting (brief→preview, preview→full).
	if !run.collapsed && detail < detailFull {
		detail++
	}
	chevron := "▸"
	if detail != detailBrief {
		chevron = "▾"
	}
	glyph := runGlyph(run)

	// Single-call cards upgrade their head to the tool's own call line once
	// detail is available, so a bash card reads "$ make test" instead of
	// just "bash".
	var previews []toolPreview
	if detail != detailBrief {
		previews = make([]toolPreview, len(run.calls))
		for i := range run.calls {
			previews[i] = m.renderToolPreview(&run.calls[i], detail == detailFull)
		}
	}

	head := chevron + " " + glyph
	if len(run.calls) == 1 {
		if detail != detailBrief {
			head += previews[0].head
		} else {
			head += run.calls[0].name
			if run.calls[0].pending {
				head += "…"
			}
		}
	} else {
		head += runSummary(run)
	}
	b.WriteString(toolStyle.Render(head))

	if detail == detailBrief {
		return b.String()
	}

	if len(run.calls) == 1 {
		writePreviewBody(&b, previews[0].body)
		return b.String()
	}
	for i := range run.calls {
		b.WriteString("\n")
		b.WriteString(toolStyle.Render("  "))
		b.WriteString(callGlyph(&run.calls[i]))
		b.WriteString(previews[i].head)
		writePreviewBody(&b, previews[i].body)
	}
	return b.String()
}

// runSummary is the multi-call head: call count plus name chips.
func runSummary(run *toolRun) string {
	head := fmt.Sprintf("%d tool calls", len(run.calls))
	for i := range run.calls {
		if i >= maxToolChips {
			head += fmt.Sprintf("  +%d", len(run.calls)-i)
			break
		}
		head += "  " + run.calls[i].name
		if run.calls[i].pending {
			head += "…"
		}
	}
	return head
}

// writePreviewBody indents and appends a tool preview body below its call
// line. Body lines are already styled by the tool renderer.
func writePreviewBody(b *strings.Builder, body []string) {
	for _, line := range body {
		b.WriteString("\n")
		b.WriteString("    ")
		b.WriteString(line)
	}
}

// callGlyph is the per-call status marker (pending / error / ok).
func callGlyph(c *toolCall) string {
	switch {
	case c.pending:
		return toolStyle.Render("⚙ ")
	case c.err != "":
		return errorStyle.Render("⚠ ")
	}
	return toolStyle.Render("✓ ")
}

// handleMouseClick toggles the tool-run card under a transcript click. The
// terminal Y is translated to a transcript content line via the viewport
// scroll offset; the layout reserves one header row above the transcript
// (runtime status is rendered on that same row).
func (m *model) handleMouseClick(msg tea.MouseClickMsg) {
	if msg.Button != tea.MouseLeft || !m.mouse {
		return
	}
	contentLine := msg.Y - 1 + m.viewport.YOffset()
	if contentLine < 0 {
		return
	}
	idx := m.blockAtContentLine(contentLine)
	if idx < 0 {
		return
	}
	if b := &m.blocks[idx]; b.kind == blockTool && b.run != nil {
		b.run.collapsed = !b.run.collapsed
		b.pieceOK = false // collapsed state changed with the same text
		m.markTranscriptDirty()
		m.syncViewport(false)
	}
}

// blockAtContentLine maps a line index into the rendered transcript to the
// block that owns it. Blocks are laid out exactly as renderTranscript emits
// them, separated by a blank line ("\n\n") between blocks, behind the
// scrollback marker when the transcript window is truncated. Lines that
// soft-wrap beyond the viewport width are approximated by their hard
// newlines; this is exact for glamour-wrapped markdown and collapsed cards,
// which is where clicking matters.
func (m *model) blockAtContentLine(contentLine int) int {
	line := 0
	if m.renderFrom > 0 {
		// The marker is written as the first piece, then the usual blank
		// separator before the first visible block.
		if contentLine < 1 {
			return -1
		}
		line = 3
	}
	for i := m.renderFrom; i < len(m.blocks); i++ {
		piece := m.piece(i)
		if piece == "" {
			continue // hidden block (thinking level off) — not rendered
		}
		rows := strings.Count(piece, "\n") + 1
		if contentLine < line+rows {
			return i
		}
		line += rows + 2 // separator blank line between blocks
	}
	return -1
}
