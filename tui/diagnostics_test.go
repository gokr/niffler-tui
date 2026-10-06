package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDiagnosticsLiveAndReplayLevels(t *testing.T) {
	const path = "tests/t_vars.nim"
	const text = "6 diagnostics\nerror one\nerror two\nerror three\nerror four\nerror five\nerror six"
	var event sessionEvent
	if err := json.Unmarshal([]byte(`{"sessionId":"console","path":"tests/t_vars.nim","text":"6 diagnostics\nerror one\nerror two\nerror three\nerror four\nerror five\nerror six"}`), &event); err != nil {
		t.Fatal(err)
	}
	live := newTestModel()
	live.applySessionEvent(sessionEventMsg{kind: "diagnostics", event: event})
	replay := newTestModel()
	replay.blocks = replayConversation([]storedMessage{{Role: "user", Content: messageContent("[lsp diagnostics for " + path + " — asynchronously delivered after your edit, when the server answered]\n" + text)}})
	if len(live.blocks) != 1 || len(replay.blocks) != 1 || live.blocks[0].kind != blockDiagnostics || replay.blocks[0].kind != blockDiagnostics {
		t.Fatal("diagnostics must be distinct blocks live and on replay")
	}
	for _, level := range []toolLevel{toolOff, toolBrief, toolMedium, toolFull} {
		live.toolLevel, replay.toolLevel = level, level
		got := ansi.Strip(live.piece(0))
		if got != ansi.Strip(replay.piece(0)) {
			t.Fatalf("live/replay mismatch at %s", level)
		}
		if level == toolOff {
			if got != "" {
				t.Fatal("off must hide diagnostics")
			}
			continue
		}
		if !strings.Contains(got, "diagnostics("+path+")") {
			t.Fatalf("missing path label: %q", got)
		}
		if (level != toolBrief) != strings.Contains(got, "error one") {
			t.Fatalf("wrong body at %s: %q", level, got)
		}
		if (level == toolFull) != strings.Contains(got, "error six") {
			t.Fatalf("wrong truncation at %s: %q", level, got)
		}
		if (level == toolMedium) != strings.Contains(got, "more lines") {
			t.Fatalf("wrong expand hint at %s: %q", level, got)
		}
	}
}

func TestDiagnosticsColors(t *testing.T) {
	for _, tc := range []struct{ text, color string }{
		{"f.go: no diagnostics — clean", currentTheme.barOK},
		{"f.go:3:2  error  undeclared name", currentTheme.error},
		{"f.go: 2 diagnostics (1 errors), none in the changed range — the lsp tool lists them all.", currentTheme.error},
		{"f.go:3:2  warning  unused name", currentTheme.barWarn},
		{"f.go:3:2  warning  the word error in a warning", currentTheme.barWarn},
		{"f.go: [E_LSP_TIMEOUT] no diagnostics yet", currentTheme.barWarn},
	} {
		if got := diagnosticsColor(tc.text); got != tc.color {
			t.Fatalf("color for %q = %s, want %s", tc.text, got, tc.color)
		}
	}
}

func TestDiagnosticsIgnoreMetadataOnlyAndDoNotGroup(t *testing.T) {
	m := newTestModel()
	m.appendToolCall(toolCall{name: "edit"})
	m.appendDiagnostics("f.go", "")
	if len(m.blocks) != 1 {
		t.Fatal("old metadata-only event created an empty block")
	}
	m.appendDiagnostics("f.go", "clean")
	m.appendToolCall(toolCall{name: "read"})
	if len(m.blocks) != 3 || m.blocks[1].kind != blockDiagnostics {
		t.Fatal("diagnostics grouped with actual tool calls")
	}
	for _, content := range []string{"hello", "[lsp diagnostics for f.go]\nhello", "[lsp diagnostics for  — asynchronously delivered after your edit, when the server answered]\nhello"} {
		if _, _, ok := storedDiagnostics(content); ok {
			t.Fatalf("recognized non-wrapper %q", content)
		}
	}
}
