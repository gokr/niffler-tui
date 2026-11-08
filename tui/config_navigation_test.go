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
