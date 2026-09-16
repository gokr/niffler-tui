// User-defined slash aliases (/alias): a name bound to a fixed prompt that,
// when invoked, is sent as an ordinary conversation turn. Aliases are a
// client-side convenience — they never touch the slash registry (no
// component, no tool call) — and live in a small JSON file in the per-user
// state dir, alongside the theme, locale and session choice.
//
// Forms:
//
//	/alias                      list defined aliases
//	/alias list                 list defined aliases
//	/alias <name> <prompt…>     define (sugar for `add`)
//	/alias add <name> <prompt…> define
//	/alias rm <name>            delete (`remove` is an alias)
//
// Invoking `/commit and push` sends the stored prompt for /commit with
// " and push" appended; an alias with no extra text sends the prompt
// verbatim. Aliases appear in Tab completion and in /help's own section.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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
// entries that fail validation (name or empty prompt) are dropped — a
// hand-edited file must never crash or block the UI.
func loadAliases() map[string]string {
	aliases := map[string]string{}
	path := aliasFilePath()
	if path == "" {
		return aliases
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return aliases
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return aliases
	}
	for name, prompt := range raw {
		n := normalizeAliasName(name)
		p := strings.TrimSpace(prompt)
		if !validAliasName(n) || p == "" {
			continue
		}
		aliases[n] = p
	}
	return aliases
}

// saveAliases writes the alias file (0600 file, 0755 dir). encoding/json
// sorts map keys, so the serialized form is deterministic. Returns an error
// only so the caller can surface it; the in-memory map still applies.
func saveAliases(aliases map[string]string) error {
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

// aliasNames returns the defined alias names, sorted.
func (m model) aliasNames() []string {
	names := make([]string, 0, len(m.aliases))
	for name := range m.aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// aliasLines renders one "  /name — prompt" line per defined alias.
func (m model) aliasLines() []string {
	names := m.aliasNames()
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, "  /"+name+" — "+m.aliases[name])
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
// becomes the alias name and the remainder its prompt).
func localAlias(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if m.aliases == nil {
		m.aliases = map[string]string{}
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
	return aliasAdd(m, cmd, argument)
}

// aliasAdd defines (or overwrites) an alias. The name is normalized to
// lowercase and validated; a built-in name (or `alias` itself) is refused so
// a user alias can never shadow a real command.
func aliasAdd(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if m.aliases == nil {
		m.aliases = map[string]string{}
	}
	name, prompt := splitCommand(argument)
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
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return m.aliasError("alias.promptRequired", name)
	}
	m.aliases[name] = prompt
	if err := saveAliases(m.aliases); err != nil {
		return m.aliasError("alias.saveFailed", err.Error())
	}
	m.addBlock(blockMeta, t(m.loc, "alias.created", name, prompt))
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

// runAlias sends an alias's stored prompt as a conversation turn, mirroring
// the Enter key's idle and busy (steer) branches in main.go. Extra text typed
// after the alias name is appended to the stored prompt separated by a space.
func (m model) runAlias(name, extra string) (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.aliases[name])
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
	m.addBlock(blockUser, prompt)
	m.layout()
	m.syncViewport(true)
	return m, tea.Batch(m.sendTurn(prompt), m.armSpinner())
}
