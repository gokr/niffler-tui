package main

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// ---- the subagent pane's line renderer -------------------------------------

// TestWorkerTailLinesRendersTheChildsWork pins what the pane shows for a
// subagent: what it was asked, what it answered, each tool call with its
// argument, and a bounded tail of every tool outcome — the rows that make the
// card useful, from the transcript a child's frames never carry.
func TestWorkerTailLinesRendersTheChildsWork(t *testing.T) {
	messages := []storedMessage{
		{Role: "user", Content: "You are a subagent. Work autonomously on the task below."},
		{Role: "assistant", Reasoning: "internal monologue, not rendered",
			Content: "Starting with the build.\nSecond answer line.",
			ToolCalls: []storedToolCall{{
				ID:       "c1",
				Function: storedToolFunction{Name: "bash", Arguments: `{"command":"go build ./..."}`},
			}}},
		{Role: "tool", ToolCallID: "c1", Name: "bash",
			Content: "line one\nline two\nline three\nline four"},
		{Role: "tool", ToolCallID: "c2", Name: "read",
			Content: "ERROR: no such file"},
		{Role: "user", Notice: []byte(`{"kind":"settled"}`), Content: "[subagent done]"},
	}
	got := strings.Join(workerTailLines(messages), "\n")
	for _, want := range []string{
		"» You are a subagent", "Starting with the build.", "Second answer line.",
		"→ bash go build ./...", "← bash line two", "line four",
		"! read no such file", "▸ [subagent done]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("pane lines missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"internal monologue", "line one"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("pane lines contain %q (reasoning is dropped, tool output is a tail):\n%s", unwanted, got)
		}
	}
}

// TestWorkerTailLinesClean: an empty transcript renders nothing (the pane then
// says "no output yet"), and a result that is JSON unwraps to its text.
func TestWorkerTailLinesClean(t *testing.T) {
	if lines := workerTailLines(nil); len(lines) != 0 {
		t.Fatalf("empty transcript = %#v", lines)
	}
	lines := workerTailLines([]storedMessage{
		{Role: "tool", Name: "read", Content: `{"text":"package main\n\nfunc main() {}"}`},
	})
	if len(lines) != 3 || lines[0] != "← read package main" {
		t.Fatalf("wrapped result rows = %#v", lines)
	}
}

func TestTailCursorAndWorkerWindow(t *testing.T) {
	if got := tailCursor("conv", 0, 40); got != "" {
		t.Fatalf("empty tail cursor = %q", got)
	}
	if got := tailCursor("conv", 40, 40); got != "" {
		t.Fatalf("exactly-full tail cursor = %q", got)
	}
	if got := tailCursor("conv", 100, 40); got != "conv:"+seqPrefix(60) {
		t.Fatalf("tail cursor = %q, want %q", got, "conv:"+seqPrefix(60))
	}
	if got := tailCursor("conv", 100, 0); got != "" {
		t.Fatalf("zero-size tail cursor = %q", got)
	}
}

// ---- the /sessions browser's subagent filter -------------------------------

// TestSessionSummariesCarryLineage covers the join the filter rests on: a
// conversation with a sessionmeta record is a subagent, one without is not.
func TestSessionSummariesCarryLineage(t *testing.T) {
	conversations := []sessionRow{
		{ID: "human", Value: conversationHeader{Title: "Human work", CreatedAt: 10}},
		{ID: "agent-1", Value: conversationHeader{CreatedAt: 20}},
	}
	metas := []metaRow{{ID: "agent-1", Value: sessionMeta{Parent: "human", Closed: true}}}

	sessions := buildSessionSummaries(conversations, metas)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[0].ID != "agent-1" || !sessions[0].subagent() || sessions[0].Parent != "human" || !sessions[0].Closed {
		t.Fatalf("lineage not joined: %#v", sessions[0])
	}
	if sessions[1].subagent() {
		t.Fatalf("a root conversation was marked as a subagent: %#v", sessions[1])
	}
	// The lineage read is best-effort: without it nothing is filtered.
	orphan := buildSessionSummaries(conversations, nil)
	for _, s := range orphan {
		if s.subagent() {
			t.Fatalf("missing lineage still hid %q", s.ID)
		}
	}
}

// TestSessionSelectorHidesSubagents pins the browser default: the subagent
// sessions a delegating conversation accumulates are held back, and the `a`
// toggle brings them back marked with the parent that spawned them.
func TestSessionSelectorHidesSubagents(t *testing.T) {
	sessions := []sessionSummary{
		{ID: "human", Title: "Real conversation", CreatedAt: 10},
		{ID: "agent-1", Title: "You are a subagent…", CreatedAt: 20, Parent: "human"},
	}
	hidden := sessionSelectorItems(LocaleEN, "human", sessions, false)
	if ids := selectorIDs(hidden); strings.Contains(strings.Join(ids, " "), "agent-1") {
		t.Fatalf("subagent session listed by default: %v", ids)
	}
	shown := sessionSelectorItems(LocaleEN, "human", sessions, true)
	if !strings.Contains(strings.Join(selectorIDs(shown), " "), "agent-1") {
		t.Fatalf("subagent session not listed when shown: %v", selectorIDs(shown))
	}
	// Its entry names the parent that spawned it, so a shown child is still
	// distinguishable from a conversation of one's own.
	descriptions := ""
	for _, item := range shown {
		if entry, ok := item.(selectorItem); ok {
			descriptions += entry.description + "\n"
		}
	}
	if !strings.Contains(descriptions, "subagent of human") {
		t.Fatalf("shown child does not name its parent:\n%s", descriptions)
	}
}

// TestSessionsKeyTogglesSubagentFilter: the browser's `a` key flips the filter
// and rebuilds the list in place; the title reports what is hidden.
func TestSessionsKeyTogglesSubagentFilter(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.session = "human"
	m.width, m.height = 80, 24
	m.sessionList = []sessionSummary{
		{ID: "human", Title: "Real conversation", CreatedAt: 10},
		{ID: "agent-1", Title: "child", CreatedAt: 20, Parent: "human"},
	}
	m.mode = modeSessions
	m.selector = newSelector(catalogEn["selector.sessions"],
		sessionSelectorItems(LocaleEN, "human", m.sessionList, false), 80, 20)

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	got := updated.(model)
	if !got.showSubagents {
		t.Fatal("`a` did not show the subagent sessions")
	}
	if !strings.Contains(got.selector.list.Title, "subagent") {
		t.Fatalf("title does not report the shown children: %q", got.selector.list.Title)
	}
	if ids := strings.Join(selectorIDs(got.selector.list.Items()), " "); !strings.Contains(ids, "agent-1") {
		t.Fatalf("children not listed after the toggle: %v", selectorIDs(got.selector.list.Items()))
	}

	updated, _ = got.handleControlKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	got = updated.(model)
	if got.showSubagents {
		t.Fatal("second `a` did not hide them again")
	}
	if !strings.Contains(got.selector.list.Title, "hidden") {
		t.Fatalf("title does not report the hidden count: %q", got.selector.list.Title)
	}
}

// selectorIDs lists the ids of selector items, in list order.
func selectorIDs(items []list.Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if entry, ok := item.(selectorItem); ok {
			ids = append(ids, strconv.Quote(entry.id))
		}
	}
	return ids
}
