// Per-tool previews (medium/full tool detail) and thinking markdown: bash
// shows its command with the tail of the log and its duration, edit shows a
// colored diff reconstructed from the call arguments, read shows the head of
// the file, and reasoning renders through glamour so code gets syntax
// highlighting while prose keeps the thinking accent.
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// shellResult builds a bash-style result object around output.
func shellResult(t *testing.T, output string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"text": "(exit 0)\n" + output})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestShellPreviewShowsTailAndDuration(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN

	var output strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&output, "line %d\n", i)
	}
	c := &toolCall{
		name:       "bash",
		args:       json.RawMessage(`{"command":"make test"}`),
		result:     shellResult(t, output.String()),
		durationMs: 4200,
	}

	pv := m.renderToolPreview(c, false)
	if !strings.Contains(pv.head, "$ make test") {
		t.Fatalf("shell head = %q", pv.head)
	}
	body := ansi.Strip(strings.Join(pv.body, "\n"))
	for _, want := range []string{"line 10", "line 6", "earlier lines", "exit 0", "Took 4.2s"} {
		if !strings.Contains(body, want) {
			t.Fatalf("preview body missing %q:\n%s", want, body)
		}
	}
	// The tail, not the head: early lines are collapsed away.
	if strings.Contains(body, "line 5") || strings.Contains(body, "line 1 ") {
		t.Fatalf("preview kept the head instead of the tail:\n%s", body)
	}

	// Full detail shows everything without the collapse hint.
	full := ansi.Strip(strings.Join(m.renderToolPreview(c, true).body, "\n"))
	if !strings.Contains(full, "line 1\n") || strings.Contains(full, "earlier lines") {
		t.Fatalf("full shell body = %q", full)
	}
}

func TestShellPreviewErrors(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	c := &toolCall{
		name: "bash",
		args: json.RawMessage(`{"command":"rm -rf /"}`),
		err:  "approval denied",
	}
	pv := m.renderToolPreview(c, false)
	if body := strings.Join(pv.body, "\n"); !strings.Contains(body, "approval denied") {
		t.Fatalf("error not shown: %q", body)
	}
}

func TestEditPreviewRendersDiff(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	result, _ := json.Marshal(map[string]any{
		"text": "Successfully applied 1 edit", "added_lines": 1, "removed_lines": 1,
	})
	c := &toolCall{
		name:   "edit",
		args:   json.RawMessage(`{"path":"tui/main.go","edits":[{"old_string":"old line","new_string":"new line"}]}`),
		result: result,
	}
	pv := m.renderToolPreview(c, false)
	head := ansi.Strip(pv.head)
	if !strings.Contains(head, "edit tui/main.go") || !strings.Contains(head, "+1") || !strings.Contains(head, "-1") {
		t.Fatalf("edit head = %q", head)
	}
	body := ansi.Strip(strings.Join(pv.body, "\n"))
	if !strings.Contains(body, "- old line") || !strings.Contains(body, "+ new line") {
		t.Fatalf("edit diff = %q", body)
	}
}

func TestReadPreviewHeadAndRange(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	c := &toolCall{
		name:   "read",
		args:   json.RawMessage(`{"path":"tui/main.go","offset":10,"limit":5}`),
		result: json.RawMessage(`{"text":"line ten\nline eleven"}`),
	}
	pv := m.renderToolPreview(c, false)
	head := ansi.Strip(pv.head)
	if !strings.Contains(head, "read tui/main.go:10-14") {
		t.Fatalf("read head = %q", head)
	}
	if body := ansi.Strip(strings.Join(pv.body, "\n")); !strings.Contains(body, "line ten") {
		t.Fatalf("read body = %q", body)
	}
}

func TestGenericPreviewUsesResultText(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("match %d", i))
	}
	result, _ := json.Marshal(map[string]any{"text": strings.Join(lines, "\n")})
	c := &toolCall{
		name:   "grep",
		args:   json.RawMessage(`{"pattern":"match"}`),
		result: result,
	}
	pv := m.renderToolPreview(c, false)
	if head := ansi.Strip(pv.head); !strings.Contains(head, "grep") || !strings.Contains(head, "match") {
		t.Fatalf("generic head = %q", head)
	}
	body := ansi.Strip(strings.Join(pv.body, "\n"))
	if !strings.Contains(body, "match 1") || !strings.Contains(body, "match 12") ||
		!strings.Contains(body, "more lines") {
		t.Fatalf("generic preview body = %q", body)
	}
	if strings.Contains(body, "match 20") {
		t.Fatalf("preview should collapse the tail: %q", body)
	}
	full := ansi.Strip(strings.Join(m.renderToolPreview(c, true).body, "\n"))
	if !strings.Contains(full, "match 20") || strings.Contains(full, "more lines") {
		t.Fatalf("generic full body = %q", full)
	}
}

