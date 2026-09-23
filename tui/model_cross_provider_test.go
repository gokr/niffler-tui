package main

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newModelPicker(providers []providerSummary, status providerStatusResponse,
	candidates []modelCandidate) model {
	m := newTestModel()
	m.width = 100
	m.height = 40
	m.providers = providers
	m.providerStatus = status
	m.allModels = candidates
	m.allModelsKey = allModelsKey(providers, status)
	m.openModelSelector() // sets mode modeModels
	return m
}

func crossProviderCandidates() []modelCandidate {
	return []modelCandidate{
		{modelSummary: modelSummary{ID: "deepseek-v4-flash", Name: "DeepSeek V4"}, Provider: "deepseek"},
		{modelSummary: modelSummary{ID: "gpt-5", Name: "GPT-5"}, Provider: "openai-codex"},
		{modelSummary: modelSummary{ID: "glm-5.3-flash", Name: "GLM"}, Provider: "xiaomi"},
	}
}

// The /model picker must list every configured provider's models together and
// tag each row with its provider, so the list's FilterValue narrows on either
// the nickname or the model id — the reason /model alone replaces /provider
// followed by /model.
func TestModelSelectorListsAllProvidersAndFiltersOnEither(t *testing.T) {
	providers := []providerSummary{
		{Nickname: "deepseek", Catalog: "deepseek"},
		{Nickname: "xiaomi", Catalog: ""},
	}
	m := newModelPicker(providers, providerStatusResponse{Source: "store"}, crossProviderCandidates())

	items := m.selector.list.Items()
	if len(items) != 4 { // "use provider default" + 3 models
		t.Fatalf("want 4 items, got %d", len(items))
	}
	byID := map[string]selectorItem{}
	for _, raw := range items {
		item := raw.(selectorItem)
		byID[item.id] = item
	}
	// Each row must carry its provider, so typing "xiaomi" or "gpt-5" both hit.
	for _, id := range []string{"deepseek-v4-flash", "gpt-5", "glm-5.3-flash"} {
		item, ok := byID[id]
		if !ok {
			t.Fatalf("model %q missing from the cross-provider list", id)
		}
		if _, ok := item.payload.(modelCandidate); !ok {
			t.Fatalf("%s payload is %T, want modelCandidate", id, item.payload)
		}
	}
	if got := byID["glm-5.3-flash"].FilterValue(); !containsAll(got, "xiaomi", "glm-5.3-flash") {
		t.Fatalf("glm row must be filterable by provider and id: %q", got)
	}
	if got := byID["deepseek-v4-flash"].FilterValue(); !containsAll(got, "deepseek") {
		t.Fatalf("deepseek row must carry its provider: %q", got)
	}
}

// Typing in the model picker starts the fuzzy filter without pressing "/" first
// (other selectors keep their command keys: only modeModels auto-filters), and
// the filter narrows on the provider nickname.
func TestModelPickerTypeToFilterNarrowsByProvider(t *testing.T) {
	m := newModelPicker(
		[]providerSummary{{Nickname: "deepseek", Catalog: "deepseek"}, {Nickname: "xiaomi"}},
		providerStatusResponse{Source: "store"}, crossProviderCandidates())
	before := len(m.selector.list.VisibleItems())

	// No "/" — the first printable key must open the filter AND become its
	// first character, so typing "xiaomi" straight through works.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = updated.(model)
	if !m.selector.list.SettingFilter() {
		t.Fatal("typing did not start filtering in the model picker")
	}
	for _, ch := range "iaomi" {
		var cmd tea.Cmd
		updated, cmd = m.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)})
		m = updated.(model)
		for _, msg := range filterMatchesFrom(cmd) {
			updated, _ = m.Update(msg)
			m = updated.(model)
		}
	}
	visible := m.selector.list.VisibleItems()
	if len(visible) >= before {
		t.Fatalf("filter did not narrow: %d visible (was %d)", len(visible), before)
	}
	if len(visible) != 1 {
		t.Fatalf("want only the xiaomi row, got %d", len(visible))
	}
	if id := visible[0].(selectorItem).id; id != "glm-5.3-flash" {
		t.Fatalf("filtered to %q, want glm-5.3-flash", id)
	}
}

// Navigation and the list's own "/" keep working: they are not auto-filter keys.
func TestAutoFilterRuneIgnoresNavigationAndSlash(t *testing.T) {
	yes := []tea.KeyPressMsg{
		{Code: 'x', Text: "x"}, {Code: '?', Text: "?"}, {Code: '1', Text: "1"},
	}
	no := []tea.KeyPressMsg{
		{Code: '/', Text: "/"},         // the list's own filter key
		{Code: 'a', Text: ""},          // ctrl/a has no printable text
		{Code: tea.KeyEnter, Text: ""}, // enter
		{Code: tea.KeyDown, Text: ""},  // arrow
	}
	for _, msg := range yes {
		if !autoFilterRune(msg) {
			t.Errorf("autoFilterRune(%q) = false, want true", msg.String())
		}
	}
	for _, msg := range no {
		if autoFilterRune(msg) {
			t.Errorf("autoFilterRune(%q) = true, want false", msg.String())
		}
	}
}

