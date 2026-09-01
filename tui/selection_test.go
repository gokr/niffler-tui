package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSelectedScreenTextHandlesANSIAndWideGraphemes(t *testing.T) {
	m := newTestModel()
	m.selection = mouseSelection{
		anchor:  selectionPoint{x: 1, y: 0},
		focus:   selectionPoint{x: 1, y: 1}, // second cell of 第
		dragged: true,
	}
	content := "\x1b[31mhello world\x1b[0m\n第二 line"
	if got, want := m.selectedScreenText(content), "ello world\n第"; got != want {
		t.Fatalf("selected text = %q, want %q", got, want)
	}

	// Reverse drags produce the same ordered selection.
	m.selection.anchor, m.selection.focus = m.selection.focus, m.selection.anchor
	if got, want := m.selectedScreenText(content), "ello world\n第"; got != want {
		t.Fatalf("reverse selected text = %q, want %q", got, want)
	}
}

func TestApplyMouseSelectionPreservesRenderedText(t *testing.T) {
	m := newTestModel()
	m.selection = mouseSelection{
		anchor:  selectionPoint{x: 1, y: 0},
		focus:   selectionPoint{x: 3, y: 0},
		dragged: true,
	}
	content := "a\x1b[32mbcde\x1b[0mf"
	rendered := m.applyMouseSelection(content)
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("selection has no reverse-video styling: %q", rendered)
	}
	if got, want := ansi.Strip(rendered), ansi.Strip(content); got != want {
		t.Fatalf("selection changed rendered text: %q, want %q", got, want)
	}
}

func TestMouseDragSelectsWithoutTogglingToolCard(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.layout()
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":"make"}`)})
	m.syncViewport(true)
	if !m.blocks[0].run.collapsed {
		t.Fatal("fresh tool card should be collapsed")
	}

	updated, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	updated, _ = m.Update(tea.MouseMotionMsg{X: 4, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	updated, cmd := m.Update(tea.MouseReleaseMsg{X: 4, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("drag selection did not produce a clipboard command")
	}
	if !m.blocks[0].run.collapsed {
		t.Fatal("drag selection toggled the tool card")
	}
	if _, ok := m.selection.bounds(); !ok {
		t.Fatal("drag selection was not retained for highlighting")
	}
}

func TestPlainMouseClickStillTogglesToolCard(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.layout()
	m.appendToolCall(toolCall{name: "bash"})
	m.syncViewport(true)

	updated, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	updated, _ = m.Update(tea.MouseReleaseMsg{X: 0, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	if m.blocks[0].run.collapsed {
		t.Fatal("plain click did not expand the tool card")
	}
}

func TestMouseWheelClearsScreenSelection(t *testing.T) {
	m := newTestModel()
	m.selection = mouseSelection{
		anchor:  selectionPoint{x: 0, y: 0},
		focus:   selectionPoint{x: 2, y: 0},
		dragged: true,
	}
	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = updated.(model)
	if _, ok := m.selection.bounds(); ok {
		t.Fatal("wheel left a stale screen-coordinate selection active")
	}
}
