// Agent definitions (store kind "agent"): the birth identity for
// conversations started from here on. A persona is the text; an agent
// definition also names the toolset, model, thinking and budgets that freeze
// into a new conversation's header. Selection is CLIENT state, exactly like
// the toolset: it applies to conversations created from here on (/new, the
// session browser's new entry), never rewriting one already born — the
// harness refuses that with "agent is birth-only".
package main

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// agentDefSummary is one stored definition as the picker shows it: the display
// fields the TUI needs, not the whole document (the definition's semantics
// belong to the harness — the client only names it).
type agentDefSummary struct {
	Name     string `json:"name"`
	Note     string `json:"note"`
	Persona  string `json:"persona"`
	Toolset  string `json:"toolset"`
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
}

// agentDefsResponse decodes the store list of kind "agent". The documents carry
// `name` only when the author wrote one, so the id is the fallback: a
// definition is identified by its store key, like a toolset or a persona.
type agentDefsResponse struct {
	Agents []agentDefSummary
}

func (r *agentDefsResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		Items []struct {
			ID    string `json:"id"`
			Value struct {
				Name     string `json:"name"`
				Note     string `json:"note"`
				Persona  string `json:"persona"`
				Toolset  string `json:"toolset"`
				Model    string `json:"model"`
				Thinking string `json:"thinking"`
			} `json:"value"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	for _, item := range wire.Items {
		name := item.Value.Name
		if name == "" {
			name = item.ID
		}
		r.Agents = append(r.Agents, agentDefSummary{
			Name: name, Note: item.Value.Note, Persona: item.Value.Persona,
			Toolset: item.Value.Toolset, Model: item.Value.Model,
			Thinking: item.Value.Thinking,
		})
	}
	return nil
}

// agentDefsMsg carries a loaded definition list back into the update loop.
type agentDefsMsg struct {
	Agents []agentDefSummary
	Err    error
}

// agentDefsCmd lists the stored definitions. Read-only store access, no approval
// gate and no harness round trip beyond the store itself.
func agentDefsCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		var response agentDefsResponse
		err := requestInto(comp, "store", "list",
			map[string]any{"kind": "agent", "limit": 1000}, &response)
		return agentDefsMsg{Agents: response.Agents, Err: err}
	}
}

// openAgentSelector opens the /agent picker with a loading placeholder; the
// agentDefsMsg handler rebuilds it with the loaded list, so a slow store shows
// the picker immediately rather than nothing.
func (m *model) openAgentSelector() {
	m.openAgentSelectorWith(nil)
}

func (m *model) openAgentSelectorWith(agents []agentDefSummary) {
	title := t(m.loc, "selector.agents")
	if agents == nil {
		title = t(m.loc, "selector.agentsLoading")
	}
	m.selector = newSelector(title,
		agentDefSelectorItems(m.loc, m.agent, agents), m.width, m.height-3)
	m.mode = modeAgents
	m.layout()
}

// describeAgentChange is the confirmation line for a selection: it names what
// will freeze into the next conversation, and says plainly that the choice is
// inert for the one on screen (the failure mode a user would otherwise hit
// once, confusingly, on their next turn).
func describeAgentChange(loc Locale, name string, agents []agentDefSummary) string {
	if name == "" {
		return t(loc, "agent.cleared")
	}
	for _, agent := range agents {
		if agent.Name != name {
			continue
		}
		var parts []string
		if agent.Persona != "" {
			parts = append(parts, t(loc, "selector.agentPersona", agent.Persona))
		}
		if agent.Toolset != "" {
			parts = append(parts, t(loc, "selector.agentToolset", agent.Toolset))
		}
		if agent.Model != "" {
			parts = append(parts, agent.Model)
		}
		if len(parts) > 0 {
			return t(loc, "agent.selected", name, strings.Join(parts, " · "))
		}
		break
	}
	return t(loc, "agent.selectedBare", name)
}

// agentExists reports whether a definition is stored, so `/agent nope` fails
// at the prompt instead of silently adopting nothing and surprising the user
// at the next /new. The error text is the caller's to localize.
func agentExists(comp *sdk.Component, name string) (bool, error) {
	if name == "" {
		return true, nil
	}
	var response agentDefsResponse
	if err := requestInto(comp, "store", "list",
		map[string]any{"kind": "agent", "limit": 1000}, &response); err != nil {
		return false, err
	}
	for _, agent := range response.Agents {
		if agent.Name == name {
			return true, nil
		}
	}
	return false, nil
}
