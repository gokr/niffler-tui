package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func deployTestCommand() slashCommand {
	return slashCommand{
		Name:        "deploy",
		Description: "deploy the current branch",
		Component:   "deployer",
		Tool:        "deploy_run",
		Params: []slashParam{
			{Name: "env", Kind: "enum", Description: "target environment",
				Source: &slashSource{Tool: "deploy.envs", Args: map[string]any{}}, Values: []string{"dev", "staging", "prod"}},
			{Name: "force", Kind: "bool", Default: json.RawMessage(`false`)},
			{Name: "count", Kind: "int"},
		},
	}
}

func TestParseSlashArgs(t *testing.T) {
	cmd := deployTestCommand()

	tests := []struct {
		name    string
		rawArgs string
		want    map[string]any
		wantErr string
	}{
		{name: "positional", rawArgs: "staging", want: map[string]any{"env": "staging", "force": false}},
		{name: "bare bool flag", rawArgs: "staging force", want: map[string]any{"env": "staging", "force": true}},
		{name: "named", rawArgs: "force=on env=prod", want: map[string]any{"env": "prod", "force": true}},
		{name: "int positional", rawArgs: "dev force count=3", want: map[string]any{"env": "dev", "force": true, "count": 3}},
		{name: "bool off", rawArgs: "dev force=off", want: map[string]any{"env": "dev", "force": false}},
		{name: "too many", rawArgs: "dev 1 2 3", wantErr: "too many arguments"},
		{name: "unknown named", rawArgs: "bogus=x", wantErr: "unknown argument"},
		{name: "bad bool", rawArgs: "force=maybe", wantErr: "expected on/off"},
		{name: "bad int", rawArgs: "count=abc", wantErr: "expected a number"},
		{name: "bad enum", rawArgs: "mars", wantErr: "expected one of"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseSlashArgs(cmd, test.rawArgs)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseSlashArgs(%q) error = %v, want containing %q", test.rawArgs, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSlashArgs(%q): %v", test.rawArgs, err)
			}
			for key, want := range test.want {
				if got[key] != want {
					t.Fatalf("parseSlashArgs(%q)[%s] = %#v, want %#v", test.rawArgs, key, got[key], want)
				}
			}
			if len(got) != len(test.want) {
				t.Fatalf("parseSlashArgs(%q) = %#v, want %#v", test.rawArgs, got, test.want)
			}
		})
	}
}

func TestExtractCompletionValues(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		field string
		want  []string
	}{
		{name: "items with id", raw: `{"items":[{"id":"b","rev":1},{"id":"a","rev":2}]}`, want: []string{"a", "b"}},
		{name: "items with field", raw: `{"items":[{"nickname":"z"},{"nickname":"m"}]}`, field: "nickname", want: []string{"m", "z"}},
		{name: "string array", raw: `["c","a","b"]`, want: []string{"a", "b", "c"}},
		{name: "plain string", raw: `"solo"`, want: []string{"solo"}},
		{name: "garbage", raw: `{"nope": 1}`, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractCompletionValues(json.RawMessage(test.raw), test.field)
			if len(got) != len(test.want) {
				t.Fatalf("extractCompletionValues(%s) = %v, want %v", test.raw, got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("extractCompletionValues(%s) = %v, want %v", test.raw, got, test.want)
				}
			}
		})
	}
}

func TestSlashRegistryShadowing(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{
		{Name: "help", Description: "evil", Component: "evil", Tool: "evil_help"},
		{Name: "deploy", Description: "d", Component: "deployer", Tool: "deploy_run"},
	})
	if cmd, ok := m.slash.lookup("help"); !ok || !cmd.builtin {
		t.Fatalf("plugin shadowed the /help builtin: %+v", cmd)
	}
	if cmd, ok := m.slash.lookup("deploy"); !ok || cmd.builtin {
		t.Fatalf("plugin command /deploy not registered: %+v", cmd)
	}
	if plugins := m.slash.pluginCommands(); len(plugins) != 1 || plugins[0].Name != "deploy" {
		t.Fatalf("pluginCommands = %+v, want [deploy]", plugins)
	}
}

func TestSlashCompletionCommandNames(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{deployTestCommand()})
	m.input.SetValue("/se")

	updated, _ := m.handleSlashTab(false)
	if !updated.slashComp.active {
		t.Fatal("tab did not activate command-name completion")
	}
	want := []string{"session", "sessions"}
	if len(updated.slashComp.candidates) != len(want) {
		t.Fatalf("candidates = %v, want %v", updated.slashComp.candidates, want)
	}
	if got := updated.input.Value(); got != "/session" {
		t.Fatalf("first candidate fill = %q, want /session", got)
	}

	// Second tab cycles to the next candidate.
	updated, _ = updated.handleSlashTab(false)
	if got := updated.input.Value(); got != "/sessions" {
		t.Fatalf("cycle fill = %q, want /sessions", got)
	}
	// Shift+tab cycles back.
	updated, _ = updated.handleSlashTab(true)
	if got := updated.input.Value(); got != "/session" {
		t.Fatalf("backward cycle fill = %q, want /session", got)
	}
}

func TestSlashCompletionSingleCandidateFillsDirectly(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/mouse")
	updated, _ := m.handleSlashTab(false)
	if got := updated.input.Value(); got != "/mouse" {
		t.Fatalf("single candidate fill = %q, want /mouse", got)
	}
	if updated.slashComp.active {
		t.Fatal("single-candidate completion should not open a list")
	}
}

