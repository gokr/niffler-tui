// Scrollback windowing and streamed repaint coalescing: the transcript keeps
// only the tail of the rendered conversation in the viewport, and token
// frames are batched into ~30fps flushes, so per-token cost stays flat no
// matter how long a session runs (previously every token re-rendered and
// rescanned the whole transcript, which slowed to a crawl).
package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestScrollbackWindowCapsRenderedTranscript(t *testing.T) {
	m := newTestModel()
	m.scrollback = 10
	m.pieceEpoch = 1
	for i := 0; i < 20; i++ {
		m.addBlock(blockUser, fmt.Sprintf("line %d", i))
	}
	m.syncViewport(true)

	out := m.renderTranscript()
	if m.renderFrom == 0 {
		t.Fatalf("renderFrom = 0, want a windowed transcript")
	}
	if strings.Contains(out, "line 9") || strings.Contains(out, "line 0") {
		t.Fatalf("window leaked older blocks:\n%s", out)
	}
	if !strings.Contains(out, "line 19") {
		t.Fatalf("window dropped the tail block:\n%s", out)
	}
	if !strings.Contains(out, "earlier messages hidden") {
		t.Fatalf("missing scrollback marker:\n%s", out)
	}

	// Hit-testing follows the window: row 0 is the marker (no block), and
	// the first visible block sits two rows below it (marker + blank line).
	if idx := m.blockAtContentLine(0); idx != -1 {
		t.Fatalf("marker row mapped to block %d, want -1", idx)
	}
	if idx := m.blockAtContentLine(2); idx != m.renderFrom {
		t.Fatalf("first visible row mapped to block %d, want %d", idx, m.renderFrom)
	}
	if idx := m.blockAtContentLine(4); idx != m.renderFrom+1 {
		t.Fatalf("second visible row mapped to block %d, want %d", idx, m.renderFrom+1)
	}
}

func TestScrollbackUnlimitedKeepsWholeTranscript(t *testing.T) {
	m := newTestModel()
	m.scrollback = 0
	m.pieceEpoch = 1
	for i := 0; i < 20; i++ {
		m.addBlock(blockUser, fmt.Sprintf("line %d", i))
	}
	m.syncViewport(true)
	out := m.renderTranscript()
	if m.renderFrom != 0 {
		t.Fatalf("renderFrom = %d, want 0 without a cap", m.renderFrom)
	}
	if !strings.Contains(out, "line 0") || !strings.Contains(out, "line 19") {
		t.Fatalf("unlimited transcript lost blocks:\n%s", out)
	}
	if strings.Contains(out, "earlier messages hidden") {
		t.Fatalf("unlimited transcript got a truncation marker:\n%s", out)
	}
}

// TestPieceCacheInvalidatedByDisplayFlags guards the cache key: changing a
// display flag directly (not via the epoch) must re-render affected blocks.
func TestPieceCacheInvalidatedByDisplayFlags(t *testing.T) {
	m := newTestModel()
	m.pieceEpoch = 1
	m.addBlock(blockThinking, "deep reasoning")

	if got := m.piece(0); !strings.Contains(got, "deep reasoning") {
		t.Fatalf("full thinking piece = %q", got)
	}
	m.thinkLevel = thinkBrief
	if got := m.piece(0); !strings.Contains(got, "thinking…") || strings.Contains(got, "deep reasoning") {
		t.Fatalf("brief thinking piece = %q", got)
	}

	m.thinkLevel = thinkOff
	if got := m.piece(0); got != "" {
		t.Fatalf("hidden thinking piece = %q", got)
	}

	// Tool level likewise (card runs only; legacy tool lines always render).
	m.appendToolCall(toolCall{name: "bash"})
	m.toolLevel = toolOff
	if got := m.piece(1); got != "" {
		t.Fatalf("hidden tool card piece = %q", got)
	}
	m.toolLevel = toolBrief
	if got := m.piece(1); !strings.Contains(got, "bash") {
		t.Fatalf("visible tool card piece = %q", got)
	}
}

