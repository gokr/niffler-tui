package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aliasTestModel builds a model with the alias store pointed at a private
// state dir (XDG_STATE_HOME drives aliasFilePath, like the theme/locale
// files) and no aliases loaded.
func aliasTestModel(t *testing.T) model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newTestModel()
	m.connected = true
	m.aliases = map[string]aliasEntry{}
	return m
}

// runAliasCommand dispatches through executeLocalCommand, the same path Enter
// uses, and returns the updated model plus the last block's text.
func runAliasCommand(t *testing.T, m model, line string) (model, string) {
	t.Helper()
	updated, _ := m.executeLocalCommand(line)
	out := updated.(model)
	text := ""
	if len(out.blocks) > 0 {
		text = out.blocks[len(out.blocks)-1].text
	}
	return out, text
}

func TestAliasCreatePersistReload(t *testing.T) {
	m := aliasTestModel(t)

	updated, text := runAliasCommand(t, m, "/alias commit Please commit all changes properly")
	if got := updated.aliases["commit"].Prompt; got != "Please commit all changes properly" {
		t.Fatalf("alias not stored: %#v", updated.aliases)
	}
	if !strings.Contains(text, "/commit") || !strings.Contains(text, "Please commit all changes properly") {
		t.Fatalf("create confirmation = %q", text)
	}

	// The file exists, is 0600, and its directory 0755.
	path := aliasFilePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("alias file not written: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("alias file mode = %o, want 600", got)
	}
	if di, err := os.Stat(filepath.Dir(path)); err == nil {
		if got := di.Mode().Perm(); got != 0o755 {
			t.Fatalf("alias dir mode = %o, want 755", got)
		}
	}

	// A reload (fresh process) sees the alias.
	if got := loadAliases()["commit"].Prompt; got != "Please commit all changes properly" {
		t.Fatalf("reload = %q, want the stored prompt", got)
	}

	// `add` is explicit sugar for the same thing.
	updated, _ = runAliasCommand(t, updated, "/alias add push push everything now")
	if got := updated.aliases["push"].Prompt; got != "push everything now" {
		t.Fatalf("add did not store: %#v", updated.aliases)
	}

	// The serialized form is a JSON object sorted by name (deterministic).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"commit\": \"Please commit all changes properly\",\n  \"push\": \"push everything now\"\n}\n"
	if string(data) != want {
		t.Fatalf("serialized aliases = %q, want %q", data, want)
	}
}

func TestAliasListAndHelp(t *testing.T) {
	m := aliasTestModel(t)
	m.aliases = map[string]aliasEntry{
		"commit": {Prompt: "Please commit all changes properly"},
		"lint":   {Prompt: "Run the linters and fix what they report"},
	}

	updated, text := runAliasCommand(t, m, "/alias")
	if !strings.Contains(text, "/commit — Please commit all changes properly") ||
		!strings.Contains(text, "/lint — Run the linters") {
		t.Fatalf("/alias listing = %q", text)
	}
	// Sorted by name: commit before lint.
	if strings.Index(text, "/commit") > strings.Index(text, "/lint") {
		t.Fatalf("/alias listing is not sorted by name: %q", text)
	}

	if _, listText := runAliasCommand(t, updated, "/alias list"); listText != text {
		t.Fatalf("/alias list = %q, want the same as /alias = %q", listText, text)
	}

	// /help carries its own alias section, after the built-in list.
	_, help := runAliasCommand(t, updated, "/help")
	if !strings.Contains(help, "aliases:") {
		t.Fatalf("/help has no alias section:\n%s", help)
	}
	if !strings.Contains(help, "/commit — Please commit all changes properly") {
		t.Fatalf("/help does not list the aliases:\n%s", help)
	}
	// The dynamic alias is not in the built-in listing (helpCommands), so the
	// built-in section and the alias section stay separate.
	for _, cmd := range updated.slash.helpCommands() {
		if cmd.Name == "commit" {
			t.Fatal("a user alias leaked into the built-in /help listing")
		}
	}

	// With no aliases the listing is a hint, not an empty header.
	empty := aliasTestModel(t)
	if _, none := runAliasCommand(t, empty, "/alias"); !strings.Contains(none, "no aliases defined") {
		t.Fatalf("empty /alias = %q", none)
	}
}

