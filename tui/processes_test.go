// The /processes panel + status-line badge. The processes component is the
// model's hands for long-running commands; this is the human's window.
// The tests cover the pure layer: running-first ordering with numeric id
// sort (p2 < p10), the two-stage kill arm prefix, the empty-registry note,
// the badge text (empty when nothing runs), and the peek view's tail
// clamping — the bus commands themselves are thin requestInto wrappers
// already exercised end-to-end by the component's own suite.
package main

import (
	"strings"
	"testing"
)

func testProcesses() []processSummary {
	return []processSummary{
		{ID: "p10", Label: "watcher", Command: "inotifywait -m .", Status: "running"},
		{ID: "p2", Label: "dev-server", Command: "npm run dev", Status: "running"},
		{ID: "p3", Label: "tests", Command: "go test ./...", Status: "exited(code 0)", ExitCode: 0},
		{ID: "p4", Label: "db", Command: "docker compose up", Status: "killed(signal 15)", ExitCode: 15},
	}
}

func TestProcessSelectorItemsRunningFirstNumericSort(t *testing.T) {
	items := processSelectorItems(LocaleEN, testProcesses(), "")
	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d", len(items))
	}
	// running first, then numeric id order (p2 before p3/p4 — not string
	// order, which would put p10 before p2)
	want := []string{"p2", "p10", "p3", "p4"}
	for i, id := range want {
		item := items[i].(selectorItem)
		if item.id != id {
			t.Fatalf("position %d: want %s, got %s", i, id, item.id)
		}
	}
}

func TestProcessSelectorItemsConfirmKillPrefix(t *testing.T) {
	items := processSelectorItems(LocaleEN, testProcesses(), "p2")
	item := items[0].(selectorItem)
	if item.id != "p2" {
		t.Fatalf("armed entry must stay first: %s", item.id)
	}
	if !strings.Contains(item.title, "kill?") {
		t.Fatalf("armed entry must carry the kill prompt: %q", item.title)
	}
	// disarming restores the plain title
	item = processSelectorItems(LocaleEN, testProcesses(), "")[0].(selectorItem)
	if strings.Contains(item.title, "kill?") {
		t.Fatalf("unarmed entry must not carry the prompt: %q", item.title)
	}
	if !strings.HasPrefix(item.title, "● ") {
		t.Fatalf("running entries carry the live dot: %q", item.title)
	}
}

func TestProcessSelectorItemsEmptyRegistryNote(t *testing.T) {
	items := processSelectorItems(LocaleEN, nil, "")
	if len(items) != 1 {
		t.Fatalf("empty registry must show exactly the note item, got %d", len(items))
	}
	item := items[0].(selectorItem)
	if item.kind != selectorProcessNote {
		t.Fatalf("expected the note kind, got %v", item.kind)
	}
}

func TestProcessesBadgeText(t *testing.T) {
	if got := processesBadgeText(LocaleEN, testProcesses()); got != "bg 2" {
		t.Fatalf("two running: want %q, got %q", "bg 2", got)
	}
	if got := processesBadgeText(LocaleEN, nil); got != "" {
		t.Fatalf("nothing running: want no badge, got %q", got)
	}
	finished := []processSummary{
		{ID: "p1", Label: "x", Status: "exited(code 1)", ExitCode: 1},
	}
	if got := processesBadgeText(LocaleEN, finished); got != "" {
		t.Fatalf("only finished: want no badge, got %q", got)
	}
}

func TestPeekViewClampsTail(t *testing.T) {
	p := processSummary{ID: "p2", Label: "dev-server", Status: "running"}
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("line ")
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString("\n")
	}
	view := peekView(LocaleEN, p, sb.String(), 80, 20, false)
	if strings.Contains(view, "line 0\n") && strings.Contains(view, "line 10") {
		t.Fatal("clamped tail must drop early lines")
	}
	if !strings.Contains(view, "p2") || !strings.Contains(view, "dev-server") {
		t.Fatalf("peek must identify the process: %q", view[:80])
	}
	// loading and empty states
	if loading := peekView(LocaleEN, p, "", 80, 20, true); !strings.Contains(loading, "loading") {
		t.Fatalf("loading state missing: %q", loading)
	}
	if empty := peekView(LocaleEN, p, "", 80, 20, false); !strings.Contains(empty, "no output yet") {
		t.Fatalf("empty state missing: %q", empty)
	}
}

func TestClampTail(t *testing.T) {
	text := "a\nb\nc\nd\ne\n"
	if got := clampTail(text, 2); got != "d\ne" {
		t.Fatalf("want last two lines, got %q", got)
	}
	if got := clampTail(text, 10); got != "a\nb\nc\nd\ne" {
		t.Fatalf("short input passes through: %q", got)
	}
	if got := clampTail("", 3); got != "" {
		t.Fatalf("empty input: %q", got)
	}
	if got := clampTail(text, 0); got != "" {
		t.Fatalf("zero lines: %q", got)
	}
}

// The badge's refresh chain must survive its own tick. armBgTick is an "at most
// once" gate for the other callers (the armSpinner lesson), so a tick handler
// that asked it while bgTicking was still set got nil back: no snapshot fetch,
// no re-arm. The chain died after its first tick and the badge then kept
// claiming a background process was running long after it had exited — until
// some unrelated event (a tool call's done frame, opening ctrl+o, a
// conversation load) happened to refresh the snapshot.
func TestBgTickRefreshesThenDisarmsWhenIdle(t *testing.T) {
	m := newTestModel()
	m.processes = testProcesses() // p10 + p2 running, p3 + p4 finished
	m.bgTicking = true            // the state a live chain is in when its tick fires

	updated, cmd := m.Update(bgTickMsg{})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("bgTickMsg issued no work — the chain dies after one tick and the badge goes stale")
	}
	if !m.bgTicking {
		t.Fatal("the chain must stay armed while a process is still running")
	}
	if got := processesBadgeText(LocaleEN, m.processes); got != "bg 2" {
		t.Fatalf("the tick must not change the snapshot before its fetch lands: %q", got)
	}

	// The fetch's result: everything finished. The next tick disarms the chain.
	m.processes = []processSummary{{ID: "p2", Label: "dev-server", Status: "exited(code 0)"}}
	updated, _ = m.Update(bgTickMsg{})
	m = updated.(model)
	if m.bgTicking {
		t.Fatal("the chain must disarm once nothing is running")
	}
	if got := processesBadgeText(LocaleEN, m.processes); got != "" {
		t.Fatalf("a finished process must not keep the badge: %q", got)
	}
}
