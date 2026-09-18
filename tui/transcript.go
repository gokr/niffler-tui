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
	"time"

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

// thinkRenderInterval caps how often the active thinking block's rendering
// is recomputed while tokens stream: only its tail chunk changes per frame,
// and glamour is too expensive to pay at 30fps regardless of chunk size.
// Staleness is bounded by the interval and invisible in practice.
const thinkRenderInterval = 150 * time.Millisecond

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
	blockNotice
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

	// thinking chunk cache (see renderThinkingChunks): fence-aware paragraphs
	// with cached glamour renders, valid for one (width, epoch) pair. A long
	// reasoning stream must never re-render the whole block: glamour costs
	// ~6.5µs/byte (measured 5KB≈31ms, 50KB≈330ms), which would stall the UI.
	chunks     []thinkRenderChunk
	chunkW     int
	chunkEpoch int
	chunkOK    bool
}

// pieceKey captures every non-text input a block's rendering depends on, so
// a cache hit needs only a struct comparison: the model's display epoch
// (theme/renderer rebuilds), the viewport width (clampLines), and the
// display flags that change rendering without touching block text.
// Streaming is included per block: only the active (unfinalized) assistant
// block renders as plain text until it settles (see renderBlock), so settled
// blocks keep their cached pieces across stream/settle flips instead of
// being re-rendered wholesale on every flip.
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
//
// Only the ACTIVE streaming block — the unfinalized assistant tail — renders
// as plain text. Keying this off the global streaming flag used to restyle
// the whole transcript (plain ↔ markdown) on every flip and re-render every
// block through glamour; that was the perceived "markdown and color
// flicker" plus a stall on each flip.
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
	// The active assistant block stays plain while tokens stream: glamour
	// costs ~6.5µs/byte, so a large answer only gets one markdown render
	// at settle. Edge newlines are trimmed (models open content with blank
	// lines, and reasoning-adjacent answers start with them); they would
	// stack onto the block separator into walls of empty space.
	if block.kind == blockAssistant && m.streaming && !block.finalized {
		return strings.Trim(text, "\n\r")
	}
	out := strings.Trim(text, "\n\r")
	if block.kind == blockThinking {
		// Thinking renders through the chunked incremental path in both
		// modes, so reasoning looks final while it streams instead of
		// flipping styles when it completes.
		if rendered, ok := m.renderThinkingChunks(block, text); ok {
			out = rendered
		} else {
			// No markdown renderer: keep the pre-markdown dim italic look.
			out = thinkingStyle.Render(out)
		}
	} else if m.renderer != nil {
		if rendered, err := m.renderer.Render(text); err == nil {
			out = strings.Trim(rendered, "\n")
		}
	}
	block.rendered = out
	block.renderedText = block.text
	block.renderedOK = true
	return out
}

// noticeLines renders a notice block's text as a collapsed machinery row:
// the head line (e.g. "[subagent agent-… done]") takes the ▸ prefix, the
// bounded summary beneath it is indented, so a multi-line notice still reads
// as one attributed row rather than pasted user text.
func noticeLines(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "▸ notice"
	}
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString("▸ ")
		} else {
			b.WriteString("  ")
		}
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
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
	case blockNotice:
		// Settlement/wake machinery folded in by the runner (subagent done,
		// process exited, an autonomous wake turn's prompt): dim and
		// prefixed so it reads as a system row, never as something the human
		// typed. The head line names what happened; the bounded summary the
		// runner attached follows it.
		return noticeStyle.Render(noticeLines(block.text))
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
		streaming: m.streaming && !block.finalized && block.kind == blockAssistant,
	}
	if block.pieceOK && block.pieceKey == key {
		return block.piece
	}
	// The active thinking block re-renders its tail chunk per frame while
	// tokens stream; cap that rate so a dense burst cannot pay for glamour
	// more often than thinkRenderInterval (staleness ≤ the interval).
	if block.kind == blockThinking && m.streaming && !block.finalized &&
		block.pieceOK && time.Since(m.lastThinkRender) < thinkRenderInterval {
		return block.piece
	}
	out := clampLines(m.renderPiece(i), m.viewport.Width())
	block.piece = out
	block.pieceKey = key
	block.pieceOK = true
	if block.kind == blockThinking {
		m.lastThinkRender = time.Now()
	}
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

