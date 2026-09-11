// The /profile creation form: "＋ New profile…" in the picker opens a
// three-field form (name, selectors, note) whose save validates selectors
// against the live catalog before storing — core's resolver silently skips
// unresolvable selectors, so the form must reject them up front and never
// silently overwrite an existing name.
package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func liveTestComponents() []visibilityComponent {
	hiddenUndo := visibilityTool{Name: "undo_last_edit"}
	hiddenUndo.Schema.Harness.Hidden = true
	return []visibilityComponent{
		{Name: "edit", Running: true, Tools: []visibilityTool{
			{Name: "read"}, {Name: "read_many"}, {Name: "edit"}, {Name: "write"}, hiddenUndo,
		}},
		{Name: "bash", Running: true, Tools: []visibilityTool{{Name: "bash"}}},
		{Name: "git", Running: true, Tools: []visibilityTool{{Name: "git_log"}, {Name: "git_status"}}},
		{Name: "down", Running: false, Tools: []visibilityTool{{Name: "gone"}}},
	}
}

// TestMissingProfileSelectors pins the selector grammar: a bare selector is a
// component name, not a tool name — the trap that silently produces an empty
// profile (e.g. bare "read" when the tool is edit.read).
func TestMissingProfileSelectors(t *testing.T) {
	comps := liveTestComponents()
	cases := []struct {
		sel     string
		missing bool
	}{
		{"edit", false},      // whole component
		{"edit.read", false}, // component.tool
		{"git_log", true},    // bare tool names are NOT positive selectors
		{"git.git_log", false},
		{"-bash", false},    // exclusions name a bare tool
		{"-git_log", false}, // …including ones only reachable via comp.tool
		{"-read_many", false},
		{"-read", false}, // read is a real tool, so its exclusion is nameable
		{"read", true},   // read lives in edit — the reported trap
		{"nope", true},
		{"edit.undo_last_edit", true}, // hidden tools are not nameable
		// Registered-but-stopped components stay nameable: a profile resolves
		// at a future conversation's first turn, when the component may be
		// back. Only currently-hidden tools are unreachable.
		{"down", false},
		{"down.gone", false},
	}
	for _, tc := range cases {
		got := missingProfileSelectors([]string{tc.sel}, comps)
		if tc.missing && len(got) == 0 {
			t.Errorf("selector %q resolved, want missing", tc.sel)
		}
		if !tc.missing && len(got) != 0 {
			t.Errorf("selector %q reported missing: %v", tc.sel, got)
		}
	}
}

// formField sets a form field by index.
func formField(m *model, index int, value string) {
	m.profileForm.inputs[index].SetValue(value)
}

// TestProfileFormRejectsBadNames covers the client-side guards that must fire
// before any store write.
func TestProfileFormRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "   ", "default", "two words"} {
		m := newTestModel()
		m.profileForm = newProfileForm(80, LocaleEN)
		formField(&m, 0, name)
		formField(&m, 1, "edit.read")
		if _, err := m.profileForm.values(); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	// A valid draft round-trips, and the selectors split on both separators.
	m := newTestModel()
	m.profileForm = newProfileForm(80, LocaleEN)
	formField(&m, 0, "my-recon")
	formField(&m, 1, "edit.read, git")
	draft, err := m.profileForm.values()
	if err != nil {
		t.Fatalf("valid draft rejected: %v", err)
	}
	if draft.name != "my-recon" || len(draft.tools) != 2 {
		t.Fatalf("draft = %+v", draft)
	}
}

// TestProfileFormNewEntryOpens covers the picker entry that starts the flow.
func TestProfileFormNewEntryOpens(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.openProfileSelectorWith(testProfiles())
	items := m.selector.list.Items()
	newIndex := -1
	for i, item := range items {
		if item.(selectorItem).kind == selectorProfileNew {
			newIndex = i
		}
	}
	if newIndex < 0 {
		t.Fatalf("no + New profile entry among %d items", len(items))
	}
	m.selector.list.Select(newIndex)
	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeProfileForm {
		t.Fatalf("new entry mode = %v, want modeProfileForm", got.mode)
	}
	if !strings.Contains(got.View().Content, "New profile") {
		t.Fatalf("form did not render:\n%s", got.View().Content)
	}
	// Esc returns to the picker and re-loads it.
	updated, cmd := got.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	got = updated.(model)
	if got.mode != modeProfiles || cmd == nil {
		t.Fatalf("esc: mode = %v cmd = %v, want picker + reload", got.mode, cmd)
	}
}

// requestKind drives the fake transport: refuseBeforeSave never reaches the
// save call, saveOK returns ok:true, saveNotOK reaches save with ok:false.
type requestKind int

const (
	refuseBeforeSave requestKind = iota
	saveOK
	saveNotOK
)

