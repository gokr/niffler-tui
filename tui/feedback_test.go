package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestBatchedReadHeading(t *testing.T) {
	m := newTestModel()
	for _, tc := range []struct{ args, want string }{
		{`{"reads":[{"path":"a.go"},{"path":"b.go"}]}`, "read 2 items"},
		{`{"reads":[{"path":"a.go"}]}`, "read a.go"},
		{`{"reads":[{"glob":"**/*.go"}]}`, "read **/*.go"},
		{`{"path":"a.go"}`, "read a.go"},
	} {
		c := &toolCall{name: "read", args: json.RawMessage(tc.args)}
		if got := ansi.Strip(m.renderReadPreview(c, false).head); got != tc.want {
			t.Fatalf("heading = %q, want %q", got, tc.want)
		}
	}
}

func TestSteerClearsOnlyOnModelDispatch(t *testing.T) {
	m := newTestModel()
	m.addBlock(blockUser, "Steer: next")
	m.addBlock(blockUser, "Steer: later")
	m.applySessionEvent(sessionEventMsg{kind: "steer", event: sessionEvent{Content: "next"}})
	if m.blocks[0].text != "Steer: next" {
		t.Fatal("folding is not model dispatch")
	}
	m.applySessionEvent(sessionEventMsg{kind: "steer_sent", event: sessionEvent{Contents: []string{"next"}}})
	if m.blocks[0].text != "next" || m.blocks[1].text != "Steer: later" {
		t.Fatal("wrong pending steer acknowledged")
	}
}

func TestCtrlHoverUsesClickClassifier(t *testing.T) {
	m := newTestModel()
	m.mouse = true
	m.hoverCtrl = true
	m.hover = selectionPoint{x: 5, y: 0}
	for _, text := range []string{"see https://example.com now", "see src/a.go now", "see 漢字/a.go now"} {
		got := m.applyLinkHover(text)
		if !strings.Contains(got, "\x1b[4m") || ansi.Strip(got) != text {
			t.Fatalf("hover failed for %q: %q", text, got)
		}
	}
	if got := m.applyLinkHover("see plainword now"); got != "see plainword now" {
		t.Fatal("non-target highlighted")
	}
	m.hoverCtrl = false
	if got := m.applyLinkHover("see src/a.go now"); strings.Contains(got, "\x1b[4m") {
		t.Fatal("unmodified hover highlighted")
	}
	updated, _ := m.Update(tea.MouseMotionMsg{X: 5, Y: 0, Mod: tea.ModCtrl})
	m = updated.(model)
	if !m.hoverCtrl {
		t.Fatal("Ctrl motion not tracked")
	}
	updated, _ = m.Update(tea.MouseMotionMsg{X: 5, Y: 0})
	if updated.(model).hoverCtrl {
		t.Fatal("hover not cleared")
	}
}

func TestCleanDiagnosticReplay(t *testing.T) {
	blocks := replayConversation([]storedMessage{{Role: "diagnostic", Name: "a.go", Content: "a.go: no diagnostics — clean."}})
	if len(blocks) != 1 || blocks[0].kind != blockDiagnostics {
		t.Fatal("clean diagnostic not replayed as UI-only block")
	}
}
