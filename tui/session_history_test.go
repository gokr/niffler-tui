package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// storedAssistant builds one persisted assistant message with optional
// reasoning/content and tool calls in the normalized OpenAI shape.
func storedAssistant(content, reasoning string, calls ...storedToolCall) storedMessage {
	return storedMessage{Role: "assistant", Content: content, Reasoning: reasoning, ToolCalls: calls}
}

func storedCall(id, name, arguments string) storedToolCall {
	var call storedToolCall
	call.ID = id
	call.Function.Name = name
	call.Function.Arguments = arguments
	return call
}

func storedTool(id, name, content string) storedMessage {
	return storedMessage{Role: "tool", ToolCallID: id, Name: name, Content: content}
}

func TestReplayConversationPreservesOrderAndKinds(t *testing.T) {
	blocks := replayConversation([]storedMessage{
		{Role: "user", Content: "hello"},
		storedAssistant("hi there", "let me think"),
		{Role: "error", Content: "provider exploded"},
	})

	if len(blocks) != 4 {
		t.Fatalf("blocks = %d, want 4: %+v", len(blocks), blocks)
	}
	if blocks[0].kind != blockUser || blocks[0].text != "hello" {
		t.Fatalf("user block = %+v", blocks[0])
	}
	// Reasoning renders before the answer of the same round, like the live
	// token stream (reasoning deltas arrive first).
	if blocks[1].kind != blockThinking || blocks[1].text != "let me think" || !blocks[1].finalized {
		t.Fatalf("thinking block = %+v", blocks[1])
	}
	if blocks[2].kind != blockAssistant || blocks[2].text != "hi there" || !blocks[2].finalized {
		t.Fatalf("assistant block = %+v", blocks[2])
	}
	if blocks[3].kind != blockError || blocks[3].text != "provider exploded" {
		t.Fatalf("error block = %+v", blocks[3])
	}
}

func TestReplayConversationToolRuns(t *testing.T) {
	blocks := replayConversation([]storedMessage{
		{Role: "user", Content: "do it"},
		// One round, two parallel calls: both arrive with the assistant
		// message and are completed by the following tool messages.
		storedAssistant("working", "",
			storedCall("call_1", "bash", `{"command": "ls"}`),
			storedCall("call_2", "read", `{"path": "x.go"}`),
		),
		storedTool("call_1", "bash", "(exit 0)\nmain.go"),
		storedTool("call_2", "read", "ERROR: no such file"),
		// A later round with no content folds into the same card, exactly
		// like the live appendToolCall path.
		storedAssistant("", "", storedCall("call_3", "grep", `{"pattern": "x"}`)),
		storedTool("call_3", "grep", "x.go:3: match"),
	})

	var cards []transcriptBlock
	for _, b := range blocks {
		if b.kind == blockTool {
			cards = append(cards, b)
		}
	}
	if len(cards) != 1 {
		t.Fatalf("tool cards = %d, want 1 (consecutive calls share a run): %+v", len(cards), blocks)
	}
	run := cards[0].run
	if run == nil || len(run.calls) != 3 {
		t.Fatalf("run = %+v, want 3 calls", run)
	}
	if run.calls[0].name != "bash" || run.calls[0].pending || string(run.calls[0].args) != `{"command": "ls"}` {
		t.Fatalf("call 1 = %+v", run.calls[0])
	}
	if got := compactJSON(run.calls[0].result); got != "(exit 0)\nmain.go" {
		t.Fatalf("call 1 result = %q", got)
	}
	if run.calls[1].err != "no such file" || run.calls[1].result != nil {
		t.Fatalf("call 2 error = %+v", run.calls[1])
	}
	if run.calls[2].pending || run.calls[2].err != "" {
		t.Fatalf("call 3 = %+v", run.calls[2])
	}
}

