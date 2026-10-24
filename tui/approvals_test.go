package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// approveAllKey is the shifted "a" the gate modal listens for; Text carries
// the printable form the terminal actually delivers.
func approveAllKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'A', Text: "A"}
}

// approveKey is a bare "a" (remember this tool for the session).
func approveKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'a', Text: "a"}
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

// A session-less request cannot key a persisted grant, so "a"/"A" do
// nothing on it. They must say so: a key that is consumed with no visible
// effect reads as broken next to enter, which has no such precondition.
func TestInertApprovalKeysExplainThemselves(tst *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "c1", tool: "bash"}})

	if _, consumed := m.approvalKey(approveKey()); !consumed {
		tst.Fatal("a not consumed; it must be taken and explained")
	}
	if want := t(m.loc, "approval.noSession"); m.approvalNotice != want {
		tst.Fatalf("a gave no notice: %q", m.approvalNotice)
	}
	if !strings.Contains(m.approvalBox(), t(m.loc, "approval.noSession")) {
		tst.Fatal("notice not rendered in the modal")
	}

	// The up-front hint must not promise a/A either.
	m.approvalNotice = ""
	if !strings.Contains(m.approvalBox(), t(m.loc, "approval.hintNoSession")) {
		tst.Fatal("session-less request still advertises a/A in the hint")
	}

	// A: arms, then explains instead of granting.
	m.approvalKey(approveAllKey())
	m.approvalKey(approveAllKey())
	if want := t(m.loc, "approval.noSessionAll"); m.approvalNotice != want {
		tst.Fatalf("A gave no notice: %q", m.approvalNotice)
	}
	if len(m.approvals) != 1 {
		tst.Fatalf("session-less request answered by A: %+v", m.approvals)
	}
}

// With a session, the keys work as before and clear any stale notice.
func TestApprovalKeysWorkWithSession(tst *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "c1", tool: "bash", sessionID: "sess-1"}})
	m.approvalNotice = t(m.loc, "approval.noSession") // stale from a prior prompt
	m.approvalKey(approveKey())
	if len(m.approvals) != 0 {
		tst.Fatalf("a did not answer a session request: %+v", m.approvals)
	}
	if m.approvalNotice != "" {
		tst.Fatalf("stale notice survived: %q", m.approvalNotice)
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

func TestApproveAllDefersSaveUntilTurnCompletion(t *testing.T) {
	m := newTestModel()
	m.session = "sess-1"
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{
		id: "wait", tool: "bash", sessionID: "sess-1",
	}})
	m.approvalKey(approveAllKey())
	cmd, consumed := m.approvalKey(approveAllKey())
	if !consumed || cmd != nil || !m.approvalSaves["sess-1"] || !m.autoAll["sess-1"] {
		t.Fatalf("approval wait scheduled a control before turn completion: consumed=%v cmd=%v pending=%v", consumed, cmd != nil, m.approvalSaves)
	}
	// Further requests within the same turn are answered by the local grant.
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{
		id: "later", tool: "core.spawn", sessionID: "sess-1",
	}, directed: true})
	if len(m.approvals) != 0 {
		t.Fatalf("later request queued: %+v", m.approvals)
	}
	updated, _ := m.Update(turnDoneMsg{session: "sess-1", reply: "done"})
	got := updated.(model)
	if got.approvalSaves["sess-1"] || got.isAutoApproved("sess-1", "another-tool") {
		t.Fatal("completion must wait for core to persist the grant")
	}
	got.applyApprovalsMode(approvalsModeMsg{Session: "sess-1", Mode: "auto"})
	if !got.isAutoApproved("sess-1", "another-tool") {
		t.Fatal("later turn lost persisted grant")
	}
	got.applyApprovalsMode(approvalsModeMsg{Session: "sess-1", Mode: "", Err: errors.New("store unavailable")})
	if got.isAutoApproved("sess-1", "another-tool") {
		t.Fatal("failed save left a silent later-turn grant")
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

// bigApproval queues a gate whose argument block is far taller than any
// terminal, the case the modal used to overflow.
func bigApproval(id string, lines int) approvalRequest {
	code := strings.Repeat("print(\"a fairly long line of program source\")\n", lines)
	args, err := json.Marshal(map[string]any{"code": code})
	if err != nil {
		panic(err)
	}
	return approvalRequest{id: id, tool: "fabric", args: args, sessionID: "sess-1"}
}

func approvalRows(s string) int { return len(strings.Split(s, "\n")) }

// A gate prompt must never be taller than the terminal: the verdict keys and
// the tool name are the whole point of asking.
func TestApprovalBoxFitsTheScreen(tst *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 24
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("big", 200)})

	box := m.approvalBox()
	if got := approvalRows(box); got > m.height {
		tst.Fatalf("modal is %d rows on a %d-row terminal", got, m.height)
	}
	// The compact form still says what is being asked and how to see the rest.
	more := t(m.loc, "approval.more", fmt.Sprint(len(m.approvalArgsLines(m.approvals[0]))))
	for _, want := range []string{"fabric", more, t(m.loc, "approval.expand"), t(m.loc, "approval.hint")} {
		if !strings.Contains(box, want) {
			tst.Fatalf("compact modal lost %q: \n%s", want, box)
		}
	}
}

