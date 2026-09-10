package main

import (
	"strings"
	"testing"
)

func visComp(name string, tools ...string) visibilityComponent {
	c := visibilityComponent{Name: name, Running: true}
	for _, t := range tools {
		var tool visibilityTool
		tool.Name = t
		c.Tools = append(c.Tools, tool)
	}
	return c
}

func hiddenVisComp(name string, tools ...string) visibilityComponent {
	c := visComp(name, tools...)
	for i := range c.Tools {
		c.Tools[i].Schema.Harness.Hidden = true
	}
	return c
}

// TestRenderComponentsFilterNeverEmpty is the regression for
// `/components undiscovered` printing a blank block. Every accepted filter
// must either list something or say plainly that the set is empty.
func TestRenderComponentsFilterNeverEmpty(t *testing.T) {
	comps := []visibilityComponent{
		visComp("plugins", "plugin_install", "plugin_update"),
		visComp("git", "git_status", "git_log"),
	}
	exposure := &exposureDoc{Direct: []toolRef{{Component: "git", Name: "git_status"}}}

	for _, filter := range []string{"", "all", "direct", "discovered", "undiscovered", "unknown"} {
		text, err := renderComponents(comps, exposure, filter)
		if err != nil {
			t.Fatalf("filter %q: unexpected error: %v", filter, err)
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("filter %q rendered an empty block", filter)
		}
	}
}

// TestRenderComponentsUndiscoveredSelectsWithoutSnapshot pins the reported
// symptom: with no exposure doc every tool is "unknown", and the
// "undiscovered" filter used to match nothing at all.
func TestRenderComponentsUndiscoveredSelectsWithoutSnapshot(t *testing.T) {
	comps := []visibilityComponent{visComp("plugins", "plugin_install", "plugin_update")}

	text, err := renderComponents(comps, nil, "undiscovered")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "plugin_install") || !strings.Contains(text, "plugin_update") {
		t.Fatalf("undiscovered should list visible tools without a snapshot, got:\n%s", text)
	}
	if !strings.Contains(text, "exposure unknown") {
		t.Errorf("expected the unknown-exposure note to still be shown, got:\n%s", text)
	}

	// The "unknown" state must be selectable too, so the tools are reachable
	// under the state name the renderer actually assigns.
	unknown, err := renderComponents(comps, nil, "unknown")
	if err != nil {
		t.Fatalf("unknown filter rejected: %v", err)
	}
	if !strings.Contains(unknown, "plugin_install") {
		t.Fatalf("unknown should list visible tools without a snapshot, got:\n%s", unknown)
	}
}

// TestRenderComponentsStatesAndHidden keeps the state machine honest: hidden
// tools are hidden under every filter, and a restored snapshot classifies
// direct and discovered tools distinctly.
func TestRenderComponentsStatesAndHidden(t *testing.T) {
	comps := []visibilityComponent{
		visComp("plugins", "plugin_install", "plugin_search"),
		hiddenVisComp("core", "discover"),
	}
	exposure := &exposureDoc{
		Direct:     []toolRef{{Component: "plugins", Name: "plugin_install"}},
		Discovered: []toolRef{{Component: "plugins", Name: "plugin_search"}},
	}

	all, err := renderComponents(comps, exposure, "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"plugin_install [direct]", "plugin_search [discovered]", "discover [hidden]"} {
		if !strings.Contains(all, want) {
			t.Errorf("expected %q in:\n%s", want, all)
		}
	}

	// A hidden tool must not be selectable by any exposure filter.
	for _, filter := range []string{"direct", "discovered", "undiscovered", "unknown"} {
		text, err := renderComponents(comps, exposure, filter)
		if err != nil {
			t.Fatalf("filter %q: unexpected error: %v", filter, err)
		}
		if strings.Contains(text, "discover [") {
			t.Errorf("filter %q leaked a hidden tool:\n%s", filter, text)
		}
	}

	// With a snapshot present, an unlisted tool is genuinely undiscovered
	// and must not be relabelled just because the filter asked for it.
	text, err := renderComponents(comps, exposure, "undiscovered")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "undiscovered") && !strings.Contains(text, "No undiscovered tools.") {
		t.Errorf("unexpected undiscovered rendering:\n%s", text)
	}
}

// TestRenderComponentsRejectsUnknownFilter guards the validator message.
func TestRenderComponentsRejectsUnknownFilter(t *testing.T) {
	if _, err := renderComponents(nil, nil, "bogus"); err == nil {
		t.Fatal("expected an error for an unsupported filter")
	}
}

// TestRenderComponentsEmptySetIsExplained is the other half of the blank
// block bug: a filter matching nothing renders text, not "".
func TestRenderComponentsEmptySetIsExplained(t *testing.T) {
	comps := []visibilityComponent{visComp("plugins", "plugin_install")}
	exposure := &exposureDoc{Direct: []toolRef{{Component: "plugins", Name: "plugin_install"}}}

	text, err := renderComponents(comps, exposure, "discovered")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("an empty result set must not render as a blank block")
	}
	if !strings.Contains(text, "No discovered tools.") {
		t.Fatalf("expected an explanatory empty message, got:\n%s", text)
	}
}
