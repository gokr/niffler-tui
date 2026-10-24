// Approval gate — the TUI's half of the human-approval protocol
// (x-harness.approval tools, docs/WIRE.md "Approvals").
//
// Requests arrive two ways:
//   - directed: svc.approval.<name>.request, the TUI's private subject.
//     Core routes turns it drives (caller == componentName) here; the TUI
//     acks on ev.approval.reply so the runner knows a human is being asked,
//     then answers with the decision.
//   - broadcast: ev.approval.request, used for direct (non-session) calls
//     and fallbacks whose driver vanished; any interactive client may
//     answer, first reply wins.
//
// ev.approval.resolved dismisses stale modals when another client answered.
package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	maxApprovalArgs = 3000
	// approvalFrameRows is the box's own chrome: top and bottom border plus
	// one row of padding on each side.
	approvalFrameRows = 4
	// approvalHeadRows is the fixed part above the argument block: title,
	// intro line, blank separator and the tool name.
	approvalHeadRows = 4
)

// approvalRequest is one queued human-gate prompt.
type approvalRequest struct {
	id        string
	tool      string
	args      json.RawMessage
	sessionID string
}

// approvalEventMsg delivers a gate request seen on the bus. directed marks
// requests from this component's private subject; those must be acked.
type approvalEventMsg struct {
	req      approvalRequest
	directed bool
}

// approvalResolvedMsg drops a queued request whose gate already reached a
// verdict (possibly answered by another client).
type approvalResolvedMsg struct {
	id string
}

