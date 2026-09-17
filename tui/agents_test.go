package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHumanAge(t *testing.T) {
	cases := map[float64]string{0: "", -1: "", 12: "12s", 60: "1m", 3599: "59m", 3600: "1h0m", 86399: "23h59m", 86400: "1d0h"}
	for elapsed, want := range cases {
		if got := humanAge(elapsed); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", elapsed, got, want)
		}
	}
}

func TestWorkerBadgesAndRoster(t *testing.T) {
	processes := []processSummary{
		{ID: "p10", Status: "running", StartedAt: 900},
		{ID: "p2", Status: "running", StartedAt: 800},
		{ID: "p3", Status: "exited(code 0)"},
	}
	agents := []agentSummary{
		{SessionID: "agent-done", Status: "idle"},
		{SessionID: "agent-live", Status: "running", StartedAt: 950},
	}
	if got := processesBadgeText(LocaleEN, processes, 1000); got != "bg 2 (1m)" {
		t.Fatalf("process badge = %q", got)
	}
	if got := agentsBadgeText(LocaleEN, agents, nil, 1000); got != "agent 1 (50s)" {
		t.Fatalf("agent badge = %q", got)
	}
	roster := bgRoster(processes, agents, nil, 1000)
	if len(roster) != 3 || roster[0].id != "p2" || roster[1].id != "p10" || roster[2].kind != "agent" {
		t.Fatalf("unexpected worker roster: %#v", roster)
	}
}

func TestAgentSettlementText(t *testing.T) {
	if got := agentSettlementText(agentSummary{LastStatus: "failed", Error: "token budget exhausted"}); got != "subagent failed: token budget exhausted" {
		t.Fatalf("failed settlement = %q", got)
	}
	if got := agentSettlementText(agentSummary{LastStatus: "done"}); got != "" {
		t.Fatalf("done settlement should be quiet: %q", got)
	}
}

func TestWorkerBadgeDegradesWithoutTimestamp(t *testing.T) {
	if got := processesBadgeText(LocaleEN, []processSummary{{ID: "p1", Status: "running"}}, 1000); got != "bg 1" {
		t.Fatalf("timestamp-less process badge = %q", got)
	}
	if got := agentsBadgeText(LocaleEN, []agentSummary{{Status: "running"}}, nil, 1000); got != "agent 1" {
		t.Fatalf("timestamp-less agent badge = %q", got)
	}
}

// TestAgentBadgeCountsFreshActivity closes the bug where the badge showed one
// working subagent while four ran: the roster is a snapshot refreshed on
// lifecycle events and a slow tick, so a child whose frames are fresh counts
// even while its roster status still reads idle — and a settled child never
// counts, however recently it spoke.
func TestAgentBadgeCountsFreshActivity(t *testing.T) {
	agents := []agentSummary{
		{SessionID: "agent-lagging", Status: "idle", StartedAt: 900},
		{SessionID: "agent-settled", Status: "idle", LastStatus: "done", StartedAt: 950},
	}
	act := map[string]childActivity{
		"agent-lagging": {LastSeen: 990, Phase: "tool", Now: "bash go build ./..."},
		"agent-settled": {LastSeen: 999, Phase: "responding"},
	}
	if got := runningAgents(agents, act, 1000); got != 1 {
		t.Fatalf("stale-roster count = %d, want 1 (a settled child must not count)", got)
	}
	if got := agentsBadgeText(LocaleEN, agents, act, 1000); got != "agent 1 (1m)" {
		t.Fatalf("badge = %q", got)
	}
	// Activity ages out: an un-settled child with nothing recent is not working.
	late := 990 + childActivityTTL + 1
	if got := runningAgents(agents, act, late); got != 0 {
		t.Fatalf("expired activity still counted: %d", got)
	}
	// The cycle admits the same child, so ctrl+o can reach what the badge counts.
	roster := bgRoster(nil, agents, act, 1000)
	if len(roster) != 1 || roster[0].id != "agent-lagging" {
		t.Fatalf("cycle roster = %#v", roster)
	}
}

