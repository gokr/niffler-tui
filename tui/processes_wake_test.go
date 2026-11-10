package main

// The status line's wake answer, and the per-process ownership row.
//
// The question a human actually has is not "is something running" (the badge
// already said that) but "if it finishes while I am away, will this
// conversation come back to me?". Wander over a three-message exchange is
// invisible unless the client says so, so these tests pin the four states the
// answer can be in, and — just as important — that an UNKNOWN answer prints no
// promise at all rather than a plausible-looking guess.

import (
	"strings"
	"testing"
)

func wakeProcesses() []processSummary {
	return []processSummary{
		{ID: "p2", Label: "build", Status: "running", Session: "conv-a", Notifies: true},
	}
}

func TestWakeBadgePromisesWhileBudgetRemains(t *testing.T) {
	got := processesBadgeTextFor(LocaleEN, wakeProcesses(), "conv-a", wakeInfo{Max: 3, Used: 1})
	if got != "bg 1 · wake 1/3" {
		t.Fatalf("badge = %q, want a wake promise naming the remaining budget", got)
	}
}

// A spent budget is the case that silently misleads: the job WILL finish and
// nothing will happen until the human types. Say so.
func TestWakeBadgeSaysWhenTheBudgetIsSpent(t *testing.T) {
	got := processesBadgeTextFor(LocaleEN, wakeProcesses(), "conv-a", wakeInfo{Max: 3, Used: 3})
	if got != "bg 1 · wake budget spent (3/3) — completion waits for your next message" {
		t.Fatalf("badge = %q, want the exhausted-budget wording", got)
	}
}

func TestWakeBadgeSaysWhenWakesAreDisabled(t *testing.T) {
	got := processesBadgeTextFor(LocaleEN, wakeProcesses(), "conv-a", wakeInfo{Max: 0, Used: 0})
	// Zero used + zero max is ALSO the "not loaded" shape, so this asserts the
	// contract that an unloaded readback stays silent; the disabled notice is
	// only reachable once core has answered with a real budget.
	if got != "bg 1" {
		t.Fatalf("badge = %q, want no promise before core answers", got)
	}
	got = processesBadgeTextFor(LocaleEN, wakeProcesses(), "conv-a", wakeInfo{Max: 0, Used: 2})
	if got != "bg 1 · wakes disabled — completion waits for your next message" {
		t.Fatalf("badge = %q, want the wakes-disabled wording", got)
	}
}

// A process owned by ANOTHER conversation (or by a direct caller with no
// session) still runs, so it stays in the count — but it will never wake this
// conversation, and the badge must not imply otherwise.
func TestWakeBadgeRefusesToPromiseForForeignProcesses(t *testing.T) {
	foreign := []processSummary{
		{ID: "p1", Label: "watcher", Status: "running", Session: "conv-b", Notifies: true},
		{ID: "p2", Label: "adhoc", Status: "running"},
	}
	got := processesBadgeTextFor(LocaleEN, foreign, "conv-a", wakeInfo{Max: 3, Used: 0})
	if got != "bg 2 · completion will not wake this conversation" {
		t.Fatalf("badge = %q, want both processes counted and no wake promised", got)
	}
}

// No process of mine running means the wake budget is irrelevant: a running
// foreign job must not borrow my remaining budget for its own promise.
func TestWakeBadgeIgnoresBudgetWhenNothingOfMineRuns(t *testing.T) {
	foreign := []processSummary{
		{ID: "p1", Label: "watcher", Status: "running", Session: "conv-b", Notifies: true},
	}
	got := processesBadgeTextFor(LocaleEN, foreign, "conv-a", wakeInfo{Max: 3, Used: 3})
	if got != "bg 1 · completion will not wake this conversation" {
		t.Fatalf("badge = %q: a foreign process must not be reported as spent budget", got)
	}
}

func TestProcessRowNamesTheOwningConversation(t *testing.T) {
	processes := []processSummary{
		{ID: "p1", Label: "mine", Status: "running", Session: "conv-a", Notifies: true},
		{ID: "p2", Label: "theirs", Status: "running", Session: "conv-b", Notifies: true},
		{ID: "p3", Label: "nobody", Status: "running"},
	}
	notes := map[string]string{}
	for _, raw := range processSelectorItemsFor(LocaleEN, processes, "", "conv-a") {
		item := raw.(selectorItem)
		notes[item.id] = item.description
	}
	if got := notes["p1"]; !strings.Contains(got, "notifies this conversation") {
		t.Fatalf("p1 description = %q", got)
	}
	if got := notes["p2"]; !strings.Contains(got, "notifies another conversation") {
		t.Fatalf("p2 description = %q", got)
	}
	if got := notes["p3"]; !strings.Contains(got, "nobody is notified") {
		t.Fatalf("p3 description = %q", got)
	}
}

// Finished rows carry no promise: the notify note answers "will its exit reach
// me", which is moot once it already exited (and the notice is in the lane).
func TestProcessRowOmitsNotifyNoteForFinishedEntries(t *testing.T) {
	processes := []processSummary{
		{ID: "p1", Label: "done", Status: "exited(code 0)", Session: "conv-a", Notifies: true},
	}
	for _, raw := range processSelectorItemsFor(LocaleEN, processes, "", "conv-a") {
		item := raw.(selectorItem)
		if strings.Contains(item.description, "notifies") {
			t.Fatalf("finished row = %q, want no notify note", item.description)
		}
	}
}

// Every key the status line and the panel now use must exist in all three
// catalogs, or a human switching to zh/zh-TW sees an English fallback mid-line.
func TestWakeKeysExistInEveryLocale(t *testing.T) {
	for _, key := range []string{"processes.wakeReady", "processes.wakeSpent",
		"processes.wakeOff", "processes.noWake", "processes.notifyHere",
		"processes.notifyOther", "processes.notifyNone"} {
		for loc := range catalogs {
			if _, ok := catalogs[loc][key]; !ok {
				t.Fatalf("locale %v is missing %s", loc, key)
			}
		}
	}
}
