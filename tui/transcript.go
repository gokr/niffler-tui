// Transcript blocks and their terminal rendering. Blocks are appended as
// events arrive; assistant blocks cache their styled markdown rendering and
// the joined transcript string is cached too, so a token burst only pays for
// the growing tail block instead of re-joining the whole transcript.
package main

import (
	"strings"
)

type blockKind int

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
	// with glamour on every token would stall the UI.
	if m.renderer != nil && m.streaming {
		return block.text
	}
	out := block.text
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
		// Render reasoning in Pi style: pure gray italic, no label.
		return thinkingStyle.Render(block.text)
	case blockTool:
		if block.run != nil {
			return renderToolRun(block.run)
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
func (m *model) renderTranscript() string {
	if !m.transcriptDirty && m.transcript != "" {
		return m.transcript
	}
	var out strings.Builder
	for i := range m.blocks {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(m.piece(i))
	}
	m.transcript = out.String()
	m.transcriptDirty = false
	return m.transcript
}
