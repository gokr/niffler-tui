package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---- the split worker view (ctrl+o) ----------------------------------------

// TestBgPeekLaysOutRosterAndPane pins the layout the cycle exists for: the
// roster stays visible (capped, with a "+N more" edge when it is longer than
// its share) and the output pane below it gets the screen's remaining rows —
// the last lines of output, not one summary line.
func TestBgPeekLaysOutRosterAndPane(t *testing.T) {
	roster := make([]bgTarget, 0, 14)
	roster = append(roster, bgTarget{kind: "agent", id: "agent-1", status: "running", startedAt: 900,
		task: "survey the docs"})
	for i := 2; i <= 14; i++ {
		roster = append(roster, bgTarget{kind: "agent", id: "agent-" + strconv.Itoa(i),
			status: "running", startedAt: 900})
	}
	tail := make([]string, 0, 40)
	for i := 1; i <= 40; i++ {
		tail = append(tail, "row "+strconv.Itoa(i))
	}
	ctx := bgPeekContext{Roster: roster, Index: 0, Now: 1000, Tail: tail}
	view := ansi.Strip(bgPeekView(LocaleEN, roster[0], "", 100, 30, false, ctx))
	rows := strings.Split(view, "\n")
	if len(rows) > 29 {
		t.Fatalf("view is %d rows for a 30-row screen (the frame owns one):\n%s", len(rows), view)
	}
	for _, want := range []string{"workers: 14", "▸ agent", "+4 more", "row 40"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	// The pane takes the rows left over (here: 10 of the 40), newest last.
	if !strings.Contains(view, "row 31") || strings.Contains(view, "row 30\n") {
		t.Fatalf("pane rows are not the last of the output:\n%s", view)
	}
	// The pane is a tail: the oldest rows fall off, the newest are shown.
	if strings.Contains(view, "row 1\n") {
		t.Fatalf("pane kept the oldest row:\n%s", view)
	}
	// A pane with content supersedes the single streamed line.
	if strings.Contains(view, "out: ") {
		t.Fatalf("card still renders the one-line fallback next to the pane:\n%s", view)
	}
}

// TestBgPeekWindowKeepsSelectionVisible: the roster cap may never hide the
// selected entry, wherever the cycle is.
func TestBgPeekWindowKeepsSelectionVisible(t *testing.T) {
	cases := []struct{ count, index, max int }{
		{3, 0, 5}, {9, 0, 4}, {9, 4, 4}, {9, 8, 4}, {1, 0, 1},
	}
	for _, c := range cases {
		lo, hi := bgPeekWindow(c.count, c.index, c.max)
		if lo < 0 || hi > c.count || hi-lo > c.max {
			t.Fatalf("window(%+v) = [%d,%d)", c, lo, hi)
		}
		if c.index < lo || c.index >= hi {
			t.Fatalf("window(%+v) = [%d,%d) hides the selection", c, lo, hi)
		}
	}
}

// TestBgPeekWaitsForAFreshSnapshot: ctrl+o must not answer "nothing is
// running" from a snapshot that may be stale — a process another client
// started, a subagent this client has not heard about. An empty local roster
// asks for both refreshes first, and the answers decide: workers open the
// cycle, an empty roster answers the note.
func TestBgPeekWaitsForAFreshSnapshot(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN

	// Nothing here yet: the local answer would be "nothing is running", so the
	// press only asks for fresh snapshots.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	got := updated.(model)
	if got.mode != modeChat || !got.bgPeekPendingProc || !got.bgPeekPendingAgent {
		t.Fatalf("stale ctrl+o opened mode=%v pending=%v/%v",
			got.mode, got.bgPeekPendingProc, got.bgPeekPendingAgent)
	}
	if cmd == nil {
		t.Fatal("stale ctrl+o asked for no refresh")
	}

	// An answer that already shows work opens the cycle; the other snapshot is
	// still on its way and lands into the open roster.
	got.processes = []processSummary{{ID: "p2", Label: "watch", Status: "running", StartedAt: 900}}
	got.bgPeekPendingProc = false
	if cmd := got.settlePendingBgPeek(); cmd == nil {
		t.Fatal("cycle did not open on a snapshot that shows work")
	}
	if got.mode != modeBgPeek || len(got.bgPeekRoster) != 1 || got.bgPeekRoster[0].id != "p2" {
		t.Fatalf("mode=%v roster=%#v", got.mode, got.bgPeekRoster)
	}

	// One empty answer is not "nothing is running": the other snapshot is still
	// in flight, and only when both have come back empty is the note truthful.
	quiet := newTestModel()
	quiet.loc = LocaleEN
	quiet.bgPeekPending, quiet.bgPeekPendingProc, quiet.bgPeekPendingAgent = true, true, true
	quiet.bgPeekPendingProc = false
	if cmd := quiet.settlePendingBgPeek(); cmd != nil || quiet.contextNote != "" {
		t.Fatalf("half an answer settled it: note=%q cmd=%v", quiet.contextNote, cmd != nil)
	}
	quiet.bgPeekPendingAgent = false
	quiet.settlePendingBgPeek()
	if quiet.mode != modeChat || quiet.contextNote != catalogEn["note.nothingRunning"] {
		t.Fatalf("empty refresh: mode=%v note=%q", quiet.mode, quiet.contextNote)
	}
}

// TestBgPeekPaneOnlyShowsItsOwnWorker: the pane is attributed to one worker.
// A roster rebuilt into a different entry, and a read that lands after the
// cycle moved on, must not paint one worker's output under another's name.
func TestBgPeekPaneOnlyShowsItsOwnWorker(t *testing.T) {
	m := newTestModel()
	m.mode = modeBgPeek
	m.bgPeekRoster = []bgTarget{
		{kind: "agent", id: "agent-1", status: "running"},
		{kind: "process", id: "p2", label: "sleep", status: "running"},
	}
	m.bgPeekIdx = 0
	m.bgPeekPaneFor = "agent-1"
	m.bgPeekTail = []string{"line one"}

	if !m.bgPeekPaneAttributed("agent", "agent-1") {
		t.Fatal("the selected worker's own pane was rejected")
	}
	if m.bgPeekPaneAttributed("process", "p2") {
		t.Fatal("another worker's id was accepted as its own pane")
	}
	m.bgPeekIdx = 1 // cycling moved the selection
	if lines := m.bgPeekPaneLines(); lines != nil {
		t.Fatalf("stale pane attributed to the new selection: %#v", lines)
	}
	// A dropped pane is re-opened for the new worker, not kept.
	m.attributeBgPeekPane()
	if !m.bgPeekPaneAttributed("process", "p2") || len(m.bgPeekTail) != 0 {
		t.Fatalf("re-open kept the previous worker's pane: %#v", m.bgPeekTail)
	}
	// A settled worker's rows stay readable; the buffer only keeps its tail.
	m.bgPeekTail = appendPaneLines(nil, make([]string, bgPeekTailMaxLines+5))
	if len(m.bgPeekTail) != bgPeekTailMaxLines {
		t.Fatalf("pane buffer = %d rows, want %d", len(m.bgPeekTail), bgPeekTailMaxLines)
	}
}

// TestBgPeekProcessPaneShowsRawTail: a process card's pane is its stdout tail,
// clamped to the pane's rows.
func TestBgPeekProcessPaneShowsRawTail(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&sb, "log line %d\n", i)
	}
	tgt := bgTarget{kind: "process", id: "p3", label: "make watch", status: "running", startedAt: 990}
	ctx := bgPeekContext{Roster: []bgTarget{tgt}, Index: 0, Now: 1000}
	view := ansi.Strip(bgPeekView(LocaleEN, tgt, sb.String(), 100, 24, false, ctx))
	if !strings.Contains(view, "log line 60") {
		t.Fatalf("pane missing the newest output:\n%s", view)
	}
	if strings.Contains(view, "log line 1\n") {
		t.Fatalf("pane kept output older than its rows:\n%s", view)
	}
	if len(strings.Split(view, "\n")) > 23 {
		t.Fatalf("process pane overflows the screen:\n%s", view)
	}
	// With nothing to show yet the pane says so instead of staying blank.
	quiet := ansi.Strip(bgPeekView(LocaleEN, tgt, "", 100, 24, true, ctx))
	if !strings.Contains(quiet, "loading") {
		t.Fatalf("loading pane = %q", quiet)
	}
	if empty := ansi.Strip(bgPeekView(LocaleEN, tgt, "", 100, 24, false, ctx)); !strings.Contains(empty, "no output yet") {
		t.Fatalf("empty pane = %q", empty)
	}
}
