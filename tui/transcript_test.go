package main

import "testing"

func TestClampLines(t *testing.T) {
	// Breaks at spaces, never mid-token.
	got := clampLines("5h rolling 1248.8/1250 | search 4/250 per hour", 20)
	if got != "5h rolling\n1248.8/1250 | search\n4/250 per hour" {
		t.Fatalf("clampLines = %q", got)
	}
	// Multi-line blocks keep their own line structure; short lines pass through.
	if got := clampLines("line one\nsecond line here", 9); got != "line one\nsecond\nline here" {
		t.Fatalf("clampLines multi-line = %q", got)
	}
	if got := clampLines("short", 80); got != "short" {
		t.Fatalf("clampLines short = %q", got)
	}
	// Unbreakable runs are hard-truncated — nothing may reach the last
	// terminal column, or the soft-wrap desyncs the renderer.
	if got := clampLines("0123456789", 5); got != "01234" {
		t.Fatalf("clampLines unbreakable = %q", got)
	}
	if got := clampLines("whatever", 0); got != "whatever" {
		t.Fatalf("clampLines width 0 = %q", got)
	}
}
