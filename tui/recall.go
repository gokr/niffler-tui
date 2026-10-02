// /recall: search this conversation's canonical history through the recall
// component's context_recall (mode: search) and render bounded hits — the
// transcript's own grep for a human (the LLM reaches the same tool through
// discover + invoke). `ask` hands the hits to the model as a user turn to
// answer from, via the userMessage convention (docs/WIRE.md) that
// applySlashResult already implements for registered commands.
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

// localRecall handles /recall <words…> [ask]: search first, then render or
// hand to the LLM. Argument parsing is free-form (the query is words, not
// tokens): one standalone `ask` word, leading or trailing, sets the flag.
func localRecall(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	query, ask := recallQuery(argument)
	if query == "" {
		m.addBlock(blockError, "/recall: needs words to search for — /recall <words> [ask]")
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
		if err == nil && ask {
			result = recallAskPayload(result, query)
		}
		return slashResultMsg{Name: cmd.Name, Session: session, Result: result, Err: err}
	}
}

// recallQuery splits an argument into the search words and the ask flag:
// one standalone `ask` token, leading or trailing, is the flag (a query
// that genuinely contains "ask" only loses it at the ends).
func recallQuery(argument string) (query string, ask bool) {
	fields := strings.Fields(argument)
	if len(fields) > 0 && strings.EqualFold(fields[0], "ask") {
		ask, fields = true, fields[1:]
	} else if len(fields) > 1 && strings.EqualFold(fields[len(fields)-1], "ask") {
		ask, fields = true, fields[:len(fields)-1]
	}
	return strings.Join(fields, " "), ask
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

// recallAskPayload wraps a search result for the ask variant: `text` is the
// human rendering (shown as the meta block), `userMessage` is the prompt that
// enters the conversation as a user turn — applySlashResult's convention
// path does the sending (or mid-turn steering), exactly as a registered
// command's userMessage result would.
func recallAskPayload(result json.RawMessage, query string) json.RawMessage {
	hits := recallHitsText(result)
	payload := map[string]any{
		"text": hits,
		"userMessage": "Use these context_recall hits from this conversation's " +
			"history to answer the question. A hit id can be read in full with " +
			"context_recall {\"mode\": \"full\", \"ref\": {\"source\": \"canonical\", \"id\": \"<id>\"}} " +
			"when a snippet is not enough.\n\nQuestion: " + query + "\n\n" + hits,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return result
	}
	return raw
}
