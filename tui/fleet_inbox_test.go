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