// The cached cross-provider list is valid only for the provider set it was
// built from: adding a provider must invalidate it (refetch on next /model).
func TestAllModelsKeyInvalidatesOnProviderChange(t *testing.T) {
	providers := []providerSummary{{Nickname: "deepseek", Catalog: "deepseek"}}
	status := providerStatusResponse{Source: "store"}
	m := newModelPicker(providers, status, crossProviderCandidates())
	if !m.allModelsUsable() {
		t.Fatal("cache for the current provider set should be usable")
	}
	m.providers = append(m.providers, providerSummary{Nickname: "xiaomi"})
	if m.allModelsUsable() {
		t.Fatal("provider set changed but the cached list was still used")
	}
}

// A cross-provider pick pins provider AND model; a failed save rolls both back,
// and a model-only failure must not clear the provider pin.
func TestModelActionCommitsAndRollsBackProviderPin(t *testing.T) {
	m := newTestModel()
	m.session = "s1"
	m.providerOverride = "deepseek"
	m.modelOverride = "old-model"

	updated, _ := m.Update(modelActionMsg{
		Session: "s1", Selected: "new-model", Previous: "old-model",
		SelectedProvider: "xiaomi", PreviousProvider: "deepseek", ProviderChanged: true,
		Runtime: runtimeResolution{OK: true, Provider: "xiaomi", Model: "new-model"},
	})
	m = updated.(model)
	if m.providerOverride != "xiaomi" || m.modelOverride != "new-model" {
		t.Fatalf("commit: provider=%q model=%q", m.providerOverride, m.modelOverride)
	}

	m.controlPending = true
	updated, _ = m.Update(modelActionMsg{
		Session: "s1", Selected: "broken", Previous: "new-model",
		SelectedProvider: "openai-codex", PreviousProvider: "xiaomi", ProviderChanged: true,
		Err: errors.New("save failed"),
	})
	m = updated.(model)
	if m.providerOverride != "xiaomi" || m.modelOverride != "new-model" {
		t.Fatalf("rollback: provider=%q model=%q", m.providerOverride, m.modelOverride)
	}

	// A model-only failure keeps the pin it never tried to change.
	m.controlPending = true
	updated, _ = m.Update(modelActionMsg{
		Session: "s1", Selected: "broken2", Previous: "new-model",
		Err: errors.New("save failed"),
	})
	m = updated.(model)
	if m.providerOverride != "xiaomi" || m.modelOverride != "new-model" {
		t.Fatalf("model-only rollback touched the pin: provider=%q model=%q",
			m.providerOverride, m.modelOverride)
	}
}

// A global provider switch from another client must not strip this
// conversation's model choice when it has its own provider pin — that was the
// cross-TUI bleed (bar shows the pin, request follows the global switch).
func TestExternalSwitchKeepsPinnedConversationModel(t *testing.T) {
	m := newTestModel()
	m.session = "s1"
	m.providerOverride = "xiaomi"
	m.modelOverride = "pinned-model"

	updated, _ := m.Update(providerBusEventMsg{Changed: true})
	pinned := updated.(model)
	if pinned.modelOverride != "pinned-model" {
		t.Fatalf("pinned conversation lost its model on an external switch: %q",
			pinned.modelOverride)
	}

	// Without a provider pin the conversation follows the global backend, so
	// the old model must be released exactly as before.
	m.providerOverride = ""
	m.modelOverride = "global-model"
	updated, _ = m.Update(providerBusEventMsg{Changed: true})
	unpinned := updated.(model)
	if unpinned.modelOverride != "" {
		t.Fatalf("unpinned conversation kept a stale model: %q", unpinned.modelOverride)
	}
}

// Selecting a row from another provider pins it optimistically and returns a
// save command — the cross-provider half of the UX (/model alone, no /provider
// step). This is the decision the returned command then persists atomically.
func TestCrossProviderSelectionPinsProvider(t *testing.T) {
	providers := []providerSummary{
		{Nickname: "deepseek", Catalog: "deepseek"},
		{Nickname: "xiaomi", Catalog: ""},
	}
	m := newModelPicker(providers, providerStatusResponse{Source: "store"},
		crossProviderCandidates())
	m.providerOverride = "deepseek"
	m.modelOverride = "deepseek-v4-flash"

	// Rows are default + candidates in order: move to xiaomi's glm row.
	for i := 0; i < 3; i++ {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = updated.(model)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)

	if m.providerOverride != "xiaomi" || m.modelOverride != "glm-5.3-flash" {
		t.Fatalf("selection did not pin provider+model: provider=%q model=%q",
			m.providerOverride, m.modelOverride)
	}
	if !m.controlPending {
		t.Fatal("selection did not enter the pending-save state")
	}
	if cmd == nil {
		t.Fatal("cross-provider selection returned no save command")
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
