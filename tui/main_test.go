package main

import (
	"encoding/json"
	"testing"

	"charm.land/bubbles/v2/viewport"
)

func TestSanitizeSessionID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "game", want: "game"},
		{name: "preserves allowed separators", in: "game_dev-2", want: "game_dev-2"},
		{name: "replaces subject punctuation", in: "game.dev/web", want: "game-dev-web"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeSessionID(test.in); got != test.want {
				t.Fatalf("sanitizeSessionID(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestStreamingBlocksStayStable(t *testing.T) {
	m := model{
		session:      "game",
		viewport:     viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		assistantIdx: -1,
		thinkingIdx:  -1,
	}

	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "think ", Content: "Hello",
	}})
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "more", Content: " world",
	}})

	if len(m.blocks) != 2 {
		t.Fatalf("got %d blocks, want thinking + assistant", len(m.blocks))
	}
	if got := m.blocks[0].text; got != "think more" {
		t.Fatalf("thinking block = %q", got)
	}
	if got := m.blocks[1].text; got != "Hello world" {
		t.Fatalf("assistant block = %q", got)
	}

	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "Hello world!",
	}})
	if got := m.blocks[1].text; got != "Hello world!" {
		t.Fatalf("final assistant block = %q", got)
	}
	m.finishTurn("Hello world!", "")
	if len(m.blocks) != 2 {
		t.Fatalf("request reply duplicated the done event: got %d blocks", len(m.blocks))
	}

	m.applySessionEvent(sessionEventMsg{kind: "toolcall", event: sessionEvent{
		SessionID: "game", Tool: "bash", Args: json.RawMessage(`{"cmd":"make"}`),
	}})
	if m.assistantIdx != -1 || m.thinkingIdx != -1 {
		t.Fatal("tool call did not close the current LLM round")
	}
}
