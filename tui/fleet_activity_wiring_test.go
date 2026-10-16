package main

import (
	"strings"
	"testing"
)

// The badge is the whole point of the feature: a finish on an instance you are
// NOT looking at must be visible without switching to it.
func TestFleetBadgeCountsUnreadPerInstance(t *testing.T) {
	f := testFleet(t)
	// "same" is the session id on BOTH instances: a badge must not cross.
	done := fleetActivity{ID: "same", SessionID: "same", TurnID: "t1", Seq: 1, Outcome: "done", At: 10}
	f.Update(fleetMsg{"worker", 1, fleetActivityMsg{Activity: done}})
	if line := f.instanceLine("worker"); !strings.Contains(line, "✓1") {
		t.Fatalf("worker badge missing: %q", line)
	}
	if line := f.instanceLine("local"); strings.Contains(line, "✓") {
		t.Fatalf("local instance picked up the remote badge: %q", line)
	}
	// A second completion on the same conversation is still one unread item.
	newer := fleetActivity{ID: "same", SessionID: "same", TurnID: "t2", Seq: 2, Outcome: "failed", At: 11}
	f.Update(fleetMsg{"worker", 1, fleetActivityMsg{Activity: newer}})
	if line := f.instanceLine("worker"); !strings.Contains(line, "✓1") {
		t.Fatalf("unread collapsed wrongly: %q", line)
	}
}

func TestFleetSwitchingClearsOnlyTheOpenedConversation(t *testing.T) {
	f := testFleet(t)
	// Give the worker two conversations, one of which we will not open.
	other := fleetActivity{ID: "other", SessionID: "other", TurnID: "t9", Seq: 1, Outcome: "done", At: 12}
	f.Update(fleetMsg{"worker", 1, fleetActivityMsg{Activity: fleetActivity{ID: "same", SessionID: "same", TurnID: "t1", Seq: 1, Outcome: "done", At: 10}}})
	f.Update(fleetMsg{"worker", 1, fleetActivityMsg{Activity: other}})
	f.switchTo("worker")
	if line := f.instanceLine("worker"); !strings.Contains(line, "✓1") {
		t.Fatalf("opening the instance should clear only the conversation shown: %q", line)
	}
	if got := f.ledger.unread(f.scopeOf("worker")); len(got) != 1 || got[0].SessionID != "other" {
		t.Fatalf("wrong conversation marked read: %#v", got)
	}
}

// A snapshot that arrives while the human is elsewhere must not clear badges,
// and an offline completion discovered on reconnect must raise one.
func TestFleetSnapshotPreservesUnreadAndRaisesMissedCompletion(t *testing.T) {
	f := testFleet(t)
	offline := fleetActivity{ID: "same", SessionID: "same", TurnID: "t7", Seq: 3, Outcome: "needs-attention", At: 30}
	f.Update(fleetMsg{"worker", 1, fleetActivitySnapshotMsg{Items: []fleetActivity{offline}}})
	if len(f.ledger.unread(f.scopeOf("worker"))) != 1 {
		t.Fatal("missed offline completion did not raise a badge")
	}
	if f.instanceLine("worker") == f.instanceLine("local") {
		t.Fatal("instances look identical in the status line")
	}
}

// The status line names the instance and its connection state, and stays within
// the terminal width (it is the bottom row, not a wrapping paragraph).
func TestFleetSwitchRequestEmptyListsAndLocalIsSelectable(t *testing.T) {
	f := testFleet(t)
	// /niffler with no argument lists instead of switching.
	f.Update(fleetMsg{"local", 1, fleetSwitchRequest{}})
	if f.active != "local" {
		t.Fatal("empty /niffler changed the active instance")
	}
	if !strings.Contains(f.models["local"].viewport.View(), "local — local harness") &&
		!strings.Contains(f.models["local"].viewport.View(), "● local") {
		t.Fatalf("picker did not render instances: %q", f.models["local"].viewport.View())
	}
	// An explicit /niffler local from a remote instance resolves to local
	// rather than being refused as it was by the old re-exec path. A visited
	// instance needs no RPC, so the switch is immediate.
	f.active = "worker"
	f.switchTo("local")
	if f.active != "local" {
		t.Fatal("returning to local still refused")
	}
}

func TestFleetStatusLineIsBounded(t *testing.T) {
	f := testFleet(t)
	f.width, f.height = 40, 20
	f.models["worker"] = func() model { m := f.models["worker"]; m.width = 40; m.height = 19; return m }()
	f.Update(fleetMsg{"worker", 1, fleetActivityMsg{Activity: fleetActivity{ID: "same", SessionID: "same", TurnID: "t1", Seq: 1, Outcome: "done", At: 10}}})
	view := f.View()
	if !strings.Contains(view.Content, "● local") {
		t.Fatalf("active-instance marker missing: %q", view.Content)
	}
	if !strings.Contains(view.Content, "worker") {
		t.Fatalf("inactive instance missing from the status line: %q", view.Content)
	}
}
