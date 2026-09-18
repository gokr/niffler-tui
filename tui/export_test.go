package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// prettyRequest is what /export writes to a file (and renders in the
// transcript). Indentation must be the ONLY change: /export exists to
// reproduce a provider request, so key order, number spelling and string
// escapes have to survive. A decode/re-encode would sort the keys and print
// 1e3 as 1000 — exactly the drift this test forbids.
func TestPrettyRequestIndentsOnly(t *testing.T) {
	raw := json.RawMessage(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"max_tokens":1e3,"stream":true}`)
	got, err := prettyRequest(raw)
	if err != nil {
		t.Fatalf("prettyRequest: %v", err)
	}
	want := "{\n" +
		"  \"model\": \"deepseek-chat\",\n" +
		"  \"messages\": [\n" +
		"    {\n" +
		"      \"role\": \"user\",\n" +
		"      \"content\": \"hi\"\n" +
		"    }\n" +
		"  ],\n" +
		"  \"max_tokens\": 1e3,\n" +
		"  \"stream\": true\n" +
		"}"
	if string(got) != want {
		t.Fatalf("pretty request mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The indented document must still be the same JSON value.
	var a, b any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if err := json.Unmarshal(got, &b); err != nil {
		t.Fatalf("pretty output is not JSON: %v", err)
	}
	if !jsonEqual(t, a, b) {
		t.Fatalf("pretty output changed the value:\n raw: %v\n got: %v", a, b)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(ja) == string(jb)
}

// An export that is not JSON at all is reported as an error instead of being
// written to the user's file.
func TestPrettyRequestRejectsInvalidJSON(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"a":`), // truncated
		json.RawMessage(nil),     // empty
		json.RawMessage("oops"),  // not JSON
	} {
		if _, err := prettyRequest(raw); err == nil {
			t.Fatalf("prettyRequest(%q): expected an error", string(raw))
		}
	}
}

// textField decodes the "text" field exportInline produces, so the tests assert
// what the transcript actually renders.
func textField(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var probe struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("exportInline output is not a text result: %v (%s)", err, raw)
	}
	return probe.Text
}

// The transcript renders a result's "text" field verbatim, so /export has to
// ride that field. Handing formatSlashResult the bare document would send it
// through the decode/re-encode fallback, which sorts keys and prints 1e3 as
// 1000 — the drift prettyRequest exists to prevent.
func TestExportInlineSurvivesTheResultFormatter(t *testing.T) {
	raw := json.RawMessage(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"max_tokens":1e3}`)
	pretty, err := prettyRequest(raw)
	if err != nil {
		t.Fatalf("prettyRequest: %v", err)
	}
	if got := formatSlashResult(exportInline(pretty)); got != string(pretty) {
		t.Fatalf("the transcript did not get the request verbatim:\n got: %s\nwant: %s", got, pretty)
	}
	// The control: the same document without the wrapper is re-encoded, which
	// is why the wrapper is not decoration.
	if got := formatSlashResult(pretty); got == string(pretty) {
		t.Fatalf("fallback rendering is now byte-exact — exportInline can be simplified")
	}
}

// The transcript window is a byte budget; a request carrying multi-byte text
// would be cut mid-rune by a plain slice, leaving invalid UTF-8 on screen.
func TestExportInlineCutsOnARuneBoundary(t *testing.T) {
	content := strings.Repeat("界", exportInlineLimit)
	raw := json.RawMessage(`{"model":"deepseek-chat","messages":[{"role":"user","content":"` + content + `"}]}`)
	pretty, err := prettyRequest(raw)
	if err != nil {
		t.Fatalf("prettyRequest: %v", err)
	}
	text := textField(t, exportInline(pretty))
	if !strings.HasPrefix(text, string(pretty)[:100]) {
		t.Fatalf("the kept window is not the head of the request: %q", text[:100])
	}
	if len(text) >= len(pretty) {
		t.Fatalf("expected the document to be capped (%d bytes kept)", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatalf("the window ends inside a multi-byte rune")
	}
	if strings.ContainsRune(text, utf8.RuneError) {
		t.Fatalf("the window contains a replacement character")
	}
	if !strings.HasSuffix(text, "pass a path to /export for the full request") {
		t.Fatalf("the truncation is not explained: %q", text[len(text)-60:])
	}
}

// A request that fits the window is rendered untouched — no truncation marker,
// no re-indentation.
func TestExportInlineLeavesShortRequestsAlone(t *testing.T) {
	pretty := json.RawMessage("{\n  \"model\": \"m\",\n  \"max_tokens\": 1e3\n}")
	text := textField(t, exportInline(pretty))
	if text != string(pretty) {
		t.Fatalf("short request changed:\n got: %q\nwant: %q", text, string(pretty))
	}
}
