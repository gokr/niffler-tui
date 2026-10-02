package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecallQueryAskFlag(t *testing.T) {
	cases := []struct {
		in      string
		wantQ   string
		wantAsk bool
	}{
		{"retry policy", "retry policy", false},
		{"retry policy ask", "retry policy", true},
		{"ask retry policy", "retry policy", true},
		{"ASK retry", "retry", true},
		{"ask", "", true}, // flag only — the query is empty and refused
		{"  spaced   words ", "spaced words", false},
		{"ask ask", "ask", true}, // one leading flag, one word kept
	}
	for _, c := range cases {
		q, ask := recallQuery(c.in)
		if q != c.wantQ || ask != c.wantAsk {
			t.Fatalf("recallQuery(%q) = (%q, %v), want (%q, %v)", c.in, q, ask, c.wantQ, c.wantAsk)
		}
	}
}

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

func TestRecallAskPayload(t *testing.T) {
	result := json.RawMessage(`{"count": 1, "via": "store.search",
		"matches": [{"id": "conv-a:000003", "role": "tool", "snippet": "the [hit]"}]}`)
	raw := recallAskPayload(result, "what hit")
	var payload struct {
		Text        string `json:"text"`
		UserMessage string `json:"userMessage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if !strings.Contains(payload.Text, "conv-a:000003") {
		t.Fatalf("text missing hits: %q", payload.Text)
	}
	for _, want := range []string{"Question: what hit", "conv-a:000003", "context_recall"} {
		if !strings.Contains(payload.UserMessage, want) {
			t.Fatalf("userMessage missing %q:\n%s", want, payload.UserMessage)
		}
	}
}
