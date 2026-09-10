// Transcript blocks and their terminal rendering. Blocks are appended as
// events arrive; assistant blocks cache their styled markdown rendering and
// the joined transcript string is cached too, so a token burst only pays for
// the growing tail block instead of re-joining the whole transcript.
package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// blankRunRe matches runs of blank lines inside streamed reasoning text.
// Gateways emit paragraph breaks as repeated newlines across deltas; without
// compaction they render as walls of empty rows between thinking paragraphs.
// Runs are capped at one blank line, not erased: paragraph breaks stay
// visible between thinking blocks.
var blankRunRe = regexp.MustCompile(`(\r?\n){2,}`)

// defaultScrollbackLines caps how many rendered transcript rows the viewport
// carries (NIF_TUI_SCROLLBACK overrides; 0 = unlimited). Every block's
// rendering is cached and only the tail is recomputed per frame, but the
// viewport itself scans its whole content on each sync/scroll — the cap is
// what keeps that scan (and therefore streaming) flat as a session grows.
const defaultScrollbackLines = 3000

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

	// piece caches the final width-clamped rendering of this block — what
	// renderTranscript joins — alongside the exact inputs it was built from.
	// This is what makes streaming cost O(tail block) instead of
	// O(whole transcript) per token: settled blocks hit the cache, only the
	// growing tail is recomputed. Tool-run mutations clear pieceOK directly.
	piece    string
	pieceKey pieceKey
	pieceOK  bool
}

// pieceKey captures every non-text input a block's rendering depends on, so
// a cache hit needs only a struct comparison: the model's display epoch
// (theme/renderer rebuilds), the viewport width (clampLines), and the
// display flags that change rendering without touching block text.
// Streaming is included because the active assistant block renders as plain
// text until it settles (see renderBlock).
type pieceKey struct {
	text      string
	epoch     int
	width     int
	think     thinkingLevel
	tool      toolLevel
	streaming bool
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

// invalidatePieces drops every cached block rendering for changes that do
// not show up in a block's pieceKey or text (a theme swap re-styles every
// block via the global style variables). Cheap mutations that a key cannot
// capture — tool-run completion, card collapse — invalidate their block's
// pieceOK directly instead (see toolcard.go).
func (m *model) invalidatePieces() {
	m.pieceEpoch++
	m.markTranscriptDirty()
}

// renderBlock returns the terminal rendering of block i, re-rendering only
// when the block text changed since the cached render or the renderer was
// rebuilt. Assistant blocks are rendered as markdown; everything else is
// plain text.
func (m *model) renderBlock(i int) string {
	block := &m.blocks[i]
	if block.kind != blockAssistant && block.kind != blockThinking {
		return block.text
	}
	if block.renderedOK && block.renderedText == block.text {
		return block.rendered
	}
	text := block.text
	if block.kind == blockThinking {
		// Reasoning arrives with runaway blank runs and edge newlines; the
		// transcript always compacted those before rendering.
		text = compactThinkingText(text)
	}
	// While tokens are still streaming, keep showing plain text and defer
	// the markdown render to the settle tick: re-rendering a large block
	// with glamour on every token would stall the UI. Edge newlines are
	// trimmed (models open content with blank lines, and reasoning-adjacent
	// answers start with them); they would stack onto the block separator
	// into walls of empty space.
	if m.streaming {
		out := strings.Trim(text, "\n\r")
		if block.kind == blockThinking {
			return thinkingStyle.Render(out)
		}
		return out
	}
	out := strings.Trim(text, "\n\r")
	renderer := m.renderer
	if block.kind == blockThinking {
		renderer = m.thinkingRenderer
	}
	if renderer != nil {
		if rendered, err := renderer.Render(text); err == nil {
			out = strings.Trim(rendered, "\n")
		}
	} else if block.kind == blockThinking {
		// No markdown renderer: keep the pre-markdown dim italic look.
		out = thinkingStyle.Render(out)
	}
	block.rendered = out
	block.renderedText = block.text
	block.renderedOK = true
	return out
}

// renderPiece renders block i from scratch (no caching, no width clamp) as
// the exact styled string the transcript joins.
func (m *model) renderPiece(i int) string {
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
		// Render reasoning in Pi style: pure gray italic, no label. Edge
		// newlines are trimmed and blank-line runs capped at one blank
		// line — streamed reasoning would otherwise stack paragraph
		// breaks into walls of empty rows, while genuine paragraph gaps
		// between thinking blocks stay visible. With a markdown renderer
		// available the text (including any fenced code) is styled there
		// instead (see renderBlock).
		return m.renderBlock(i)
	case blockTool:
		if block.run != nil {
			detail, ok := m.toolDetail()
			if !ok {
				return "" // hidden (tool level off) — not rendered
			}
			return m.renderToolRun(block.run, detail)
		}
		return toolStyle.Render("tool> " + block.text)
	case blockMeta:
		return metaStyle.Render(block.text)
	case blockError:
		return errorStyle.Render("error> " + block.text)
	}
	return block.text
}

