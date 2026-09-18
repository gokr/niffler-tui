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
// The cycle is split: the worker roster on top — so "how many are there" is
// answered by looking, not by rotating — and an output pane below it for the
// selected worker, filling the screen's remaining rows. A process pane is its
// raw stdout tail; a subagent pane is the tail of its own persisted
// transcript, continued live from a store cursor while the pane is open
// (its frames carry streamed text only, and the transcript is where its tool
// calls and their output land). A pane refresh runs every couple of seconds,
// because a tail that only moves when the user presses r is not a view of
// what is going on.

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
		if snippet := activitySnippet(event.Tool, toolArgsMap(event.Args)); snippet != "" {
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

// toolArgsMap decodes a tool call's raw JSON arguments for the snippet
// helper (a session frame's args, or a stored tool_call's arguments string);
// bad JSON yields nil, which renders as the bare tool name.
func toolArgsMap(raw json.RawMessage) map[string]any {
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
// whole roster (rendered as the list you can count), the selection, the live
// child activity, and the output pane's content.
type bgPeekContext struct {
	Roster   []bgTarget
	Index    int
	Activity map[string]childActivity
	Now      float64
	// Tail is the output pane for the selected target: a subagent's
	// persisted transcript lines (continued live from a store cursor while
	// the pane is open), or a process's raw output tail. Empty while the
	// first read is in flight — the pane then says so instead of showing
	// another worker's output.
	Tail []string
}

// The pane split. The roster keeps at most a third of the screen (the count
// stays visible without rotating), the output pane takes the rest, and the
// pane never shrinks below bgPeekMinPane rows: a card that lists twelve
// workers and shows two lines of output answered "who is running" while
// hiding "what are they doing", which is what the cycle is opened for.
const (
	bgPeekMinPane    = 8
	bgPeekMaxRoster  = 12
	bgPeekChromeRows = 4 // view header (1), rule below the roster, blank, footer hint
)

// bgPeekRosterEntries is how many roster entries the list shows: at most a
// third of the screen, and never the whole roster when it is long. The title
// row and the up-to-two "+N more" rows are added on top by bgPeekList.
func bgPeekRosterEntries(rosterLen, height int) int {
	if rosterLen == 0 {
		return 0
	}
	entries := min(rosterLen, max(3, height/3))
	return min(entries, bgPeekMaxRoster)
}

// bgPeekPaneRows is the output pane's row budget: everything left after the
// roster block and the card's meta rows, never below bgPeekMinPane.
func bgPeekPaneRows(height, rosterRows, metaRows int) int {
	return max(bgPeekMinPane, height-bgPeekChromeRows-rosterRows-metaRows)
}

// bgPeekWindow is the slice of the roster the list shows: at most maxRows
// entries, kept centered on the selection so the selected worker is never the
// one the cap hides.
func bgPeekWindow(count, index, maxRows int) (lo, hi int) {
	if maxRows <= 0 || count <= maxRows {
		return 0, count
	}
	lo = index - maxRows/2
	if lo < 0 {
		lo = 0
	}
	if lo > count-maxRows {
		lo = count - maxRows
	}
	return lo, lo + maxRows
}

// bgPeekCard builds the selected worker's card: the meta rows above the pane
// (status, what it is doing now, job, session, task) and the pane's own rows.
// A process pane is its raw output tail; a subagent pane is the tail of its
// persisted transcript — its answers, its tool calls and their bounded
// outcomes — which is what turns one summary line into a usable view.
func bgPeekCard(loc Locale, tgt bgTarget, text string, width int, age string, ctx bgPeekContext) (meta, pane []string) {
	if tgt.kind == "agent" {
		act := ctx.Activity[tgt.id]
		meta = append(meta, lipglossBold(truncate(t(loc, "bgpeek.titleAgent", tgt.status, age), width)))
		now := act.Now
		if now == "" {
			if ago := humanAge(ctx.Now - act.LastSeen); act.LastSeen > 0 && ago != "" {
				now = t(loc, "bgpeek.agentIdle", ago)
			} else {
				now = t(loc, "bgpeek.agentNoActivity")
			}
		}
		meta = append(meta, truncate(t(loc, "bgpeek.agentNow", now), width))
		if act.Error != "" {
			meta = append(meta, errorStyle.Render(truncate(t(loc, "bgpeek.agentError", act.Error), width)))
		}
		if tgt.jobID != "" {
			meta = append(meta, truncate(t(loc, "bgpeek.agentJob", tgt.jobID), width))
		}
		meta = append(meta, truncate(t(loc, "bgpeek.agentSession", tgt.id), width))
		if task := strings.TrimSpace(tgt.task); task != "" {
			meta = appendRows(meta, t(loc, "bgpeek.agentTask", firstLine(task)), width, 2)
		}
		pane = ctx.Tail
		if len(pane) == 0 && act.Output != "" {
			// Nothing persisted yet (a child that just started, or a store
			// read in flight): the last streamed line still belongs here.
			meta = append(meta, truncate(t(loc, "bgpeek.agentOutput", act.Output), width))
		}
		return meta, pane
	}
	meta = append(meta, lipglossBold(truncate(
		t(loc, "bgpeek.titleProc", tgt.id, tgt.label, tgt.status, age), width)))
	return meta, resultLines(text)
}

// bgPeekView renders the cycle: the worker roster on top, a rule, the selected
// entry's card, and the output pane filling the rest — the last rows of the
// worker's output rather than a single summary line. Pure so the layout is
// testable.
func bgPeekView(loc Locale, tgt bgTarget, text string, width, height int, loading bool, ctx bgPeekContext) string {
	age := ""
	if tgt.startedAt > 0 {
		age = humanAge(ctx.Now - tgt.startedAt)
	}
	meta, pane := bgPeekCard(loc, tgt, text, width, age, ctx)

	var b strings.Builder
	rosterRows := 0
	if list := bgPeekList(loc, ctx, width, bgPeekRosterEntries(len(ctx.Roster), height)); list != "" {
		b.WriteString(list)
		b.WriteString("\n")
		rosterRows = strings.Count(list, "\n") + 1
	}
	b.WriteString(metaStyle.Render(strings.Repeat("─", max(8, min(width-1, 72)))))
	b.WriteString("\n")
	for _, row := range meta {
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString(bgPeekPane(loc, pane, loading, width,
		bgPeekPaneRows(height, rosterRows, len(meta))))
	b.WriteString("\n\n" + metaStyle.Render(t(loc, "bgpeek.help")))
	return b.String()
}

// bgPeekPane renders the output pane: the last rows of the target's output,
// hard-wrapped to the width so a long line stays readable instead of being
// cut, or the loading/empty note when there is nothing yet.
func bgPeekPane(loc Locale, pane []string, loading bool, width, rows int) string {
	var wrapped []string
	for _, line := range pane {
		wrapped = append(wrapped, strings.Split(wrapLine(line, width), "\n")...)
	}
	if len(wrapped) == 0 {
		if loading {
			return metaStyle.Render(t(loc, "processes.peekLoading"))
		}
		return metaStyle.Render(t(loc, "processes.peekEmpty"))
	}
	kept, _ := collapseLines(wrapped, rows, true) // the tail: newest output wins
	return strings.Join(kept, "\n")
}

// bgPeekList renders the worker list with the selected entry marked, a window
// of at most maxEntries rows: the count and the neighbours stay visible
// without rotating, and a hidden window edge says how much is out of sight.
func bgPeekList(loc Locale, ctx bgPeekContext, width, maxEntries int) string {
	if len(ctx.Roster) == 0 || maxEntries <= 0 {
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
	lo, hi := bgPeekWindow(len(ctx.Roster), ctx.Index, maxEntries)
	var b strings.Builder
	b.WriteString(lipglossBold(t(loc, "bgpeek.listTitle",
		strconv.Itoa(len(ctx.Roster)), strconv.Itoa(agents), strconv.Itoa(processes))))
	if lo > 0 {
		b.WriteString("\n" + metaStyle.Render(truncate(
			t(loc, "bgpeek.listMore", strconv.Itoa(lo)), max(8, width-2))))
	}
	for i := lo; i < hi; i++ {
		tgt := ctx.Roster[i]
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
	if hi < len(ctx.Roster) {
		b.WriteString("\n" + metaStyle.Render(truncate(
			t(loc, "bgpeek.listMore", strconv.Itoa(len(ctx.Roster)-hi)), max(8, width-2))))
	}
	return b.String()
}

// appendRows wraps text to width and appends at most maxRows rows to the
// card's meta block: a longer task line is cut rather than allowed to push
// the output pane out of the view, keeping the row budget predictable.
func appendRows(rows []string, text string, width, maxRows int) []string {
	all := strings.Split(wrapLine(text, width), "\n")
	if len(all) > maxRows {
		all = all[:maxRows]
	}
	return append(rows, all...)
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
		Tail: m.bgPeekPaneLines(),
	}
}

// bgPeekPaneLines is the pane content for the selected target. Content that
// belongs to another worker — the roster rebuilt under the cycle, or an
// abandoned read — is dropped rather than attributed to the wrong entry.
func (m model) bgPeekPaneLines() []string {
	tgt, ok := m.bgPeekTarget()
	if !ok || m.bgPeekPaneFor != tgt.id {
		return nil
	}
	if tgt.kind == "agent" {
		return m.bgPeekTail
	}
	return resultLines(m.bgPeekText)
}

// bgPeekPaneAttributed is "the pane in hand belongs to the selected worker":
// same entry, and the attribution the pane was opened with. It is the
// stale-pane guard the view renders with and the check a landed read uses
// before it fills the pane.
func (m model) bgPeekPaneAttributed(kind, id string) bool {
	tgt, ok := m.bgPeekTarget()
	return ok && tgt.kind == kind && tgt.id == id && m.bgPeekPaneFor == id
}

// resetBgPeekPane drops the pane's content and its attribution, so the next
// load reads the (new) worker's own output.
func (m *model) resetBgPeekPane() {
	m.bgPeekPaneFor, m.bgPeekCursor, m.bgPeekText, m.bgPeekErr = "", "", "", ""
	m.bgPeekTail, m.bgPeekLoading = nil, false
}

// attributeBgPeekPane points the pane at the selected worker, dropping any
// content that belonged to another one (the roster can rebuild under the
// cycle, and a landed read must never paint one worker's output under
// another's name).
func (m *model) attributeBgPeekPane() {
	tgt, ok := m.bgPeekTarget()
	if !ok || !m.bgPeekPaneAttributed(tgt.kind, tgt.id) {
		m.resetBgPeekPane()
	}
	if ok {
		m.bgPeekPaneFor = tgt.id
	}
}

// loadBgPeekBody fetches the current target's output pane: the raw tail for a
// process (the shared peek fetch, tail: "1" — it re-reads without touching
// the model's drain cursor), the transcript tail for a subagent. A worker
// whose pane is not attributed yet starts a fresh read; afterwards the
// subagent read continues from the store cursor the last page returned, so a
// refresh is one cheap page instead of re-reading the window.
func (m *model) loadBgPeekBody() tea.Cmd {
	tgt, ok := m.bgPeekTarget()
	if !ok {
		return nil
	}
	m.attributeBgPeekPane()
	m.bgPeekErr = ""
	m.bgPeekLoading = m.bgPeekCursor == "" && m.bgPeekText == "" && len(m.bgPeekTail) == 0
	if tgt.kind == "process" {
		return processesPeekCmd(m.comp, tgt.id)
	}
	return workerTailCmd(m.comp, tgt.id, m.bgPeekCursor)
}

// refreshBgPeekRoster rebuilds the cycle from fresh snapshots, keeping the
// position when the roster only grew, clamping when it shrank — and dropping
// the pane when the entry at that position is now a different worker.
func (m *model) refreshBgPeekRoster() {
	before, hadBefore := m.bgPeekTarget()
	roster := bgRoster(m.processes, m.agents, m.childAct, epochNow())
	if m.bgPeekIdx >= len(roster) {
		m.bgPeekIdx = 0
	}
	m.bgPeekRoster = roster
	if after, ok := m.bgPeekTarget(); !ok || !hadBefore || after.id != before.id {
		m.resetBgPeekPane()
	}
}

// appendPaneLines appends freshly read lines to the pane and keeps the last
// bgPeekTailMaxLines of them: the pane only ever renders its tail, so a long
// working subagent must not grow the buffer for the whole session.
func appendPaneLines(existing, added []string) []string {
	if len(added) == 0 {
		return existing
	}
	lines := append(existing, added...)
	if len(lines) > bgPeekTailMaxLines {
		lines = lines[len(lines)-bgPeekTailMaxLines:]
	}
	return lines
}

// bgPeekTickMsg drives the output pane's live refresh while the cycle is open.
// A tail view that only moves when the user presses r is not a view of what is
// going on: the pane re-reads every couple of seconds until the cycle closes.
type bgPeekTickMsg struct{}

const bgPeekTickInterval = 2 * time.Second

func bgPeekTickCmd() tea.Cmd {
	return tea.Tick(bgPeekTickInterval, func(time.Time) tea.Msg { return bgPeekTickMsg{} })
}

// armBgPeekTick is an "at most once" gate for the pane's timer (the armSpinner
// lesson): the pending tick is the only thing that clears the flag, so a
// leave-and-reopen cannot leave two live timers behind.
func (m *model) armBgPeekTick() tea.Cmd {
	if m.bgPeekTicking || m.mode != modeBgPeek {
		return nil
	}
	m.bgPeekTicking = true
	return bgPeekTickCmd()
}

// settlePendingBgPeek decides a ctrl+o that found nothing in a possibly-stale
// snapshot: an answer that shows work opens the cycle right away (the other
// answer lands into the open roster), while the note is only answered once
// both refreshes have come back empty — one snapshot still in flight must not
// turn into "nothing is running".
func (m *model) settlePendingBgPeek() tea.Cmd {
	if !m.bgPeekPending {
		return nil
	}
	roster := bgRoster(m.processes, m.agents, m.childAct, epochNow())
	if len(roster) == 0 {
		if m.bgPeekPendingProc || m.bgPeekPendingAgent {
			return nil // the other snapshot is still on its way
		}
		m.bgPeekPending = false
		m.contextNote = t(m.loc, "note.nothingRunning")
		m.layout()
		return nil
	}
	m.bgPeekPending, m.bgPeekPendingProc, m.bgPeekPendingAgent = false, false, false
	m.bgPeekRoster = roster
	m.bgPeekIdx = 0
	m.resetBgPeekPane()
	m.mode = modeBgPeek
	m.layout()
	return tea.Batch(m.loadBgPeekBody(), m.armBgPeekTick())
}

// bgPeekTailMaxLines bounds the pane's line buffer (see appendPaneLines).
const bgPeekTailMaxLines = 400

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