// A short call is shown in full and never advertises an expander that would
// do nothing.
func TestApprovalBoxNotClippedWhenItFits(tst *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 40
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{
		id: "small", tool: "bash", sessionID: "sess-1",
		args: json.RawMessage(`{"command":"ls -la"}`),
	}})
	box := m.approvalBox()
	if !strings.Contains(box, "ls -la") {
		tst.Fatalf("small call not shown: \n%s", box)
	}
	if strings.Contains(box, t(m.loc, "approval.expand")) {
		tst.Fatalf("unclipped modal offers an expander: \n%s", box)
	}
	if m.approvalOverflows(m.approvals[0]) {
		tst.Fatal("a two-line call reported as overflowing")
	}
}

// "v" opens the full argument block in place, scrolls through it with the
// usual keys and closes again — without answering the gate.
func TestApprovalExpandScrollsFullArguments(tst *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 24
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("big", 200)})

	press := func(k string) tea.KeyPressMsg {
		switch k {
		case "pgdown":
			return tea.KeyPressMsg{Code: tea.KeyPgDown}
		case "end":
			return tea.KeyPressMsg{Code: tea.KeyEnd}
		default:
			return tea.KeyPressMsg{Code: 'v', Text: k}
		}
	}
	if _, consumed := m.approvalKey(press("v")); !consumed {
		tst.Fatal("v not consumed by the modal")
	}
	if !m.approvalExpanded {
		tst.Fatal("v did not expand the arguments")
	}
	box := m.approvalBox()
	if got := approvalRows(box); got > m.height {
		tst.Fatalf("expanded modal is %d rows on a %d-row terminal", got, m.height)
	}
	window, total := m.approvalWindow(m.approvals[0])
	if !strings.Contains(box, t(m.loc, "approval.scroll", "1", fmt.Sprint(window), fmt.Sprint(total))) {
		tst.Fatalf("expanded modal does not report the window: \n%s", box)
	}
	if len(m.approvals) != 1 {
		tst.Fatal("expanding answered the gate")
	}

	// Scroll keys move the window and are consumed by the modal.
	if _, consumed := m.approvalKey(tea.KeyPressMsg{Code: tea.KeyPgDown}); !consumed {
		tst.Fatal("pgdown not consumed while expanded")
	}
	if m.approvalScroll == 0 {
		tst.Fatal("pgdown did not scroll")
	}
	if _, consumed := m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnd}); !consumed {
		tst.Fatal("end not consumed while expanded")
	}
	window, lines := m.approvalWindow(m.approvals[0])
	if want := lines - window; m.approvalScroll != want {
		tst.Fatalf("end scrolled to %d, want %d", m.approvalScroll, want)
	}

	// Collapsing keeps the gate pending and restores the compact form.
	m.approvalKey(press("v"))
	if m.approvalExpanded || m.approvalScroll != 0 {
		tst.Fatalf("collapse left expanded=%v scroll=%d", m.approvalExpanded, m.approvalScroll)
	}
	if len(m.approvals) != 1 {
		tst.Fatal("collapsing answered the gate")
	}
}

// The scroll position belongs to one call: a different prompt must open at
// the top, not mid-way through its arguments.
func TestApprovalScrollResetsWithTheFrontRequest(tst *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 24
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("first", 200)})
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("second", 200)})
	m.approvalKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.approvalScroll == 0 {
		tst.Fatal("end did not scroll")
	}

	// Answering the request on screen promotes the queued one; it must open
	// at the top of its own arguments.
	m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.approvals) != 1 || m.approvals[0].id != "second" {
		tst.Fatalf("queue after the verdict: %+v", m.approvals)
	}
	if m.approvalExpanded || m.approvalScroll != 0 {
		tst.Fatal("the next prompt inherited the previous scroll position")
	}

	// Another client answering the prompt on screen (resolved) does the same.
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("third", 200)})
	m.approvalKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.applyApprovalResolved("second")
	if m.approvalExpanded || m.approvalScroll != 0 {
		tst.Fatal("a resolved prompt carried the scroll position over")
	}

	// A request that arrives behind the one on screen changes nothing.
	m.approvalKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("fourth", 200)})
	if !m.approvalExpanded || m.approvalScroll == 0 {
		tst.Fatal("a queued request reset the prompt on screen")
	}
}

// A resize (or a shorter call) can leave the window past the end of the
// block; the view must clamp instead of slicing out of range.
func TestApprovalScrollClampsOnResize(tst *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 24
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("big", 200)})
	m.approvalKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnd})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	got := updated.(model)
	window, total := got.approvalWindow(got.approvals[0])
	if limit := max(0, total-window); got.approvalScroll > limit {
		tst.Fatalf("scroll %d past the end (%d rows, %d-row window)", got.approvalScroll, total, window)
	}
	if box := got.approvalBox(); approvalRows(box) > got.height {
		tst.Fatalf("modal is %d rows on a %d-row terminal", approvalRows(box), got.height)
	}
}

// The wheel scrolls the expanded arguments rather than the transcript hidden
// behind the modal.
func TestApprovalWheelScrollsExpandedArguments(tst *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 24
	m.applyApprovalEvent(approvalEventMsg{req: bigApproval("big", 200)})

	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if got := updated.(model); got.approvalScroll != 0 {
		tst.Fatal("the wheel scrolled the compact modal")
	}

	m.approvalKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	updated, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	got := updated.(model)
	if got.approvalScroll == 0 {
		tst.Fatal("the wheel did not scroll the expanded arguments")
	}
	if got.viewport.YOffset() != m.viewport.YOffset() {
		tst.Fatal("the wheel moved the transcript behind the modal")
	}
	updated, _ = got.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if back := updated.(model); back.approvalScroll >= got.approvalScroll {
		tst.Fatalf("wheel up did not scroll back: %d → %d", got.approvalScroll, back.approvalScroll)
	}
}
