package main

// Subagent roster + the ctrl+o worker cycle ("what is working right now").
//
// The agent component's agent_list derives "your children" from the caller's
// session context, which a UI caller does not have; it therefore accepts an
// explicit sessionId argument for exactly this read-only listing. On older
// components the call errors and the roster and badge stay empty — the TUI
// degrades quietly, never an error surface.
//
// The roster is a *snapshot*: it is refreshed on agent lifecycle events, when
// a conversation loads, when /processes opens, and by a 10s tick while
// something works. That is too coarse to answer "how many are working" — a
// burst of spawns, or a refresh that fails while the agent component is busy
// running children, leaves the badge behind. So membership comes from the
// roster and *liveness* additionally from the ev.session.* frames this client
// already receives for every session on the bus: noteChildActivity records
// what each known child is doing (its running tool, the tail of its streamed
// text), which makes the cycle card live, admits a child whose status the
// roster has not caught up with, and lets the badge count instead of lag.
//
// The cycle shows the whole worker list — processes and subagents, selected
// entry expanded — so "how many are there" is answered by looking, not by
// rotating. A process entry shows its raw tail; a subagent entry is a status
// card (status, age, job, session, running tool, last output line, task),
// because a child's output reaches the parent conversation as settlement
// notices anyway.

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	sdk "niffler.dev/sdk"
)

// childActivityTTL is how long a child's last session frame counts as evidence
// of life. Providers think silently for minutes, so this is generous — but only
// un-settled children consult it (a child whose last activation is done,
// stopped or failed is never counted as working).
const childActivityTTL = 120.0 // seconds

// agentSummary is one entry of agent_list's children. Status is the
// component's contract: "running" (a live turn right now), "idle" (a
// resident runner between turns), "ready" (storage only — resumable, NOT
// finished). LastStatus is the last activation's outcome. StartedAt is the
// running turn's start or the last activation's start; older components omit
// it and the age stays empty.
type agentSummary struct {
	SessionID  string  `json:"sessionId"`
	Parent     string  `json:"parent"`
	Depth      int     `json:"depth"`
	Status     string  `json:"status"`
	LastStatus string  `json:"lastStatus"`
	JobID      string  `json:"jobId"`
	Task       string  `json:"task"`
	Error      string  `json:"error"`
	StartedAt  float64 `json:"startedAt"`
}

func (a agentSummary) running() bool { return a.Status == "running" }

// settled reports a terminal last activation: done/stopped/failed children are
// not working, however recently they emitted a frame.
func (a agentSummary) settled() bool {
	switch a.LastStatus {
	case "done", "failed", "stopped":
		return true
	}
	return false
}

// live reports whether the child belongs in the badge and the cycle: the
// roster says a turn is running, or the child is un-settled while its session
// frames are fresh (the roster lags a burst of spawns and a refresh can fail
// under load — the frames do not).
func (a agentSummary) live(act map[string]childActivity, now float64) bool {
	if a.settled() {
		return false
	}
	if a.running() {
		return true
	}
	activity, ok := act[a.SessionID]
	return ok && now-activity.LastSeen <= childActivityTTL
}

func agentSettlementText(a agentSummary) string {
	if a.LastStatus != "failed" && a.LastStatus != "stopped" {
		return ""
	}
	label := "subagent " + a.LastStatus
	if a.Error != "" {
		label += ": " + a.Error
	}
	return label
}

// ---- live child activity ---------------------------------------------------

// childActivity is what one subagent session is doing right now, recorded from
// the ev.session.* frames the client receives for every session on the bus. It
// is deliberately tiny: the roster owns identity and status, this owns "now".
type childActivity struct {
	LastSeen float64
	Phase    string // thinking | responding | tool | done
	Tool     string // running tool name ("bash"), empty when between tools
	Now      string // "bash go build ./..." / "thinking…" — the card's now line
	Output   string // last visible line of the child's streamed text
	Error    string
}

