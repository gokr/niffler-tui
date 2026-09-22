package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolSubjectNamesTheTarget(t *testing.T) {
	cases := []struct {
		what string
		call toolCall
		want string
	}{
		{
			what: "discover by query",
			call: toolCall{name: "discover", args: json.RawMessage(`{"query":"mcp"}`)},
			want: "mcp",
		},
		{
			what: "discover by component",
			call: toolCall{name: "discover", args: json.RawMessage(`{"component":"mcp"}`)},
			want: "mcp",
		},
		{
			what: "discover by tool list",
			call: toolCall{name: "discover", args: json.RawMessage(`{"tools":["chetter_list_tasks","chetter_task_status"]}`)},
			want: "chetter_list_tasks,chetter_task_status",
		},
		{
			what: "discover without a subject",
			call: toolCall{name: "discover", args: json.RawMessage(`{}`)},
			want: "",
		},
		{
			what: "invoke names the tool",
			call: toolCall{name: "invoke", args: json.RawMessage(`{"tool":"chetter_list_tasks","arguments":{}}`)},
			want: "chetter_list_tasks",
		},
		{
			what: "self-describing tools carry no subject",
			call: toolCall{name: "bash", args: json.RawMessage(`{"command":"ls"}`)},
			want: "",
		},
	}
	for _, tc := range cases {
		if got := toolSubject(&tc.call); got != tc.want {
			t.Errorf("%s: toolSubject = %q, want %q", tc.what, got, tc.want)
		}
	}
}

func TestCallLabelAppendsSubject(t *testing.T) {
	named := toolCall{name: "discover", args: json.RawMessage(`{"query":"mcp"}`)}
	if got := callLabel(&named); got != "discover(mcp)" {
		t.Errorf("callLabel = %q, want %q", got, "discover(mcp)")
	}
	plain := toolCall{name: "bash", args: json.RawMessage(`{"command":"ls"}`)}
	if got := callLabel(&plain); got != "bash" {
		t.Errorf("callLabel = %q, want %q", got, "bash")
	}
}

// The brief card is the level where nothing else identifies the call, which is
// exactly what the user sees while work streams past.
func TestBriefCardHeadNamesTheTarget(t *testing.T) {
	m := newTestModel()
	run := &toolRun{collapsed: true, calls: []toolCall{
		{name: "discover", args: json.RawMessage(`{"query":"mcp"}`)},
	}}
	head := m.renderToolRunLines(run, detailBrief)
	if !strings.Contains(head, "discover(mcp)") {
		t.Fatalf("brief single-call head does not name the target: %q", head)
	}

	// Multi-call cards carry the same names in their chips.
	multi := &toolRun{collapsed: true, calls: []toolCall{
		{name: "invoke", args: json.RawMessage(`{"tool":"chetter_list_tasks"}`)},
		{name: "read", args: json.RawMessage(`{"path":"docs/WIRE.md"}`)},
	}}
	head = m.renderToolRunLines(multi, detailBrief)
	if !strings.Contains(head, "invoke(chetter_list_tasks)") {
		t.Fatalf("brief chips do not name the target: %q", head)
	}
	if !strings.Contains(head, "read") {
		t.Fatalf("brief chips dropped a self-describing tool: %q", head)
	}
}
