package main

import (
	"encoding/json"
	"slices"
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
	_, token, _, source, first, loading := m.slashCandidates("/deploy ")
	if !loading || source == nil || source.Tool != "deploy.envs" {
		t.Fatalf("deploy first arg: loading=%v source=%v, want a deploy.envs fetch", loading, source)
	}
	if !first {
		t.Fatal("the first positional slot should report first=true")
	}
	if token != "" {
		t.Fatalf("fresh-argument token = %q, want empty", token)
	}

	// /mouse completes its inline enum values.
	m.input.SetValue("/mouse o")
	_, _, candidates, _, _, loading := m.slashCandidates("/mouse o")
	if loading {
		t.Fatal("/mouse completion should not fetch")
	}
	if len(candidates) != 2 || candidates[0] != "on" || candidates[1] != "off" {
		t.Fatalf("/mouse candidates = %v, want [on off]", candidates)
	}

	// Second positional param (int) has no candidates at all, and is not the
	// subcommand slot even for a command that declares subcommands.
	_, _, candidates, _, _, _ = m.slashCandidates("/deploy dev force 1")
	if candidates != nil {
		t.Fatalf("int param should not complete: %v", candidates)
	}
	_, _, _, _, first, _ = m.slashCandidates("/mcp add server")
	if first {
		t.Fatal("a later positional must not report the first slot")
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

// TestSlashCompletionMergesDeclaredSubcommands: /provider's first slot is both
// the sourced nickname list and the declared subcommand slot, so Tab offers
// environment/env/strip alongside the provider_list nicknames (the gap that
// made `/provider strip` undiscoverable).
func TestSlashCompletionMergesDeclaredSubcommands(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry(nil)
	m.input.SetValue("/provider ")

	_, _, _, source, first, loading := m.slashCandidates("/provider ")
	if !loading || source == nil || source.Tool != "provider.provider_list" {
		t.Fatalf("completion: loading=%v source=%v, want a provider_list fetch", loading, source)
	}
	if !first {
		t.Fatal("/provider's argument is the first positional slot")
	}

	updated, _ := m.handleSlashTab(false)
	if got := updated.slashComp.extra; !slices.Equal(got, []string{"environment", "env", "strip"}) {
		t.Fatalf("extra candidates = %v, want [environment env strip]", got)
	}

	// The fetched nicknames join the declared tokens, deduped and sorted.
	updated.applySlashSource(slashSourceMsg{Token: "", Values: []string{"strip", "deepseek", "environment"}})
	want := []string{"deepseek", "env", "environment", "strip"}
	if got := updated.slashComp.candidates; !slices.Equal(got, want) {
		t.Fatalf("merged candidates = %v, want %v", got, want)
	}

	// A later positional (a sourced param on a command with subcommands) must
	// not pull the subcommand tokens in.
	_, _, _, _, first, _ = m.slashCandidates("/mcp add ")
	if first {
		t.Fatal("/mcp add's server-name slot is not the subcommand slot")
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

// helpText runs /help and returns the rendered block.
func helpText(t *testing.T, m model) string {
	t.Helper()
	updated, _ := m.executeLocalCommand("/help")
	blocks := updated.(model).blocks
	if len(blocks) == 0 {
		t.Fatal("/help produced no block")
	}
	return blocks[len(blocks)-1].text
}

// TestHelpListsEveryBuiltin is the regression for /components, /discover and
// /profile missing from /help: the listing is generated from the registry,
// so every registered built-in that is not an explicit alias must appear.
func TestHelpListsEveryBuiltin(tt *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry(nil)
	text := helpText(tt, m)

	listed := 0
	for _, cmd := range builtinSlashCommands() {
		if cmd.aliasOf != "" {
			if strings.Contains(text, "/"+cmd.Name+" ") {
				tt.Errorf("alias /%s should not be listed in /help", cmd.Name)
			}
			continue
		}
		listed++
		if !strings.Contains(text, "/"+cmd.Name) {
			tt.Errorf("/help is missing the built-in /%s\n%s", cmd.Name, text)
		}
	}
	if got := len(m.slash.helpCommands()); got != listed {
		tt.Errorf("helpCommands() = %d commands, want %d (registry order vs /help drift)", got, listed)
	}
	// Every listed built-in carries a localized line; the registry-derived
	// fallback exists, but the catalogs are expected to cover them all.
	for _, cmd := range m.slash.helpCommands() {
		if line := t(LocaleEN, "help."+cmd.Name); line == "" {
			tt.Errorf("catalog is missing help.%s", cmd.Name)
		}
	}
	// /components, /discover and /profile are the ones that disappeared.
	for _, name := range []string{"/components", "/discover", "/profile"} {
		if !strings.Contains(text, name) {
			tt.Errorf("/help is missing %s\n%s", name, text)
		}
	}
}

// TestEveryBuiltinIsHandled covers the other direction of the same drift:
// a name in the registry that executeLocalCommand does not handle would
// complete and be listed, then fall through to "unknown local command".
func TestEveryBuiltinIsHandled(t *testing.T) {
	for _, cmd := range builtinSlashCommands() {
		if cmd.run == nil {
			t.Errorf("/%s is registered without a handler", cmd.Name)
			continue
		}
		m := newTestModel()
		m.mergeSlashRegistry(nil)
		updated, _ := m.executeLocalCommand("/" + cmd.Name)
		for _, block := range updated.(model).blocks {
			if strings.Contains(block.text, "unknown local command") {
				t.Errorf("/%s is in the registry but has no handler: %s", cmd.Name, block.text)
			}
		}
	}
}

// TestSubcommandsAreDeclaredAndHandled covers the second level of the same
// invariant: a subcommand the table declares is dispatched from the table,
// and the entry's `subcommand` enum is derived from it rather than written
// out again. /provider strip and /mcp search are the two that had drifted.
func TestSubcommandsAreDeclaredAndHandled(t *testing.T) {
	for _, cmd := range builtinSlashCommands() {
		if len(cmd.subcommands) == 0 {
			continue
		}
		for _, sub := range cmd.subcommands {
			if sub.run == nil {
				t.Errorf("/%s %s is declared without a handler", cmd.Name, sub.name)
			}
			if _, ok := cmd.localSubcommand(sub.name); !ok {
				t.Errorf("/%s: %s is not resolvable", cmd.Name, sub.name)
			}
			for _, alias := range sub.aliases {
				if _, ok := cmd.localSubcommand(alias); !ok {
					t.Errorf("/%s: alias %s does not resolve", cmd.Name, alias)
				}
			}
		}
		p, ok := cmd.paramByName("subcommand")
		if !ok {
			continue
		}
		if got, want := strings.Join(p.Values, ","), strings.Join(subcommandNames(cmd.subcommands), ","); got != want {
			t.Errorf("/%s subcommand enum = [%s], want [%s] (derived from the table)", cmd.Name, got, want)
		}
	}
}

// TestProviderStripIsReachable pins the subcommand that existed only as a
// string comparison deep in the old switch: it must resolve through the
// registry and dispatch without an "unknown argument" error.
func TestProviderStripIsReachable(t *testing.T) {
	cmd, ok := builtinCommand("provider")
	if !ok {
		t.Fatal("/provider is not registered")
	}
	sub, ok := cmd.localSubcommand("strip")
	if !ok {
		t.Fatal("/provider strip is not declared")
	}
	if sub.run == nil {
		t.Fatal("/provider strip has no handler")
	}
	m := newTestModel()
	m.connected = true
	updated, call := m.executeLocalCommand("/provider strip off")
	if call == nil {
		t.Fatal("/provider strip off produced no command")
	}
	if blocks := updated.(model).blocks; len(blocks) > 0 {
		t.Fatalf("/provider strip off added a block: %+v", blocks)
	}
	if !updated.(model).controlPending {
		t.Fatal("/provider strip off did not mark the control plane pending")
	}
}

// TestMcpSearchIsDeclaredAndDispatched: /mcp search shipped without appearing
// in the declared subcommand set (it was a switch-only extra).
func TestMcpSearchIsDeclaredAndDispatched(t *testing.T) {
	cmd, ok := builtinCommand("mcp")
	if !ok {
		t.Fatal("/mcp is not registered")
	}
	for _, name := range []string{"search", "s"} {
		if _, ok := cmd.localSubcommand(name); !ok {
			t.Errorf("/mcp %s is not declared", name)
		}
	}
	p, ok := cmd.paramByName("subcommand")
	if !ok || !containsStr(p.Values, "search") {
		t.Errorf("/mcp subcommand enum does not offer search: %+v", p.Values)
	}
	m := newTestModel()
	m.connected = true
	updated, _ := m.executeLocalCommand("/mcp search weather")
	if got := updated.(model).mode; got != modeMcpSearch {
		t.Fatalf("/mcp search mode = %v, want the search selector", got)
	}
	updated, _ = m.executeLocalCommand("/mcp s weather")
	if got := updated.(model).mode; got != modeMcpSearch {
		t.Fatalf("/mcp s (alias) mode = %v, want the search selector", got)
	}
}

// TestPluginCommandDispatch: local dispatch falls through to a registered
// plugin command, while a built-in still wins over a same-named registration.
func TestPluginCommandDispatch(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry([]slashCommand{deployTestCommand()})
	updated, call := m.executeLocalCommand("/deploy staging")
	if call == nil {
		t.Fatal("/deploy issued no tool call")
	}
	blocks := updated.(model).blocks
	if len(blocks) != 1 || blocks[0].kind != blockMeta || !strings.Contains(blocks[0].text, "→ /deploy staging") {
		t.Fatalf("blocks = %+v, want the exec meta line", blocks)
	}

	m.mergeSlashRegistry([]slashCommand{{Name: "help", Description: "evil", Component: "evil", Tool: "evil_help"}})
	updated, _ = m.executeLocalCommand("/help")
	if text := updated.(model).blocks[0].text; !strings.Contains(text, "local commands:") {
		t.Fatalf("/help did not run the built-in: %q", text)
	}
}

// TestAliasDispatch: every alias names its canonical command and carries the
// same handler, so dispatch and /help agree on what is an alias of what.
func TestAliasDispatch(t *testing.T) {
	aliases := map[string]string{
		"?": "help", "newsession": "new", "providers": "provider",
		"models": "model", "sessions": "session",
	}
	for alias, canonical := range aliases {
		entry, ok := builtinCommand(alias)
		if !ok {
			t.Errorf("alias /%s is not registered", alias)
			continue
		}
		if entry.aliasOf != canonical {
			t.Errorf("alias /%s: aliasOf = %q, want %q", alias, entry.aliasOf, canonical)
		}
		if entry.run == nil {
			t.Errorf("alias /%s has no handler", alias)
		}
	}
	seen := map[string]bool{}
	for _, cmd := range builtinSlashCommands() {
		if cmd.aliasOf != "" {
			seen[cmd.Name] = true
		}
	}
	if len(seen) != len(aliases) {
		t.Errorf("registry has %d aliases, test knows %d: %v", len(seen), len(aliases), seen)
	}
}

// TestHelpLineFallback covers the derived line for a command whose locale// entry is absent: /help must still name it rather than print a blank.
func TestHelpLineFallback(t *testing.T) {
	m := newTestModel()
	line := m.helpLine(slashCommand{
		Name: "synthetic", Description: "do a thing", builtin: true,
		Params: []slashParam{{Name: "mode", Values: []string{"fast", "slow"}}, {Name: "force", Kind: "bool"}},
	})
	want := "  /synthetic [fast|slow] [force]  — do a thing"
	if line != want {
		t.Fatalf("helpLine fallback = %q, want %q", line, want)
	}
}

// TestLocaleCommandIsRegistered covers the other half of the same drift:
// /locale was implemented but absent from the registry, so Tab never
// completed it and /help listed it by hand.
func TestLocaleCommandIsRegistered(t *testing.T) {
	m := newTestModel()
	m.mergeSlashRegistry(nil)
	cmd, ok := m.slash.lookup("locale")
	if !ok || !cmd.builtin {
		t.Fatalf("/locale not registered: %+v", cmd)
	}
	m.input.SetValue("/loc")
	updated, _ := m.handleSlashTab(false)
	if got := updated.input.Value(); got != "/locale" {
		t.Fatalf("tab completion for /loc = %q, want /locale", got)
	}
}