// noteChildActivity folds one session frame into the activity map. Only
// children the roster already knows are tracked, so another conversation's
// subagents (they share the bus) cannot leak into this conversation's badge.
func (m *model) noteChildActivity(kind string, event sessionEvent) {
	if event.SessionID == "" || !m.childKnown(event.SessionID) {
		return
	}
	if m.childAct == nil {
		m.childAct = map[string]childActivity{}
	}
	act := m.childAct[event.SessionID]
	act.LastSeen = epochNow()
	switch kind {
	case "token":
		// Content wins over reasoning: once the model is answering, that is
		// what the child is doing.
		if event.Content != "" {
			act.Phase, act.Tool, act.Now = "responding", "", t(m.loc, "status.responding")
			if line := lastVisibleLine(event.Content); line != "" {
				act.Output = line
			}
		} else if event.Reasoning != "" {
			act.Phase, act.Tool, act.Now = "thinking", "", t(m.loc, "status.thinking")
			if line := lastVisibleLine(event.Reasoning); line != "" {
				act.Output = line
			}
		}
	case "toolcall":
		if event.Phase == "done" {
			act.Tool, act.Now = "", ""
			break
		}
		act.Phase, act.Tool = "tool", event.Tool
		act.Now = event.Tool
		if snippet := activitySnippet(event.Tool, childToolArgs(event.Args)); snippet != "" {
			act.Now += " " + snippet
		}
	case "assistant":
		if event.Content != "" {
			if line := lastVisibleLine(event.Content); line != "" {
				act.Output = line
			}
		}
	case "error":
		if event.Error != "" {
			act.Error = event.Error
		}
	case "done":
		act.Phase, act.Tool, act.Now = "done", "", ""
	}
	m.childAct[event.SessionID] = act
}

// childKnown reports whether the id is one of this conversation's children per
// the roster snapshot.
func (m model) childKnown(id string) bool {
	for _, a := range m.agents {
		if a.SessionID == id {
			return true
		}
	}
	return false
}

// lastVisibleLine returns the last non-empty line of a streamed fragment,
// trimmed and clipped — the card shows one line of "what just came out".
func lastVisibleLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(ansi.Strip(lines[i])); line != "" {
			if width := 120; len(line) > width {
				line = line[len(line)-width:]
			}
			return line
		}
	}
	return ""
}

// childToolArgs decodes a toolcall frame's args for the snippet helper; bad
// JSON yields nil, which renders as the bare tool name.
func childToolArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	return args
}

// ---- roster, badge and cycle ----------------------------------------------

// agentsListMsg carries the roster snapshot for the badge and the ctrl+o
// cycle. An error means the component is absent or too old: keep the empty
// roster (the badge disappears) rather than nagging.
type agentsListMsg struct {
	Agents []agentSummary
	Err    error
}

// agentEventMsg wakes the UI on lifecycle events so the badge does not wait
// for an unrelated process tick. The roster remains authoritative.
type agentEventMsg struct {
	Parent string `json:"parent"`
	Status string `json:"status"`
	Error  string `json:"error"`
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

// runningAgents counts children holding a live turn (roster status or fresh
// session activity).
func runningAgents(agents []agentSummary, act map[string]childActivity, now float64) int {
	count := 0
	for _, a := range agents {
		if a.live(act, now) {
			count++
		}
	}
	return count
}

// longestAgentAge is the age of the earliest-started live child — how long
// this conversation has had a subagent working, which is what the badge
// claims. StartedAt is the running turn's start, so the earliest is the
// longest-running one.
func longestAgentAge(agents []agentSummary, act map[string]childActivity, now float64) string {
	best := float64(0)
	for _, a := range agents {
		if !a.live(act, now) || a.StartedAt <= 0 {
			continue
		}
		if best == 0 || a.StartedAt < best {
			best = a.StartedAt
		}
	}
	if best == 0 {
		return ""
	}
	return humanAge(now - best)
}

// agentsBadgeText is the status-line segment: "agent 2 (37s)" while any child
// works, empty otherwise (most conversations never spawn one).
func agentsBadgeText(loc Locale, agents []agentSummary, act map[string]childActivity, now float64) string {
	count := runningAgents(agents, act, now)
	if count == 0 {
		return ""
	}
	if age := longestAgentAge(agents, act, now); age != "" {
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
	kind       string // "process" or "agent"
	id         string // process id ("p3") or child session id
	label      string // process label; empty for agents
	status     string
	lastStatus string // agents: last activation outcome
	jobID      string // agents: job of the last activation
	startedAt  float64
	task       string // agents: the delegated task
}

// bgRoster merges the two snapshots into the cycle order: background
// processes first (numeric id order, sortProcesses), then subagents in the
// component's own listing order. Live entries only — the cycle is a "who is
// alive" view, not a registry browser (/agents is that). A child that the
// roster has not caught up with still qualifies when its session frames are
// fresh.
func bgRoster(processes []processSummary, agents []agentSummary, act map[string]childActivity, now float64) []bgTarget {
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
		if !a.live(act, now) {
			continue
		}
		roster = append(roster, bgTarget{
			kind: "agent", id: a.SessionID, status: a.Status,
			lastStatus: a.LastStatus, jobID: a.JobID,
			startedAt: a.StartedAt, task: a.Task,
		})
	}
	return roster
}

// bgPeekContext is everything the view needs beyond the selected target: the
// whole roster (rendered as the list you can count), the selection, and the
// live child activity.
type bgPeekContext struct {
	Roster   []bgTarget
	Index    int
	Activity map[string]childActivity
	Now      float64
}

// bgPeekView renders the cycle: the worker list, the selected entry expanded
// (raw tail for a process, a live status card for a subagent), and the footer
// hint. Pure so the layout is testable.
func bgPeekView(loc Locale, tgt bgTarget, text string, width, height int, loading bool, ctx bgPeekContext) string {
	var b strings.Builder
	age := ""
	if tgt.startedAt > 0 {
		age = humanAge(ctx.Now - tgt.startedAt)
	}
	if list := bgPeekList(loc, ctx, width); list != "" {
		b.WriteString(list)
		b.WriteString("\n\n")
	}
	if tgt.kind == "agent" {
		act := ctx.Activity[tgt.id]
		title := t(loc, "bgpeek.titleAgent", tgt.status, age)
		b.WriteString(lipglossBold(title))
		b.WriteString("\n\n")
		now := act.Now
		if now == "" {
			if ago := humanAge(ctx.Now - act.LastSeen); act.LastSeen > 0 && ago != "" {
				now = t(loc, "bgpeek.agentIdle", ago)
			} else {
				now = t(loc, "bgpeek.agentNoActivity")
			}
		}
		b.WriteString(wrapLine(t(loc, "bgpeek.agentNow", now), width))
		b.WriteString("\n")
		if act.Output != "" {
			b.WriteString(wrapLine(t(loc, "bgpeek.agentOutput", truncate(act.Output, max(8, width-8))), width))
			b.WriteString("\n")
		}
		if act.Error != "" {
			b.WriteString(wrapLine(t(loc, "bgpeek.agentError", act.Error), width))
			b.WriteString("\n")
		}
		if tgt.jobID != "" {
			b.WriteString(t(loc, "bgpeek.agentJob", tgt.jobID))
			b.WriteString("\n")
		}
		b.WriteString(t(loc, "bgpeek.agentSession", tgt.id))
		b.WriteString("\n")
		if task := strings.TrimSpace(tgt.task); task != "" {
			b.WriteString(wrapLine(t(loc, "bgpeek.agentTask", task), width))
			b.WriteString("\n")
		}
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
			body = clampTail(body, max(3, height-6-len(ctx.Roster)))
		}
		b.WriteString(body)
	}
	b.WriteString("\n\n" + metaStyle.Render(t(loc, "bgpeek.help")))
	return b.String()
}