// TestToolDetailLevels covers the level resolution and the per-card click
// bump: brief stays a summary, medium previews, full expands, off hides.
func TestToolDetailLevels(t *testing.T) {
	m := newTestModel()
	for _, tc := range []struct {
		level toolLevel
		want  toolDetail
		ok    bool
	}{
		{toolBrief, detailBrief, true},
		{toolMedium, detailPreview, true},
		{toolFull, detailFull, true},
		{toolOff, 0, false},
	} {
		m.toolLevel = tc.level
		got, ok := m.toolDetail()
		if got != tc.want || ok != tc.ok {
			t.Fatalf("level %v -> (%v,%v), want (%v,%v)", tc.level, got, ok, tc.want, tc.ok)
		}
	}

	// A clicked (expanded) card bumps one step.
	m.toolLevel = toolBrief
	run := &toolRun{collapsed: false, calls: []toolCall{{name: "bash", args: json.RawMessage(`{"command":"ls"}`)}}}
	out := ansi.Strip(m.renderToolRun(run, detailBrief))
	if !strings.Contains(out, "$ ls") {
		t.Fatalf("expanded brief card should show the preview: %q", out)
	}
	m.toolLevel = toolMedium
	out = m.renderToolRun(run, detailPreview)
	if !strings.Contains(ansi.Strip(out), "$ ls") {
		t.Fatalf("expanded medium card should keep the preview: %q", out)
	}
}

var sgrColorRe = regexp.MustCompile(`\x1b\[38;5;(\d+)m`)

func TestThinkingRendersMarkdownWithCodeHighlighting(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.width, m.height = 100, 30
	m.layout()
	m.thinkLevel = thinkFull

	// While streaming the block stays plain (the thinking accent only); the
	// markdown render is deferred until the settle tick.
	m.streaming = true
	m.addBlock(blockThinking, "Let me check the entry point.\n\n```go\nfunc main() { println(\"hi\") }\n```\n")
	streamed := m.piece(0)
	streamedColors := map[string]bool{}
	for _, match := range sgrColorRe.FindAllStringSubmatch(streamed, -1) {
		streamedColors[match[1]] = true
	}
	if len(streamedColors) != 0 {
		t.Fatalf("streaming thinking should stay plain (colors=%v): %q", streamedColors, streamed)
	}

	// Settled: markdown renders, so the fenced Go gets syntax colours.
	m.setStreaming(false)
	piece := m.piece(0)
	if !strings.Contains(ansi.Strip(piece), "func main") {
		t.Fatalf("thinking text lost: %q", ansi.Strip(piece))
	}
	colors := map[string]bool{}
	for _, match := range sgrColorRe.FindAllStringSubmatch(piece, -1) {
		colors[match[1]] = true
	}
	if len(colors) < 2 {
		t.Fatalf("thinking code was not syntax-highlighted (colors=%v): %q", colors, piece)
	}
}

func TestReplayCarriesToolDuration(t *testing.T) {
	calls := []storedToolCall{}
	var call storedToolCall
	call.ID = "call_1"
	call.Function.Name = "bash"
	call.Function.Arguments = `{"command":"ls"}`
	calls = append(calls, call)

	blocks := replayConversation([]storedMessage{
		{Role: "assistant", ToolCalls: calls},
		{Role: "tool", ToolCallID: "call_1", Name: "bash", Content: "(exit 0)\nok", DurationMs: 1234},
	})
	if len(blocks) != 1 || blocks[0].run == nil {
		t.Fatalf("replay blocks = %+v", blocks)
	}
	if got := blocks[0].run.calls[0].durationMs; got != 1234 {
		t.Fatalf("replayed duration = %d, want 1234", got)
	}
}
