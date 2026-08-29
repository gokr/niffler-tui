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
	var persist tea.Cmd
	if auto {
		persist = m.rememberAutoApprove(req.sessionID, req.tool)
	}
	m.replyApproval(req.id, false, ok)
	m.approvals = m.approvals[1:]
	return persist
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
	if tools := m.autoApproved[req.sessionID]; len(tools) > 0 {
		b.WriteString("\n\nauto-approving this session: " + strings.Join(tools, ", "))
	}
	width := 72
	if m.width > 0 {
		width = max(24, min(72, m.width-4))
	}
	hint := t(m.loc, "approval.hint")
	return approvalBoxStyle.Width(width).Render(b.String()) + "\n" +
		metaStyle.Render(hint)
}

// approvalKey takes Enter/Esc/"a" while a gate prompt is pending. Returns
// the key's follow-up command (if any) and whether the key was consumed.
func (m *model) approvalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if len(m.approvals) == 0 {
		return nil, false
	}
	switch msg.String() {
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
