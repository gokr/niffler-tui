package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The agent definition summary comes from a store list of kind "agent": the
// documents carry an optional `name`, so the store id is the fallback — a
// definition is identified by its key, like a toolset or a persona.
func TestAgentDefsResponsePrefersNameThenID(t *testing.T) {
	var response agentDefsResponse
	raw := `{"items":[
	  {"id":"reviewer","value":{"persona":"reviewer","toolset":"read","model":"m","thinking":"high"}},
	  {"id":"triager","value":{"name":"triager","note":"nightly"}}
	]}`
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Agents) != 2 {
		t.Fatalf("decoded %d definitions, want 2: %#v", len(response.Agents), response)
	}
	if response.Agents[0].Name != "reviewer" || response.Agents[0].Persona != "reviewer" {
		t.Fatalf("id fallback failed: %#v", response.Agents[0])
	}
	if response.Agents[1].Name != "triager" || response.Agents[1].Note != "nightly" {
		t.Fatalf("explicit name/note lost: %#v", response.Agents[1])
	}
}

// The picker marks the current choice and shows each definition's building
// blocks, with the "no agent" entry always first so clearing is one keypress.
func TestAgentDefSelectorItems(t *testing.T) {
	agents := []agentDefSummary{
		{Name: "reviewer", Persona: "reviewer", Toolset: "read", Thinking: "high"},
		{Name: "bare"},
	}
	items := agentDefSelectorItems(LocaleEN, "reviewer", agents)
	if len(items) != 3 {
		t.Fatalf("items = %d, want no-agent + 2 definitions", len(items))
	}
	first := items[0].(selectorItem)
	if first.kind != selectorAgentDefNone {
		t.Fatalf("first entry kind = %v, want the no-agent entry", first.kind)
	}
	current := items[1].(selectorItem)
	if current.kind != selectorAgentDef || current.id != "reviewer" {
		t.Fatalf("second entry = %#v", current)
	}
	if !strings.HasPrefix(current.title, "● ") {
		t.Fatalf("current selection not marked: %q", current.title)
	}
	if !strings.Contains(current.description, "persona: reviewer") ||
		!strings.Contains(current.description, "toolset: read") {
		t.Fatalf("building blocks missing: %q", current.description)
	}
	bare := items[2].(selectorItem)
	if bare.description == "" || strings.Contains(bare.description, "persona:") {
		t.Fatalf("a bare definition should say so, got %q", bare.description)
	}
	// With nothing selected, the no-agent entry is the marked one.
	unset := agentDefSelectorItems(LocaleEN, "", agents)[0].(selectorItem)
	if !strings.HasPrefix(unset.title, "● ") {
		t.Fatalf("unset choice not marked: %q", unset.title)
	}
}

// describeAgentChange names what will freeze into the next conversation, and
// the empty case says plainly that the choice was cleared.
func TestDescribeAgentChange(t *testing.T) {
	agents := []agentDefSummary{{Name: "reviewer", Persona: "reviewer", Toolset: "read"}}
	if got := describeAgentChange(LocaleEN, "", agents); !strings.Contains(got, "cleared") {
		t.Fatalf("cleared case = %q", got)
	}
	got := describeAgentChange(LocaleEN, "reviewer", agents)
	if !strings.Contains(got, "reviewer") || !strings.Contains(got, "persona: reviewer") {
		t.Fatalf("selection line = %q", got)
	}
	// A name with no matching summary still names the agent.
	if got := describeAgentChange(LocaleEN, "ghost", nil); !strings.Contains(got, "ghost") {
		t.Fatalf("fallback line = %q", got)
	}
}

// /agent is registered, and its help line exists in every locale — the same
// drift check the other commands get.
func TestAgentCommandRegistered(tt *testing.T) {
	entry, ok := builtinCommand("agent")
	if !ok {
		tt.Fatal("/agent is not registered")
	}
	if entry.aliasOf != "" || entry.run == nil {
		tt.Fatalf("agent command = %#v", entry)
	}
	// Every locale EXCEPT English must actually carry the strings: t() falls
	// back to the English catalog, so a missing translation would silently
	// render English instead of failing.
	for _, loc := range []Locale{LocaleZH, LocaleZHTW} {
		for _, key := range []string{"help.agent", "agent.cleared", "selector.agents"} {
			if _, ok := catalogs[loc][key]; !ok {
				tt.Fatalf("locale %v is missing %q", loc, key)
			}
		}
	}
	if catalogs[LocaleEN]["help.agent"] == "" {
		tt.Fatal("the English catalog is missing help.agent")
	}
}
