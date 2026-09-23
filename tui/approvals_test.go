package main

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// approveAllKey is the shifted "a" the gate modal listens for; Text carries
// the printable form the terminal actually delivers.
func approveAllKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'A', Text: "A"}
}

func TestApproveAllKeyGrantsEveryToolInConversation(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "a1", tool: "core.spawn", sessionID: "sess-1"},
	})
	approveAll(&m)
	if len(m.approvals) != 0 {
		t.Fatalf("queue not empty after approve-all: %+v", m.approvals)
	}
	// Every tool, including ones this conversation never saw.
	for _, tool := range []string{"core.spawn", "bash", "fabric", "anything.else"} {
		if !m.isAutoApproved("sess-1", tool) {
			t.Fatalf("tool %q not auto-approved by 'A'", tool)
		}
	}
	// And only this conversation.
	if m.isAutoApproved("sess-2", "bash") {
		t.Fatal("approve-all leaked into another conversation")
	}
}

func TestApproveAllAnswersLaterRequestsWithoutQueueing(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "b1", tool: "bash", sessionID: "sess-1"},
	})
	approveAll(&m)

	// A different gated tool, later in the same turn: answered, never queued.
	m.applyApprovalEvent(approvalEventMsg{
		req:      approvalRequest{id: "b2", tool: "core.spawn", sessionID: "sess-1"},
		directed: true,
	})
	if len(m.approvals) != 0 {
		t.Fatalf("request queued despite approve-all: %+v", m.approvals)
	}

	// Another conversation keeps gating.
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "b3", tool: "bash", sessionID: "sess-2"},
	})
	if len(m.approvals) != 1 || m.approvals[0].id != "b3" {
		t.Fatalf("other conversation not gated: %+v", m.approvals)
	}
}

func TestApproveAllIgnoredWithoutSession(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "c1", tool: "bash"}})
	m.approvalKey(approveAllKey())
	m.approvalKey(approveAllKey())
	// A session-less request cannot be remembered per conversation, so it
	// stays queued instead of being silently granted.
	if len(m.approvals) != 1 {
		t.Fatalf("session-less request answered by 'A': %+v", m.approvals)
	}
	if len(m.autoAll) != 0 {
		t.Fatalf("approve-all recorded without a session: %v", m.autoAll)
	}
}

func TestRememberAutoApproveAllPersistsOnlyWithComponent(t *testing.T) {
	// With a live component the command persists the mode so core stops
	// asking any client; in tests comp is nil, so only memory records it.
	m := newTestModel()
	if cmd := m.rememberAutoApproveAll("sess-1"); cmd != nil {
		t.Fatal("persist command expected only with a live component")
	}
	if !m.autoAll["sess-1"] {
		t.Fatal("in-memory approve-all not recorded")
	}
	// Empty session is ignored.
	m.rememberAutoApproveAll("")
	if len(m.autoAll) != 1 {
		t.Fatalf("empty session recorded: %v", m.autoAll)
	}
}

func TestApprovalsModeMsgSyncsLocalFlag(t *testing.T) {
	m := newTestModel()
	m.applyApprovalsMode(approvalsModeMsg{Session: "sess-1", Mode: "auto"})
	if !m.isAutoApproved("sess-1", "bash") {
		t.Fatal("readback 'auto' did not arm the local flag")
	}
	m.applyApprovalsMode(approvalsModeMsg{Session: "sess-1"})
	if m.isAutoApproved("sess-1", "bash") {
		t.Fatal("readback 'ask' did not clear the local flag")
	}
}

func TestApprovalBoxReportsAllMode(tt *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "d1", tool: "bash", sessionID: "sess-1"},
	})
	m.setAutoApproveAllLocal("sess-1", true)
	if box := m.approvalBox(); !strings.Contains(box, t(LocaleEN, "approval.allOn")) {
		tt.Fatalf("box does not report approve-all: %q", box)
	}
}

// approveAll grants the queued request(s) the way a user would: arm with one
// press, confirm with the second.
func approveAll(m *model) {
	m.approvalKey(approveAllKey())
	m.approvalKey(approveAllKey())
}