func TestAliasRemove(t *testing.T) {
	m := aliasTestModel(t)
	m.aliases = map[string]aliasEntry{"commit": {Prompt: "commit please"}, "lint": {Prompt: "lint please"}}

	updated, text := runAliasCommand(t, m, "/alias rm commit")
	if _, ok := updated.aliases["commit"]; ok {
		t.Fatalf("rm did not delete: %#v", updated.aliases)
	}
	if !strings.Contains(text, "removed") {
		t.Fatalf("rm confirmation = %q", text)
	}
	if got := loadAliases(); len(got) != 1 || got["lint"].Prompt != "lint please" {
		t.Fatalf("reload after rm = %#v", got)
	}

	// `remove` is the declared alias for `rm`.
	if _, removedNote := runAliasCommand(t, updated, "/alias remove lint"); !strings.Contains(removedNote, "removed") {
		t.Fatalf("/alias remove = %q", removedNote)
	}

	// Unknown name: a clear error, no deletion.
	if _, unknown := runAliasCommand(t, m, "/alias rm nope"); !strings.Contains(unknown, "no alias /nope") {
		t.Fatalf("unknown rm = %q", unknown)
	}
}

func TestAliasValidation(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr string // empty: expected success
	}{
		{name: "missing name", line: "/alias add", wantErr: "alias name is required"},
		{name: "missing prompt", line: "/alias commit", wantErr: "a prompt is required"},
		{name: "space is not a valid name", line: "/alias rm", wantErr: "alias name is required"},
		{name: "uppercase normalized", line: "/alias Commit please commit"},
		{name: "digit start ok", line: "/alias 9lives nine lives"},
		{name: "dash ok", line: "/alias code-review review the code"},
		{name: "underscore rejected", line: "/alias code_review review the code", wantErr: "invalid alias name"},
		{name: "leading dash rejected", line: "/alias -x nope", wantErr: "invalid alias name"},
		{name: "punctuation rejected", line: "/alias commit! nope", wantErr: "invalid alias name"},
		{name: "builtin collision", line: "/alias help do my own help", wantErr: "built-in command"},
		{name: "builtin alias collision", line: "/alias newsession nope", wantErr: "built-in command"},
		{name: "alias itself", line: "/alias alias nope nope", wantErr: "built-in command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := aliasTestModel(t)
			updated, text := runAliasCommand(t, m, test.line)
			if test.wantErr != "" {
				if !strings.Contains(text, test.wantErr) {
					t.Fatalf("%q: block = %q, want containing %q", test.line, text, test.wantErr)
				}
				if len(updated.aliases) != 0 {
					t.Fatalf("%q: rejected create still stored %#v", test.line, updated.aliases)
				}
				return
			}
			if len(updated.aliases) != 1 {
				t.Fatalf("%q: aliases = %#v, want one", test.line, updated.aliases)
			}
		})
	}

	// The normalized (lowercase) name is the stored key.
	m := aliasTestModel(t)
	updated, _ := runAliasCommand(t, m, "/alias Commit please commit")
	if got := updated.aliases["commit"].Prompt; got != "please commit" {
		t.Fatalf("normalized alias = %#v, want commit→please commit", updated.aliases)
	}
}

func TestAliasInvokeSendsPromptTurn(t *testing.T) {
	m := aliasTestModel(t)
	m.aliases["commit"] = aliasEntry{Prompt: "Please commit all changes properly"}

	// No extra text: the stored prompt verbatim, as a user turn.
	updated, cmd := m.executeLocalCommand("/commit")
	out := updated.(model)
	if cmd == nil {
		t.Fatal("/commit issued no command")
	}
	if !out.busy {
		t.Fatal("/commit did not start a turn")
	}
	if len(out.blocks) != 1 || out.blocks[0].kind != blockUser ||
		out.blocks[0].text != "Please commit all changes properly" {
		t.Fatalf("blocks = %#v, want the prompt as a user turn", out.blocks)
	}

	// Extra words are appended separated by a space.
	updated, cmd = m.executeLocalCommand("/commit and push")
	out = updated.(model)
	if cmd == nil {
		t.Fatal("/commit and push issued no command")
	}
	if len(out.blocks) != 1 || out.blocks[0].kind != blockUser ||
		out.blocks[0].text != "Please commit all changes properly and push" {
		t.Fatalf("blocks = %#v, want the prompt with the extra text appended", out.blocks)
	}

	// An alias shadowed by no built-in is still reachable as a plain name.
	if _, ok := builtinCommand("commit"); ok {
		t.Fatal("/commit collides with a built-in; test setup invalid")
	}
}

func TestAliasCompletion(t *testing.T) {
	m := aliasTestModel(t)
	m.aliases = map[string]aliasEntry{"commit": {Prompt: "commit please"}}

	names := m.completionNames()
	if !containsStr(names, "commit") {
		t.Fatalf("completionNames = %v, want it to include the alias", names)
	}
	// Sorted, deduped.
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("completionNames not sorted/deduped: %v", names)
		}
	}

	m.input.SetValue("/com")
	updated, _ := m.handleSlashTab(false)
	if got := updated.input.Value(); got != "/commit" {
		t.Fatalf("Tab completion for /com = %q, want /commit", got)
	}
}

