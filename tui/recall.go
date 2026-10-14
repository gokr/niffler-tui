// /recall: search this conversation's canonical history through the recall
// component's context_recall (mode: search) and render bounded hits — the
// transcript's own grep for a human (the LLM reaches the same tool through
// discover + invoke). Asking about the hits is the "?" marker's job now
// (ask.go): "?/recall <words>" runs this command and hands what it printed to
// the model, exactly as it does for any other local command.
//
// The call is a direct bus call naming the conversation explicitly: no
// lease is involved, and the scope stays this conversation unless the
// command were to say otherwise (context_recall's own ownership rules:
// cross-conversation search is refused to leased sessions).
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// localRecall handles /recall <words…>: search and render the bounded hits.
func localRecall(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	query := strings.TrimSpace(argument)
	if query == "" {
		m.addBlock(blockError, "/recall: needs words to search for — /recall <words>")
		m.syncViewport(true)
		return m, nil
	}
	session := m.session // the result must belong to this conversation
	m.addBlock(blockMeta, "→ /recall"+slashArgText(argument))
	m.syncViewport(true)
	return m, func() tea.Msg {
		var result json.RawMessage
		err := requestInto(m.comp, "recall", "context_recall",
			map[string]any{"mode": "search", "query": query, "session": session},
			&result)
		return slashResultMsg{Name: cmd.Name, Session: session, Result: result, Err: err}
	}
}

// recallHitsText renders a context_recall mode:search result as the bounded
// one-line hit list the transcript shows: count and lane first, then one
// line per hit (id, role, snippet). Falls back to the compact JSON when the
// result is not the expected shape.
func recallHitsText(raw json.RawMessage) string {
	var r struct {
		Count   int    `json:"count"`
		Ranked  any    `json:"ranked"`
		Via     string `json:"via"`
		Capped  bool   `json:"capped"`
		Matches []struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Snippet string `json:"snippet"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Count == 0 && len(r.Matches) == 0 && r.Via == "" {
		return compactJSON(raw)
	}
	var b strings.Builder
	lane := r.Via
	if lane == "" {
		lane = "search"
	}
	fmt.Fprintf(&b, "%d hit(s) via %s", len(r.Matches), lane)
	if r.Capped {
		b.WriteString(" (capped — narrow the query)")
	}
	b.WriteString(":")
	for _, hit := range r.Matches {
		snip := strings.TrimSpace(strings.ReplaceAll(hit.Snippet, "\n", " "))
		if len(snip) > 160 {
			snip = snip[:160] + "…"
		}
		fmt.Fprintf(&b, "\n  %s %s  %s", hit.ID, hit.Role, snip)
	}
	return b.String()
}
