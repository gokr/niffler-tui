package main

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// TestProviderSelectorFilterNarrowsList is the regression for the /provider
// filter box accepting text but never narrowing the list: filtering runs
// asynchronously (list.filterItems returns a FilterMatchesMsg command) and the
// selector Update loop forwarded key presses to the list but dropped that
// resulting message, so m.filteredItems was never rebuilt.
func TestProviderSelectorFilterNarrowsList(t *testing.T) {
	m := newTestModel()
	m.width = 100
	m.height = 40
	m.providers = []providerSummary{
		{Nickname: "deepseek", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"},
		{Nickname: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Model: "openrouter/auto"},
		{Nickname: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4-6"},
	}
	m.providerStatus = providerStatusResponse{Source: "store"}
	m.openProviderSelector()
	before := len(m.selector.list.VisibleItems())

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updated.(model)
	if !m.selector.list.SettingFilter() {
		t.Fatal("pressing / did not start filtering")
	}

	for _, ch := range "deepseek" {
		var cmd tea.Cmd
		updated, cmd = m.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)})
		m = updated.(model)
		// Feed the filter result back the way the bubbletea runtime would.
		for _, msg := range filterMatchesFrom(cmd) {
			updated, _ = m.Update(msg)
			m = updated.(model)
		}
	}

	visible := m.selector.list.VisibleItems()
	if len(visible) >= before {
		t.Fatalf("filter did not narrow the list: %d visible (was %d)", len(visible), before)
	}
	var nicknames []string
	for _, raw := range visible {
		nicknames = append(nicknames, raw.(selectorItem).title)
	}
	found, leaked := false, false
	for _, name := range nicknames {
		if name == "deepseek" {
			found = true
		}
		if name == "openrouter" || name == "anthropic" {
			leaked = true
		}
	}
	if !found {
		t.Fatalf("filtered list dropped the matching provider: %v", nicknames)
	}
	if leaked {
		t.Fatalf("filtered list kept non-matching providers: %v", nicknames)
	}
}

// filterMatchesFrom runs a command, expanding a batch, and returns the
// asynchronous FilterMatchesMsg values it produced. Commands that block
// (textinput.Blink) are abandoned after a short timeout so the test does not
// wait on a cursor-blink timer.
func filterMatchesFrom(cmd tea.Cmd) []list.FilterMatchesMsg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(250 * time.Millisecond):
		return nil
	}
	switch mm := msg.(type) {
	case list.FilterMatchesMsg:
		return []list.FilterMatchesMsg{mm}
	case tea.BatchMsg:
		var out []list.FilterMatchesMsg
		for _, sub := range mm {
			out = append(out, filterMatchesFrom(sub)...)
		}
		return out
	}
	return nil
}
