// Transcript blocks and their terminal rendering. Blocks are appended as
// events arrive; assistant blocks cache their styled markdown rendering and
// the joined transcript string is cached too, so a token burst only pays for
// the growing tail block instead of re-joining the whole transcript.
package main

import (
	"strings"
)

type blockKind int

// thinkingLevel controls how reasoning blocks render (ctrl+t cycles).
// Display-side only: the backend has no thinking-level knob, so the
// transcript always receives the full reasoning and the UI decides how
// much of it to show.
type thinkingLevel int

const (
	thinkFull  thinkingLevel = iota // render reasoning blocks in full
	thinkBrief                      // one dim collapsed line per block
	thinkOff                        // hide reasoning blocks entirely
)

func (l thinkingLevel) String() string {
	switch l {
	case thinkBrief:
		return "brief"
	case thinkOff:
		return "off"
	}
	return "full"
}

const (
	blockUser blockKind = iota
	blockAssistant
	blockThinking
	blockTool
	blockMeta
	blockError
)

type transcriptBlock struct {
	kind blockKind
	text string

	// run holds the grouped tool-run card data when kind == blockTool (and
	// the block was created by the card path rather than addBlock).
	run *toolRun

	// finalized marks an assistant block as complete: its text was set by the
	// round's final assistant event (full content). While false the block is
	// the active streamed partial, which token frames keep appending to. The
	// distinction lets the assistant event and late token frames coalesce
	// into the right block even when tool-call events arrive out of order
	// (NATS orders per subject, not across them), instead of opening a fresh
	// duplicate block.
	finalized bool

	// rendered is the cached terminal rendering of text, used by the
	// transcript renderer. renderedOK reports whether the cache is valid for
	// renderedText; blocks are re-rendered when the text changes or the
	// renderer is rebuilt (e.g. on resize).
	rendered     string
	renderedText string
	renderedOK   bool
}

func (m *model) addBlock(kind blockKind, text string) {
	m.blocks = append(m.blocks, transcriptBlock{kind: kind, text: text})
	m.markTranscriptDirty()
}

func (m *model) addUniqueBlock(kind blockKind, text string) {
	if len(m.blocks) > 0 {
		last := m.blocks[len(m.blocks)-1]
		if last.kind == kind && last.text == text {
			return
		}
	}
	m.addBlock(kind, text)
}

// markTranscriptDirty invalidates the cached joined transcript. Called by
// every site that mutates blocks, toggles tool-run collapse, rebuilds the
// markdown renderer, or flips the streaming flag (which changes how the
// streaming block renders).
func (m *model) markTranscriptDirty() {
	m.transcriptDirty = true
}

// renderBlock returns the terminal rendering of block i, re-rendering only
// when the block text changed since the cached render or the renderer was
// rebuilt. Assistant blocks are rendered as markdown; everything else is
// plain text.
func (m *model) renderBlock(i int) string {
	block := &m.blocks[i]
	if block.kind != blockAssistant {
		return block.text
	}
	if block.renderedOK && block.renderedText == block.text {
		return block.rendered
	}
	// While tokens are still streaming, keep showing plain text and defer
	// the markdown render to the settle tick: re-rendering a large block
	// with glamour on every token would stall the UI. Edge newlines are
	// trimmed (models open content with blank lines, and reasoning-adjacent
	// answers start with them); they would stack onto the block separator
	// into walls of empty space.
	if m.renderer != nil && m.streaming {
		return strings.Trim(block.text, "\n\r")
	}
	out := strings.Trim(block.text, "\n\r")
	if m.renderer != nil {
		if rendered, err := m.renderer.Render(block.text); err == nil {
			out = strings.Trim(rendered, "\n")
		}
	}
	block.rendered = out
	block.renderedText = block.text
	block.renderedOK = true
	return out
}

// piece returns the terminal rendering of transcript block i — the exact
// string renderTranscript joins (one blank line between blocks). Shared with
// blockAtContentLine so mouse hit-testing matches what is on screen.
func (m *model) piece(i int) string {
	block := &m.blocks[i]
	switch block.kind {
	case blockUser:
		return userStyle.Render(block.text)
	case blockAssistant:
		return m.renderBlock(i)
	case blockThinking:
		switch m.thinkLevel {
		case thinkOff:
			return ""
		case thinkBrief:
			// Collapsed reasoning: a single dim line, like a tool card head.
			return thinkingStyle.Render("▸ thinking…")
		}
		// Reasoning deltas carry the model's raw edge newlines: reasoning
		// opens with blank lines and closes the same way. Rendering them
		// stacks onto the "\n\n" block separator into walls of empty space,
		// so trim the edges and keep the interior paragraph breaks.
		// Render reasoning in Pi style: pure gray italic, no label.
		return thinkingStyle.Render(strings.Trim(block.text, "\n\r"))
	case blockTool:
		if block.run != nil {
			switch m.toolLevel {
			case toolOff:
				return "" // hidden (tool level off) — not rendered
			case toolFull:
				return renderToolRun(block.run, true)
			}
			return renderToolRun(block.run, false)
		}
		return toolStyle.Render("tool> " + block.text)
	case blockMeta:
		return metaStyle.Render(block.text)
	case blockError:
		return errorStyle.Render("error> " + block.text)
	}
	return block.text
}

// renderTranscript joins all block renderings, caching the result until a
// block mutation or a render-affecting flag change marks the model dirty.
// Hidden thinking blocks (level off) are skipped entirely; the separator
// logic tracks whether anything was written so no stray blank lines remain.
func (m *model) renderTranscript() string {
	if !m.transcriptDirty && m.transcript != "" {
		return m.transcript
	}
	var out strings.Builder
	wrote := false
	for i := range m.blocks {
		piece := m.piece(i)
		if piece == "" {
			continue
		}
		if wrote {
			out.WriteString("\n\n")
		}
		out.WriteString(piece)
		wrote = true
	}
	m.transcript = out.String()
	m.transcriptDirty = false
	return m.transcript
}
