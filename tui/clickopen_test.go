package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTokenTargetURLs(t *testing.T) {
	cases := []struct {
		token string
		want  string
	}{
		{"https://example.com/a?b=1", "https://example.com/a?b=1"},
		{"http://example.com", "http://example.com"},
		// Sentence punctuation glues onto the token but is not part of it.
		{"https://example.com/a.", "https://example.com/a"},
		{"https://example.com/a,", "https://example.com/a"},
		{"(https://example.com/a)", "https://example.com/a"},
		{"\"https://example.com/a\"", "https://example.com/a"},
		{"<https://example.com/a>", "https://example.com/a"},
		{"www.example.com/docs", "https://www.example.com/docs"},
	}
	for _, tc := range cases {
		got, isURL, ok := tokenTarget(tc.token)
		if !ok || !isURL || got != tc.want {
			t.Fatalf("tokenTarget(%q) = (%q, %v, %v), want (%q, true, true)", tc.token, got, isURL, ok, tc.want)
		}
	}
}

func TestTokenTargetPaths(t *testing.T) {
	cases := []struct {
		token string
		want  string
	}{
		{"components/foo/main.go:1657", "components/foo/main.go"},
		{"components/foo/main.go:1657:12", "components/foo/main.go"},
		{"components/foo/main.go:1657.", "components/foo/main.go"},
		{"`tui/main.go:42`", "tui/main.go"},
		{"/abs/path/file.txt", "/abs/path/file.txt"},
		{"~/notes/a.txt", "~/notes/a.txt"},
		{"./scripts/run.sh:10", "./scripts/run.sh"},
		{"main.go", "main.go"},
		{"file:///home/g/a.txt", "/home/g/a.txt"},
	}
	for _, tc := range cases {
		got, isURL, ok := tokenTarget(tc.token)
		if !ok || isURL || got != tc.want {
			t.Fatalf("tokenTarget(%q) = (%q, %v, %v), want (%q, false, true)", tc.token, got, isURL, ok, tc.want)
		}
	}
}

func TestTokenTargetRejects(t *testing.T) {
	for _, token := range []string{
		"", "make", "hello world", "3.14", "v0.4.1", "elapsed 0s",
		"user@example.com", "12:34", "2026-07-30T12:00:00Z",
		"ftp://example.com/file", "*.go", "std::vector<int>::push_back",
	} {
		if _, _, ok := tokenTarget(token); ok {
			t.Fatalf("tokenTarget(%q) accepted, want rejected", token)
		}
	}
}

func TestScreenTokenAt(t *testing.T) {
	// Styled line: the token boundary is on the plain text, styling ignored.
	content := "\x1b[36msee \x1b[1mhttps://example.com/a.\x1b[0m next"
	// "see " occupies cells 0-3; the URL starts at cell 4.
	if got := screenTokenAt(content, 0, 0); got != "see" {
		t.Fatalf("screenTokenAt at 0 = %q, want %q", got, "see")
	}
	if got := screenTokenAt(content, 0, 7); got != "https://example.com/a." {
		t.Fatalf("screenTokenAt mid-URL = %q", got)
	}
	// A blank cell and an out-of-range row yield nothing.
	if got := screenTokenAt(content, 0, 3); got != "" {
		t.Fatalf("screenTokenAt on space = %q, want empty", got)
	}
	if got := screenTokenAt(content, 9, 0); got != "" {
		t.Fatalf("screenTokenAt out of range = %q, want empty", got)
	}

	// Wide glyphs: clicking either cell of 第 yields the whole grapheme run.
	wide := "第二 line"
	if got := screenTokenAt(wide, 0, 1); got != "第二" {
		t.Fatalf("screenTokenAt mid-glyph = %q, want %q", got, "第二")
	}
	// 第二 fills cells 0-3; cell 4 is the space, cell 5 starts "line".
	if got := screenTokenAt(wide, 0, 4); got != "" {
		t.Fatalf("screenTokenAt on the separator = %q, want empty", got)
	}
	if got := screenTokenAt(wide, 0, 5); got != "line" {
		t.Fatalf("screenTokenAt after glyph = %q, want %q", got, "line")
	}
}

func TestResolveOpenPathPrefersExistingBase(t *testing.T) {
	m := newTestModel()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.cwd = base

	if got := m.resolveOpenPath("a.txt"); got != filepath.Join(base, "a.txt") {
		t.Fatalf("resolveOpenPath = %q, want the existing cwd-joined path", got)
	}
	// Unresolvable: the first candidate is passed through unchanged.
	if got := m.resolveOpenPath("nope.txt"); got != filepath.Join(base, "nope.txt") {
		t.Fatalf("resolveOpenPath missing = %q, want the first candidate", got)
	}
	// Absolute paths are never rewritten.
	if got := m.resolveOpenPath("/etc/hosts"); got != "/etc/hosts" {
		t.Fatalf("resolveOpenPath absolute = %q, want unchanged", got)
	}
}

func TestCtrlClickDoesNotToggleToolCard(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.layout()
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":"make"}`)})
	m.syncViewport(true)
	if !m.blocks[0].run.collapsed {
		t.Fatal("fresh tool card should be collapsed")
	}

	updated, _ := m.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	m = updated.(model)
	updated, _ = m.Update(tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	m = updated.(model)
	if !m.blocks[0].run.collapsed {
		t.Fatal("ctrl+click toggled the tool card — the open gesture must be reserved")
	}
	if m.contextNote == "" {
		t.Fatal("ctrl+click on nothing should say so instead of failing silently")
	}

	// The same click without ctrl still toggles.
	updated, _ = m.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	updated, _ = m.Update(tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	m = updated.(model)
	if m.blocks[0].run.collapsed {
		t.Fatal("plain click did not expand the tool card")
	}
}

func TestOpenUnderPointerExtractsTarget(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.layout()
	m.addBlock(blockMeta, "read https://example.com/docs first")
	m.syncViewport(true)

	// Find the row the URL rendered on (layout internals stay out of the
	// assertion); clicking cell 8 lands inside "https://…".
	row := renderedRowContaining(m.View().Content, "https://example.com/docs")
	if row < 0 {
		t.Fatal("URL not found in the rendered screen")
	}
	cmd := m.openUnderPointer(8, row)
	if cmd == nil {
		t.Fatal("ctrl+click on a URL produced no open command")
	}
	// The command only starts the opener; executing it here would spawn
	// xdg-open, so the assertion stays on the command's existence.

	// Nothing openable: a note, no command.
	m2 := newTestModel()
	m2.width = 80
	m2.height = 40
	m2.layout()
	m2.addBlock(blockMeta, "just words here")
	m2.syncViewport(true)
	row = renderedRowContaining(m2.View().Content, "just words here")
	if cmd := m2.openUnderPointer(2, row); cmd != nil {
		t.Fatal("ctrl+click on a bare word produced an open command")
	}
}

// renderedRowContaining returns the first screen row of rendered screen
// text containing s, or -1.
func renderedRowContaining(content, s string) int {
	for row, line := range strings.Split(content, "\n") {
		if strings.Contains(ansi.Strip(line), s) {
			return row
		}
	}
	return -1
}
