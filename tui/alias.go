// User-defined slash aliases (/alias): a name bound to either a fixed prompt
// that, when invoked, is sent as an ordinary conversation turn, or a
// `cli call <tool> '<json>'` command that invokes a harness tool directly.
// Aliases are a client-side convenience — they never touch the slash registry
// (no component, no tool call for prompt aliases) — and live in a small JSON
// file in the per-user state dir, alongside the theme, locale and session
// choice.
//
// Forms:
//
//	/alias                            list defined aliases
//	/alias list                       list defined aliases
//	/alias <name> <prompt…>           define a prompt (sugar for `add`)
//	/alias add <name> <prompt…>       define a prompt
//	/alias add <name> cli call …      define a call alias (auto-detected)
//	/alias call <name> cli call …     define a call alias (explicit)
//	/alias prompt <name> <prompt…>    define a prompt (explicit)
//	/alias rm <name>                  delete (`remove` is an alias)
//
// Invoking `/commit and push` sends the stored prompt for /commit with
// " and push" appended; an alias with no extra text sends the prompt
// verbatim. Aliases appear in Tab completion and in /help's own section.
//
// A call alias's body is the same grammar the `cli` binary accepts:
// `cli call <tool> '<json-args>'` (tool may be dotted or flat; args default
// to {}). The JSON argument strings interpolate the invocation's positional
// arguments: $1…$9, $0 (the alias name), $* (all arguments) and $$ (a literal
// $). Every other `$…` is literal, so shell snippets in a bash alias survive.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// aliasEntry is one user-defined alias. Exactly one field is set: Prompt for
// a prompt alias (sent as a conversation turn), Call for a call alias (a
// `cli call …` line executed as a tool invocation).
type aliasEntry struct {
	Prompt string
	Call   string
}

// MarshalJSON preserves the on-disk shape: a prompt alias serializes as the
// bare string it has always been (no migration, keeps hand-edited files
// familiar), a call alias as {"call": "…"}.
func (e aliasEntry) MarshalJSON() ([]byte, error) {
	if e.Call != "" {
		return json.Marshal(map[string]string{"call": e.Call})
	}
	return json.Marshal(e.Prompt)
}

// aliasKind selects how aliasDefine interprets its body.
type aliasKind int

const (
	aliasKindAuto aliasKind = iota // call if the body is a cli call line, else prompt
	aliasKindPrompt
	aliasKindCall
)

// aliasSubcommands returns /alias's declared subcommands. The entry
// references this table for its `subcommand` enum, for dispatch and for Tab
// completion, so the accepted set cannot drift from the documented one. It is
// a function, not a package var: the handlers call back into the built-in
// registry (aliasAdd refuses to shadow a built-in), and a var initializer
// referencing them would be an initialization cycle.
func aliasSubcommands() []slashSubcommand {
	return []slashSubcommand{
		{name: "add", run: aliasAdd},
		{name: "list", run: aliasList},
		{name: "rm", aliases: []string{"remove"}, run: aliasRemove},
		{name: "call", run: aliasAddCall},
		{name: "prompt", run: aliasAddPrompt},
	}
}

// ---- persistence -----------------------------------------------------------

// aliasFilePath is the JSON persistence file for user-defined aliases: the
// same per-user state dir as the theme/locale/session files, but global —
// aliases are not tied to a harness or workspace (empty when unavailable).
func aliasFilePath() string {
	dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "niffler-tui", "aliases")
}

// loadAliases reads the alias file. Best effort and total: a missing,
// unreadable or corrupt file yields no aliases rather than an error, and
// entries that fail validation (name, empty body, malformed call line) are
// dropped — a hand-edited file must never crash or block the UI.
func loadAliases() map[string]aliasEntry {
	aliases := map[string]aliasEntry{}
	path := aliasFilePath()
	if path == "" {
		return aliases
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return aliases
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return aliases
	}
	for name, value := range raw {
		n := normalizeAliasName(name)
		entry, ok := decodeAlias(value)
		if !validAliasName(n) || !ok {
			continue
		}
		aliases[n] = entry
	}
	return aliases
}

