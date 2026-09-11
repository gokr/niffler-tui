// The /profile picker: a bare /profile must open a selectable list of the
// stored tool profiles (with the current choice marked and a "no profile"
// entry that clears it), matching the help text and the sibling pickers
// (/theme, /model, /provider, /mcp). An explicit /profile NAME still applies
// directly.
package main

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testProfiles() []toolProfileSummary {
	return []toolProfileSummary{
		{Name: "review", Tools: []string{"read", "grep"}, Note: "read-only", ToolCount: 2, EstTokens: 1500},
		{Name: "full", Tools: []string{"*"}, ToolCount: 9, EstTokens: 12000, Missing: []string{"gone"}},
	}
}

// TestBareProfileOpensPicker is the regression test for the reported bug: the
// help text promises a picker on an empty argument, and previously the command
// merely dumped a JSON listing as a meta block.
func TestBareProfileOpensPicker(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	updated, cmd := m.executeLocalCommand("/profile")
	got := updated.(model)
	if got.mode != modeProfiles {
		t.Fatalf("bare /profile mode = %v, want modeProfiles", got.mode)
	}
	if cmd == nil {
		t.Fatal("bare /profile did not schedule a profile load")
	}
	// A mode missing from View()'s switch renders only the header, so assert
	// the list is actually on screen with its loaded entries.
	got = got.applyProfiles(testProfiles())
	if got.mode != modeProfiles {
		t.Fatalf("profilesMsg closed the picker: mode = %v", got.mode)
	}
	view := stripANSI(got.View().Content)
	for _, want := range []string{"Tool profiles", "review", "full", "2 tools"} {
		if !strings.Contains(view, want) {
			t.Fatalf("profile picker missing %q:\n%s", want, view)
		}
	}
}

// applyProfiles feeds a profilesMsg through Update, the way the backend does.
func (m model) applyProfiles(profiles []toolProfileSummary) model {
	updated, _ := m.Update(profilesMsg{Profiles: profiles})
	return updated.(model)
}

// TestProfilePickerMarksAndClearsSelection drives the picker end to end:
// choosing a profile applies it, and the "no profile" entry clears it again.
func TestProfilePickerMarksAndClearsSelection(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.openProfileSelectorWith(testProfiles())

	// The no-profile entry, every stored profile, and the creation entry.
	items := m.selector.list.Items()
	if len(items) != 4 {
		t.Fatalf("picker items = %d, want 2 profiles + no-profile + new", len(items))
	}
	if items[0].(selectorItem).kind != selectorProfileDefault {
		t.Fatalf("first item = %v, want the no-profile entry", items[0])
	}
	// With nothing selected, the no-profile entry carries the current marker.
	if !strings.HasPrefix(items[0].(selectorItem).title, "● ") {
		t.Fatalf("no-profile entry not marked current: %q", items[0].(selectorItem).title)
	}

	reviewIndex := -1
	for i, item := range items {
		if item.(selectorItem).id == "review" {
			reviewIndex = i
		}
	}
	if reviewIndex < 0 {
		t.Fatal("review missing from the picker")
	}
	m.selector.list.Select(reviewIndex)
	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeChat {
		t.Fatalf("enter did not return to chat: mode = %v", got.mode)
	}
	// toolProfile is the field sendTurn reads for the session call's
	// "profile" argument, so setting it is the contract that matters.
	if got.toolProfile != "review" {
		t.Fatalf("toolProfile = %q, want review", got.toolProfile)
	}
	if n := len(got.blocks); n == 0 || got.blocks[n-1].kind != blockMeta {
		t.Fatalf("no confirmation block: %+v", got.blocks)
	}

	// Re-open: the chosen profile is the marked one, and the default entry
	// clears it.
	got.openProfileSelectorWith(testProfiles())
	if title := got.selector.list.Items()[0].(selectorItem).title; strings.HasPrefix(title, "● ") {
		t.Fatalf("no-profile entry still marked after choosing review: %q", title)
	}
	got.selector.list.Select(0)
	updated, _ = got.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = updated.(model)
	if got.toolProfile != "" {
		t.Fatalf("clearing did not take: toolProfile = %q", got.toolProfile)
	}
}

// TestProfilePickerEscLeavesSelection pins that backing out changes nothing.
func TestProfilePickerEscLeavesSelection(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.toolProfile = "review"
	m.openProfileSelectorWith(testProfiles())

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := updated.(model)
	if got.mode != modeChat {
		t.Fatalf("esc mode = %v, want chat", got.mode)
	}
	if got.toolProfile != "review" {
		t.Fatalf("esc changed the profile: %q", got.toolProfile)
	}
}

// TestProfileArgumentStillAppliesDirectly keeps the non-picker path working:
// /profile NAME validates against the store and does not open a selector.
func TestProfileArgumentStillAppliesDirectly(t *testing.T) {
	m := newTestModel()
	updated, _ := m.executeLocalCommand("/profile myprof")
	got := updated.(model)
	if got.mode == modeProfiles {
		t.Fatal("an explicit profile argument must not open the picker")
	}
}

// TestProfilePickerLoadingTitle covers the round trip: the picker opens
// immediately with a loading title, so a slow store read still shows the list
// frame (and a failure drops back to chat with a note).
func TestProfilePickerLoadingTitle(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.openProfileSelector()
	if m.mode != modeProfiles {
		t.Fatalf("loading picker mode = %v", m.mode)
	}
	if title := m.selector.list.Title; !strings.Contains(title, "loading") {
		t.Fatalf("loading title = %q", title)
	}

	// A failed load must not strand the user in an empty picker.
	updated, _ := m.Update(profilesMsg{Err: errProfiles})
	got := updated.(model)
	if got.mode != modeChat {
		t.Fatalf("failed load mode = %v, want chat", got.mode)
	}
	if !strings.Contains(got.contextNote, "boom") {
		t.Fatalf("failure not surfaced: %q", got.contextNote)
	}
}

var errProfiles = errors.New("boom")
