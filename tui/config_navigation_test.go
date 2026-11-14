package main

// /config is a two-level browser, and both levels use row types of their own
// (configDomainItem, configFieldItem) rather than the shared selectorItem. The
// shared enter switch at the end of handleControlKey therefore cannot serve
// them — it fails its `selectedItem.(selectorItem)` assertion — so for a while
// Enter did nothing at all and the only way in was typing the domain as an
// argument. These tests pin the navigation, not the editor: what is at stake is
// that a human who sees "enter: edit" in the footer gets an editor.

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newConfigBrowser(t *testing.T, domains []configDomain) model {
	t.Helper()
	m := newTestModel()
	m.width, m.height = 100, 40
	m.connected = true
	m.configDomains = domains
	m.openConfigDomainSelector()
	if m.mode != modeConfigDomains {
		t.Fatalf("setup: mode = %v, want modeConfigDomains", m.mode)
	}
	return m
}

func testConfigDomains() []configDomain {
	return []configDomain{
		{Domain: "repomap.gates", Title: "Repomap gates", Owner: "repomap",
			Properties: 3, Application: "immediate", OwnerAvailable: true},
		{Domain: "observe.caps", Title: "Observe caps", Owner: "observe",
			Properties: 2, Application: "next turn", OwnerAvailable: false,
			OwnerTool: "observe_set"},
	}
}

// Enter on a domain row must load that domain's fields, exactly as
// `/config <domain>` does. The command is part of the assertion: the mode alone
// would still be right if the fetch were never dispatched.
func TestConfigEnterOpensTheSelectedDomain(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())

	updated, cmd := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeConfig {
		t.Fatalf("mode = %v, want modeConfig", got.mode)
	}
	if got.configEditing != "repomap.gates" {
		t.Fatalf("configEditing = %q, want repomap.gates", got.configEditing)
	}
	if cmd == nil {
		t.Fatal("no command dispatched: the field list would never load")
	}
	if got.configNote == "" {
		t.Fatal("no loading note: the screen would look frozen while config_get runs")
	}
}

// A domain whose owner is not running is still enterable: it is listed with
// that reason precisely so the human can look, and the detail reply carries the
// explanation. Refusing the keystroke here would be the dead end again.
func TestConfigEnterOpensADomainWithNoOwner(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())
	// Second row: the selector starts on the first.
	m.selector.list.Select(1)

	updated, cmd := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeConfig || got.configEditing != "observe.caps" {
		t.Fatalf("mode=%v editing=%q, want modeConfig/observe.caps",
			got.mode, got.configEditing)
	}
	if cmd == nil {
		t.Fatal("no detail fetch dispatched for an owner-less domain")
	}
}

// Enter on a field row opens the one-field editor, prefilled with help and the
// current value. Read-only and humanOnly fields open too — the form is where
// those reasons are explained.
func TestConfigEnterOpensTheFieldEditor(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())
	m.configFields = configFields(testConfigDetail())
	m.openConfigFieldSelector()
	if m.mode != modeConfig {
		t.Fatalf("setup: mode = %v, want modeConfig", m.mode)
	}

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeConfigForm {
		t.Fatalf("mode = %v, want modeConfigForm", got.mode)
	}
	// Fields are sorted by name, so the first row is autoAppend.
	if got.configEditField.Name != "autoAppend" {
		t.Fatalf("editing %q, want autoAppend", got.configEditField.Name)
	}
	if view := got.configFormView(); view == "" {
		t.Fatal("the form rendered nothing")
	}
}

// Navigation is the whole point of a browser, and it was a dead end too: the
// config handler ran BEFORE the shared list forwarding and returned nil for
// every key except enter, so arrows, j/k, page keys, the "/" filter and even
// esc were swallowed. The list could not be scrolled and there was no way back
// out of /config except ctrl+c — while the footer advertised
// "/: filter • enter: edit • esc: back".
func TestConfigArrowsMoveTheSelection(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyDown})
	got := updated.(model)
	if idx := got.selector.list.Index(); idx != 1 {
		t.Fatalf("down: index = %d, want 1", idx)
	}
	updated, _ = got.handleControlKey(tea.KeyPressMsg{Code: tea.KeyUp})
	got = updated.(model)
	if idx := got.selector.list.Index(); idx != 0 {
		t.Fatalf("up: index = %d, want 0", idx)
	}
}

// The field list navigates the same way — it is a second, separate mode.
func TestConfigFieldListArrowsMoveTheSelection(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())
	m.configFields = configFields(testConfigDetail())
	m.openConfigFieldSelector()

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyDown})
	got := updated.(model)
	if idx := got.selector.list.Index(); idx != 1 {
		t.Fatalf("down: index = %d, want 1 (rows are still the field list)", idx)
	}
	if got.mode != modeConfig {
		t.Fatalf("mode = %v after navigating, want modeConfig", got.mode)
	}
}

// esc leaves the browser. Without it the only exit was ctrl+c, which quits
// the whole client.
func TestConfigEscLeavesTheBrowser(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(model)
	if got.mode != modeChat {
		t.Fatalf("mode = %v, want modeChat", got.mode)
	}
}

// "/" starts the fuzzy filter, which the footer promises. The list must own
// the keystrokes from then on.
func TestConfigSlashStartsTheFilter(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	got := updated.(model)
	if !got.selector.list.SettingFilter() {
		t.Fatal("\"/\" did not start the filter")
	}
	// And typing narrows: the shared forwarding must keep working while the
	// filter is open, or the box would take one character and stall.
	updated, _ = got.handleControlKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	got = updated.(model)
	if got.mode != modeConfigDomains {
		t.Fatalf("mode = %v while filtering, want modeConfigDomains", got.mode)
	}
}

// Enter still opens the row it is on AFTER navigating — the regression that a
// naive fix (forward everything, then also handle enter) would introduce.
func TestConfigEnterOpensTheNavigatedRow(t *testing.T) {
	m := newConfigBrowser(t, testConfigDomains())

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyDown})
	updated, cmd := updated.(model).handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.configEditing != "observe.caps" {
		t.Fatalf("configEditing = %q, want observe.caps (the second row)",
			got.configEditing)
	}
	if cmd == nil {
		t.Fatal("no detail fetch dispatched")
	}
}

// The browser keys must not leak into other selectors: /model and /session rows
// are selectorItems and keep their own enter handling.
func TestConfigKeysDoNotShadowOtherSelectors(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 40
	m.connected = true
	m.openModelSelector()
	before := m.mode

	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode == modeConfig || got.mode == modeConfigForm {
		t.Fatalf("enter in the model picker entered a config mode: %v", got.mode)
	}
	if before != modeModels {
		t.Fatalf("setup: mode = %v, want modeModels", before)
	}
}
