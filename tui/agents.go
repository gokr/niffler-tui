package main

// Subagent roster + the ctrl+o output cycle ("what is working right now").
//
// The agent component's agent_list derives "your children" from the
// caller's session context, which a UI caller does not have; it therefore
// accepts an explicit sessionId argument for exactly this read-only listing
// (the bg-notices affordance). On older components the call errors and the
// roster and badge simply stay empty — the TUI degrades to today's
// behavior, never an error surface.
//
// The cycle reuses the /processes peek (process_poll {tail: "1"}, which
// re-reads the bounded raw tail without advancing the model's drain
// cursor); a subagent's entry is a status card (task, status, age, session
// id) with a pointer to /sessions for the transcript — the child's output
// arrives in the parent's conversation as settlement notices anyway.

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	sdk "niffler.dev/sdk"
)

// agentSummary is one entry of agent_list's children. Status is the
// component's contract: "running" (a live turn right now), "idle" (a
// resident runner between turns), "ready" (storage only — resumable, NOT
// finished). StartedAt is the running turn's start or the last
// activation's start; older components omit it and the age stays empty.
type agentSummary struct {
	SessionID string  `json:"sessionId"`
	Parent    string  `json:"parent"`
	Depth     int     `json:"depth"`
	Status    string  `json:"status"`
	Task      string  `json:"task"`
	StartedAt float64 `json:"startedAt"`
}

func (a agentSummary) running() bool { return a.Status == "running" }

// agentsListMsg carries the roster snapshot for the badge and the ctrl+o
// cycle. An error means the component is absent or too old: keep the empty
// roster (the badge disappears) rather than nagging.
type agentsListMsg struct {
	Agents []agentSummary
	Err    error
}

func loadAgents(comp *sdk.Component, session string) ([]agentSummary, error) {
	if comp == nil || session == "" {
		return nil, nil
	}
	var response struct {
		Children []agentSummary `json:"children"`
	}
	err := requestInto(comp, "agent", "agent_list",
		map[string]any{"sessionId": session, "scope": "descendants"}, &response)
	return response.Children, err
}

func agentsListCmd(comp *sdk.Component, session string) tea.Cmd {
	return func() tea.Msg {
		agents, err := loadAgents(comp, session)
		return agentsListMsg{Agents: agents, Err: err}
	}
}

// runningAgents counts entries holding a live turn.
func runningAgents(agents []agentSummary) int {
	count := 0
	for _, a := range agents {
		if a.running() {
			count++
		}
	}
	return count
}

// oldestAgentAge is the longest-running live turn's age text, "" when
// nothing runs or no entry carries a timestamp.
func oldestAgentAge(agents []agentSummary, now float64) string {
	best := float64(0)
	for _, a := range agents {
		if a.running() && a.StartedAt > 0 && a.StartedAt > best {
			best = a.StartedAt
		}
	}
	if best == 0 {
		return ""
	}
	return humanAge(now - best)
}

// agentsBadgeText is the status-line segment: "agent 2 (37s)" while any
// child works, empty otherwise (most conversations never spawn one).
func agentsBadgeText(loc Locale, agents []agentSummary, now float64) string {
	count := runningAgents(agents)
	if count == 0 {
		return ""
	}
	if age := oldestAgentAge(agents, now); age != "" {
		return t(loc, "agents.badgeAge", strconv.Itoa(count), age)
	}
	return t(loc, "agents.badge", strconv.Itoa(count))
}

// epochNow is wall-clock seconds with sub-second precision, matching the
// epochTime() timestamps the components publish.
func epochNow() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// humanAge renders an elapsed duration the way a status row wants it:
// "37s", "5m", "1h12m", "3d4h". Pure and table-tested; elapsed <= 0
// (including clock skew before the start) yields "".
func humanAge(elapsed float64) string {
	switch {
	case elapsed <= 0:
		return ""
	case elapsed < 60:
		return strconv.Itoa(int(elapsed)) + "s"
	case elapsed < 3600:
		return strconv.Itoa(int(elapsed)/60) + "m"
	case elapsed < 86400:
		return strconv.Itoa(int(elapsed)/3600) + "h" +
			strconv.Itoa((int(elapsed)%3600)/60) + "m"
	default:
		return strconv.Itoa(int(elapsed)/86400) + "d" +
			strconv.Itoa((int(elapsed)%86400)/3600) + "h"
	}
}

// ---- the ctrl+o cycle ------------------------------------------------------

// bgTarget is one entry of the ctrl+o roster: a running background process
// or a working subagent.
type bgTarget struct {
	kind      string // "process" or "agent"
	id        string // process id ("p3") or child session id
	label     string // process label; empty for agents
	status    string
	startedAt float64
	task      string // agents: the delegated task
}