// TestStreamedViewportFlushCoalescing covers the repaint batching: the first
// token schedules one flush, later tokens ride along, and the tick performs
// the pending sync exactly once.
func TestStreamedViewportFlushCoalescing(t *testing.T) {
	m := newTestModel()
	m.session = "console"

	if cmd := m.scheduleViewportFlush(); cmd == nil {
		t.Fatal("first flush did not schedule a tick")
	}
	if !m.flushPending || !m.flushTimerActive {
		t.Fatalf("flush state = pending:%v timer:%v", m.flushPending, m.flushTimerActive)
	}
	if cmd := m.scheduleViewportFlush(); cmd != nil {
		t.Fatal("second flush scheduled a redundant tick")
	}

	updated, _ := m.Update(viewportFlushMsg{})
	got := updated.(model)
	if got.flushPending || got.flushTimerActive {
		t.Fatalf("flush tick left state = pending:%v timer:%v", got.flushPending, got.flushTimerActive)
	}

	// A tick with nothing pending is a no-op.
	updated, _ = got.Update(viewportFlushMsg{})
	got = updated.(model)
	if got.flushPending || got.flushTimerActive {
		t.Fatalf("idle tick changed state = pending:%v timer:%v", got.flushPending, got.flushTimerActive)
	}
}

// TestTokenEventsDoNotSyncViewportImmediately guards the hot path: a token
// frame must only mark content pending, not rehearse the full window sync.
func TestTokenEventsDoNotSyncViewportImmediately(t *testing.T) {
	m := newTestModel()
	m.session = "console"
	m.addBlock(blockAssistant, "")
	m.assistantIdx = 0

	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "console", Content: "hello",
	}})
	if m.blocks[0].text != "hello" {
		t.Fatalf("token not applied: %q", m.blocks[0].text)
	}
	if !m.flushPending {
		t.Fatal("token did not mark a pending flush")
	}
	if m.viewportContent != "" {
		t.Fatalf("token synced the viewport synchronously: %q", m.viewportContent)
	}
}

func TestScrollbackLinesFromEnv(t *testing.T) {
	t.Setenv("NIF_TUI_SCROLLBACK", "")
	if got := scrollbackLines(); got != defaultScrollbackLines {
		t.Fatalf("default scrollback = %d, want %d", got, defaultScrollbackLines)
	}
	t.Setenv("NIF_TUI_SCROLLBACK", "1234")
	if got := scrollbackLines(); got != 1234 {
		t.Fatalf("env scrollback = %d, want 1234", got)
	}
	t.Setenv("NIF_TUI_SCROLLBACK", "0")
	if got := scrollbackLines(); got != 0 {
		t.Fatalf("disabled scrollback = %d, want 0", got)
	}
	t.Setenv("NIF_TUI_SCROLLBACK", "nonsense")
	if got := scrollbackLines(); got != defaultScrollbackLines {
		t.Fatalf("invalid scrollback = %d, want default", got)
	}
}

// BenchmarkStreamedToken measures the full per-token Update (streaming a
// token through the model) as the session grows. With the scrollback window
// the cost must stay flat; without it (scrollback=0) it grows linearly —
// that regression is what this guards.
func BenchmarkStreamedToken(b *testing.B) {
	for _, scrollback := range []int{defaultScrollbackLines, 0} {
		m := streamBenchModel(600, scrollback)
		b.Run(fmt.Sprintf("scrollback=%d", scrollback), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				updated, _ := m.Update(sessionEventMsg{kind: "token", event: sessionEvent{
					SessionID: "console", Content: "token ",
				}})
				m = updated.(model)
				_ = m.View()
				// Simulate the flush tick arriving after a few tokens (the real
				// tick is armed by the first token and fires at ~30fps).
				if i%4 == 0 {
					m.flushTimerActive = false
					if m.flushPending {
						m.flushPending = false
						m.syncViewport(false)
					}
				}
			}
		})
	}
}

func streamBenchModel(rounds, scrollback int) model {
	m := newTestModel()
	m.session = "console"
	m.width, m.height = 120, 40
	m.scrollback = scrollback
	m.pieceEpoch = 1
	m.layout()
	for i := 0; i < rounds; i++ {
		m.blocks = append(m.blocks,
			transcriptBlock{kind: blockUser, text: fmt.Sprintf("user message %d: please look at this file and explain what it does in detail", i)},
			transcriptBlock{kind: blockThinking, finalized: true, text: strings.Repeat("reasoning about the code and its implications. ", 40)},
			transcriptBlock{kind: blockAssistant, finalized: true, text: strings.Repeat("Here is a detailed explanation of the code. ", 60)},
			transcriptBlock{kind: blockTool, run: &toolRun{collapsed: true, calls: []toolCall{{name: "read", args: []byte(`{"path":"main.go"}`), result: []byte(`"package main\n..."`)}}}},
		)
	}
	m.blocks = append(m.blocks, transcriptBlock{kind: blockAssistant})
	m.markTranscriptDirty()
	m.syncViewport(true)
	return m
}
