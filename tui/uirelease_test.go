// Leaving the bus cleanly: the release of this client's ui registration is
// best effort but retried, and a total failure is reported rather than
// swallowed — a lost release leaves the conversation looking owned until the
// 20s lease runs out, which is long enough for a relaunch to be refused it.
package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestReleaseUIWithRetriesUntilAcknowledged(t *testing.T) {
	var calls []map[string]any
	req := func(component, tool string, args any, timeout time.Duration) (json.RawMessage, error) {
		if component != "core" || tool != "ui" {
			t.Fatalf("release went to %s/%s", component, tool)
		}
		if timeout <= 0 {
			t.Fatalf("release timeout = %v", timeout)
		}
		a, ok := args.(map[string]any)
		if !ok {
			t.Fatalf("release args = %#v", args)
		}
		calls = append(calls, a)
		if len(calls) < 3 {
			return nil, errors.New("bus busy")
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	if err := releaseUIWith(req, "tui-aabbccdd1122", releaseAttempts); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("attempts = %d, want 3", len(calls))
	}
	for _, a := range calls {
		if a["op"] != "release" || a["ui"] != "tui-aabbccdd1122" {
			t.Fatalf("release args = %#v", a)
		}
	}
}

func TestReleaseUIWithReportsPersistentFailure(t *testing.T) {
	calls := 0
	req := func(component, tool string, args any, timeout time.Duration) (json.RawMessage, error) {
		calls++
		return nil, errors.New("no responders")
	}
	err := releaseUIWith(req, "tui-aabbccdd1122", releaseAttempts)
	if err == nil {
		t.Fatal("persistent failure reported success")
	}
	if calls != releaseAttempts {
		t.Fatalf("calls = %d, want %d", calls, releaseAttempts)
	}
}

func TestReleaseUIWithNothingToRelease(t *testing.T) {
	calls := 0
	req := func(component, tool string, args any, timeout time.Duration) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"ok":true}`), nil
	}
	for _, tc := range []struct {
		name     string
		req      requester
		uiID     string
		attempts int
	}{
		{"no requester", nil, "tui-aabbccdd1122", 1},
		{"no identity", req, "", 1},
		{"no attempts", req, "tui-aabbccdd1122", 0},
	} {
		if err := releaseUIWith(tc.req, tc.uiID, tc.attempts); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if calls != 0 {
		t.Fatalf("nothing-to-release made %d calls", calls)
	}
}