func TestAliasFileTolerance(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Missing file: no aliases, no error.
	if got := loadAliases(); len(got) != 0 {
		t.Fatalf("missing file = %#v, want empty", got)
	}

	path := aliasFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	// Corrupt JSON is ignored rather than fatal.
	for name, content := range map[string]string{
		"garbage":   "not json at all",
		"array":     "[\"commit\"]",
		"truncated": "{\"commit\": \"please",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := loadAliases(); len(got) != 0 {
			t.Fatalf("%s: loadAliases = %#v, want empty", name, got)
		}
	}

	// Invalid entries inside a valid object are dropped, valid ones kept.
	body := `{"ok":"keep me","BAD name":"x","":"y","empty":""}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadAliases()
	if len(got) != 1 || got["ok"].Prompt != "keep me" {
		t.Fatalf("sanitized load = %#v, want only ok→keep me", got)
	}
}

func TestAliasSaveFailureIsNonFatal(t *testing.T) {
	// XDG_STATE_HOME pointed at a regular file makes MkdirAll fail, so
	// persistence errors out — the in-memory alias must still apply and the
	// failure surface as an error block, never a crash.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blocker)

	m := newTestModel()
	m.connected = true
	m.aliases = map[string]aliasEntry{}
	updated, text := runAliasCommand(t, m, "/alias commit please commit")
	if got := updated.aliases["commit"].Prompt; got != "please commit" {
		t.Fatalf("alias lost on save failure: %#v", updated.aliases)
	}
	if !strings.Contains(text, "could not save aliases") {
		t.Fatalf("save failure was not surfaced: %q", text)
	}
}

// ---- call aliases (cli call …) ---------------------------------------------

func TestAliasCallParse(t *testing.T) {
	tool, args, err := parseCallAlias(`cli call git '{"op":"pr","title":"$1"}'`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tool != "git" || string(args) != `{"op":"pr","title":"$1"}` {
		t.Fatalf("parse = %q %s", tool, args)
	}

	// Dotted tool, no arguments (defaults to {}), unquoted object.
	tool, args, err = parseCallAlias(`cli call provider.provider_status`)
	if err != nil || tool != "provider.provider_status" || string(args) != "{}" {
		t.Fatalf("defaults = %q %s (%v)", tool, args, err)
	}
	if _, args, err = parseCallAlias(`cli call provider.provider_status {"op":"list"}`); err != nil || string(args) != `{"op":"list"}` {
		t.Fatalf("unquoted = %s (%v)", args, err)
	}

	// Malformed bodies: not a call, missing tool, bad JSON, non-object.
	for _, body := range []string{
		`please commit`,
		`cli call`,
		`cli call git not json`,
		`cli call git '[1,2]'`,
	} {
		if _, _, err := parseCallAlias(body); err == nil {
			t.Fatalf("%q parsed but should have been rejected", body)
		}
	}
}

func TestAliasCallInterpolation(t *testing.T) {
	raw := json.RawMessage(`{"title":"$1","body":"$2","all":"$*","lit":"$$","keep":"$HOME","zero":"$0"}`)
	got, err := substituteAliasArgs(raw, []string{"Fix it", "details"}, "pr")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]string
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("result is not an object: %v (%s)", err, got)
	}
	if obj["title"] != "Fix it" || obj["body"] != "details" || obj["all"] != "Fix it details" {
		t.Fatalf("positional substitution = %#v", obj)
	}
	if obj["lit"] != "$" || obj["keep"] != "$HOME" || obj["zero"] != "pr" {
		t.Fatalf("literal/unknown substitution = %#v", obj)
	}

	// A missing positional fails instead of substituting empty.
	if _, err := substituteAliasArgs(json.RawMessage(`{"t":"$2"}`), []string{"one"}, "pr"); err == nil {
		t.Fatal("missing $2 did not error")
	}

	// Quotes and backslashes in an argument are inserted safely.
	tricky := `he said "hi" \ bye`
	got, err = substituteAliasArgs(json.RawMessage(`{"t":"$1"}`), []string{tricky}, "x")
	if err != nil {
		t.Fatal(err)
	}
	var one map[string]string
	if err := json.Unmarshal(got, &one); err != nil || one["t"] != tricky {
		t.Fatalf("tricky argument = %s (%v)", got, err)
	}

	// Numbers survive substitution (UseNumber, no float drift).
	got, err = substituteAliasArgs(json.RawMessage(`{"n":1234567890123,"f":1.5,"a":["$1"]}`), []string{"x"}, "n")
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, "1234567890123") || !strings.Contains(s, "1.5") || !strings.Contains(s, "\"x\"") {
		t.Fatalf("numeric/array substitution = %s", s)
	}
}

func TestAliasCallDefinePersist(t *testing.T) {
	m := aliasTestModel(t)
	updated, text := runAliasCommand(t, m, `/alias add pr cli call git '{"op":"pr","title":"$1"}'`)
	if got := updated.aliases["pr"]; got.Call == "" || got.Prompt != "" {
		t.Fatalf("call not stored: %#v", updated.aliases)
	}
	if !strings.Contains(text, "call") {
		t.Fatalf("create confirmation = %q", text)
	}

	// The listing names the call kind.
	if _, listing := runAliasCommand(t, updated, "/alias"); !strings.Contains(listing, "call: cli call git") {
		t.Fatalf("/alias listing = %q", listing)
	}

	// A reload (fresh process) reads the object form back.
	if got := loadAliases()["pr"].Call; got != `cli call git '{"op":"pr","title":"$1"}'` {
		t.Fatalf("reload = %q", got)
	}

	// A malformed call body is rejected at define time and stores nothing.
	m2 := aliasTestModel(t)
	if _, bad := runAliasCommand(t, m2, `/alias add bad cli call git nope`); !strings.Contains(bad, "invalid cli call command") {
		t.Fatalf("malformed call = %q", bad)
	}
	if _, ok := m2.aliases["bad"]; ok {
		t.Fatalf("malformed call was stored: %#v", m2.aliases)
	}
}

func TestAliasCallExplicitSubcommands(t *testing.T) {
	m := aliasTestModel(t)

	// /alias prompt keeps a body that merely looks like a call as a prompt.
	updated, _ := runAliasCommand(t, m, "/alias prompt p cli call git '{}'")
	if got := updated.aliases["p"]; got.Prompt == "" || got.Call != "" {
		t.Fatalf("/alias prompt stored %#v", got)
	}

	// /alias call refuses a non-call body.
	if _, errText := runAliasCommand(t, updated, "/alias call c just a prompt"); !strings.Contains(errText, "cli call command is required") {
		t.Fatalf("/alias call error = %q", errText)
	}

	// /alias call defines a call alias.
	withCall, _ := runAliasCommand(t, updated, `/alias call c cli call git '{}'`)
	if withCall.aliases["c"].Call == "" {
		t.Fatalf("/alias call stored %#v", withCall.aliases["c"])
	}
}

func TestAliasCallInvoke(t *testing.T) {
	m := aliasTestModel(t)
	m.aliases["pr"] = aliasEntry{Call: `cli call git '{"op":"pr","title":"$1"}'`}

	// Missing positional argument: an error block, no dispatch, no turn.
	updated, cmd := m.executeLocalCommand("/pr")
	out := updated.(model)
	if cmd != nil {
		t.Fatal("/pr with no argument still dispatched")
	}
	if out.busy {
		t.Fatal("/pr with no argument started a turn")
	}
	if last := out.blocks[len(out.blocks)-1]; !strings.Contains(last.text, "needs argument $1") {
		t.Fatalf("missing-argument block = %q", last.text)
	}

	// Provided arguments: a dispatch command and the exec meta line.
	updated, cmd = m.executeLocalCommand(`/pr "Fix the parser"`)
	out = updated.(model)
	if cmd == nil {
		t.Fatal("/pr with an argument issued no command")
	}
	if out.busy {
		t.Fatal("a call alias started a turn")
	}
	if last := out.blocks[len(out.blocks)-1]; last.text != `→ /pr "Fix the parser"` {
		t.Fatalf("exec meta line = %q", last.text)
	}
}

func TestAliasToolRegistered(t *testing.T) {
	comps := map[string][]string{
		"git":      {"git_status", "git"},
		"provider": {"provider_status"},
	}
	if !aliasToolRegistered(comps, "git") {
		t.Fatal("plain tool not found")
	}
	// invoke tolerates the dotted spelling.
	if !aliasToolRegistered(comps, "provider.provider_status") {
		t.Fatal("dotted tool not resolved to its flat name")
	}
	if aliasToolRegistered(comps, "nope") {
		t.Fatal("unknown tool reported as registered")
	}
}

func TestAliasFileObjectForms(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := aliasFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"pr":{"call":"cli call git '{}'"},"o":"a prompt","bad":{"call":"cli call git nope"},"emptyobj":{}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadAliases()
	if len(got) != 2 {
		t.Fatalf("object-form load = %#v, want pr and o only", got)
	}
	if got["pr"].Call != `cli call git '{}'` {
		t.Fatalf("call entry = %#v", got["pr"])
	}
	if got["o"].Prompt != "a prompt" {
		t.Fatalf("prompt entry = %#v", got["o"])
	}
}