func fakeRequest(t *testing.T, kind requestKind, profiles []toolProfileSummary, comps []visibilityComponent) func(string, map[string]any, any) error {
	return func(tool string, args map[string]any, out any) error {
		switch tool {
		case "profile":
			if args["op"] == "list" {
				out.(*profilesResponse).Profiles = profiles
				return nil
			}
			if kind == refuseBeforeSave {
				t.Fatal("save must not be reached")
			}
			if kind == saveOK {
				out.(*okResponse).OK = true
			}
			return nil
		case "status":
			out.(*struct {
				Components []visibilityComponent `json:"components"`
			}).Components = comps
			return nil
		}
		t.Fatalf("unexpected tool %q", tool)
		return nil
	}
}

// TestProfileFormSaveFlow drives the full save through a fake transport:
// list → status → save, then the confirmation selects the new profile.
func TestProfileFormSaveFlow(t *testing.T) {
	var calls []string
	request := func(tool string, args map[string]any, out any) error {
		calls = append(calls, tool)
		if tool == "profile" && args["op"] == "save" {
			if args["name"] != "recon" {
				t.Fatalf("save name = %v", args["name"])
			}
			tools, _ := args["tools"].([]string)
			if len(tools) != 1 || tools[0] != "edit.read" {
				t.Fatalf("save tools = %#v", args["tools"])
			}
			if args["note"] != "n" {
				t.Fatalf("save note = %#v", args["note"])
			}
			out.(*okResponse).OK = true
			return nil
		}
		return fakeRequest(t, saveOK, []toolProfileSummary{{Name: "review"}}, liveTestComponents())(tool, args, out)
	}

	draft := profileDraft{name: "recon", tools: []string{"edit.read"}, note: "n"}
	if err := createProfile(draft, LocaleEN, request); err != nil {
		t.Fatalf("createProfile: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %v, want list, status, save", calls)
	}

	// The completion must select the new profile and return to the picker.
	m := newTestModel()
	m.width, m.height = 80, 24
	m.mode = modeProfileForm
	m.profileForm = newProfileForm(80, LocaleEN)
	m.profileForm.saving = true
	updated, cmd := m.Update(profileSavedMsg{name: "recon"})
	got := updated.(model)
	if got.mode != modeProfiles {
		t.Fatalf("after save mode = %v, want the picker", got.mode)
	}
	if got.toolProfile != "recon" {
		t.Fatalf("saved profile not selected: %q", got.toolProfile)
	}
	if cmd == nil {
		t.Fatal("after save the picker list was not reloaded")
	}
}

// TestProfileFormSaveRefusals covers every refusal path: an existing name, an
// unresolvable selector, and a save the store did not acknowledge. None may
// write, and each must surface in the form rather than leaving it spinning.
func TestProfileFormSaveRefusals(t *testing.T) {
	cases := []struct {
		name    string
		draft   profileDraft
		request func(string, map[string]any, any) error
		want    string
	}{
		{"duplicate", profileDraft{name: "review", tools: []string{"edit"}},
			fakeRequest(t, refuseBeforeSave, []toolProfileSummary{{Name: "review"}}, liveTestComponents()), "exists"},
		{"unresolved", profileDraft{name: "recon", tools: []string{"read"}},
			fakeRequest(t, refuseBeforeSave, nil, liveTestComponents()), "Unresolved"},
		{"no ack", profileDraft{name: "recon", tools: []string{"edit"}},
			fakeRequest(t, saveNotOK, nil, liveTestComponents()), "acknowledged"},
	}
	for _, tc := range cases {
		err := createProfile(tc.draft, LocaleEN, tc.request)
		if err == nil {
			t.Errorf("%s: createProfile succeeded, want refusal", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q, want it to mention %q", tc.name, err, tc.want)
		}
	}

	// A refused save keeps the form open with the error, draft intact.
	m := newTestModel()
	m.width, m.height = 80, 24
	m.mode = modeProfileForm
	m.profileForm = newProfileForm(80, LocaleEN)
	m.profileForm.saving = true
	formField(&m, 0, "recon")
	updated, _ := m.Update(profileSavedMsg{err: errProfiles})
	got := updated.(model)
	if got.mode != modeProfileForm {
		t.Fatalf("refusal mode = %v, want the form to stay open", got.mode)
	}
	if got.profileForm.saving {
		t.Fatal("refusal left the form spinning")
	}
	if !strings.Contains(got.profileForm.err, "boom") {
		t.Fatalf("refusal error = %q", got.profileForm.err)
	}
	if got.toolProfile != "" {
		t.Fatalf("refusal selected a profile: %q", got.toolProfile)
	}
	// The draft survives so the user can fix the selectors and retry.
	if v := got.profileForm.inputs[0].Value(); v != "recon" {
		t.Fatalf("draft lost: name field = %q", v)
	}
}
