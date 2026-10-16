// Skills, from the client's side: list them as a menu, mark the ones this
// conversation has already loaded, and load one on selection.
//
// The component's own `/skills` slash command routes to `skill_list` and prints
// its JSON, which makes the listing unusable as a menu and says nothing about
// what is already in context — so the TUI declares `/skills` itself (a built-in
// shadows a component registration by design, tui/slash.go). The component
// stays the single source of truth: the list comes from `skill_list`, and the
// load goes through the model's own `skill_load`, so the size cap, the
// discovery order and the shadowing rules have exactly one implementation.
//
// Loading a skill is a conversation-content change, and the only client-visible
// way to append to a conversation today is a turn (core exposes `discovery` for
// schemas but no general "append this text" op). So a selection sends an
// explicit one-line prompt asking for the load. That keeps the human in the
// loop for what enters context, and costs one turn — the alternative, a
// core-side op that appends the instructions directly, is a harness change.

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// skillSummary is one skill as the picker shows it: the fields a human
// chooses by, not the whole SKILL.md. `source` is the discovery root the
// winner came from (project | bundled | home | config), which is what tells
// two same-named copies apart — the component already resolved the shadowing,
// so the same name never appears twice here.
type skillSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Dir         string   `json:"dir"`
	Tags        []string `json:"tags"`
}

// skillsResponse decodes skill_list's reply: {ok, count, skills:[…]}. A count
// with no skills array means the component answered something unexpected, not
// that the harness has no skills — the caller stays empty rather than claiming
// zero, so a shape change cannot read as "nothing installed".
type skillsResponse struct {
	Count  int            `json:"count"`
	Skills []skillSummary `json:"skills"`
	Error  string         `json:"error"`
}

// skillsListMsg carries the loaded skill list back into the update loop.
type skillsListMsg struct {
	Skills []skillSummary
	Err    error
}

// skillsListCmd asks the skills component for the local list. No query: the
// picker filters client-side (it already has fuzzy filtering), so opening it
// costs one call instead of one per keystroke, and `skill_list`'s own
// substring filter stays available for the model.
func skillsListCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		var response skillsResponse
		err := requestInto(comp, "skills", "skill_list", map[string]any{}, &response)
		if err != nil {
			return skillsListMsg{Err: err}
		}
		if response.Error != "" {
			return skillsListMsg{Err: fmt.Errorf("%s", response.Error)}
		}
		return skillsListMsg{Skills: response.Skills}
	}
}

// loadedSkillNames reports which skills this conversation has already loaded.
//
// Derived from the transcript rather than tracked separately: a load IS a
// `skill_load` tool call in the history, so the transcript is the record that
// cannot drift from what the model actually received. Scanning blocks covers
// both the live event stream and a replayed history, since replay rebuilds the
// same tool-run blocks (session_history.go).
func loadedSkillNames(blocks []transcriptBlock) map[string]bool {
	loaded := map[string]bool{}
	for i := range blocks {
		run := blocks[i].run
		if blocks[i].kind != blockTool || run == nil {
			continue
		}
		for j := range run.calls {
			call := &run.calls[j]
			if call.name != "skill_load" || len(call.args) == 0 {
				continue
			}
			// The call failed if it carries an error: a refused load left
			// nothing in context, so it must not read as loaded.
			if call.err != "" {
				continue
			}
			var args struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(call.args, &args); err != nil {
				continue
			}
			if name := strings.TrimSpace(args.Name); name != "" {
				loaded[name] = true
			}
		}
	}
	return loaded
}

// openSkillsSelector opens the /skills menu with a loading placeholder; the
// skillsListMsg handler rebuilds it with the loaded list, so a slow component
// shows the menu immediately rather than nothing.
func (m *model) openSkillsSelector() {
	m.openSkillsSelectorWith(nil)
}

func (m *model) openSkillsSelectorWith(skills []skillSummary) {
	title := t(m.loc, "selector.skills")
	if skills == nil {
		title = t(m.loc, "selector.skillsLoading")
	}
	loaded := loadedSkillNames(m.blocks)
	m.selector = newSelector(title, skillSelectorItems(m.loc, skills, loaded),
		m.width, m.height-3)
	m.mode = modeSkills
	m.layout()
}

// skillSelectorItems builds the menu: name first (that is what a human scans
// for), the discovery source as a qualifier, then the description. A loaded
// skill is marked with the same "● " convention the toolset and agent pickers
// use for the current choice, so "already in context" reads at a glance.
//
// Loaded skills stay selectable and sorted first: re-loading is legitimate (the
// instructions may have changed on disk, or the earlier load may have been
// compacted out of context), and burying them would make the one action a
// returning user wants the hardest to reach.
func skillSelectorItems(loc Locale, skills []skillSummary, loaded map[string]bool) []list.Item {
	items := make([]list.Item, 0, len(skills))
	sorted := append([]skillSummary(nil), skills...)
	sort.SliceStable(sorted, func(i, j int) bool {
		li, lj := loaded[sorted[i].Name], loaded[sorted[j].Name]
		if li != lj {
			return li // loaded first
		}
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	for _, skill := range sorted {
		title := skill.Name
		if loaded[skill.Name] {
			title = "● " + title
		}
		var parts []string
		if skill.Source != "" {
			parts = append(parts, skill.Source)
		}
		if skill.Description != "" {
			parts = append(parts, skill.Description)
		}
		items = append(items, selectorItem{
			kind:        selectorSkill,
			id:          skill.Name,
			title:       title,
			description: strings.Join(parts, " · "),
			payload:     skill,
		})
	}
	return items
}

// skillLoadPrompt is the turn a selection sends. It names the tool explicitly
// so the load does not depend on the model inferring which mechanism "use this
// skill" means, and it asks for the skill to be followed rather than merely
// read — the documented meaning of loading one.
func skillLoadPrompt(name string) string {
	return "Load the \"" + name + "\" skill (skill_load) and follow its instructions for this task."
}
