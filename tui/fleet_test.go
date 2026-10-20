package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testFleet(t *testing.T) *fleetModel {
	// Each test gets its own client-local ledger: unread state must never leak
	// between tests (or into the developer's real ~/.config).
	t.Setenv("HOME", t.TempDir())
	a := newTestModel()
	a.instanceName = "local"
	a.session = "same"
	a.ctx = context.Background()
	a.input.SetValue("local draft")
	b := newTestModel()
	b.instanceName = "worker"
	b.session = "same"
	b.ctx = context.Background()
	b.input.SetValue("remote draft")
	f := newFleet(a, "/local", localScope)
	f.models["worker"] = b
	f.generations["worker"] = 1
	f.scopes["worker"] = "instance:worker"
	f.trackInstance("worker")
	return f
}
func TestFleetRoutesByInstanceNotSession(t *testing.T) {
	f := testFleet(t)
	f.Update(fleetMsg{"worker", 1, approvalEventMsg{req: approvalRequest{id: "gate", tool: "bash", sessionID: "same"}, directed: true}})
	if len(f.models["local"].approvals) != 0 || len(f.models["worker"].approvals) != 1 {
		t.Fatal("approval crossed independent stores")
	}
	f.Update(fleetMsg{"local", 99, approvalEventMsg{req: approvalRequest{id: "stale", tool: "bash"}}})
	if len(f.models["local"].approvals) != 0 {
		t.Fatal("old generation accepted")
	}
}
func TestFleetSelectionRetainsDraftAndWork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testFleet(t)
	a := f.models["local"]
	a.busy = true
	f.models["local"] = a
	if cmd := f.switchTo("worker"); cmd != nil {
		t.Fatal("visited instance requires no RPC")
	}
	if f.active != "worker" || !f.models["local"].busy {
		t.Fatal("switch cancelled background work")
	}
	if f.models["local"].input.Value() != "local draft" || f.models["worker"].input.Value() != "remote draft" {
		t.Fatal("drafts mixed")
	}
	f.switchTo("local")
	if f.active != "local" {
		t.Fatal("implicit local missing")
	}
}

// Rotation must walk the ring the user sees (local plus visited instances),
// including an instance whose name is no longer in the registry listing.
func TestFleetRotationCoversVisitedInstances(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := testFleet(t)
	f.order = []string{"local", "worker"}
	f.switchTo("next")
	if f.active != "worker" {
		t.Fatalf("next skipped the visited instance: %q", f.active)
	}
	f.switchTo("next")
	if f.active != "local" {
		t.Fatalf("rotation did not wrap: %q", f.active)
	}
	f.switchTo("previous")
	if f.active != "worker" {
		t.Fatalf("previous did not walk backwards: %q", f.active)
	}
}
func TestFleetFailedSwitchLeavesActive(t *testing.T) {
	f := testFleet(t)
	f.pending = "worker"
	f.Update(fleetReadyMsg{name: "worker", err: errors.New("claim held")})
	if f.active != "local" || f.models["local"].input.Value() != "local draft" || f.pending != "" {
		t.Fatal("failed claim changed current instance")
	}
}
func TestFleetClipboardCommandsReachTerminal(t *testing.T) {
	for _, cmd := range []tea.Cmd{tea.SetClipboard("selected text"), tea.SetPrimaryClipboard("selected text")} {
		want := cmd()
		got := scopeCmd("worker", 7, cmd)()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("terminal clipboard message wrapped: got %T, want %T", got, want)
		}
		batch := scopeCmd("worker", 7, tea.Batch(cmd, func() tea.Msg { return connectedMsg{} }))().(tea.BatchMsg)
		if got := batch[0](); !reflect.DeepEqual(got, want) {
			t.Fatalf("batched clipboard message wrapped: %T", got)
		}
		if _, ok := batch[1]().(fleetMsg); !ok {
			t.Fatal("application message escaped instance scope")
		}
	}
}

func TestFleetScopesNestedBatches(t *testing.T) {
	cmd := scopeCmd("worker", 7, tea.Batch(func() tea.Msg { return connectedMsg{} }, tea.Batch(func() tea.Msg { return connectStoppedMsg{} })))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("batch swallowed")
	}
	first, ok := batch[0]().(fleetMsg)
	if !ok || first.name != "worker" || first.generation != 7 {
		t.Fatal("batch response untagged")
	}
	second, ok := batch[1]().(fleetMsg)
	if !ok || second.name != "worker" {
		t.Fatal("nested batch untagged")
	}
	if _, ok := scopeCmd("worker", 1, tea.Quit)().(tea.QuitMsg); !ok {
		t.Fatal("terminal quit swallowed")
	}
}
