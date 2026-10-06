package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func infoTestMsg(role, content string, extra string) json.RawMessage {
	raw := `{"role":"` + role + `","content":` + jsonOrNull(content)
	if extra != "" {
		raw += "," + extra
	}
	return json.RawMessage(raw + "}")
}

func jsonOrNull(s string) string {
	if s == "" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func TestComputeInfoStatsRolesAndResults(t *testing.T) {
	toolCall := `"tool_calls":[{"type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"make\"}"}}],` +
		`"usage":{"prompt_tokens":100,"completion_tokens":20}`
	msgs := []json.RawMessage{
		infoTestMsg("user", "hello", `"conversationId":"c1"`),
		infoTestMsg("assistant", "", toolCall),
		// The tool result carries the tool name and a content of known size.
		infoTestMsg("tool", "line one\nline two", `"name":"bash","tool_call_id":"t1","durationMs":5`),
		infoTestMsg("tool", "x", `"name":"edit","tool_call_id":"t2"`),
	}
	st := computeInfoStats("c1", nil, nil, msgs)

	if st.messages != 4 || st.roles["user"] != 1 || st.roles["assistant"] != 1 || st.roles["tool"] != 2 {
		t.Fatalf("role fold wrong: %d %v", st.messages, st.roles)
	}
	// historyChars counts the raw JSON bytes of every content field.
	if st.historyChars != int64(len(`"hello"`)+len(`null`)+len(`"line one\nline two"`)+len(`"x"`)) {
		t.Fatalf("historyChars = %d", st.historyChars)
	}
	if got := st.results["bash"]; got == nil || got.calls != 1 || got.largest != len(`"line one\nline two"`) {
		t.Fatalf("bash result stats wrong: %+v", st.results)
	}
	if st.resultCalls != 2 || len(st.resultLens) != 2 {
		t.Fatalf("resultCalls = %d, lens = %v", st.resultCalls, st.resultLens)
	}
	if st.callCount != 1 || st.calls["bash"] == nil || st.calls["bash"].bytes != len(`{"cmd":"make"}`) {
		t.Fatalf("call-site stats wrong: %+v", st.calls)
	}
	if st.responses != 1 || st.promptTokens != 100 || st.completionTokens != 20 {
		t.Fatalf("usage fold wrong: %d %d %d", st.responses, st.promptTokens, st.completionTokens)
	}
	if got := median(st.resultLens); got != (len(`"line one\nline two"`)+len(`"x"`))/2 {
		t.Fatalf("median = %d", got)
	}
	// The largest-results list holds every entry while under the cap.
	if len(st.topResults) != 2 || st.topResults[0].tool != "bash" || st.topResults[1].tool != "edit" {
		t.Fatalf("top results = %+v", st.topResults)
	}
	if st.topResults[0].preview != "line one" {
		t.Fatalf("preview should be the first line, cut: %q", st.topResults[0].preview)
	}
}

func TestComputeInfoStatsDiscoveryBreakdown(t *testing.T) {
	discoverCall := `"tool_calls":[{"type":"function","function":{"name":"discover",` +
		`"arguments":"{\"component\":\"fabric\"}"}}]`
	invokeCall := `"tool_calls":[{"type":"function","function":{"name":"invoke",` +
		`"arguments":"{\"tool\":\"fabric\"}"}}]`
	msgs := []json.RawMessage{
		infoTestMsg("assistant", "", discoverCall),
		infoTestMsg("assistant", "", invokeCall),
		// Malformed arguments are skipped, not fatal.
		infoTestMsg("assistant", "", `"tool_calls":[{"type":"function","function":{"name":"discover","arguments":"not-json"}}]`),
	}
	st := computeInfoStats("c1", nil, nil, msgs)
	if st.discovered["fabric"] != 1 {
		t.Fatalf("discover breakdown wrong: %v", st.discovered)
	}
	if st.invoked["fabric"] != 1 {
		t.Fatalf("invoke breakdown wrong: %v", st.invoked)
	}
}

func TestComputeInfoStatsTopResultsBound(t *testing.T) {
	var msgs []json.RawMessage
	sizes := []int{10, 500, 9000, 42, 70000, 1200} // top 3: 70000, 9000, 1200
	for i, s := range sizes {
		msgs = append(msgs, infoTestMsg("tool", strings.Repeat("a", s), `"name":"t`+string(rune('a'+i))+`"`))
	}
	st := computeInfoStats("c1", nil, nil, msgs)
	if len(st.topResults) != maxTopResults {
		t.Fatalf("top results = %d entries, want %d", len(st.topResults), maxTopResults)
	}
	if st.topResults[0].bytes < st.topResults[1].bytes || st.topResults[1].bytes < st.topResults[2].bytes {
		t.Fatalf("top results not biggest first: %+v", st.topResults)
	}
	if st.topResults[0].bytes != sizes[4]+2 { // +2 for the JSON quotes
		t.Fatalf("largest result size = %d, want %d", st.topResults[0].bytes, sizes[4]+2)
	}
}

func TestComputeInfoStatsHeaderAndLineage(t *testing.T) {
	header := json.RawMessage(`{"title":"the title","model":"m1","createdAt":1791314931.2}`)
	st := computeInfoStats("c1", header, nil, nil)
	if !strings.Contains(st.desc, "the title") || !strings.Contains(st.desc, "m1") {
		t.Fatalf("header description wrong: %q", st.desc)
	}
	lineage := json.RawMessage(`{"parent":"conv-parent","closed":false}`)
	st = computeInfoStats("c1", nil, lineage, nil)
	if !strings.Contains(st.desc, "subagent of conv-parent") {
		t.Fatalf("lineage description wrong: %q", st.desc)
	}
	st = computeInfoStats("c1", nil, nil, nil)
	if st.desc != "no conversation header in the store" {
		t.Fatalf("bare description wrong: %q", st.desc)
	}
}

func TestRenderInfoStatsLayout(t *testing.T) {
	msgs := []json.RawMessage{
		infoTestMsg("user", "run the build", ""),
		infoTestMsg("assistant", "", `"tool_calls":[{"type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"make\"}"}}],"usage":{"prompt_tokens":9000,"completion_tokens":1200}`),
		infoTestMsg("tool", strings.Repeat("x", 120), `"name":"bash","tool_call_id":"t1"`),
	}
	st := computeInfoStats("c1", json.RawMessage(`{"title":"stats","model":"m1"}`), nil, msgs)
	out := renderInfoStats(st)
	for _, want := range []string{
		`conversation c1`,
		"messages 3 (assistant 1 · tool 1 · user 1)",
		"tokens: ↑ 9000 prompt · ↓ 1200 completion across 1 responses",
		"tool results 1 · 122 total · median 122 · largest 122",
		"assistant tool calls 1",
		"largest tool results:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	// An empty conversation renders without the size sections.
	if empty := renderInfoStats(computeInfoStats("c2", nil, nil, nil)); strings.Contains(empty, "tool results") {
		t.Fatalf("empty conversation should not have a results section:\n%s", empty)
	}
}

func TestCompactCount(t *testing.T) {
	cases := map[int64]string{
		0: "0", 999: "999", 9999: "9999",
		10_000: "10.0k", 123_456: "123.5k", 1_200_000: "1.2M",
	}
	for in, want := range cases {
		if got := compactCount(in); got != want {
			t.Fatalf("compactCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("first line\nsecond line", 48); got != "first line" {
		t.Fatalf("multiline preview = %q", got)
	}
	if got := truncateRunes("第二三四五六", 3); got != "第二三…" {
		t.Fatalf("rune cut = %q", got)
	}
}

func TestLocalInfoDefaultsToCurrentSession(t *testing.T) {
	m := snippetsTestModel(t)
	updated, cmd := m.executeLocalCommand("/info")
	if cmd == nil {
		t.Fatal("/info produced no command")
	}
	// The dispatch echoed the invocation (no id → the current conversation).
	_ = updated
}

func TestApplyInfoStatsMsgGuardAndRender(t *testing.T) {
	m := snippetsTestModel(t)
	st := computeInfoStats("s1", nil, nil, []json.RawMessage{infoTestMsg("user", "hi", "")})

	// A result for another conversation is dropped entirely.
	before := len(m.blocks)
	m.applyInfoStatsMsg(infoStatsMsg{stats: st, session: "s2"})
	if len(m.blocks) != before {
		t.Fatal("a stale /info result changed this conversation")
	}

	m.applyInfoStatsMsg(infoStatsMsg{stats: st, session: "s1"})
	last := m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "conversation s1") {
		t.Fatalf("report not rendered: %q", last)
	}

	m.applyInfoStatsMsg(infoStatsMsg{session: "s1", err: errTestStore})
	last = m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "/info:") {
		t.Fatalf("error not reported: %q", last)
	}
}

var errTestStore = &testStoreError{}

type testStoreError struct{}

func (*testStoreError) Error() string { return "store unreachable" }