func TestReplayConversationOrphanToolAndInvalidArgs(t *testing.T) {
	// A tool message whose call was never replayed (list capped, legacy
	// entry) still shows up as its own completed call, and the next round's
	// call folds into the same run.
	blocks := replayConversation([]storedMessage{
		storedTool("call_orphan", "bash", "output"),
		storedAssistant("", "", storedCall("call_bad", "bash", "{not json")),
	})
	if len(blocks) != 1 || blocks[0].kind != blockTool || len(blocks[0].run.calls) != 2 {
		t.Fatalf("orphan tool not replayed into the run: %+v", blocks)
	}
	if blocks[0].run.calls[0].pending || blocks[0].run.calls[0].name != "bash" {
		t.Fatalf("orphan call = %+v", blocks[0].run.calls[0])
	}
	// Invalid argument JSON yields no args (never a bogus raw fragment).
	if got := blocks[0].run.calls[1].args; got != nil {
		t.Fatalf("invalid args = %q, want nil", got)
	}
	if !blocks[0].run.calls[1].pending {
		t.Fatal("call without a tool message should stay pending")
	}
}

func TestStoredToolArgsAndOutcome(t *testing.T) {
	if got := storedToolArgs("  "); got != nil {
		t.Fatalf("empty args = %q", got)
	}
	if got := storedToolArgs(`{"a": 1}`); string(got) != `{"a": 1}` {
		t.Fatalf("args = %q", got)
	}
	if result, errText := storedToolOutcome("ERROR: boom"); result != nil || errText != "boom" {
		t.Fatalf("error outcome = %v %q", result, errText)
	}
	if result, errText := storedToolOutcome("plain text"); errText != "" || string(result) != "plain text" {
		t.Fatalf("text outcome = %q %q", result, errText)
	}
	if result, errText := storedToolOutcome(""); result != nil || errText != "" {
		t.Fatalf("empty outcome = %q %q", result, errText)
	}
}

// TestConversationHistoryMsgInsertsAtAnchor covers the startup race: the
// "connected" banner exists when the store read starts, and the user may
// have sent a message before it lands. The replay must slot between them.
func TestConversationHistoryMsgInsertsAtAnchor(t *testing.T) {
	m := newTestModel()
	m.session = "console"
	m.historyGen = 1
	m.addBlock(blockMeta, "connected")
	m.addBlock(blockUser, "sent while loading")

	updated, _ := m.Update(conversationHistoryMsg{
		Session: "console", Gen: 1, Anchor: 1,
		Blocks: []transcriptBlock{
			{kind: blockUser, text: "old question", finalized: true},
			{kind: blockAssistant, text: "old answer", finalized: true},
		},
	})
	got := updated.(model)
	if len(got.blocks) != 4 {
		t.Fatalf("blocks = %d, want 4: %+v", len(got.blocks), got.blocks)
	}
	want := []string{"connected", "old question", "old answer", "sent while loading"}
	for i, text := range want {
		if got.blocks[i].text != text {
			t.Fatalf("block %d = %q, want %q", i, got.blocks[i].text, text)
		}
	}
	if got.viewportContent == "" || !strings.Contains(got.viewportContent, "old answer") {
		t.Fatalf("viewport not repainted with the history: %q", got.viewportContent)
	}
}

func TestConversationHistoryMsgStaleDropped(t *testing.T) {
	m := newTestModel()
	m.session = "new"
	m.historyGen = 2
	m.addBlock(blockUser, "current")

	// A reply for an older session or an older generation must not apply.
	for _, msg := range []conversationHistoryMsg{
		{Session: "old", Gen: 2, Blocks: []transcriptBlock{{kind: blockUser, text: "stale session"}}},
		{Session: "new", Gen: 1, Blocks: []transcriptBlock{{kind: blockUser, text: "stale load"}}},
	} {
		updated, _ := m.Update(msg)
		got := updated.(model)
		if len(got.blocks) != 1 || got.blocks[0].text != "current" {
			t.Fatalf("stale history applied: %+v", got.blocks)
		}
	}

	// A store failure surfaces in the status note, not as a block.
	updated, _ := m.Update(conversationHistoryMsg{Session: "new", Gen: 2, Err: errors.New("store unavailable")})
	got := updated.(model)
	if !strings.Contains(got.contextNote, "history unavailable") {
		t.Fatalf("context note = %q", got.contextNote)
	}
	if len(got.blocks) != 1 {
		t.Fatalf("error added blocks: %+v", got.blocks)
	}
}

