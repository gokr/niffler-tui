package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecallHitsText(t *testing.T) {
	raw := json.RawMessage(`{
		"count": 2, "via": "store.search", "ranked": true, "capped": true,
		"matches": [
			{"id": "conv-a:000003", "role": "tool", "seq": 3, "snippet": "the [zebrafish] migration"},
			{"id": "conv-a:000007", "role": "user", "seq": 7, "snippet": "line one\nline two"}
		]
	}`)
	out := recallHitsText(raw)
	for _, want := range []string{
		"2 hit(s) via store.search", "(capped — narrow the query)",
		"conv-a:000003 tool  the [zebrafish] migration",
		"conv-a:000007 user  line one line two", // newlines flattened
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("recallHitsText missing %q:\n%s", want, out)
		}
	}

	// Not the expected shape: compact JSON fallback, never a panic.
	if got := recallHitsText(json.RawMessage(`"just a string"`)); !strings.Contains(got, "just a string") {
		t.Fatalf("fallback = %q", got)
	}
}
