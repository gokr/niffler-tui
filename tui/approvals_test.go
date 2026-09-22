package main

import (
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
	if _, consumed := m.approvalKey(approveAllKey()); !consumed {
		t.Fatal("'A' not consumed by a pending approval")
	}
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
	m.approvalKey(approveAllKey())

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
	if _, consumed := m.approvalKey(approveAllKey()); !consumed {
		t.Fatal("'A' not consumed by a pending approval")
	}
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