// decodeAlias parses one stored entry: a bare JSON string is a prompt alias
// (the original format), an object carries {"call": …} or {"prompt": …}.
func decodeAlias(raw json.RawMessage) (aliasEntry, bool) {
	var prompt string
	if err := json.Unmarshal(raw, &prompt); err == nil {
		if prompt = strings.TrimSpace(prompt); prompt == "" {
			return aliasEntry{}, false
		}
		return aliasEntry{Prompt: prompt}, true
	}
	var obj struct {
		Call   string `json:"call"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return aliasEntry{}, false
	}
	if call := strings.TrimSpace(obj.Call); call != "" {
		if _, _, err := parseCallAlias(call); err != nil {
			return aliasEntry{}, false
		}
		return aliasEntry{Call: call}, true
	}
	if prompt := strings.TrimSpace(obj.Prompt); prompt != "" {
		return aliasEntry{Prompt: prompt}, true
	}
	return aliasEntry{}, false
}

// saveAliases writes the alias file (0600 file, 0755 dir). encoding/json
// sorts map keys, so the serialized form is deterministic. Returns an error
// only so the caller can surface it; the in-memory map still applies.
func saveAliases(aliases map[string]aliasEntry) error {
	path := aliasFilePath()
	if path == "" {
		return fmt.Errorf("no state directory")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(aliases, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// ---- validation ------------------------------------------------------------

// normalizeAliasName trims and lowercases a candidate alias name.
func normalizeAliasName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// validAliasName reports whether a normalized name is usable: [a-z0-9] first,
// then [a-z0-9-]. This keeps aliases typable as `/name` and unambiguous next
// to the built-in command vocabulary.
func validAliasName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// ---- cli call parsing ------------------------------------------------------

const aliasCallPrefix = "cli call"

// isCallAliasBody reports whether a define body is a cli call command rather
// than a prompt.
func isCallAliasBody(body string) bool {
	lower := strings.ToLower(strings.TrimSpace(body))
	return lower == aliasCallPrefix || strings.HasPrefix(lower, aliasCallPrefix+" ")
}

// parseCallAlias splits `cli call <tool> ['<json-args>']` into the tool name
// and its JSON argument object (defaulting to {}). tool may be dotted or flat
// (core.invoke tolerates both); arguments must be a JSON object. The args may
// be wrapped in one layer of matching quotes, the way they are typed.
func parseCallAlias(body string) (string, json.RawMessage, error) {
	trimmed := strings.TrimSpace(body)
	if !isCallAliasBody(trimmed) {
		return "", nil, fmt.Errorf("expected a \"cli call <tool> '<json>'\" command")
	}
	rest := strings.TrimSpace(trimmed[len(aliasCallPrefix):])
	if rest == "" {
		return "", nil, fmt.Errorf("missing tool name")
	}
	tool := rest
	tail := ""
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		tool = rest[:i]
		tail = strings.TrimSpace(rest[i:])
	}
	if tool == "" {
		return "", nil, fmt.Errorf("missing tool name")
	}
	args := tail
	if args == "" {
		args = "{}"
	} else if unquoted, ok := unquoteAliasArgs(args); ok {
		args = unquoted
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(args), &object); err != nil {
		return "", nil, fmt.Errorf("arguments are not a JSON object: %v", err)
	}
	return tool, json.RawMessage(args), nil
}

// unquoteAliasArgs strips one layer of matching quotes around the argument
// text, so `'{"a":1}'` and `{"a":1}` both parse.
func unquoteAliasArgs(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return s, false
}

// ---- interpolation ---------------------------------------------------------

// splitAliasArgs tokenizes an invocation's extra text into positional
// arguments, honoring single and double quotes (an unbalanced quote takes the
// remainder as one token — lenient, never an error).
func splitAliasArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inToken := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inToken = true
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\n':
			if inToken {
				out = append(out, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if inToken {
		out = append(out, cur.String())
	}
	return out
}

// substituteAliasArgs replaces the $-tokens in the string leaves of a parsed
// argument object. Substitution happens on decoded strings (not the raw JSON
// text), so an argument containing a quote or backslash is inserted safely.
// A referenced but missing positional argument ($N with no Nth argument) fails
// the whole call rather than silently passing an empty string.
func substituteAliasArgs(raw json.RawMessage, argv []string, name string) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // preserve number spelling (large ints, decimals)
	var node any
	if err := dec.Decode(&node); err != nil {
		return nil, fmt.Errorf("arguments are not valid JSON: %v", err)
	}
	missing := 0
	substituted, err := substituteAliasValue(node, argv, name, &missing)
	if err != nil {
		return nil, err
	}
	if missing > 0 {
		return nil, fmt.Errorf("needs argument $%d", missing)
	}
	return json.Marshal(substituted)
}

// substituteAliasValue walks a decoded JSON value, substituting tokens in
// every string (keys are left alone).
func substituteAliasValue(node any, argv []string, name string, missing *int) (any, error) {
	switch v := node.(type) {
	case string:
		return substituteAliasTokens(v, argv, name, missing), nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			sub, err := substituteAliasValue(item, argv, name, missing)
			if err != nil {
				return nil, err
			}
			out[i] = sub
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			sub, err := substituteAliasValue(item, argv, name, missing)
			if err != nil {
				return nil, err
			}
			out[k] = sub
		}
		return out, nil
	default:
		return node, nil
	}
}

// substituteAliasTokens expands the $-tokens in one string: $1…$9 positional,
// $0 the alias name, $*/$@ all arguments, $$ a literal $. Any other `$…` is
// left verbatim so shell snippets ($HOME, $(cmd)) survive.
func substituteAliasTokens(s string, argv []string, name string, missing *int) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch n := s[i+1]; {
		case n == '$':
			b.WriteByte('$')
			i += 2
		case n == '*' || n == '@':
			b.WriteString(strings.Join(argv, " "))
			i += 2
		case n == '0':
			b.WriteString(name)
			i += 2
		case n >= '1' && n <= '9':
			idx := int(n - '1')
			if idx < len(argv) {
				b.WriteString(argv[idx])
			} else if *missing == 0 || idx+1 < *missing {
				*missing = idx + 1
			}
			i += 2
		default:
			b.WriteByte('$')
			i++
		}
	}
	return b.String()
}

// ---- listing / completion --------------------------------------------------

// aliasNames returns the defined alias names, sorted.
func (m model) aliasNames() []string {
	names := make([]string, 0, len(m.aliases))
	for name := range m.aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// aliasLines renders one "  /name — …" line per defined alias, naming the
// call kind so a tool alias is distinguishable from a prompt alias.
func (m model) aliasLines() []string {
	names := m.aliasNames()
	lines := make([]string, 0, len(names))
	for _, name := range names {
		entry := m.aliases[name]
		if entry.Call != "" {
			lines = append(lines, "  /"+name+" — "+t(m.loc, "alias.listingCall", entry.Call))
			continue
		}
		lines = append(lines, "  /"+name+" — "+entry.Prompt)
	}
	return lines
}

// completionNames is the full set of invocable slash-command names for Tab
// completion: the merged registry (built-ins + plugin commands) plus the
// user-defined aliases, deduped and sorted.
func (m model) completionNames() []string {
	names := make([]string, 0, len(m.slash.order)+len(m.aliases))
	names = append(names, m.slash.order...)
	names = append(names, m.aliasNames()...)
	return dedupeSorted(names)
}

// ---- dispatch --------------------------------------------------------------

// localAlias is /alias's entry handler: a declared subcommand dispatches,
// an empty argument lists, anything else is the `add` sugar (the first word
// becomes the alias name and the remainder its body).
func localAlias(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if m.aliases == nil {
		m.aliases = map[string]aliasEntry{}
	}
	first, rest := splitCommand(argument)
	if first != "" {
		if sub, ok := cmd.localSubcommand(strings.ToLower(first)); ok {
			return sub.run(m, cmd, rest)
		}
	}
	if first == "" {
		return aliasList(m, cmd, "")
	}
	return aliasDefine(m, cmd, argument, aliasKindAuto)
}

// aliasAdd defines (or overwrites) an alias, auto-detecting a cli call body.
func aliasAdd(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return aliasDefine(m, cmd, argument, aliasKindAuto)
}

// aliasAddCall defines a call alias, requiring a cli call body.
func aliasAddCall(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return aliasDefine(m, cmd, argument, aliasKindCall)
}

// aliasAddPrompt defines a prompt alias explicitly (a body that happens to
// start with "cli call" is kept as a prompt).
func aliasAddPrompt(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return aliasDefine(m, cmd, argument, aliasKindPrompt)
}

// aliasDefine defines (or overwrites) an alias. The name is normalized to
// lowercase and validated; a built-in name (or `alias` itself) is refused so
// a user alias can never shadow a real command. A call body is parsed at
// define time so a malformed command is rejected here, and a check command
// warns (without failing) when the target tool is not registered yet.
func aliasDefine(m model, cmd slashCommand, argument string, kind aliasKind) (tea.Model, tea.Cmd) {
	if m.aliases == nil {
		m.aliases = map[string]aliasEntry{}
	}
	name, body := splitCommand(argument)
	name = normalizeAliasName(name)
	if name == "" {
		return m.aliasError("alias.nameRequired")
	}
	if !validAliasName(name) {
		return m.aliasError("alias.invalidName", name)
	}
	if _, taken := builtinCommand(name); taken || name == "alias" {
		return m.aliasError("alias.reserved", name)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return m.aliasError("alias.promptRequired", name)
	}
	var entry aliasEntry
	call := kind == aliasKindCall || (kind == aliasKindAuto && isCallAliasBody(body))
	if kind == aliasKindCall && !isCallAliasBody(body) {
		return m.aliasError("alias.callExpected")
	}
	if call {
		if _, _, err := parseCallAlias(body); err != nil {
			return m.aliasError("alias.badCall", name, err.Error())
		}
		entry.Call = body
	} else {
		entry.Prompt = body
	}
	m.aliases[name] = entry
	if err := saveAliases(m.aliases); err != nil {
		return m.aliasError("alias.saveFailed", err.Error())
	}
	if entry.Call != "" {
		tool, _, _ := parseCallAlias(entry.Call)
		m.addBlock(blockMeta, t(m.loc, "alias.createdCall", name, tool))
		m.syncViewport(true)
		if !m.connected {
			// Offline: skip the catalog probe (it would just time out).
			return m, nil
		}
		return m, aliasCheckCmd(m.comp, name, tool)
	}
	m.addBlock(blockMeta, t(m.loc, "alias.created", name, entry.Prompt))
	m.syncViewport(true)
	return m, nil
}

// aliasRemove deletes an alias by name.
func aliasRemove(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	name, _ := splitCommand(argument)
	name = normalizeAliasName(name)
	if name == "" {
		return m.aliasError("alias.nameRequired")
	}
	if _, ok := m.aliases[name]; !ok {
		return m.aliasError("alias.notFound", name)
	}
	delete(m.aliases, name)
	if err := saveAliases(m.aliases); err != nil {
		return m.aliasError("alias.saveFailed", err.Error())
	}
	m.addBlock(blockMeta, t(m.loc, "alias.removed", name))
	m.syncViewport(true)
	return m, nil
}

// aliasList renders the defined aliases (or a hint when there are none).
func aliasList(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	names := m.aliasNames()
	if len(names) == 0 {
		m.addBlock(blockMeta, t(m.loc, "alias.none"))
		m.syncViewport(true)
		return m, nil
	}
	lines := append([]string{t(m.loc, "alias.title")}, m.aliasLines()...)
	m.addBlock(blockMeta, strings.Join(lines, "\n"))
	m.syncViewport(true)
	return m, nil
}

// aliasError renders a localized alias error block.
func (m model) aliasError(key string, vars ...string) (tea.Model, tea.Cmd) {
	m.addBlock(blockError, t(m.loc, key, vars...))
	m.syncViewport(true)
	return m, nil
}

// runAlias runs an alias: a call alias invokes its tool, a prompt alias sends
// its stored prompt as a conversation turn, mirroring the Enter key's idle and
// busy (steer) branches in main.go. Extra text typed after the alias name is
// appended to a prompt (separated by a space) or supplied as the call's
// positional arguments.
func (m model) runAlias(name string, entry aliasEntry, extra string) (tea.Model, tea.Cmd) {
	if entry.Call != "" {
		return m.runAliasCall(name, entry.Call, extra)
	}
	prompt := strings.TrimSpace(entry.Prompt)
	if prompt == "" {
		return m.aliasError("alias.notFound", name)
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		prompt += " " + extra
	}
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	if m.controlPending {
		m.contextNote = t(m.loc, "note.controlPending")
		return m, nil
	}
	if m.busy {
		// Steer the running turn exactly as typed input does (main.go).
		m.addBlock(blockUser, "Steer: "+prompt)
		m.layout()
		m.syncViewport(true)
		return m, m.sendSteer(prompt)
	}
	m.busy = true
	m.busySince = time.Now()
	m.lastTokenAt = m.busySince
	m.streamKind = streamNone
	m.steered = 0
	m.hadAssistant = false
	m.assistantIdx = -1
	m.thinkingIdx = -1
	m.setStreaming(false)
	m.roundClosed = false
	m.turnEndRendered = false
	m.addBlock(blockUser, prompt)
	m.layout()
	m.syncViewport(true)
	return m, tea.Batch(m.sendTurn(prompt, nil), m.armSpinner())
}

// runAliasCall runs a call alias: parse its stored `cli call …` line,
// substitute the invocation's positional arguments into the JSON argument
// strings, and dispatch through core.invoke — the same gateway completion
// sources use. core.invoke enforces the tools' own approval gate, so an alias
// pointing at bash/git still prompts the human.
func (m model) runAliasCall(name, body, extra string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	if m.controlPending {
		m.contextNote = t(m.loc, "note.controlPending")
		return m, nil
	}
	tool, rawArgs, err := parseCallAlias(body)
	if err != nil {
		return m.aliasError("alias.badCall", name, err.Error())
	}
	args, err := substituteAliasArgs(rawArgs, splitAliasArgs(extra), name)
	if err != nil {
		return m.aliasError("alias.callArgs", name, err.Error())
	}
	m.addBlock(blockMeta, "→ /"+name+slashArgText(extra))
	m.syncViewport(true)
	session := m.session // guard: the result must belong to the conversation it was invoked in
	return m, func() tea.Msg {
		var result json.RawMessage
		err := requestInto(m.comp, "core", "invoke", map[string]any{
			"tool":      tool,
			"arguments": args,
		}, &result)
		return slashResultMsg{Name: name, Session: session, Result: result, Err: err}
	}
}

// aliasCheckMsg reports the async tool-existence probe for a freshly defined
// call alias. Err is a catalog read failure (silently ignored — the check is
// a courtesy); Found=false with a nil Err means the tool is not registered.
type aliasCheckMsg struct {
	Name  string
	Tool  string
	Found bool
	Err   error
}

// aliasCheckCmd probes whether a call alias's target tool is registered, so a
// typo is warned about at define time (the definition still stands — tools
// can register later). Best effort: a catalog read failure yields no warning.
func aliasCheckCmd(comp *sdk.Component, name, tool string) tea.Cmd {
	return func() tea.Msg {
		var listing struct {
			Components map[string][]string `json:"components"`
		}
		err := requestInto(comp, "core", "catalog", map[string]any{"op": "components"}, &listing)
		if err != nil {
			return aliasCheckMsg{Name: name, Tool: tool, Err: err}
		}
		return aliasCheckMsg{Name: name, Tool: tool, Found: aliasToolRegistered(listing.Components, tool)}
	}
}

// aliasToolRegistered reports whether tool appears in the component→tools
// catalog view, tolerating invoke's dotted spelling (component.tool).
func aliasToolRegistered(components map[string][]string, tool string) bool {
	for _, tools := range components {
		for _, name := range tools {
			if name == tool {
				return true
			}
		}
	}
	if i := strings.LastIndex(tool, "."); i >= 0 {
		return aliasToolRegistered(components, tool[i+1:])
	}
	return false
}

// remainderAfterFirst returns s with its first whitespace-delimited field
// removed, preserving the rest verbatim (quotes and spacing) — the raw
// argument text an alias invocation interpolates.
func remainderAfterFirst(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	idx := strings.Index(s, fields[0])
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(s[idx+len(fields[0]):])
}