// TestLongestAgentAgeUsesEarliestChild pins the badge's age claim: it is how
// long this conversation has had a subagent working, i.e. the earliest start
// among live children — not the newest one.
func TestLongestAgentAgeUsesEarliestChild(t *testing.T) {
	agents := []agentSummary{
		{SessionID: "old", Status: "running", StartedAt: 100},
		{SessionID: "new", Status: "running", StartedAt: 990},
	}
	if got := longestAgentAge(agents, nil, 1000); got != "15m" {
		t.Fatalf("age = %q, want the longest-running child (15m)", got)
	}
}

// TestChildActivityFoldsSessionFrames covers the feed that makes the card
// live: tool frames name the running tool, token frames the phase and the last
// output line, and a done frame clears the tool. Only known children are
// tracked, so another conversation's subagents cannot leak in.
func TestChildActivityFoldsSessionFrames(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.agents = []agentSummary{{SessionID: "agent-1", Status: "running"}}

	m.noteChildActivity("token", sessionEvent{SessionID: "agent-stranger", Content: "hi"})
	if len(m.childAct) != 0 {
		t.Fatalf("a stranger's frames were tracked: %#v", m.childAct)
	}

	m.noteChildActivity("toolcall", sessionEvent{SessionID: "agent-1", Phase: "start",
		Tool: "bash", Args: json.RawMessage(`{"command":"go build ./..."}`)})
	if got := m.childAct["agent-1"]; got.Phase != "tool" || !strings.Contains(got.Now, "bash") {
		t.Fatalf("tool activity = %#v", got)
	}

	m.noteChildActivity("token", sessionEvent{SessionID: "agent-1", Content: "line one\nline two"})
	if got := m.childAct["agent-1"]; got.Phase != "responding" || got.Output != "line two" {
		t.Fatalf("token activity = %#v", got)
	}

	m.noteChildActivity("toolcall", sessionEvent{SessionID: "agent-1", Phase: "done", Tool: "bash"})
	if got := m.childAct["agent-1"]; got.Now != "" || got.Tool != "" {
		t.Fatalf("finished tool not cleared: %#v", got)
	}
}

// TestBgPeekShowsWorkerListAndLiveCard pins the two UX fixes: the cycle lists
// every worker (so the count is visible without rotating) and a subagent card
// says what it is doing now, not just the task it was given.
func TestBgPeekShowsWorkerListAndLiveCard(t *testing.T) {
	ctx := bgPeekContext{
		Roster: []bgTarget{
			{kind: "agent", id: "agent-1", status: "running", jobID: "job-7",
				startedAt: 940, task: "survey the docs\nsecond line"},
			{kind: "process", id: "p2", label: "sleep 100", status: "running", startedAt: 900},
		},
		Index: 0, Now: 1000,
		Activity: map[string]childActivity{
			"agent-1": {LastSeen: 999, Phase: "tool",
				Now: "bash go build ./...", Output: "ok llm 0.02s"},
		},
	}
	view := ansi.Strip(bgPeekView(LocaleEN, ctx.Roster[0], "", 80, 24, false, ctx))
	for _, want := range []string{
		"workers: 2", "agents 1", "processes 1",
		"▸ agent", "p2", "sleep 100",
		"now: bash go build", "out: ok llm 0.02s", "job: job-7",
		"session: agent-1", "task: survey the docs",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("peek view missing %q:\n%s", want, view)
		}
	}
	// A card without any frames yet says so instead of rendering nothing.
	quiet := ansi.Strip(bgPeekView(LocaleEN, bgTarget{kind: "agent", id: "agent-9", status: "idle"},
		"", 80, 24, false, bgPeekContext{Roster: []bgTarget{{kind: "agent", id: "agent-9"}}, Now: 1000}))
	if !strings.Contains(quiet, "no activity yet") {
		t.Fatalf("quiet card = %q", quiet)
	}
}