// piece returns the terminal rendering of transcript block i — the exact
// string renderTranscript joins (one blank line between blocks). The result
// is cached per block: while a token stream grows the tail block, every
// settled block keeps its rendering and only the tail is recomputed. Shared
// with blockAtContentLine so mouse hit-testing matches what is on screen.
func (m *model) piece(i int) string {
	block := &m.blocks[i]
	key := pieceKey{
		text:      block.text,
		epoch:     m.pieceEpoch,
		width:     m.viewport.Width(),
		think:     m.thinkLevel,
		tool:      m.toolLevel,
		streaming: m.streaming,
	}
	if block.pieceOK && block.pieceKey == key {
		return block.piece
	}
	out := clampLines(m.renderPiece(i), m.viewport.Width())
	block.piece = out
	block.pieceKey = key
	block.pieceOK = true
	return out
}

// compactThinkingText prepares streamed reasoning for display: edge
// newlines are trimmed and blank-line runs capped at one blank line,
// keeping reasoning dense while paragraph breaks between thinking blocks
// stay readable.
func compactThinkingText(text string) string {
	text = strings.Trim(text, "\n\r")
	return blankRunRe.ReplaceAllString(text, "\n\n")
}

// clampLines fits every line of a transcript piece to the viewport width:
// word-wrap first so breaks land between words, then hard-truncate whatever
// is still wider (unbreakable tokens). The viewport pads its lines to its
// exact width, and a line reaching the terminal's last column flips the
// terminal into pending-wrap — bubbletea's relative cursor moves then land
// one row off, which shows up as phantom blank lines between random lines
// while streaming or scrolling. Nothing the transcript emits may reach the
// last column.
func clampLines(text string, width int) string {
	if width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) <= width {
			continue
		}
		wrapped := strings.Split(ansi.Wordwrap(line, width, ""), "\n")
		for j, l := range wrapped {
			// Wordwrap keeps unbreakable runs intact — clamp each wrapped
			// line separately (Truncate measures the string as a whole,
			// so it must never see a multi-line input).
			wrapped[j] = ansi.Truncate(l, width, "")
		}
		lines[i] = strings.Join(wrapped, "\n")
	}
	return strings.Join(lines, "\n")
}

// renderTranscript joins the visible block renderings, caching the result
// until a block mutation or a display change marks the model dirty. Only the
// tail of the transcript is rendered: the scrollback cap bounds how many
// rows the viewport carries, so per-token cost stays flat no matter how long
// the session runs. Earlier blocks stay in memory (and in the store); the
// viewport just cannot scroll past the cap (a marker says so).
func (m *model) renderTranscript() string {
	if !m.transcriptDirty && m.transcript != "" {
		return m.transcript
	}
	from := m.chooseRenderFrom()
	m.renderFrom = from
	var out strings.Builder
	wrote := false
	write := func(piece string) {
		if piece == "" {
			return
		}
		if wrote {
			out.WriteString("\n\n")
		}
		out.WriteString(piece)
		wrote = true
	}
	if from > 0 {
		write(m.scrollbackMarker())
	}
	for i := from; i < len(m.blocks); i++ {
		write(m.piece(i))
	}
	m.transcript = out.String()
	m.transcriptDirty = false
	return m.transcript
}

// chooseRenderFrom returns the first block index whose rendered rows fit in
// the scrollback window. The last block is always included — it is the one
// being streamed and may alone exceed the cap.
func (m *model) chooseRenderFrom() int {
	if m.scrollback <= 0 || len(m.blocks) == 0 {
		return 0
	}
	rows := 0
	for i := len(m.blocks) - 1; i >= 0; i-- {
		n := m.blockRows(i)
		if rows > 0 && rows+n > m.scrollback {
			return i + 1
		}
		rows += n
	}
	return 0
}

// blockRows returns how many terminal rows block i occupies in the joined
// transcript (0 for hidden blocks), sharing the piece cache so window
// selection never re-renders settled blocks.
func (m *model) blockRows(i int) int {
	piece := m.piece(i)
	if piece == "" {
		return 0
	}
	return strings.Count(piece, "\n") + 1
}

// scrollbackMarker is the dim line at the top of a windowed transcript,
// telling the user why the earlier messages are not scrollable.
func (m *model) scrollbackMarker() string {
	return metaStyle.Render(t(m.loc, "scrollback.hidden"))
}

// scrollbackLines resolves the rendered-row cap for the viewport from
// NIF_TUI_SCROLLBACK (0 disables the cap, restoring the old
// render-everything behavior). The default bounds per-token work even in
// very long sessions; the stored transcript is unaffected.
func scrollbackLines() int {
	if raw := strings.TrimSpace(os.Getenv("NIF_TUI_SCROLLBACK")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return max(0, n)
		}
	}
	return defaultScrollbackLines
}
