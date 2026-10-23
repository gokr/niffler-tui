package main

import (
	"strings"
	"testing"
)

func settledItem(peer, job string, activation int, objective, status, summary string) fleetInboxItem {
	return fleetInboxItem{
		JobID: job, Peer: peer, State: "settled", Objective: objective,
		Settled: &struct {
			Activation          int    `json:"activation"`
			Status              string `json:"status"`
			Summary             string `json:"summary"`
			TranscriptCommitted bool   `json:"transcriptCommitted"`
		}{Activation: activation, Status: status, Summary: summary},
	}
}

// Reading is not acknowledging, and unread is the client's own state: the
// renderer takes a snapshot of seen keys and must never mutate it.
func TestFleetInboxRenderIsPure(t *testing.T) {
	ledger := newActivityLedger()
	item := settledItem("worker", "fleet-abc", 1, "fix the test", "done", "did it")
	seen := ledger.fleetSeenKeys()
	out := renderFleetInbox([]fleetInboxItem{item}, seen)
	if seen[item.seenKey()] {
		t.Fatal("rendering marked the settlement as seen")
	}
	if !strings.Contains(out, "unread") {
		t.Fatalf("unread settlement not counted: %q", out)
	}
	// Marking afterwards flips it, and the row renders without the unread count.
	ledger.markFleetSeen(item.seenKey())
	out = renderFleetInbox([]fleetInboxItem{item}, ledger.fleetSeenKeys())
	if strings.Contains(out, "unread") {
		t.Fatalf("seen settlement still counted unread: %q", out)
	}
	if !strings.Contains(out, "fix the test") || !strings.Contains(out, "did it") {
		t.Fatalf("row lost the objective or the verdict: %q", out)
	}
}

// Activation is part of the key on purpose: re-running a job is a NEW
// settlement, and folding it into the old key would let a retry hide behind a
// verdict the human already read.
func TestFleetInboxReactivationIsNew(t *testing.T) {
	ledger := newActivityLedger()
	first := settledItem("worker", "fleet-abc", 1, "fix the test", "done", "first run")
	ledger.markFleetSeen(first.seenKey())
	if !ledger.fleetSeen(first.seenKey()) {
		t.Fatal("first activation not recorded")
	}
	second := settledItem("worker", "fleet-abc", 2, "fix the test", "done", "second run")
	if ledger.fleetSeen(second.seenKey()) {
		t.Fatal("a second activation was treated as already read")
	}
	// A different peer running the same job id is also distinct: job ids are
	// per-harness, not global.
	other := settledItem("niffler1", "fleet-abc", 1, "fix the test", "done", "elsewhere")
	if ledger.fleetSeen(other.seenKey()) {
		t.Fatal("another peer's settlement was treated as already read")
	}
}

// The first connect baselines: existing history must not arrive as a wall of
// unread work, and a failed baseline must not latch (it has to retry rather
// than silently presenting everything as new).
func TestFleetBaselineSemantics(t *testing.T) {
	ledger := newActivityLedger()
	if ledger.FleetBaseline {
		t.Fatal("a fresh ledger must not claim a baseline")
	}
	items := []fleetInboxItem{
		settledItem("worker", "a", 1, "one", "done", "s"),
		settledItem("worker", "b", 1, "two", "done", "s"),
		{JobID: "c", Peer: "worker", State: "running", Objective: "three"},
	}
	for _, item := range items {
		if item.settledOnly() {
			ledger.markFleetSeen(item.seenKey())
		}
	}
	ledger.FleetBaseline = true
	if got := len(ledger.fleetSeenKeys()); got != 2 {
		t.Fatalf("baseline adopted %d settlements, want 2 (running jobs are not news)", got)
	}
	// A settlement that arrives after the baseline is news.
	late := settledItem("worker", "d", 1, "four", "done", "s")
	if ledger.fleetSeen(late.seenKey()) {
		t.Fatal("a post-baseline settlement was adopted as seen")
	}
}

// Only a settled job is news; a queued or running job is something to look at
// when you choose.
func TestFleetInboxOnlySettledIsNews(t *testing.T) {
	for _, state := range []string{"queued", "submitting", "running", "failed", "uncertain"} {
		item := fleetInboxItem{JobID: "x", Peer: "worker", State: state}
		if item.settledOnly() {
			t.Errorf("state %q was treated as a settlement", state)
		}
	}
}

