package main

// A crash report from the field:
//
//	interface conversion: tea.Model is *main.model, not main.model
//	  main.(*fleetModel).updateInstance  fleet.go:284
//
// The root cause is subtle enough to deserve its own test file. `model.Update`
// has a VALUE receiver, so every path in it returns a `model`. One handler
// (`handleSearchKey`) had a POINTER receiver, and `return m` there put a
// `*model` into the `tea.Model` interface. Nothing in a single-instance session
// notices: `main` keeps the interface value and Bubble Tea only ever calls
// methods on it. Fleet is where it detonates — `fleetModel.updateInstance`
// stores each instance as a concrete `model` and type-asserts the result back
// with `updated.(model)`, which panics on the pointer.
//
// So the invariant worth pinning is not "search works" but "Update always
// returns a value model", because that is what every `.(model)` in the codebase
// silently depends on.

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// assertValueModel fails if Update handed back a pointer. The panic message
// names the actual got-type so a future regression is obvious.
func assertValueModel(t *testing.T, what string, out tea.Model) {
	t.Helper()
	if ptr, bad := out.(*model); bad {
		t.Fatalf("%s returned *model (%p); every .(model) assertion in fleet "+
			"will panic on this", what, ptr)
	}
	if _, ok := out.(model); !ok {
		t.Fatalf("%s returned %T, which is neither model nor *model", what, out)
	}
}

// The reported path: an active history search swallows key presses through
// handleSearchKey before anything else sees them.
func TestUpdateReturnsValueModelDuringHistorySearch(t *testing.T) {
	m := newTestModel()
	m.searchActive = true

	// A plain character, and each branch of the switch in handleSearchKey.
	for _, msg := range []tea.KeyPressMsg{
		{Text: "a"},
		{Code: tea.KeyBackspace},
		{Code: tea.KeyEnter},
	} {
		out, _ := m.Update(msg)
		assertValueModel(t, "Update in history-search mode", out)
		m, _ = out.(model)
		m.searchActive = true // re-arm: Enter/Esc leave search mode
	}
}

// The panic happened in fleet, so drive the exact call: an instance model
// updated while a search is active. Before the fix this panicked with the
// reported interface conversion.
func TestFleetInstanceUpdateSurvivesHistorySearch(t *testing.T) {
	f := &fleetModel{
		models:      map[string]model{},
		generations: map[string]uint64{},
		active:      "local",
	}
	inst := newTestModel()
	inst.searchActive = true
	f.models["local"] = inst
	f.generations["local"] = 1

	// updateInstance is where the panic surfaced (fleet.go:284).
	_ = f.updateInstance("local", tea.KeyPressMsg{Text: "a"})

		// The instance is still there — a pointer result is dropped rather than
	// stored, so the previous good value survives instead of the process dying.
	if len(f.models) != 1 {
		t.Fatalf("instances = %d, want 1", len(f.models))
	}
}

// Ordinary key handling is the common path and must stay a value model too —
// a pointer leaking from anywhere else would show up here first.
func TestUpdateReturnsValueModelForOrdinaryKeys(t *testing.T) {
	m := newTestModel()
	for _, msg := range []tea.KeyPressMsg{
		{Text: "x"},
		{Code: tea.KeyUp},
		{Code: tea.KeyTab},
	} {
		out, _ := m.Update(msg)
		assertValueModel(t, "Update in chat mode", out)
		m, _ = out.(model)
	}
}
