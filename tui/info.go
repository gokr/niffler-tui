// /info [id]: conversation statistics computed from the store's message
// documents. The same aggregation the console-tool-usage fabric program
// performs (roles, per-tool call counts, discover/invoke breakdown), plus
// what a human sizing context pressure needs: tool-result sizes — total,
// median, largest, with the biggest offenders and previews — and the
// conversation's summed token usage. Reading the store directly keeps the
// command native to the client: no approval gate, no fabric dependency,
// and an explicit id inspects another conversation exactly the same way.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"niffler.dev/sdk"
)

// infoMaxPages bounds one conversation's message read: 100 pages of 1000 is
// 100k messages, far beyond any conversation (and the same belt pageStoreList
// wears for whole kinds).
const infoMaxPages = 100

// maxTopResults is how many largest tool results the report lists.
const maxTopResults = 3

// toolStats is one tool's usage across a conversation: result-side (role
// "tool" messages) and call-site side (assistant tool_calls) are folded
// separately, both by name.
type toolStats struct {
	calls   int
	bytes   int
	largest int
}

// infoResult is one entry of the largest-results list.
type infoResult struct {
	tool    string
	bytes   int
	preview string
}

// infoStats is the computed report for one conversation.
type infoStats struct {
	id   string
	desc string // "title …" line, or the subagent lineage, best effort

	messages     int
	roles        map[string]int
	historyChars int64

	results     map[string]*toolStats // role "tool" messages by tool name
	resultCalls int
	resultBytes int64
	resultLens  []int // every tool-result size, for the median
	topResults  []infoResult

	calls     map[string]*toolStats // assistant tool_calls by tool name
	callCount int
	callBytes int64

	discovered map[string]int // discover arguments: component and tool=NAME
	invoked    map[string]int // invoke arguments: tool

	promptTokens     int64
	completionTokens int64
	responses        int // assistant messages carrying usage
}

func newInfoStats(id string) *infoStats {
	return &infoStats{
		id:         id,
		roles:      map[string]int{},
		results:    map[string]*toolStats{},
		calls:      map[string]*toolStats{},
		discovered: map[string]int{},
		invoked:    map[string]int{},
	}
}

// pageConversationMessages reads every message document of one conversation
// by following the store's nextAfter cursor. The idPrefix scopes the read to
// the conversation — a bare list would pull every conversation's messages.
func pageConversationMessages(comp *sdk.Component, id string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	after := ""
	for page := 0; page < infoMaxPages; page++ {
		var response struct {
			Items     []json.RawMessage `json:"items"`
			HasMore   bool              `json:"hasMore"`
			NextAfter string            `json:"nextAfter"`
		}
		args := map[string]any{"kind": "message", "idPrefix": id + ":", "limit": 1000}
		if after != "" {
			args["after"] = after
		}
		if err := requestInto(comp, "store", "list", args, &response); err != nil {
			return nil, err
		}
		all = append(all, response.Items...)
		if !response.HasMore || response.NextAfter == "" || response.NextAfter == after {
			break
		}
		after = response.NextAfter
	}
	return all, nil
}

// computeInfoStats folds raw message documents into the report. header and
// meta are the conversation's header and lineage records, both optional
// (unknown ids and subagent conversations degrade to message-side facts).
func computeInfoStats(id string, header, meta json.RawMessage, msgs []json.RawMessage) *infoStats {
	st := newInfoStats(id)
	st.desc = infoDescribe(id, header, meta)

	var value struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Name      string          `json:"name"`
		ToolCalls []struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	for _, raw := range msgs {
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		// Content is a JSON string (or null): unquote it properly — a naive
		// trim of the surrounding quotes would mangle contents that end in
		// a quote themselves.
		var content string
		_ = json.Unmarshal(value.Content, &content)
		size := len(value.Content) // raw JSON length: the bytes the transcript carries
		st.messages++
		st.roles[value.Role]++
		st.historyChars += int64(size)

		switch value.Role {
		case "assistant":
			for _, tc := range value.ToolCalls {
				name := tc.Function.Name
				if name == "" {
					name = "?"
				}
				s := st.calls[name]
				if s == nil {
					s = &toolStats{}
					st.calls[name] = s
				}
				s.calls++
				s.bytes += len(tc.Function.Arguments)
				if len(tc.Function.Arguments) > s.largest {
					s.largest = len(tc.Function.Arguments)
				}
				st.callCount++
				st.callBytes += int64(len(tc.Function.Arguments))
				infoFoldSpecial(st, name, tc.Function.Arguments)
			}
			if value.Usage.PromptTokens > 0 || value.Usage.CompletionTokens > 0 {
				st.responses++
				st.promptTokens += value.Usage.PromptTokens
				st.completionTokens += value.Usage.CompletionTokens
			}
		case "tool":
			name := value.Name
			if name == "" {
				name = "?"
			}
			s := st.results[name]
			if s == nil {
				s = &toolStats{}
				st.results[name] = s
			}
			s.calls++
			s.bytes += size
			if size > s.largest {
				s.largest = size
			}
			st.resultCalls++
			st.resultBytes += int64(size)
			st.resultLens = append(st.resultLens, size)
			infoNoteLarge(st, name, size, content)
		}
	}
	return st
}

