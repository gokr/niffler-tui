package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryLiveReplayAndDedup(t *testing.T) {
	a := memoryActivity{ID: "recall-1", Operation: "recall", Phase: "applied", Injected: true, Text: "[deps.manager] Use pnpm"}
	raw, _ := json.Marshal(a)
	live := newTestModel()
	live.applySessionEvent(sessionEventMsg{kind: "memory", event: sessionEvent{MemoryID: a.ID, Operation: a.Operation, Phase: a.Phase, Injected: true, Text: a.Text}})
	if live.busy {
		t.Fatal("idle memory activity must not latch busy")
	}
	live.appendMemory(a)
	if len(live.blocks) != 1 || live.blocks[0].kind != blockMemory {
		t.Fatalf("live/dedup: %#v", live.blocks)
	}
	replay := replayConversation([]storedMessage{{Role: "user", Content: messageContent(a.Text), Memory: raw}})
	if len(replay) != 1 || replay[0].kind != blockMemory || replay[0].text != live.blocks[0].text {
		t.Fatal("replay diverged from live memory")
	}
	merged := dedupMemoryBlocks(append(replay, live.blocks...))
	if len(merged) != 1 {
		t.Fatal("history arriving after live event duplicated memory")
	}
	for _, level := range []toolLevel{toolOff, toolBrief, toolMedium, toolFull} {
		live.toolLevel = level
		text := live.renderMemory(&live.blocks[0])
		if !strings.Contains(text, "◇ Memory") || !strings.Contains(text, "applied to context") {
			t.Fatalf("memory disappeared or lost attribution: %q", text)
		}
	}
}

func TestMemorySearchAndFallbackNeverClaimInjection(t *testing.T) {
	m := newTestModel()
	m.appendMemory(memoryActivity{ID: "search", Operation: "search", Phase: "completed", Result: json.RawMessage(`{"ranker":"text","count":0,"warning":"embedding unavailable"}`)})
	text := m.renderMemory(&m.blocks[0])
	if !strings.Contains(text, "fallback/warning") {
		t.Fatal("fallback hidden")
	}
	if !strings.Contains(text, "0 matches") || strings.Contains(text, "applied to context") {
		t.Fatal(text)
	}
	m.appendMemory(memoryActivity{ID: "refused", Operation: "remember", Phase: "refused", Subject: "build.jobs"})
	if !strings.Contains(m.renderMemory(&m.blocks[1]), "refused") {
		t.Fatal("refusal rendered as saved")
	}
}