func TestSlashCompletionArgumentPositions(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{deployTestCommand()})

	// The first positional has a source → Tab must fetch, not complete inline.
	m.input.SetValue("/deploy ")
	_, token, _, source, loading := m.slashCandidates("/deploy ")
	if !loading || source == nil || source.Tool != "deploy.envs" {
		t.Fatalf("deploy first arg: loading=%v source=%v, want a deploy.envs fetch", loading, source)
	}
	if token != "" {
		t.Fatalf("fresh-argument token = %q, want empty", token)
	}

	// /mouse completes its inline enum values.
	m.input.SetValue("/mouse o")
	_, _, candidates, _, loading := m.slashCandidates("/mouse o")
	if loading {
		t.Fatal("/mouse completion should not fetch")
	}
	if len(candidates) != 2 || candidates[0] != "on" || candidates[1] != "off" {
		t.Fatalf("/mouse candidates = %v, want [on off]", candidates)
	}

	// Second positional param (int) has no candidates at all.
	_, _, candidates, _, _ = m.slashCandidates("/deploy dev force 1")
	if candidates != nil {
		t.Fatalf("int param should not complete: %v", candidates)
	}
}

func TestSlashSourceCompletionResultApplies(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{deployTestCommand()})
	m.slashComp = slashCompleteState{
		active: true, loading: true, prefix: "/deploy ", token: "pr",
	}
	m.applySlashSource(slashSourceMsg{
		Token:  "pr",
		Values: []string{"prod", "preview"},
	})
	if m.slashComp.loading {
		t.Fatal("source result left the completion in loading state")
	}
	if len(m.slashComp.candidates) != 2 || m.slashComp.candidates[0] != "preview" || m.slashComp.candidates[1] != "prod" {
		t.Fatalf("candidates = %v, want [preview prod]", m.slashComp.candidates)
	}
	if got := m.input.Value(); got != "/deploy preview" {
		t.Fatalf("applied candidate = %q, want /deploy preview", got)
	}
}

func TestSlashSourceStaleResultDropped(t *testing.T) {
	m := newTestModel()
	m.slashComp = slashCompleteState{
		active: true, loading: true, prefix: "/deploy ", token: "newer",
	}
	m.applySlashSource(slashSourceMsg{Token: "stale", Values: []string{"old"}})
	if len(m.slashComp.candidates) != 0 {
		t.Fatalf("stale result applied: %v", m.slashComp.candidates)
	}
}

func TestExecuteSlashCommandParseError(t *testing.T) {
	m := newTestModel()
	updatedAny, cmd := m.executeSlashCommand(deployTestCommand(), "mars")
	if cmd != nil {
		t.Fatal("parse error should not issue a tool call")
	}
	updated := updatedAny.(model)
	if len(updated.blocks) != 1 || updated.blocks[0].kind != blockError {
		t.Fatalf("blocks = %+v, want a single error block", updated.blocks)
	}
}

func TestApplySlashResultError(t *testing.T) {
	m := newTestModel()
	m.applySlashResult(slashResultMsg{Name: "deploy", Err: errTestBoom{}})
	if len(m.blocks) != 1 || m.blocks[0].kind != blockError ||
		!strings.Contains(m.blocks[0].text, "/deploy failed") {
		t.Fatalf("blocks = %+v, want /deploy failed error block", m.blocks)
	}
}

func TestFormatSlashResult(t *testing.T) {
	if got := formatSlashResult(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("string result = %q, want it passed through", got)
	}
	if got := formatSlashResult(json.RawMessage(`{"summary":"Synthetic: 0/1250 requests","weekly":{"left":44.28}}`)); got != "Synthetic: 0/1250 requests" {
		t.Fatalf("summary result = %q, want the summary line verbatim", got)
	}
	got := formatSlashResult(json.RawMessage(`{"ok":true,"count":2}`))
	if !strings.Contains(got, "\n") || !strings.Contains(got, `"count": 2`) {
		t.Fatalf("object result should pretty-print, got %q", got)
	}
	if got := formatSlashResult(nil); got != "" {
		t.Fatalf("empty result = %q, want empty", got)
	}
}

func TestApplySlashResultSummary(t *testing.T) {
	m := newTestModel()
	m.applySlashResult(slashResultMsg{Name: "synthetic",
		Result: json.RawMessage(`{"summary":"Synthetic: 0/1250 requests"}`)})
	// The exec meta line already names the command — the result block must
	// not repeat it as a "/synthetic → …" prefix.
	if len(m.blocks) != 1 || m.blocks[0].kind != blockMeta ||
		m.blocks[0].text != "Synthetic: 0/1250 requests" {
		t.Fatalf("blocks = %+v, want a single meta block with the bare summary line", m.blocks)
	}
}

func TestSuggestSlash(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{deployTestCommand()})
	if got := suggestSlash(m.slash, "sess"); got != " — did you mean /session, /sessions" {
		t.Fatalf("suggestSlash(sess) = %q", got)
	}
	if got := suggestSlash(m.slash, "deply"); got != " — did you mean /deploy" {
		t.Fatalf("suggestSlash(deply) = %q, want the /deploy typo hint", got)
	}
	if got := suggestSlash(m.slash, "zzz"); got != " (try /help)" {
		t.Fatalf("suggestSlash(zzz) = %q", got)
	}
}

type errTestBoom struct{}

func (errTestBoom) Error() string { return "boom" }
