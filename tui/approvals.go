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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const maxApprovalArgs = 3000

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
	m.approvals = slices.DeleteFunc(m.approvals, func(req approvalRequest) bool {
		return req.id == id
	})
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

// approvalBox renders the modal gate prompt. Shown instead of the chat
// surface while requests are pending (like the control-mode views).
func (m *model) approvalBox() string {
	req := m.approvals[0]
	var b strings.Builder
	b.WriteString(approvalTitleStyle.Render(t(m.loc, "approval.title")))
	b.WriteString("\nA tool call with x-harness.approval is waiting for your ok:\n\n")
	b.WriteString(toolStyle.Render(req.tool) + " " + prettyApprovalArgs(req.args))
	if len(m.approvals) > 1 {
		fmt.Fprintf(&b, "\n\n+ %d more waiting", len(m.approvals)-1)
	}
	if m.autoAll[req.sessionID] {
		b.WriteString("\n\n" + t(m.loc, "approval.allOn"))
	} else if tools := m.autoApproved[req.sessionID]; len(tools) > 0 {
		b.WriteString("\n\nauto-approving this session: " + strings.Join(tools, ", "))
	}
	width := 72
	if m.width > 0 {
		width = max(24, min(72, m.width-4))
	}
	hint := t(m.loc, "approval.hint")
	if m.approveAllArmed {
		hint = t(m.loc, "approval.armed")
	}
	return approvalBoxStyle.Width(width).Render(b.String()) + "\n" +
		metaStyle.Render(hint)
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
	if key == "A" || key == "shift+a" {
		if !m.approveAllArmed {
			m.approveAllArmed = true
			return nil, true
		}
		if m.approvals[0].sessionID != "" {
			return m.answerApprovalAll(), true
		}
		m.approveAllArmed = false
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
		return nil, true
	}
	return nil, false
}
