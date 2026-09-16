package main

import "testing"

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
	if got := agentsBadgeText(LocaleEN, agents, 1000); got != "agent 1 (50s)" {
		t.Fatalf("agent badge = %q", got)
	}
	roster := bgRoster(processes, agents)
	if len(roster) != 3 || roster[0].id != "p2" || roster[1].id != "p10" || roster[2].kind != "agent" {
		t.Fatalf("unexpected worker roster: %#v", roster)
	}
}

func TestWorkerBadgeDegradesWithoutTimestamp(t *testing.T) {
	if got := processesBadgeText(LocaleEN, []processSummary{{ID: "p1", Status: "running"}}, 1000); got != "bg 1" {
		t.Fatalf("timestamp-less process badge = %q", got)
	}
	if got := agentsBadgeText(LocaleEN, []agentSummary{{Status: "running"}}, 1000); got != "agent 1" {
		t.Fatalf("timestamp-less agent badge = %q", got)
	}
}