// TestReplayedBlocksAreFinalized guards the streaming merge: a new turn's
// tokens must open fresh blocks, never append into the replayed transcript.
func TestReplayedBlocksAreFinalized(t *testing.T) {
	m := newTestModel()
	m.session = "console"
	m.historyGen = 1
	updated, _ := m.Update(conversationHistoryMsg{
		Session: "console", Gen: 1,
		Blocks: replayConversation([]storedMessage{
			{Role: "user", Content: "old question"},
			storedAssistant("old answer", "old reasoning"),
		}),
	})
	got := updated.(model)

	got.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "console", Content: "new", Reasoning: "fresh",
	}})
	if len(got.blocks) != 5 {
		t.Fatalf("blocks = %d, want 5 (old 3 + new 2): %+v", len(got.blocks), got.blocks)
	}
	if got.blocks[3].kind != blockThinking || got.blocks[3].text != "fresh" {
		t.Fatalf("new reasoning merged into old: %+v", got.blocks[3])
	}
	if got.blocks[4].kind != blockAssistant || got.blocks[4].text != "new" {
		t.Fatalf("new content merged into old: %+v", got.blocks[4])
	}
}

func TestSwitchSessionWithHistoryResetsAndStamps(t *testing.T) {
	m := newTestModel()
	m.session = "old"
	m.historyGen = 3
	m.addBlock(blockUser, "old conversation")
	m.syncViewport(true)

	got, cmd := m.switchSessionWithHistory("conv-new")
	if cmd == nil {
		t.Fatal("switch returned no history command")
	}
	if got.session != "conv-new" || len(got.blocks) != 0 {
		t.Fatalf("switch state: session=%q blocks=%d", got.session, len(got.blocks))
	}
	if got.historyGen != 4 {
		t.Fatalf("historyGen = %d, want 4", got.historyGen)
	}
	if got.viewportContent != "" {
		t.Fatalf("viewport kept the old transcript: %q", got.viewportContent)
	}
}

// TestConversationHistoryMsgKeepsSelectorMode guards that a replay is only a
// transcript update: arriving while a selector is open (e.g. the user opened
// /sessions right after startup) must not dismiss it.
func TestConversationHistoryMsgKeepsSelectorMode(t *testing.T) {
	m := newTestModel()
	m.session = "console"
	m.historyGen = 1
	m.mode = modeSessions
	m.selector = newSelector("Sessions", sessionSelectorItems(LocaleEN, "console", nil), 80, 20)

	updated, _ := m.Update(conversationHistoryMsg{
		Session: "console", Gen: 1,
		Blocks: []transcriptBlock{{kind: blockUser, text: "old", finalized: true}},
	})
	got := updated.(model)
	if got.mode != modeSessions {
		t.Fatalf("mode = %v, want sessions", got.mode)
	}
	if len(got.blocks) != 1 {
		t.Fatalf("blocks = %+v", got.blocks)
	}
}

func TestSeqPrefixAndTailStart(t *testing.T) {
	if got := seqPrefix(42); got != "000042" {
		t.Fatalf("seqPrefix(42) = %q", got)
	}
	if got := historyTailStart(0); got != "" {
		t.Fatalf("empty history tail start = %q", got)
	}
	if got := historyTailStart(historyPageSize); got != "" {
		t.Fatalf("exactly-full history tail start = %q", got)
	}
	if got := historyTailStart(historyPageSize + 1); got != seqPrefix(2) {
		t.Fatalf("overflow tail start = %q, want %q", got, seqPrefix(2))
	}
	if got := historyTailStart(2500); got != seqPrefix(1501) {
		t.Fatalf("tail start = %q, want %q", got, seqPrefix(1501))
	}
}

func TestLastMessageSeqBinarySearch(t *testing.T) {
	ids := map[int]bool{1: true, 2: true, 5: true, 999: true, 1000: true, 1001: true, 4242: true}
	fetch := func(prefix string, _ int) ([]storedMessage, error) {
		start, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("probe prefix %q is not numeric: %v", prefix, err)
		}
		for n := start; n <= 1_000_000; n++ {
			if ids[n] {
				return []storedMessage{{Role: "user"}}, nil
			}
		}
		return nil, nil
	}
	got, err := lastMessageSeq(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if got != 4242 {
		t.Fatalf("lastMessageSeq = %d, want 4242", got)
	}

	empty := func(string, int) ([]storedMessage, error) { return nil, nil }
	if got, _ := lastMessageSeq(empty); got != 0 {
		t.Fatalf("empty conversation seq = %d, want 0", got)
	}
}