// thinkRenderChunk is one fence-aware paragraph of a thinking block with its
// cached glamour rendering.
type thinkRenderChunk struct {
	text     string
	rendered string
}

// splitThinkingChunks splits reasoning text into glamour-sized chunks at
// blank lines outside fenced code. Fences stay intact (a blank line inside
// ``` is not a boundary), and a blank line between two list items does not
// split either — glamour numbers lists per render, so splitting one would
// restart every item at "1.". The split is prefix-stable under appends,
// which is what makes per-chunk caching work while tokens stream.
func splitThinkingChunks(text string) []string {
	lines := strings.Split(text, "\n")
	isList := func(s string) bool {
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		for _, p := range []string{"- ", "* ", "+ "} {
			if strings.HasPrefix(s, p) {
				return true
			}
		}
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') &&
			i+1 < len(s) && s[i+1] == ' '
	}
	var chunks []string
	var cur []string
	inFence := false
	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				flush()
				inFence = true
			}
			cur = append(cur, line)
			if len(cur) > 1 {
				// Closing fence: the fence chunk is complete.
				flush()
				inFence = false
			}
			continue
		}
		if !inFence && trimmed == "" {
			// Blank line outside a fence: a chunk boundary, unless the text
			// on both sides continues a list (see above).
			prevList := len(cur) > 0 && isList(cur[len(cur)-1])
			nextList := false
			for j := i + 1; j < len(lines); j++ {
				if t := strings.TrimSpace(lines[j]); t != "" {
					nextList = isList(t)
					break
				}
			}
			if !(prevList && nextList) {
				flush()
				continue
			}
			cur = append(cur, line)
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return chunks
}

// renderThinkingChunks renders a thinking block through the thinking
// renderer one chunk at a time, reusing cached renders for every chunk that
// did not change. Glamour costs ~6.5µs/byte on this class of hardware
// (measured: 5KB≈31ms, 20KB≈126ms, 50KB≈330ms), so re-rendering a growing
// reasoning block whole per token flush would stall the UI; chunks bound
// each frame's render to the new tail material only.
func (m *model) renderThinkingChunks(block *transcriptBlock, text string) (string, bool) {
	if m.thinkingRenderer == nil {
		return "", false
	}
	width := m.viewport.Width()
	if !block.chunkOK || block.chunkW != width || block.chunkEpoch != m.pieceEpoch {
		block.chunks = nil
		block.chunkOK = true
		block.chunkW = width
		block.chunkEpoch = m.pieceEpoch
	}
	parts := splitThinkingChunks(text)
	rendered := make([]string, len(parts))
	for i, p := range parts {
		if i < len(block.chunks) && block.chunks[i].text == p {
			rendered[i] = block.chunks[i].rendered
			continue
		}
		// Reasoning is prose, and a stray "~" in it pairs with the next one
		// on the line into GFM strikethrough (goldmark accepts single-tilde
		// delimiters), eating the tildes and striking the span between them.
		// Escape them in prose chunks; fenced chunks keep theirs (code blocks
		// never process emphasis, and a backslash there would show).
		renderInput := p
		if !strings.HasPrefix(p, "```") {
			renderInput = strings.ReplaceAll(p, "~", "\\~")
		}
		out, err := m.thinkingRenderer.Render(renderInput)
		if err != nil {
			rendered[i] = p
		} else {
			rendered[i] = strings.Trim(out, "\n")
		}
		block.chunks = append(block.chunks[:min(i, len(block.chunks))],
			thinkRenderChunk{text: p, rendered: rendered[i]})
	}
	return strings.Join(rendered, "\n\n"), true
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