// The flag is consulted when a request ARRIVES, so "approve all" has to drain
// the backlog too — otherwise the user presses A and is still asked for the
// requests that were already queued behind the one on screen.
func TestApproveAllDrainsQueuedBacklog(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "q1", tool: "bash", sessionID: "sess-1"},
	})
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "q2", tool: "core.spawn", sessionID: "sess-1"},
	})
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "q3", tool: "fabric", sessionID: "sess-2"},
	})
	approveAll(&m)

	if len(m.approvals) != 1 || m.approvals[0].id != "q3" {
		t.Fatalf("backlog not drained (or foreign request dropped): %+v", m.approvals)
	}
	if !m.isAutoApproved("sess-1", "core.spawn") {
		t.Fatal("conversation not left auto-approving")
	}
	if m.isAutoApproved("sess-2", "fabric") {
		t.Fatal("approve-all leaked into the other conversation")
	}
}

// The widest grant in the modal is two-stage, like the stop and kill
// confirmations: one press must never grant a whole conversation.
func TestApproveAllNeedsConfirmation(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "c1", tool: "core.spawn", sessionID: "sess-1"},
	})
	if _, consumed := m.approvalKey(approveAllKey()); !consumed {
		t.Fatal("first 'A' not consumed")
	}
	if len(m.approvals) != 1 || m.isAutoApproved("sess-1", "core.spawn") {
		t.Fatalf("one press granted everything: queued=%+v", m.approvals)
	}
	if !m.approveAllArmed {
		t.Fatal("first press did not arm")
	}

	m.approvalKey(approveAllKey())
	if len(m.approvals) != 0 || !m.isAutoApproved("sess-1", "bash") {
		t.Fatalf("second press did not grant: %+v", m.approvals)
	}
}

func TestApproveAllArmDisarmsOnOtherKeys(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "c2", tool: "bash", sessionID: "sess-1"},
	})
	m.approvalKey(approveAllKey())
	if !m.approveAllArmed {
		t.Fatal("first press did not arm")
	}
	// Any other key — here deny — disarms without granting anything.
	m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.approveAllArmed {
		t.Fatal("esc left the arm armed")
	}
	if m.isAutoApproved("sess-1", "bash") {
		t.Fatal("deny granted the tool")
	}

	// An arm also never outlives a fresh request.
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "c3", tool: "bash", sessionID: "sess-1"},
	})
	m.approvalKey(approveAllKey())
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "c4", tool: "bash", sessionID: "sess-1"},
	})
	if m.approveAllArmed {
		t.Fatal("a new request left the arm armed")
	}
}

// A failed control call must not leave a half-granted mode behind: the flag
// is set optimistically, so cancelling it is what makes the message true.
func TestApprovalsModeFailureCancelsGrant(t *testing.T) {
	m := newTestModel()
	m.setAutoApproveAllLocal("sess-1", true)
	m.applyApprovalsMode(approvalsModeMsg{
		Session: "sess-1", Mode: "auto", Err: errors.New("bus gone"),
	})
	if m.isAutoApproved("sess-1", "bash") {
		t.Fatal("failed save left the conversation auto-approving")
	}
}

// The flag must track core's own mode, not this client's memory of it: a
// mode another client changed back to ask has to stop this TUI granting.
func TestBootstrapReconcilesApprovalsMode(t *testing.T) {
	m := newTestModel()
	m.session = "sess-1"
	m.setAutoApproveAllLocal("sess-1", true)

	updated, _ := m.Update(bootstrapMsg{
		Session:      "sess-1",
		Conversation: conversationState{Approvals: ""},
	})
	got := updated.(model)
	if got.isAutoApproved("sess-1", "bash") {
		t.Fatal("stale auto flag survived a header that says ask")
	}

	updated, _ = got.Update(bootstrapMsg{
		Session:      "sess-1",
		Conversation: conversationState{Approvals: "auto"},
	})
	got = updated.(model)
	if !got.isAutoApproved("sess-1", "bash") {
		t.Fatal("header in auto did not arm the flag")
	}
}

// Reporting is faithful to core's stored mode: "ask" persists as "", and the
// note must not claim more than core recorded.
func TestApprovalsModeMsgEchoesStoredMode(t *testing.T) {
	m := newTestModel()
	m.applyApprovalsMode(approvalsModeMsg{Session: "sess-1", Mode: ""})
	if m.isAutoApproved("sess-1", "bash") {
		t.Fatal("stored ask rendered as auto")
	}
}