// infoFoldSpecial records the discovery/invoke breakdown the way the
// console-tool-usage fabric program does: discover's component (and any
// tool= entries), invoke's target tool. Best effort — malformed arguments
// are silently skipped.
func infoFoldSpecial(st *infoStats, name, rawArgs string) {
	if rawArgs == "" {
		return
	}
	var args map[string]any
	if json.Unmarshal([]byte(rawArgs), &args) != nil {
		return
	}
	switch name {
	case "discover":
		if c, ok := args["component"].(string); ok && c != "" {
			st.discovered[c]++
		}
		if requested, ok := args["tools"].(string); ok && requested != "" {
			for _, t := range strings.Fields(requested) {
				st.discovered["tool="+t]++
			}
		}
	case "invoke":
		if t, ok := args["tool"].(string); ok && t != "" {
			st.invoked[t]++
		}
	}
}

// infoNoteLarge maintains the largest-results list (biggest first).
func infoNoteLarge(st *infoStats, name string, size int, content string) {
	entry := infoResult{tool: name, bytes: size, preview: truncateRunes(content, 48)}
	pos := len(st.topResults)
	for i, r := range st.topResults {
		if size > r.bytes {
			pos = i
			break
		}
	}
	if pos == maxTopResults {
		return
	}
	st.topResults = append(st.topResults[:pos], append([]infoResult{entry}, st.topResults[pos:]...)...)
	if len(st.topResults) > maxTopResults {
		st.topResults = st.topResults[:maxTopResults]
	}
}

// infoDescribe renders the one-line identification: title, or the subagent
// lineage when this conversation has no header of its own.
func infoDescribe(id string, header, meta json.RawMessage) string {
	if len(header) > 0 {
		var h struct {
			Title   string  `json:"title"`
			Model   string  `json:"model"`
			Created float64 `json:"createdAt"`
		}
		if json.Unmarshal(header, &h) == nil {
			return fmt.Sprintf("%q · model %s · started %s",
				h.Title, h.Model, infoWhen(h.Created))
		}
	}
	if len(meta) > 0 {
		var m struct {
			Parent string `json:"parent"`
		}
		if json.Unmarshal(meta, &m) == nil && m.Parent != "" {
			return "subagent of " + m.Parent
		}
	}
	return "no conversation header in the store"
}

// infoWhen renders a store timestamp (unix seconds as float) as a local
// time with an age suffix.
func infoWhen(created float64) string {
	if created <= 0 {
		return "?"
	}
	t := time.Unix(int64(created), 0)
	return t.Format("2006-01-02 15:04") + " (" + fmtElapsed(time.Since(t)) + " old)"
}

// localInfo runs /info [id]: statistics for the current conversation, or
// the named one. The store read and the folding run in a command; the
// result renders as one transcript block.
func localInfo(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	id := strings.TrimSpace(argument)
	if id == "" {
		id = m.session
	}
	m.addBlock(blockMeta, "→ /info"+slashArgText(argument))
	m.syncViewport(true)
	comp, session := m.comp, m.session
	return m, func() tea.Msg {
		// Header and lineage are best effort — an unknown or subagent id
		// still gets full message-side statistics.
		header, _ := comp.StoreGet("conversation", id, controlTimeout)
		meta, _ := comp.StoreGet("sessionmeta", id, controlTimeout)
		var headerRaw, metaRaw json.RawMessage
		if header != nil {
			headerRaw = header.Value
		}
		if meta != nil {
			metaRaw = meta.Value
		}
		msgs, err := pageConversationMessages(comp, id)
		if err != nil {
			return infoStatsMsg{session: session, err: err}
		}
		return infoStatsMsg{session: session, stats: computeInfoStats(id, headerRaw, metaRaw, msgs)}
	}
}

// infoStatsMsg carries a finished /info report for rendering.
type infoStatsMsg struct {
	stats   *infoStats
	session string // conversation id at invocation time
	err     error
}

