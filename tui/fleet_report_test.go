package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFleetReportPagesPreserveProse(t *testing.T) {
	calls := 0
	cmd := fleetTaskDetailReadCmd("job", func(args map[string]any, dest any) error {
		if args["human"] != true || args["jobId"] != "job" {
			t.Fatalf("lost authorization context: %v", args)
		}
		calls++
		if calls == 1 {
			return json.Unmarshal([]byte(`{"state":"settled","report":"Fixed it.\n\n","hasMore":true,"nextOffset":11}`), dest)
		}
		if args["offset"] != 11 {
			t.Fatalf("wrong cursor: %v", args)
		}
		return json.Unmarshal([]byte(`{"report":"PR https://github.com/example/repo/pull/24","hasMore":false}`), dest)
	})
	got := cmd().(fleetTaskDetailMsg)
	if got.Err != nil || calls != 2 || !strings.Contains(got.Reply, "Fixed it.\n\nPR https://") {
		t.Fatalf("report: %+v (%d calls)", got, calls)
	}
}

func TestFleetReportRejectsStalledCursor(t *testing.T) {
	cmd := fleetTaskDetailReadCmd("job", func(_ map[string]any, dest any) error {
		return json.Unmarshal([]byte(`{"report":"part","hasMore":true,"nextOffset":0}`), dest)
	})
	if got := cmd().(fleetTaskDetailMsg); got.Err == nil {
		t.Fatal("stalled cursor accepted")
	}
}

func TestFleetReportLegacySummary(t *testing.T) {
	cmd := fleetTaskDetailReadCmd("job", func(_ map[string]any, dest any) error {
		return json.Unmarshal([]byte(`{"summary":"Old report","detail":"Full transcript on worker"}`), dest)
	})
	got := cmd().(fleetTaskDetailMsg)
	if got.Err != nil || !strings.Contains(got.Reply, "Old report") || !strings.Contains(got.Reply, "Full transcript on worker") {
		t.Fatalf("legacy report: %+v", got)
	}
}
