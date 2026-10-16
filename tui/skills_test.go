// Tests for the client side of skills (skills.go): which skills read as
// loaded, the menu's ordering and marks, the load prompt, and the refusal to
// load mid-turn.

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// toolBlock builds one tool-run block carrying a single call, the same shape
// the live event stream and a replayed history both produce.
func toolBlock(name, args, errText string) transcriptBlock {
	call := toolCall{name: name, callID: "c1"}
	if args != "" {
		call.args = json.RawMessage(args)
	}
	if errText != "" {
		call.err = errText
	}
	return transcriptBlock{kind: blockTool, run: &toolRun{calls: []toolCall{call}}}
}

// TestLoadedSkillNames pins what counts as loaded. The transcript is the record
// of what the model actually received, so a load that FAILED must not read as
// loaded — otherwise the menu claims the instructions are in context when the
// call was refused.
func TestLoadedSkillNames(t *testing.T) {
	blocks := []transcriptBlock{
		{kind: blockUser, text: "hello"},
		{kind: blockAssistant, text: "hi"},
		toolBlock("read", `{"path":"x.go"}`, ""),
		toolBlock("skill_load", `{"name":"niffler-harness"}`, ""),
		toolBlock("skill_load", `{"name":"niffler-tools"}`, "skill not found: niffler-tools"),
		toolBlock("skill_load", `{}`, ""),                         // no name
		toolBlock("skill_load", `{"name":"  "}`, ""),              // blank name
		toolBlock("skill_load", `not json`, ""),                   // undecodable
		toolBlock("skill_load", `{"name":"niffler-harness"}`, ""), // re-load
	}
	got := loadedSkillNames(blocks)

	if !got["niffler-harness"] {
		t.Fatalf("a successful load is not marked loaded: %v", got)
	}
	if got["niffler-tools"] {
		t.Fatalf("a REFUSED load is marked loaded: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("loaded set = %v, want exactly niffler-harness", got)
	}
}

func TestLoadedSkillNamesIgnoresNonToolBlocks(t *testing.T) {
	// A skill named in prose is not a load: only a tool call is evidence.
	got := loadedSkillNames([]transcriptBlock{
		{kind: blockUser, text: `let me read {"name":"niffler-harness"}`},
		{kind: blockTool}, // a tool block with no run
	})
	if len(got) != 0 {
		t.Fatalf("loaded set from prose = %v, want empty", got)
	}
}

func TestSkillSelectorItemsOrderAndMarks(t *testing.T) {
	skills := []skillSummary{
		{Name: "zeta", Description: "last alphabetically", Source: "bundled"},
		{Name: "alpha", Description: "first alphabetically", Source: "project"},
		{Name: "mid", Description: "loaded, so it sorts first", Source: "home"},
	}
	loaded := map[string]bool{"mid": true}
	items := skillSelectorItems(LocaleEN, skills, loaded)

	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	first, ok := items[0].(selectorItem)
	if !ok {
		t.Fatalf("item 0 is %T, want selectorItem", items[0])
	}
	// Loaded skills sort first: re-loading is the action a returning user
	// most often wants, so it must not be buried.
	if first.id != "mid" {
		t.Fatalf("first item = %q, want the loaded one (mid)", first.id)
	}
	if !strings.HasPrefix(first.title, "● ") {
		t.Fatalf("loaded item is not marked: %q", first.title)
	}
	if !strings.Contains(first.description, "home") {
		t.Fatalf("description does not name the discovery source: %q", first.description)
	}

	// The rest stay alphabetical (case-insensitive).
	second, _ := items[1].(selectorItem)
	third, _ := items[2].(selectorItem)
	if second.id != "alpha" || third.id != "zeta" {
		t.Fatalf("unloaded order = %q, %q; want alpha, zeta", second.id, third.id)
	}
	if strings.HasPrefix(second.title, "● ") {
		t.Fatalf("an unloaded item is marked: %q", second.title)
	}

	// Every item carries the summary for the selection handler, and the kind
	// the mode switches on.
	for _, item := range items {
		it := item.(selectorItem)
		if it.kind != selectorSkill {
			t.Fatalf("item %q kind = %v, want selectorSkill", it.id, it.kind)
		}
		if _, ok := it.payload.(skillSummary); !ok {
			t.Fatalf("item %q payload = %T, want skillSummary", it.id, it.payload)
		}
	}
}

func TestSkillLoadPromptNamesTheTool(t *testing.T) {
	// The prompt must name skill_load explicitly: the load then does not
	// depend on the model inferring which mechanism "use this skill" means.
	got := skillLoadPrompt("niffler-harness")
	if !strings.Contains(got, "skill_load") {
		t.Fatalf("prompt does not name the tool: %q", got)
	}
	if !strings.Contains(got, "niffler-harness") {
		t.Fatalf("prompt does not name the skill: %q", got)
	}
}

// TestLoadSkillRefusesMidTurn: a load enters context as a turn, so mid-turn it
// is refused with a note rather than queued — the human retries when they
// choose, instead of the instructions arriving at a moment they did not pick.
func TestLoadSkillRefusesMidTurn(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.busy = true
	m.contextNote = ""

	updated, cmd := m.loadSkill("niffler-harness")
	m = updated.(model)
	if cmd != nil {
		t.Fatal("loadSkill returned a command while busy")
	}
	if m.contextNote == "" {
		t.Fatal("no note explaining the refusal")
	}
	if len(m.blocks) != 0 {
		t.Fatalf("blocks rendered while busy: %+v", m.blocks)
	}
}

// TestLoadSkillStartsATurn: the accepted path marks the model busy and echoes
// the prompt as a user block, so the transcript shows what was asked.
func TestLoadSkillStartsATurn(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.session = "sess-1"

	updated, cmd := m.loadSkill("niffler-harness")
	m = updated.(model)
	if cmd == nil {
		t.Fatal("loadSkill returned no command")
	}
	if !m.busy {
		t.Fatal("model is not busy after starting a load")
	}
	if len(m.blocks) == 0 {
		t.Fatal("no user block for the load prompt")
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockUser || !strings.Contains(last.text, "niffler-harness") {
		t.Fatalf("load block = %q (kind %v)", last.text, last.kind)
	}
}

// TestLocalSkillsRefusesWhenDisconnected: nothing to load into without a bus.
func TestLocalSkillsRefusesWhenDisconnected(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = false
	cmd, _ := builtinCommand("skills")

	updated, got := localSkills(m, cmd, "")
	out := updated.(model)
	if got != nil {
		t.Fatal("localSkills returned a command while disconnected")
	}
	if out.contextNote == "" {
		t.Fatal("no note explaining the refusal")
	}
}

// TestLocalSkillsArgumentLoadsDirectly: /skills <name> skips the menu.
func TestLocalSkillsArgumentLoadsDirectly(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.session = "sess-1"
	cmd, _ := builtinCommand("skills")

	updated, got := localSkills(m, cmd, "niffler-harness")
	out := updated.(model)
	if got == nil {
		t.Fatal("no command for a direct load")
	}
	if out.mode != modeChat {
		t.Fatalf("mode = %v, want modeChat (no menu opened)", out.mode)
	}
	if !out.busy {
		t.Fatal("direct load did not start a turn")
	}
}

// TestLocalSkillsBareOpensMenu: /skills with no argument opens the picker.
func TestLocalSkillsBareOpensMenu(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.session = "sess-1"
	m.width, m.height = 100, 40
	cmd, _ := builtinCommand("skills")

	updated, got := localSkills(m, cmd, "")
	out := updated.(model)
	if got == nil {
		t.Fatal("no command for the list fetch")
	}
	if out.mode != modeSkills {
		t.Fatalf("mode = %v, want modeSkills", out.mode)
	}
}

// TestBuiltinSkillsShadowsComponentCommand pins the shadowing the README
// describes. The skills component registers its own /skills that routes to
// skill_list and prints JSON; the built-in must win, or the menu this file
// implements is unreachable. Built-ins win by construction (mergeSlashRegistry
// adds them first and `add` keeps the first registration), so this test fails
// loudly if that ordering ever changes.
func TestBuiltinSkillsShadowsComponentCommand(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{{
		Name: "skills", Description: "List skills (component)",
		Component: "skills", Tool: "skill_list",
	}})
	got, ok := m.slash.lookup("skills")
	if !ok {
		t.Fatal("/skills missing from the registry")
	}
	if !got.builtin {
		t.Fatalf("/skills resolved to the component command, not the built-in: %+v", got)
	}
	if got.run == nil {
		t.Fatal("the winning /skills has no local handler")
	}
}