// parseApprovalPayload extracts {id, tool, args, sessionId} from a bus
// payload. Empty when the payload is not a well-formed request.
func parseApprovalPayload(payload json.RawMessage) (approvalRequest, bool) {
	var p struct {
		ID        string          `json:"id"`
		Tool      string          `json:"tool"`
		Args      json.RawMessage `json:"args"`
		SessionID string          `json:"sessionId"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.ID == "" || p.Tool == "" {
		return approvalRequest{}, false
	}
	return approvalRequest{id: p.ID, tool: p.Tool, args: p.Args,
		sessionID: p.SessionID}, true
}

// applyApprovalEvent runs in Update: auto-approved tools are answered
// immediately, directed requests are acked and queued, broadcast requests
// are queued without an ack.
func (m *model) applyApprovalEvent(msg approvalEventMsg) tea.Cmd {
	req := msg.req
	// A fresh request invalidates a pending "approve all" arm: the arm was
	// raised for a different prompt on screen and must never carry over.
	m.approveAllArmed = false
	// The first request of a quiet queue becomes the front of the modal; its
	// arguments start at the top, with no scroll position from a prior gate.
	if len(m.approvals) == 0 {
		m.resetApprovalView()
	}
	if m.isAutoApproved(req.sessionID, req.tool) {
		// A decision alone resolves the gate; no ack needed.
		m.replyApproval(req.id, false, true)
		return nil
	}
	if msg.directed {
		// Ack so the runner knows a human is being asked, then show the modal.
		m.replyApproval(req.id, true, false)
	}
	m.approvals = append(m.approvals, req)
	return nil
}

// applyApprovalResolved removes a request whose gate reached a verdict.
func (m *model) applyApprovalResolved(id string) {
	m.approveAllArmed = false
	front := ""
	if len(m.approvals) > 0 {
		front = m.approvals[0].id
	}
	m.approvals = slices.DeleteFunc(m.approvals, func(req approvalRequest) bool {
		return req.id == id
	})
	// A different request may now be on screen; it must open at the top.
	if len(m.approvals) == 0 || m.approvals[0].id != front {
		m.resetApprovalView()
	}
}

// answerApproval replies to the front queued request; auto also remembers
// the tool for the rest of the session (per-conversation auto-approve). The
// returned command persists the new auto-approve to the store off the
// update loop; the UI must not block on it.
func (m *model) answerApproval(ok, auto bool) tea.Cmd {
	if len(m.approvals) == 0 {
		return nil
	}
	req := m.approvals[0]
	m.approveAllArmed = false
	var persist tea.Cmd
	if auto {
		persist = m.rememberAutoApprove(req.sessionID, req.tool)
	}
	m.replyApproval(req.id, false, ok)
	m.approvals = m.approvals[1:]
	m.resetApprovalView()
	return persist
}

// approvalsModeMsg reports the outcome of a gate-mode change ("A" on the
// modal, or /approvals): the mode the conversation now carries, or the error
// that kept it from being persisted.
type approvalsModeMsg struct {
	Session string
	Mode    string // "auto", or "" for ask
	Err     error
}

// answerApprovalAll grants the request on screen and every other request
// already queued for the same conversation, then turns that conversation's
// gate off: every gated tool is granted without asking from here on. Where
// "a" remembers one tool name, this needs to know nothing in advance — which
// is what a human wants once they have decided to trust a whole run instead
// of a list of tools.
//
// Draining the backlog matters: the flag below is consulted when a request
// ARRIVES, so it does not by itself answer requests already sitting in the
// queue — those would keep raising modals after a plain approve-all.
//
// It has two effects, because core's gate mode applies from the NEXT turn on:
//
//   - the in-memory flag answers everything raised later in the current
//     turn, by replying from this client, and
//   - the persisted conversation control (approvals: "auto") stops core from
//     asking any client at all on later turns, and survives a TUI restart or
//     a resumed conversation.
//
// Program-shaped gates (fabric, agent_run) are covered by the mode as well:
// this is a blanket opt-out of the human gate for one conversation, which is
// why approvalKey arms it before it runs.
func (m *model) answerApprovalAll() tea.Cmd {
	if len(m.approvals) == 0 {
		return nil
	}
	req := m.approvals[0]
	m.approveAllArmed = false
	// The runner refuses session controls mid-turn. The local grant covers
	// this turn; a completion schedules the persistent mode for later turns.
	if m.approvalSaves == nil {
		m.approvalSaves = map[string]bool{}
	}
	m.approvalSaves[req.sessionID] = true
	m.rememberAutoApproveAll(req.sessionID)
	defer m.resetApprovalView()
	kept := make([]approvalRequest, 0, len(m.approvals))
	for _, pending := range m.approvals {
		if pending.id == req.id || (req.sessionID != "" && pending.sessionID == req.sessionID) {
			m.replyApproval(pending.id, false, true)
			continue
		}
		kept = append(kept, pending)
	}
	m.approvals = kept
	return nil
}

// rememberAutoApproveAll marks the conversation as granting every gated tool
// for the current turn. The caller defers persisting the mode until the
// runner has finished that turn; controls sent during approval wait get busy.
func (m *model) rememberAutoApproveAll(sessionID string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	if m.autoAll == nil {
		m.autoAll = map[string]bool{}
	}
	m.autoAll[sessionID] = true
	if m.comp == nil {
		return nil
	}
	return setConversationApprovalsCmd(m.comp, sessionID, "auto")
}

// setAutoApproveAllLocal records (or clears) the conversation's all-tools
// flag without a bus call, keeping the TUI's own answering in step with the
// mode core reports — including one set by another client.
func (m *model) setAutoApproveAllLocal(sessionID string, on bool) {
	if sessionID == "" {
		return
	}
	if m.autoAll == nil {
		m.autoAll = map[string]bool{}
	}
	if on {
		m.autoAll[sessionID] = true
		return
	}
	delete(m.autoAll, sessionID)
}

// applyApprovalsMode lands a gate-mode change: it reports what the
// conversation now does and re-syncs the local all-tools flag with it, so a
// bare /approvals readback also reflects a mode set from another client.
//
// A failed change CANCELS the grant rather than keeping a half of it: the
// flag was set optimistically before the control call, and leaving it set
// would silently approve gates for a mode core never recorded.
func (m *model) applyApprovalsMode(msg approvalsModeMsg) tea.Cmd {
	if msg.Err != nil {
		m.setAutoApproveAllLocal(msg.Session, false)
		if msg.Session == m.session {
			m.addBlock(blockError, t(m.loc, "note.approvalsFailed", msg.Err.Error()))
			m.syncViewport(true)
		}
		return nil
	}
	m.setAutoApproveAllLocal(msg.Session, msg.Mode == "auto")
	if msg.Session != m.session {
		return nil
	}
	if msg.Mode == "auto" {
		m.addBlock(blockMeta, t(m.loc, "note.approvalsAuto"))
	} else {
		m.addBlock(blockMeta, t(m.loc, "note.approvalsAsk"))
	}
	m.syncViewport(true)
	return nil
}

// replyApproval publishes one frame of the reply protocol on
// ev.approval.reply: {id, ack: true} or {id, ok}. The bus component may be
// absent in tests; replies are then simply dropped.
func (m *model) replyApproval(id string, ack, ok bool) {
	if m.comp == nil {
		return
	}
	payload := map[string]any{"id": id}
	if ack {
		payload["ack"] = true
	} else {
		payload["ok"] = ok
	}
	_ = m.comp.Emit("ev.approval.reply", payload)
}

func (m *model) isAutoApproved(sessionID, tool string) bool {
	// "A" (approve all) turns the whole conversation's gate off, so no tool
	// name has to be known in advance — see answerApprovalAll.
	if sessionID != "" && m.autoAll[sessionID] {
		return true
	}
	return slices.Contains(m.autoApproved[sessionID], tool)
}

// rememberAutoApprove records a per-session auto-approve in memory and
// returns a command that persists it to the store so the core's gate honors
// it for every client (no dialog at all, not even a flash). The store put
// must not run inside Update; the command moves it off the update loop.
// Best effort: the in-memory list still covers this session if the store is
// unreachable. Idempotent; nil session ids are ignored.
func (m *model) rememberAutoApprove(sessionID, tool string) tea.Cmd {
	if sessionID == "" {
		return nil
	}
	if slices.Contains(m.autoApproved[sessionID], tool) {
		return nil
	}
	if m.autoApproved == nil {
		m.autoApproved = map[string][]string{}
	}
	m.autoApproved[sessionID] = append(m.autoApproved[sessionID], tool)
	if m.comp == nil {
		return nil
	}
	return func() tea.Msg {
		_, _ = m.comp.Request("store", "put", map[string]any{
			"kind":  "approval",
			"id":    sessionID + ":" + tool,
			"value": map[string]any{"tool": tool, "sessionId": sessionID},
		}, 5*time.Second)
		return nil
	}
}

// prettyApprovalArgs renders the call arguments as indented JSON, truncated
// to a readable ceiling (matching the web UI's modal cap).
func prettyApprovalArgs(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "{}"
	}
	var node any
	if err := json.Unmarshal(raw, &node); err == nil {
		if pretty, err := json.MarshalIndent(node, "", "  "); err == nil {
			s = string(pretty)
		}
	}
	if len(s) > maxApprovalArgs {
		// Rune-aware truncation: a byte slice would split CJK characters
		// mid-rune and produce invalid UTF-8 in the approval box.
		s = string([]rune(s)[:maxApprovalArgs]) + "…"
	}
	return s
}

var (
	// Compiled-in default theme colors; applyTheme (theme.go) overwrites
	// both when another theme is active.
	approvalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("3")).
				Padding(1, 2)
	approvalTitleStyle = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("3"))
)

// approvalBoxWidth keeps the modal readable on a wide terminal and inside a
// narrow one (the same clamp the legend below uses).
func (m model) approvalBoxWidth() int {
	width := 72
	if m.width > 0 {
		width = max(24, min(72, m.width-4))
	}
	return width
}

// approvalContentWidth is the box's inner text width; the frame's horizontal
// padding eats two cells on each side.
func (m model) approvalContentWidth() int {
	return max(8, m.approvalBoxWidth()-4)
}

// approvalViewWidth is the width available to the legend under the box.
// Zero means the terminal size is not known yet.
func (m model) approvalViewWidth() int {
	if m.width <= 0 {
		return 0
	}
	return max(8, m.width-1)
}

// wrapRows counts the terminal rows a line of text occupies at width, counting
// wraps the terminal would do. Height budgets have to include those, or a long
// legend silently pushes the top of the modal off the screen.
func wrapRows(text string, width int) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	if width <= 0 {
		return 1
	}
	return len(strings.Split(clampLines(text, width), "\n"))
}

// clampToView fits text to the client width. Unknown width leaves it alone
// (tests run before the first resize).
func (m model) clampToView(text string) string {
	if width := m.approvalViewWidth(); width > 0 {
		return clampLines(text, width)
	}
	return text
}

// approvalArgsLines is the pretty-printed argument block split into rows that
// fit the box. Wrapping first means the window math below counts what the
// terminal will draw, and no long JSON line spills past the right edge.
func (m model) approvalArgsLines(req approvalRequest) []string {
	return strings.Split(clampLines(prettyApprovalArgs(req.args), m.approvalContentWidth()), "\n")
}

// approvalTail is the text under the argument block: how many requests are
// queued behind this one, and what the conversation has already been granted.
func (m model) approvalTail(req approvalRequest) []string {
	var tail []string
	if len(m.approvals) > 1 {
		tail = append(tail, "", fmt.Sprintf("+ %d more waiting", len(m.approvals)-1))
	}
	if m.autoAll[req.sessionID] {
		tail = append(tail, "", t(m.loc, "approval.allOn"))
	} else if tools := m.autoApproved[req.sessionID]; len(tools) > 0 {
		tail = append(tail, "", "auto-approving this session: "+strings.Join(tools, ", "))
	}
	if len(tail) == 0 {
		return nil
	}
	return strings.Split(clampLines(strings.Join(tail, "\n"), m.approvalContentWidth()), "\n")
}

// approvalHint is the key legend under the modal; an explanation for an inert
// "a"/"A" overrides it.
func (m model) approvalHint(req approvalRequest) string {
	hint := t(m.loc, "approval.hint")
	if m.approveAllArmed {
		hint = t(m.loc, "approval.armed")
	}
	// A session-less request cannot be remembered, so the hint must not
	// promise that "a"/"A" will do anything. Cheaper to say it up front
	// than to let the key silently do nothing.
	if req.sessionID == "" {
		hint = t(m.loc, "approval.hintNoSession")
		if m.approveAllArmed {
			hint = t(m.loc, "approval.noSessionAll")
		}
	}
	if m.approvalNotice != "" {
		hint = m.approvalNotice
	}
	return hint
}

// approvalContentRows is how many text rows the box may use: the terminal
// height minus the header line, the frame and the footer lines below the box
// (the key legend plus the expand/scroll line, whose widest form is always
// reserved — a budget that shrank for a clipped modal would make it overflow
// by the very row it then draws). Zero means the height is unknown and
// nothing is clipped.
func (m model) approvalContentRows(hint string) int {
	if m.height <= 0 {
		return 0
	}
	width := m.approvalViewWidth()
	used := 1 + approvalFrameRows + wrapRows(hint, width) + wrapRows(m.approvalLongestLegend(), width)
	return max(1, m.height-used)
}

// approvalLongestLegend is the widest form the second footer line can take.
func (m model) approvalLongestLegend() string {
	return t(m.loc, "approval.scroll", "9999", "9999", "9999")
}

// approvalOverflows reports whether the arguments need more rows than the
// compact modal has. Only then does "v" have something more to reveal.
func (m model) approvalOverflows(req approvalRequest) bool {
	rows := m.approvalContentRows(m.approvalHint(req))
	if rows == 0 {
		return false
	}
	return approvalHeadRows+len(m.approvalTail(req))+len(m.approvalArgsLines(req)) > rows
}

// approvalScrollLegend is the line under the expanded view: which rows of the
// argument block are on screen, and how to move them.
func (m model) approvalScrollLegend(start, end, total int) string {
	return t(m.loc, "approval.scroll",
		strconv.Itoa(start), strconv.Itoa(end), strconv.Itoa(total))
}

// approvalWindow is the number of argument rows the expanded view shows and
// the height of the whole block it scrolls through.
func (m model) approvalWindow(req approvalRequest) (window, total int) {
	total = len(m.approvalArgsLines(req))
	rows := m.approvalContentRows(m.approvalHint(req))
	window = rows - approvalHeadRows - len(m.approvalTail(req))
	if rows == 0 || window >= total {
		window = total
	}
	return max(1, window), max(1, total)
}

// approvalFooters is the text under the box: the key legend, plus the
// expand/scroll line — the one key that reveals the rest of a clipped call
// has to be discoverable, and while it is open the same line says which rows
// are visible. `start` is the first visible argument row (0-based).
func (m model) approvalFooters(req approvalRequest, clipped bool, start, total int) []string {
	footers := []string{m.approvalHint(req)}
	switch {
	case m.approvalExpanded && clipped:
		window, _ := m.approvalWindow(req)
		first := min(start+1, total)
		footers = append(footers, m.approvalScrollLegend(first, min(total, first+window-1), total))
	case clipped:
		footers = append(footers, t(m.loc, "approval.expand"))
	}
	return footers
}

// clampApprovalScroll keeps the expanded window inside the argument block —
// after a resize, a switch to a shorter request, or a key that ran past the
// end.
func (m *model) clampApprovalScroll() {
	if len(m.approvals) == 0 {
		m.resetApprovalView()
		return
	}
	window, total := m.approvalWindow(m.approvals[0])
	m.approvalScroll = min(max(0, m.approvalScroll), max(0, total-window))
}

// resetApprovalView returns the modal to its compact, top-of-arguments state.
// Called whenever the front request changes, so one call's scroll position
// never leaks onto the next.
func (m *model) resetApprovalView() {
	m.approvalExpanded, m.approvalScroll = false, 0
}

// toggleApprovalExpanded opens or closes the full-argument view. It only does
// something when the compact modal actually had to clip arguments: an empty
// toggle on a small call would just swap identical views.
func (m *model) toggleApprovalExpanded() {
	if len(m.approvals) == 0 {
		return
	}
	if !m.approvalExpanded && !m.approvalOverflows(m.approvals[0]) {
		return
	}
	m.approvalExpanded = !m.approvalExpanded
	m.approvalScroll = 0
}

// approvalWheel scrolls the expanded argument view with the wheel. Without
// this the wheel would reach the transcript viewport behind the modal and
// move content the human cannot see.
func (m *model) approvalWheel(msg tea.MouseWheelMsg) bool {
	if len(m.approvals) == 0 || !m.approvalExpanded {
		return false
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.approvalScroll = max(0, m.approvalScroll-3)
	case tea.MouseWheelDown:
		m.approvalScroll += 3
	default:
		return false
	}
	m.clampApprovalScroll()
	return true
}

// approvalScrollKey moves the expanded window. It returns whether the key was
// consumed, so scroll keys never leak into the textarea behind the modal.
func (m *model) approvalScrollKey(key string) (tea.Cmd, bool) {
	if len(m.approvals) == 0 || !m.approvalExpanded {
		return nil, false
	}
	window, total := m.approvalWindow(m.approvals[0])
	maxScroll := max(0, total-window)
	switch key {
	case "up", "ctrl+up":
		m.approvalScroll = max(0, m.approvalScroll-1)
	case "down", "ctrl+down":
		m.approvalScroll = min(maxScroll, m.approvalScroll+1)
	case "pgup":
		m.approvalScroll = max(0, m.approvalScroll-window)
	case "pgdown":
		m.approvalScroll = min(maxScroll, m.approvalScroll+window)
	case "home":
		m.approvalScroll = 0
	case "end":
		m.approvalScroll = maxScroll
	default:
		return nil, false
	}
	m.clampApprovalScroll()
	return nil, true
}

// approvalBox renders the modal gate prompt. Shown instead of the chat
// surface while requests are pending (like the control-mode views).
//
// The argument block is clipped to the rows the terminal has left, and "v"
// opens the full, scrollable block in place: a large program-shaped call (a
// fabric program, a long script) must never push the verdict keys or the tool
// name off the screen — the human has to see what is being granted.
func (m *model) approvalBox() string {
	req := m.approvals[0]
	width := m.approvalBoxWidth()
	lines := m.approvalArgsLines(req)
	tail := m.approvalTail(req)
	hint := m.approvalHint(req)
	base := approvalHeadRows + len(tail)

	visible := len(lines)
	clipped := false
	moreText := ""
	if rows := m.approvalContentRows(hint); rows > 0 && base+len(lines) > rows {
		clipped = true
		moreText = t(m.loc, "approval.more", strconv.Itoa(len(lines)))
		markerRows := wrapRows(moreText, m.approvalContentWidth())
		visible = rows - base - markerRows
		if visible >= len(lines) {
			visible, clipped, moreText = len(lines), false, ""
		} else if visible < 1 {
			// A terminal too short for even the marker: one line of
			// arguments plus the expand legend is the most that fits.
			visible, moreText = 1, ""
		}
	}

	body := []string{
		approvalTitleStyle.Render(t(m.loc, "approval.title")),
		"A tool call with x-harness.approval is waiting for your ok:",
		"",
		clampLines(toolStyle.Render(req.tool), m.approvalContentWidth()),
	}

	if m.approvalExpanded && clipped {
		window, total := m.approvalWindow(req)
		start := min(max(0, m.approvalScroll), max(0, total-window))
		body = append(body, lines[start:min(total, start+window)]...)
	} else {
		body = append(body, lines[:visible]...)
		if clipped && moreText != "" {
			body = append(body, clampLines(moreText, m.approvalContentWidth()))
		}
	}
	body = append(body, tail...)

	parts := []string{approvalBoxStyle.Width(width).Render(strings.Join(body, "\n"))}
	for _, line := range m.approvalFooters(req, clipped, m.approvalScroll, len(lines)) {
		parts = append(parts, metaStyle.Render(m.clampToView(line)))
	}
	return strings.Join(parts, "\n")
}

// approvalKey takes Enter/Esc/"a"/"A" while a gate prompt is pending.
// Returns the key's follow-up command (if any) and whether the key was
// consumed. "a" remembers one tool for this session.
//
// "A" (approve all) is two-stage, like the stop and kill confirmations: the
// first press arms it and swaps the hint, the second runs it. Granting a
// whole conversation — program-shaped gates included — is the widest grant
// this modal can issue, so it must not be one careless keystroke away.
func (m *model) approvalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if len(m.approvals) == 0 {
		return nil, false
	}
	key := msg.String()
	// A key that does nothing must say so rather than vanish: "a"/"A" are
	// gated on a session (the grant is persisted per conversation), so on a
	// session-less request they are inert. Swallowing them made the modal
	// look broken next to Enter, which has no such precondition.
	m.approvalNotice = ""
	if key == "v" {
		// Looking at the arguments is not a verdict: an armed approve-all
		// must not survive it (same rule as any other non-A key).
		m.approveAllArmed = false
		m.toggleApprovalExpanded()
		return nil, true
	}
	if cmd, consumed := m.approvalScrollKey(key); consumed {
		m.approveAllArmed = false
		return cmd, true
	}
	if key == "A" || key == "shift+a" {
		if !m.approveAllArmed {
			m.approveAllArmed = true
			return nil, true
		}
		if m.approvals[0].sessionID != "" {
			return m.answerApprovalAll(), true
		}
		m.approveAllArmed = false
		m.approvalNotice = t(m.loc, "approval.noSessionAll")
		return nil, true
	}
	// Any other key disarms a pending arm, so it can never outlive the
	// prompt it was raised for.
	m.approveAllArmed = false
	switch key {
	case "enter":
		return m.answerApproval(true, false), true
	case "esc":
		return m.answerApproval(false, false), true
	case "a":
		if m.approvals[0].sessionID != "" {
			return m.answerApproval(true, true), true
		}
		m.approvalNotice = t(m.loc, "approval.noSession")
		return nil, true
	}
	return nil, false
}
