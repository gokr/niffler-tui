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
	name    string
	callID  string
	args    json.RawMessage
	result  json.RawMessage
	err     string
	pending bool
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
func (m *model) completeToolCall(callID, name string, args, result json.RawMessage, err string) bool {
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
	toolBrief toolLevel = iota // collapsed cards (default)
	toolFull                   // every card expanded
	toolOff                    // hide tool cards entirely
)

func (l toolLevel) String() string {
	switch l {
	case toolFull:
		return "full"
	case toolOff:
		return "off"
	}
	return "brief"
}

// cycleToolVisibility advances the tool-card display level
// (brief → full → off → brief). Per-card clicks still toggle collapse,
// visible while the level is brief.
func (m *model) cycleToolVisibility() {
	m.toolLevel = (m.toolLevel + 1) % 3
	m.markTranscriptDirty()
}

// renderToolRun renders one card. expanded forces the expanded form
// (tool level full); otherwise the card's own collapsed state decides.
// Collapsed: a single summary line. Expanded: one line per call (glyph +
// name + compact args) followed by its result or error, indented.
func renderToolRun(run *toolRun, expanded bool) string {
	var b strings.Builder
	chevron := "▸"
	if expanded || !run.collapsed {
		chevron = "▾"
	}
	head := chevron + " " + runGlyph(run)
	if len(run.calls) == 1 {
		head += run.calls[0].name
		if run.calls[0].pending {
			head += "…"
		}
	} else {
		head += fmt.Sprintf("%d tool calls", len(run.calls))
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
	}
	b.WriteString(toolStyle.Render(head))

	if !expanded && run.collapsed {
		return b.String()
	}

	for i := range run.calls {
		c := &run.calls[i]
		b.WriteString("\n")
		b.WriteString(toolStyle.Render("  "))
		if c.pending {
			b.WriteString(toolStyle.Render("⚙ "))
		} else if c.err != "" {
			b.WriteString(errorStyle.Render("⚠ "))
		} else {
			b.WriteString(toolStyle.Render("✓ "))
		}
		b.WriteString(toolStyle.Render(c.name))
		if args := compactJSON(c.args); args != "" {
			b.WriteString("  " + truncate(args, toolPreviewLen))
		}
		if c.err != "" {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render("    error: " + truncate(c.err, maxToolText)))
		} else if res := compactJSON(c.result); res != "" && res != "null" {
			b.WriteString("\n")
			b.WriteString(metaStyle.Render("    " + truncate(res, maxToolText)))
		}
	}
	return b.String()
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
		m.markTranscriptDirty()
		m.syncViewport(false)
	}
}

// blockAtContentLine maps a line index into the rendered transcript to the
// block that owns it. Blocks are laid out exactly as renderTranscript emits
// them, separated by a blank line ("\n\n") between blocks. Lines that soft-wrap
// beyond the viewport width are approximated by their hard newlines; this is
// exact for glamour-wrapped markdown and collapsed cards, which is where
// clicking matters.
func (m *model) blockAtContentLine(contentLine int) int {
	line := 0
	for i := range m.blocks {
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
