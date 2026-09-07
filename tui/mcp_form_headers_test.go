package main

import (
	"strings"
	"testing"
)

func TestMcpFormHeadersJSON(t *testing.T) {
	form := newMcpForm(80, LocaleEN)
	form.inputs[mcpFieldName].SetValue("srv")
	form.inputs[mcpFieldCommand].SetValue("npx")

	// A valid headers object round-trips into the form values.
	form.inputs[mcpFieldHeaders].SetValue(`{"Authorization":"Bearer ${CHETTER_MCP_TOKEN}"}`)
	values, err := form.values()
	if err != nil {
		t.Fatalf("valid headers rejected: %v", err)
	}
	if values.HeadersJSON != `{"Authorization":"Bearer ${CHETTER_MCP_TOKEN}"}` {
		t.Fatalf("HeadersJSON = %q", values.HeadersJSON)
	}

	// Malformed JSON is rejected with the headers message.
	form.inputs[mcpFieldHeaders].SetValue("{nope")
	if _, err := form.values(); err == nil || !strings.Contains(err.Error(), "headers") {
		t.Fatalf("malformed headers err = %v; want the headers message", err)
	}

	// Non-string values are rejected: the wire type is map[string]string.
	form.inputs[mcpFieldHeaders].SetValue(`{"A":1}`)
	if _, err := form.values(); err == nil || !strings.Contains(err.Error(), "headers") {
		t.Fatalf("non-string headers err = %v; want the headers message", err)
	}
}

func TestNewEditMcpFormHeadersBlank(t *testing.T) {
	// Header values are redacted server-side, so the edit form starts
	// blank; a blank field keeps the stored headers on save.
	form := newEditMcpForm(mcpServerSummary{
		Name: "chetter", Type: "http", URL: "https://chetter.example/mcp",
	}, 80, LocaleEN)
	if got := form.inputs[mcpFieldHeaders].Value(); got != "" {
		t.Fatalf("edit headers prefill = %q; want blank", got)
	}
	if _, err := form.values(); err != nil {
		t.Fatalf("blank headers on edit must validate: %v", err)
	}
}
