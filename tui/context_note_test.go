package main

import (
	"encoding/json"
	"testing"
)

// TestContextNoteForRendersEveryReason pins the rendering of the context
// event vocabulary core emits. The terminal note used to know only "trimmed"
// and a bare threshold warning, so a compaction refusal — the reason the
// ladder kept trimming — was dropped on the floor, and the warning invented
// "will trim soon" instead of naming the line core actually trims at.
func TestContextNoteForRendersEveryReason(t *testing.T) {
	cases := []struct {
		name  string
		event sessionEvent
		used  int
		limit int
		want  string
	}{
		{
			name:  "threshold warning names the trim percentage core printed",
			event: sessionEvent{Reason: "warn:threshold", TrimAt: 90000, UsedTokens: 50000, Context: 100000, Warning: json.RawMessage("true")},
			used:  50000, limit: 100000,
			want: "context at 50% — will compact/trim at 90%",
		},
		{
			name:  "legacy threshold warning without trimAt still renders",
			event: sessionEvent{Reason: "warn:threshold", Warning: json.RawMessage("true")},
			used:  50000, limit: 100000,
			want:  "context at 50% — will compact/trim soon",
		},
		{
			name:  "committed compaction reports before and after",
			event: sessionEvent{Reason: "reset:compact", BeforeTokens: 42000, AfterTokens: 3100, Generation: 2},
			want:  "context compacted — ~42000 → ~3100 tokens (generation 2)",
		},
		{
			name:  "lossless prune reports the bytes it reclaimed",
			event: sessionEvent{Reason: "reset:prune", BytesSaved: 12345},
			want:  "tool results pruned — 12345 bytes reclaimed",
		},
		{
			name:  "lossy trim keeps its existing wording",
			event: sessionEvent{Reason: "reset:trim", Trimmed: 5},
			want:  "context trimmed — dropped 5 earlier messages",
		},
		{
			name:  "runner-side decline is visible with its reason",
			event: sessionEvent{Reason: "compact:declined", Detail: "no permitted cut exists yet"},
			want:  "compaction declined — no permitted cut exists yet",
		},
		{
			name:  "unavailable compactor is visible",
			event: sessionEvent{Reason: "compact:unavailable", Detail: "no compaction component available"},
			want:  "compaction unavailable — no compaction component available",
		},
		{
			name:  "invalid candidate is visible",
			event: sessionEvent{Reason: "compact:invalid", Detail: "checkpoint does not strictly reduce the covered span"},
			want:  "compaction rejected — checkpoint does not strictly reduce the covered span",
		},
		{
			name:  "stale boundary is visible",
			event: sessionEvent{Reason: "compact:stale", Detail: "projection generation changed before commit"},
			want:  "compaction stale — projection generation changed before commit",
		},
		{
			name:  "dispatch failure falls back to its error field",
			event: sessionEvent{Reason: "compact:failed", Error: "i/o timeout"},
			want:  "compaction failed — i/o timeout",
		},
		{
			name:  "reason without detail still names the degradation",
			event: sessionEvent{Reason: "compact:failed"},
			want:  "compaction failed",
		},
		{
			name:  "unknown reason keeps the previous note",
			event: sessionEvent{Reason: "something:else"},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextNoteFor(tc.event, tc.used, tc.limit); got != tc.want {
				t.Fatalf("contextNoteFor = %q, want %q", got, tc.want)
			}
		})
	}
}
