package main

import (
	"errors"
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

// ---- store-side session search (issue #77) -------------------------------

// sessionBrowser opens the /session browser over a loaded list, as the
// sessionListMsg handler does, ready for filter keystrokes.
func sessionBrowser(t *testing.T, sessions []sessionSummary) model {
	t.Helper()
	m := newTestModel()
	m.loc = LocaleEN
	m.session = "human"
	m.width, m.height = 80, 24
	m.openSessionSelector(sessions)
	return m
}

// sessionIDs lists the session ids the browser is showing (the new-session
// row carries __new__ and is ignored by callers comparing session ids).
func sessionIDs(m model) []string {
	ids := make([]string, 0)
	for _, id := range selectorIDs(m.selector.list.Items()) {
		if strings.Trim(id, `"`) != "__new__" {
			ids = append(ids, strings.Trim(id, `"`))
		}
	}
	return ids
}

// TestSessionSearchSyncSchedulesOneDebouncedCall: a changed filter box in
// the browser schedules the store search exactly once per change (the
// debounce owns the pacing), and an unchanged box — or any other mode —
// never reaches the store.
func TestSessionSearchSyncSchedulesOneDebouncedCall(t *testing.T) {
	m := sessionBrowser(t, []sessionSummary{
		{ID: "human", Title: "Check the PRs", CreatedAt: 10},
	})
	gen := m.sessionSearchGen
	m.selector.list.SetFilterText("prs")
	cmd := m.sessionSearchSync()
	if cmd == nil {
		t.Fatal("typing did not schedule a store search")
	}
	if m.sessionSearchGen != gen+1 || m.sessionSearchQuery != "prs" {
		t.Fatalf("sync state = gen %d query %q, want gen %d query \"prs\"",
			m.sessionSearchGen, m.sessionSearchQuery, gen+1)
	}
	if again := m.sessionSearchSync(); again != nil {
		t.Fatal("unchanged box rescheduled the search")
	}
	// Other selectors keep their local filter: no store call, no generation.
	m.mode = modeThemes
	m.selector.list.SetFilterText("zen")
	if cmd := m.sessionSearchSync(); cmd != nil || m.sessionSearchQuery == "zen" {
		t.Fatal("a non-session selector reached the store")
	}
}

// TestSessionSearchResultsSwapTheListAndKeepTheQuery: results replace the
// loaded list, the typed query survives the rebuild in editing state, and
// the local fuzzy pass stops hiding anything — a server match in a
// different word order is no subsequence of its title and would vanish.
func TestSessionSearchResultsSwapTheListAndKeepTheQuery(t *testing.T) {
	m := sessionBrowser(t, []sessionSummary{
		{ID: "human", Title: "Check the PRs", CreatedAt: 10},
		{ID: "other", Title: "Unrelated chat", CreatedAt: 20},
	})
	m.selector.list.SetFilterText("prs check")
	_ = m.sessionSearchSync()

	m.applySessionSearchResults([]sessionSummary{
		{ID: "conv-9", Title: "Check the PRs", CreatedAt: 30},
	}, "prs check")

	ids := sessionIDs(m)
	if len(ids) != 1 || ids[0] != "conv-9" {
		t.Fatalf("browser shows %v, want only the server's match", ids)
	}
	if got := m.selector.list.FilterValue(); got != "prs check" {
		t.Fatalf("typed query lost in the rebuild: %q", got)
	}
	if m.selector.list.FilterState() != list.Filtering {
		t.Fatalf("filter state = %v, want Filtering (typing continues)", m.selector.list.FilterState())
	}
	targets := make([]string, 0, len(m.selector.list.Items()))
	for _, item := range m.selector.list.Items() {
		targets = append(targets, item.FilterValue())
	}
	if ranked := m.selector.list.Filter("prs check", targets); len(ranked) != len(targets) {
		t.Fatalf("local pass still hides server matches: %d of %d kept", len(ranked), len(targets))
	}
	// Clearing the box (esc resets it) shows the full loaded list again.
	m.selector.list.SetFilterText("")
	_ = m.sessionSearchSync()
	if m.sessionSearchResults != nil {
		t.Fatal("server results survived an emptied box")
	}
	if ids := sessionIDs(m); len(ids) != 2 {
		t.Fatalf("full list not restored after clearing: %v", ids)
	}
}

// TestSessionSearchDropsStaleReplies: only the reply for the keystroke that
// is still in the box is shown — a slower reply for an abandoned query is
// dropped, never pasted over newer results.
func TestSessionSearchDropsStaleReplies(t *testing.T) {
	m := sessionBrowser(t, []sessionSummary{
		{ID: "human", Title: "Check the PRs", CreatedAt: 10},
	})
	m.selector.list.SetFilterText("old")
	_ = m.sessionSearchSync()
	staleGen := m.sessionSearchGen
	m.selector.list.SetFilterText("new")
	_ = m.sessionSearchSync()
	freshGen := m.sessionSearchGen

	updated, _ := m.Update(sessionSearchMsg{
		Gen: staleGen, Query: "old",
		Sessions: []sessionSummary{{ID: "conv-stale", CreatedAt: 1}},
	})
	got := updated.(model)
	if got.sessionSearchResults != nil {
		t.Fatalf("stale reply applied: %v", sessionIDs(got))
	}

	updated, _ = got.Update(sessionSearchMsg{
		Gen: freshGen, Query: "new",
		Sessions: []sessionSummary{{ID: "conv-fresh", CreatedAt: 2}},
	})
	got = updated.(model)
	if ids := sessionIDs(got); len(ids) != 1 || ids[0] != "conv-fresh" {
		t.Fatalf("fresh reply not applied: %v", ids)
	}
	// A debounce tick for the abandoned query never reaches the store.
	updated, cmd := got.Update(sessionSearchTickMsg{Gen: staleGen, Query: "old"})
	if cmd != nil {
		t.Fatal("stale debounce tick scheduled a store call")
	}
	if updated.(model).sessionSearchQuery != "new" {
		t.Fatal("stale tick touched the live query")
	}
	// The live one does.
	if _, cmd := got.Update(sessionSearchTickMsg{Gen: freshGen, Query: "new"}); cmd == nil {
		t.Fatal("live debounce tick did not schedule the store call")
	}
}

// TestSessionSearchFallsBackToLocalFiltering: a store without `search` (an
// older harness) or a hiccup must not blank the browser — the loaded list
// comes back, the typed query stays in the box, the local fuzzy filter
// narrows it, and no further store calls are attempted this browser.
func TestSessionSearchFallsBackToLocalFiltering(t *testing.T) {
	m := sessionBrowser(t, []sessionSummary{
		{ID: "human", Title: "Real conversation", CreatedAt: 10},
		{ID: "other", Title: "Check the PRs", CreatedAt: 20},
	})
	m.selector.list.SetFilterText("real")
	_ = m.sessionSearchSync()

	updated, _ := m.Update(sessionSearchMsg{
		Gen: m.sessionSearchGen, Query: "real",
		Err: errors.New("store.search: unknown tool"),
	})
	got := updated.(model)
	if !got.sessionSearchOff {
		t.Fatal("search failure did not engage the fallback")
	}
	if got.sessionSearchResults != nil {
		t.Fatalf("fallback kept server results: %v", sessionIDs(got))
	}
	if ids := sessionIDs(got); len(ids) != 2 {
		t.Fatalf("fallback lost the loaded list: %v", ids)
	}
	if got.selector.list.FilterValue() != "real" {
		t.Fatalf("typed query lost in the fallback: %q", got.selector.list.FilterValue())
	}
	// The local pass owns filtering again: it narrows the loaded list.
	targets := []string{"Real conversation", "Check the PRs"}
	if ranked := got.selector.list.Filter("real", targets); len(ranked) != 1 {
		t.Fatalf("local fuzzy filter not restored: %d of %d kept", len(ranked), len(targets))
	}
	// And the store is not asked again for this browser.
	if cmd := got.sessionSearchSync(); cmd != nil {
		t.Fatal("fallback browser still schedules store searches")
	}
}

// TestServerSessionFilterKeepsEveryItem pins the pass-through contract the
// results path relies on: every target ranks, in order.
func TestServerSessionFilterKeepsEveryItem(t *testing.T) {
	targets := []string{"a", "b", "c"}
	ranked := serverSessionFilter("anything", targets)
	if len(ranked) != len(targets) {
		t.Fatalf("kept %d of %d targets", len(ranked), len(targets))
	}
	for i, r := range ranked {
		if r.Index != i {
			t.Fatalf("rank %d = index %d, order not preserved", i, r.Index)
		}
	}
}
