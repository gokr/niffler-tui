package main

import (
	"testing"
)

// A content search returns MESSAGE ids ("<conversationId>:<seq>"); the browser
// needs conversations. The mapping is where a body match either survives or
// silently turns into an empty result, so it is tested directly.
func TestContentHitToSessionID(tt *testing.T) {
	cases := []struct{ id, want string }{
		{"conv-abc123:17", "conv-abc123"},
		{"agent-1793-45:3", "agent-1793-45"},
		{"no-separator", ""}, // not a message id: skipped, not guessed
		{":9", ""},           // empty conversation id
		{"conv-x:", "conv-x"},
	}
	for _, c := range cases {
		id, ok := cutSessionID(c.id)
		if c.want == "" {
			if ok {
				tt.Errorf("cutSessionID(%q) = %q, want none", c.id, id)
			}
			continue
		}
		if !ok || id != c.want {
			tt.Errorf("cutSessionID(%q) = (%q, %v), want %q", c.id, id, ok, c.want)
		}
	}
}

// A session that matched on CONTENT must keep its snippet as the reason, and a
// session that matched on TITLE must carry no reason (the title already says
// why). Ordering is by the store's relevance ranking, not by id.
func TestMergeSearchResults(tt *testing.T) {
	titles := []sessionSummary{
		{ID: "conv-a", Title: "worktrees"},
		{ID: "conv-b", Title: "provider"},
	}
	content := []sessionSummary{
		{ID: "conv-c", Title: "unrelated", Match: "…the [provider] component…"},
		{ID: "conv-a", Title: "worktrees", Match: "ignored: already a title hit"},
		{ID: "conv-d", Title: "", Match: "…[provider] registry…"},
	}
	merged := mergeSearchHits(titles, content)
	if len(merged) != 4 {
		tt.Fatalf("merged %d, want 4: %+v", len(merged), merged)
	}
	if merged[0].ID != "conv-a" || merged[1].ID != "conv-b" {
		tt.Errorf("title hits must come first, got %q,%q", merged[0].ID, merged[1].ID)
	}
	if merged[0].Match != "" {
		tt.Errorf("a title hit keeps no reason, got %q", merged[0].Match)
	}
	if merged[2].ID != "conv-c" || merged[2].Match == "" {
		tt.Errorf("a content hit keeps its snippet, got %+v", merged[2])
	}
	if merged[3].ID != "conv-d" {
		tt.Errorf("a titleless content hit is kept, got %+v", merged[3])
	}
}

// The snippet the store returns is span-marked already; the browser must not
// re-wrap or mangle it, only trim.
func TestSnippetRendering(tt *testing.T) {
	s := sessionSummary{ID: "conv-x", Match: "  …a [provider] blip…  "}
	items := sessionSelectorItems(LocaleEN, "", []sessionSummary{s}, false)
	var found bool
	for _, it := range items {
		sel, ok := it.(selectorItem)
		if !ok || sel.kind != selectorSession {
			continue
		}
		found = true
		if sel.description != "…a [provider] blip…" {
			tt.Errorf("description = %q, want the trimmed snippet", sel.description)
		}
	}
	if !found {
		tt.Fatal("the session was not rendered")
	}
}

func TestSearchArgsCarryRanking(tt *testing.T) {
	args := messageSearchArgs("provider flaky", 40)
	if args["kind"] != "message" || args["rank"] != true || args["snippet"] != true {
		tt.Errorf("content search must rank and snippet: %+v", args)
	}
	if args["query"] != "provider flaky" {
		tt.Errorf("query not passed through: %+v", args)
	}
}