// bgPeekList renders the worker list with the selected entry marked, so the
// count and the neighbours are visible without rotating. Empty when there is
// nothing to list (the caller already handles the no-worker case).
func bgPeekList(loc Locale, ctx bgPeekContext, width int) string {
	if len(ctx.Roster) == 0 {
		return ""
	}
	agents, processes := 0, 0
	for _, tgt := range ctx.Roster {
		if tgt.kind == "agent" {
			agents++
		} else {
			processes++
		}
	}
	var b strings.Builder
	b.WriteString(lipglossBold(t(loc, "bgpeek.listTitle",
		strconv.Itoa(len(ctx.Roster)), strconv.Itoa(agents), strconv.Itoa(processes))))
	for i, tgt := range ctx.Roster {
		marker := "  "
		if i == ctx.Index {
			marker = "▸ "
		}
		age := ""
		if tgt.startedAt > 0 {
			age = humanAge(ctx.Now - tgt.startedAt)
		}
		var line string
		if tgt.kind == "agent" {
			line = t(loc, "bgpeek.listAgent", age, tgt.status)
			if task := strings.TrimSpace(tgt.task); task != "" {
				line += "  " + firstLine(task)
			}
		} else {
			line = t(loc, "bgpeek.listProc", tgt.id, tgt.label, tgt.status)
		}
		if i == ctx.Index {
			line = headerStyle.Render(line)
		}
		b.WriteString("\n" + marker + truncate(line, max(8, width-2)))
	}
	return b.String()
}

// firstLine clips a task to its first line for the list view.
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
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

// bgPeekContextOf snapshots the view inputs for the current model state.
func (m *model) bgPeekContextOf() bgPeekContext {
	return bgPeekContext{
		Roster: m.bgPeekRoster, Index: m.bgPeekIdx,
		Activity: m.childAct, Now: epochNow(),
	}
}

// loadBgPeekBody fetches the current target's live view: the raw tail for a
// process (the shared peek fetch, tail: "1" — it re-reads without touching
// the model's drain cursor), nothing for an agent (its card renders from the
// roster plus the session frames noteChildActivity has been folding in).
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
	roster := bgRoster(m.processes, m.agents, m.childAct, epochNow())
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