// applyInfoStatsMsg renders the report as a transcript meta block.
func (m *model) applyInfoStatsMsg(msg infoStatsMsg) {
	if msg.session != m.session {
		return // conversation switched mid-read: the report belongs elsewhere
	}
	if msg.err != nil {
		m.addBlock(blockError, "/info: "+msg.err.Error())
		m.syncViewport(true)
		return
	}
	m.addBlock(blockMeta, renderInfoStats(msg.stats))
	m.syncViewport(true)
}

// renderInfoStats renders the report as aligned plain text. Transcript
// labels stay English (same convention as the tool-run cards).
func renderInfoStats(st *infoStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "conversation %s — %s", st.id, st.desc)

	roles := make([]string, 0, len(st.roles))
	for role := range st.roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	roleParts := make([]string, 0, len(roles))
	for _, role := range roles {
		roleParts = append(roleParts, fmt.Sprintf("%s %d", role, st.roles[role]))
	}
	fmt.Fprintf(&b, "\nmessages %d (%s) · history ≈ %s chars (~%s tokens /4)",
		st.messages, strings.Join(roleParts, " · "),
		compactCount(st.historyChars), compactCount(st.historyChars/4))
	if st.responses > 0 {
		fmt.Fprintf(&b, "\ntokens: ↑ %s prompt · ↓ %s completion across %d responses",
			compactCount(st.promptTokens), compactCount(st.completionTokens), st.responses)
	}

	if st.resultCalls > 0 {
		fmt.Fprintf(&b, "\ntool results %d · %s total · median %s · largest %s",
			st.resultCalls, compactCount(st.resultBytes),
			compactCount(int64(median(st.resultLens))), compactCount(int64(maxInt(st.resultLens))))
		b.WriteString("\n  tool            calls  results  largest")
		for _, row := range infoSortedTools(st.results) {
			s := st.results[row]
			fmt.Fprintf(&b, "\n  %-14s %5d  %8s  %7s", row, s.calls, compactCount(int64(s.bytes)), compactCount(int64(s.largest)))
		}
	}
	if st.callCount > 0 {
		fmt.Fprintf(&b, "\nassistant tool calls %d · arguments %s total", st.callCount, compactCount(st.callBytes))
		for _, row := range infoSortedTools(st.calls) {
			s := st.calls[row]
			fmt.Fprintf(&b, "\n  %-14s %5d", row, s.calls)
		}
	}
	if len(st.discovered) > 0 {
		b.WriteString("\ndiscovered: " + infoCounts(st.discovered))
	}
	if len(st.invoked) > 0 {
		b.WriteString("\ninvoked: " + infoCounts(st.invoked))
	}
	if len(st.topResults) > 0 {
		b.WriteString("\nlargest tool results:")
		for _, r := range st.topResults {
			fmt.Fprintf(&b, "\n  %7s  %-14s %s", compactCount(int64(r.bytes)), r.tool, r.preview)
		}
	}
	return b.String()
}

// infoSortedTools orders a per-tool map: most calls first, then by name.
func infoSortedTools(m map[string]*toolStats) []string {
	rows := make([]string, 0, len(m))
	for name := range m {
		rows = append(rows, name)
	}
	sort.Slice(rows, func(i, j int) bool {
		if m[rows[i]].calls != m[rows[j]].calls {
			return m[rows[i]].calls > m[rows[j]].calls
		}
		return rows[i] < rows[j]
	})
	return rows
}

// infoCounts renders "name (n)" pairs in first-insertion-stable order:
// most calls first, then alphabetically.
func infoCounts(m map[string]int) string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s (%d)", name, m[name]))
	}
	return strings.Join(parts, " · ")
}

// median of a slice of sizes (average of the two middle values for even
// lengths). The input is sorted in place.
func median(lens []int) int {
	if len(lens) == 0 {
		return 0
	}
	sorted := append([]int(nil), lens...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func maxInt(lens []int) int {
	m := 0
	for _, v := range lens {
		if v > m {
			m = v
		}
	}
	return m
}

// compactCount renders a character/token count for humans: plain integer
// below 10k, then k/M with one decimal (the attachment chip's humanSize
// renders file bytes — counts want k/M, not KB/MB).
func compactCount(n int64) string {
	switch {
	case n < 0:
		return "?"
	case n < 10_000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
}

// truncateRunes cuts a plain string to n runes on one line (store contents
// are plain text; a rune-safe cut keeps CJK and emoji previews intact).
func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.SplitN(s, "\n", 2)[0], "\t", " "))
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