// The seen list is bounded: it lives in a private file capped at 8 MiB, and an
// unbounded slice would grow it forever.
func TestFleetSeenIsBounded(t *testing.T) {
	ledger := newActivityLedger()
	for i := 0; i < 2500; i++ {
		ledger.markFleetSeen(settledItem("worker", string(rune('a'+i%26))+"-"+strings.Repeat("x", i%5)+"-"+itoa(i), 1, "o", "done", "s").seenKey())
	}
	if len(ledger.FleetSeen) > 2000 {
		t.Fatalf("seen list grew to %d entries", len(ledger.FleetSeen))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// requestInto is the seam the inbox and the drill-down both cross, and it is
// exactly where the human-scope bug lived: the list was a human read, the
// report was a conversation read, and the client had no conversation. These
// tests pin the ARGUMENTS the client sends, which no unit test of either side
// could have caught.
func TestFleetRequestsCarryHumanScope(t *testing.T) {
	// fleet_task_inbox must not require a session (it never sends human, and
	// does not need to: it is a human read by construction on the harness side).
	inboxArgs := inboxRequestArgs(100, "")
	if _, ok := inboxArgs["human"]; ok {
		t.Fatalf("inbox does not need the human flag: %#v", inboxArgs)
	}
	if inboxArgs["limit"] != 100 {
		t.Fatalf("inbox limit = %#v", inboxArgs["limit"])
	}
	if _, ok := inboxArgs["after"]; ok {
		t.Fatalf("an empty cursor must not be sent: %#v", inboxArgs)
	}
	if got := inboxRequestArgs(100, "cur"); got["after"] != "cur" {
		t.Fatalf("cursor not forwarded: %#v", got)
	}

	// The report DOES need it, or the harness refuses with "session context
	// required" and the UI can never open what it lists.
	detailArgs := detailRequestArgs("fleet-abc")
	if detailArgs["human"] != true {
		t.Fatalf("report read must carry human scope: %#v", detailArgs)
	}
	if detailArgs["jobId"] != "fleet-abc" {
		t.Fatalf("jobId = %#v", detailArgs["jobId"])
	}
}

// The artifact is the reason the inbox is worth opening for a coding task, so
// its rendering has to say what changed — and must never imply success from a
// missing field.
func TestFleetArtifactRendering(t *testing.T) {
	// A produced commit: base, head, subject, diffstat, and the link.
	produced := &fleetArtifact{
		Version: 1, Observed: true, ProducedCommit: true, HeadMoved: true, OnBase: true,
	}
	produced.Base = &struct {
		Commit string `json:"commit"`
		Short  string `json:"short"`
	}{Short: "9cba035"}
	produced.Head = &struct {
		Commit  string `json:"commit"`
		Short   string `json:"short"`
		Subject string `json:"subject"`
		Branch  string `json:"branch"`
		URL     string `json:"url"`
	}{Short: "4a54dee", Subject: "fix the flaky test", URL: "https://github.com/x/y/commit/4a54dee"}
	produced.Diffstat = " a.go | 12 +++++\n 1 file changed"
	got := strings.Join(produced.render(), "\n")
	for _, want := range []string{"9cba035 → 4a54dee", "fix the flaky test", "a.go", "github.com"} {
		if !strings.Contains(got, want) {
			t.Fatalf("artifact render missing %q:\n%s", want, got)
		}
	}

	// History replaced: the one thing a reviewer must not have to infer.
	produced.OnBase = false
	if got := strings.Join(produced.render(), "\n"); !strings.Contains(got, "not on the base") {
		t.Fatalf("off-base commit not flagged:\n%s", got)
	}
	produced.OnBase = true

	// No commit, dirty tree: a real outcome, not a failure to hide.
	noCommit := &fleetArtifact{Version: 1, Observed: true, Dirty: true, Untracked: 2}
	got = strings.Join(noCommit.render(), "\n")
	if !strings.Contains(got, "no commit") || !strings.Contains(got, "dirty") {
		t.Fatalf("uncommitted change not described:\n%s", got)
	}

	// Unobservable: says so, and explicitly claims nothing to review.
	blind := &fleetArtifact{Version: 1, Observed: false}
	got = strings.Join(blind.render(), "\n")
	if !strings.Contains(got, "not observable") || !strings.Contains(got, "nothing to review") {
		t.Fatalf("unobservable artifact must not imply success:\n%s", got)
	}

	// No artifact at all renders nothing rather than an empty label.
	if out := (*fleetArtifact)(nil).render(); len(out) != 0 {
		t.Fatalf("nil artifact rendered %v", out)
	}
}