// bgRoster merges the two snapshots into the cycle order: background
// processes first (numeric id order, sortProcesses), then subagents in the
// component's own listing order. Running/working entries only — the cycle
// is a "who is alive" view, not a registry browser (/processes is that).
func bgRoster(processes []processSummary, agents []agentSummary) []bgTarget {
	roster := make([]bgTarget, 0, len(processes)+len(agents))
	sorted := make([]processSummary, len(processes))
	copy(sorted, processes)
	sortProcesses(sorted)
	for _, p := range sorted {
		if !p.running() {
			continue
		}
		roster = append(roster, bgTarget{
			kind: "process", id: p.ID, label: p.Label,
			status: p.Status, startedAt: p.StartedAt,
		})
	}
	for _, a := range agents {
		if !a.running() {
			continue
		}
		roster = append(roster, bgTarget{
			kind: "agent", id: a.SessionID, status: a.Status,
			startedAt: a.StartedAt, task: a.Task,
		})
	}
	return roster
}

// bgPeekView renders the cycle: header, the clamped raw tail (processes)
// or a status card (agents), footer hint. Pure so the layout is testable.
func bgPeekView(loc Locale, tgt bgTarget, text string, width, height int, loading bool, now float64) string {
	var b strings.Builder
	age := ""
	if tgt.startedAt > 0 {
		age = humanAge(now - tgt.startedAt)
	}
	if tgt.kind == "agent" {
		title := t(loc, "bgpeek.titleAgent", tgt.status, age)
		b.WriteString(lipglossBold(title))
		b.WriteString("\n\n")
		if task := strings.TrimSpace(tgt.task); task != "" {
			b.WriteString(wrapLine(t(loc, "bgpeek.agentTask", task), width))
			b.WriteString("\n")
		}
		b.WriteString(t(loc, "bgpeek.agentSession", tgt.id))
		b.WriteString("\n")
		b.WriteString(metaStyle.Render(t(loc, "bgpeek.agentHint")))
	} else {
		title := t(loc, "bgpeek.titleProc", tgt.id, tgt.label, tgt.status, age)
		b.WriteString(lipglossBold(title))
		b.WriteString("\n\n")
		body := text
		if loading {
			body = t(loc, "processes.peekLoading")
		} else if strings.TrimSpace(body) == "" {
			body = t(loc, "processes.peekEmpty")
		} else {
			body = clampTail(body, max(3, height-6))
		}
		b.WriteString(body)
	}
	b.WriteString("\n\n" + metaStyle.Render(t(loc, "bgpeek.help")))
	return b.String()
}

// lipglossBold isolates the one style use here so the pure view function
// stays import-light in tests.
func lipglossBold(s string) string { return headerStyle.Render(s) }

// bgPeekTarget is the cycled entry, false past the end (the roster shrank
// under an open cycle).
func (m *model) bgPeekTarget() (bgTarget, bool) {
	if m.bgPeekIdx < 0 || m.bgPeekIdx >= len(m.bgPeekRoster) {
		return bgTarget{}, false
	}
	return m.bgPeekRoster[m.bgPeekIdx], true
}

// loadBgPeekBody fetches the current target's live view: the raw tail for a
// process (the shared peek fetch, tail: "1" — it re-reads without touching
// the model's drain cursor), nothing for an agent (the card renders from
// the roster snapshot; the child's output reaches the parent conversation
// as settlement notices).
func (m *model) loadBgPeekBody() tea.Cmd {
	tgt, ok := m.bgPeekTarget()
	m.bgPeekText, m.bgPeekErr = "", ""
	if !ok {
		return nil
	}
	if tgt.kind == "process" {
		m.bgPeekLoading = true
		return processesPeekCmd(m.comp, tgt.id)
	}
	m.bgPeekLoading = false
	return nil
}

// refreshBgPeekRoster rebuilds the cycle from fresh snapshots, keeping the
// position when the roster only grew, clamping when it shrank.
func (m *model) refreshBgPeekRoster() {
	roster := bgRoster(m.processes, m.agents)
	if m.bgPeekIdx >= len(roster) {
		m.bgPeekIdx = 0
	}
	m.bgPeekRoster = roster
}

// wrapLine hard-wraps one logical line at width columns (ansi-aware via the
// same width helper the status row uses), so a long task text cannot push
// the card body out of the pane.
func wrapLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			candidate := word
			if line != "" {
				candidate = line + " " + word
			}
			if ansi.StringWidth(candidate) > width && line != "" {
				out = append(out, line)
				line = word
			} else {
				line = candidate
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
