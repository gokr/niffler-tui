// Slash commands: built-in local commands plus the declarative registry
// components publish via reg.publish (docs/WIRE.md). Core validates the
// spec, checkpoints the merged table to the store (kind "slash", id
// "slash"), and announces ev.catalog.updated on every change — the TUI
// reads store-first, follows the event live, and falls back to the
// catalog snapshot when the checkpoint is missing (older core).
//
// A registered command is a thin alias for a tool call: the TUI parses
// the command line against the declared params (positional values,
// name=value, bare bool flags), then issues a regular svc.<component>.call
// and renders the result as a meta block. Completion is UI-side only:
// Tab completes command names, inline "values" candidates, or fetches
// candidates from the param's source tool (resolved server-side through
// core.invoke so no client-side tool index is needed).
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// ---- wire shapes -----------------------------------------------------------

type slashSource struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	// Field selects the completion value inside each result item when the
	// source tool returns objects (e.g. "nickname" for provider_list).
	Field string `json:"field,omitempty"`
}

type slashParam struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Source      *slashSource    `json:"source"`
	Default     json.RawMessage `json:"default"`
	Values      []string        `json:"values"`
}

type slashCommand struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Component   string       `json:"component"`
	Tool        string       `json:"tool"`
	Params      []slashParam `json:"params"`
	builtin     bool         // UI-local command, not from the registry
}

// slashRegistry is the merged view of built-in and registered commands.
// Built-ins win on name collision (a plugin cannot shadow /help).
type slashRegistry struct {
	commands map[string]slashCommand
	order    []string
}

func newSlashRegistry() slashRegistry {
	reg := slashRegistry{commands: map[string]slashCommand{}}
	for _, cmd := range builtinSlashCommands() {
		reg.add(cmd)
	}
	return reg
}

func (r *slashRegistry) add(cmd slashCommand) {
	if _, taken := r.commands[cmd.Name]; taken {
		return
	}
	r.commands[cmd.Name] = cmd
	r.order = append(r.order, cmd.Name)
}

func (r slashRegistry) lookup(name string) (slashCommand, bool) {
	cmd, ok := r.commands[strings.TrimPrefix(name, "/")]
	return cmd, ok
}

// pluginCommands returns the registered (non-builtin) commands sorted by
// name, for /help.
func (r slashRegistry) pluginCommands() []slashCommand {
	names := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if !r.commands[name].builtin {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]slashCommand, 0, len(names))
	for _, name := range names {
		result = append(result, r.commands[name])
	}
	return result
}

// builtinSlashCommands describes the TUI's local commands for completion
// and /help; the behavior stays in executeLocalCommand. Sources reference
// tool names resolved server-side via core.invoke.
func builtinSlashCommands() []slashCommand {
	return []slashCommand{
		{Name: "provider", Description: "choose the global provider", builtin: true, Params: []slashParam{
			{Name: "nickname", Kind: "string", Description: "provider nickname (or 'environment')",
				Source: &slashSource{Tool: "provider.provider_list", Args: map[string]any{}, Field: "nickname"}},
		}},
		{Name: "providers", Description: "choose the global provider", builtin: true, Params: []slashParam{
			{Name: "nickname", Kind: "string",
				Source: &slashSource{Tool: "provider.provider_list", Args: map[string]any{}, Field: "nickname"}},
		}},
		{Name: "model", Description: "choose this conversation's model", builtin: true, Params: []slashParam{
			{Name: "id", Kind: "string", Description: "model id or 'default'"},
		}},
		{Name: "models", Description: "choose this conversation's model", builtin: true, Params: []slashParam{
			{Name: "id", Kind: "string", Description: "model id or 'default'"},
		}},
		{Name: "connect", Description: "store a provider connection", builtin: true},
		{Name: "status", Description: "show provider/model/context details", builtin: true},
		{Name: "new", Description: "start a new conversation", builtin: true, Params: []slashParam{
			{Name: "id", Kind: "string", Description: "optional conversation id"},
		}},
		{Name: "newsession", Description: "start a new conversation", builtin: true, Params: []slashParam{
			{Name: "id", Kind: "string", Description: "optional conversation id"},
		}},
		{Name: "session", Description: "switch conversation", builtin: true, Params: []slashParam{
			{Name: "id", Kind: "string", Description: "conversation id",
				Source: &slashSource{Tool: "store.list", Args: map[string]any{"kind": "conversation"}, Field: "id"}},
		}},
		{Name: "sessions", Description: "switch conversation", builtin: true, Params: []slashParam{
			{Name: "id", Kind: "string", Description: "conversation id",
				Source: &slashSource{Tool: "store.list", Args: map[string]any{"kind": "conversation"}, Field: "id"}},
		}},
		{Name: "mouse", Description: "wheel scrolling and drag selection", builtin: true, Params: []slashParam{
			{Name: "state", Kind: "enum", Values: []string{"on", "off"}},
		}},
		{Name: "help", Description: "show this help", builtin: true},
		{Name: "?", Description: "show this help", builtin: true},
	}
}

// ---- loading ---------------------------------------------------------------

// slashTableMsg carries the merged slash registry: the store checkpoint
// first, falling back to the catalog snapshot when the checkpoint is
// missing (older core without checkpointing).
type slashTableMsg struct {
	Commands []slashCommand
	Err      error
}

// loadSlashTable loads the slash registry store-first (kind "slash",
// id "slash"), falling back to the live catalog snapshot.
func loadSlashTable(comp *sdk.Component) ([]slashCommand, error) {
	var checkpoint struct {
		OK    bool `json:"ok"`
		Value struct {
			Commands []slashCommand `json:"commands"`
		} `json:"value"`
	}
	err := requestInto(comp, "store", "get", map[string]any{
		"kind": "slash", "id": "slash",
	}, &checkpoint)
	if err == nil && checkpoint.OK {
		return checkpoint.Value.Commands, nil
	}

	// Fallback: derive the table from the live catalog snapshot. This is
	// the authoritative view; the store read above is just the preferred
	// (single-call) path.
	var snapshot struct {
		Components []struct {
			Name  string         `json:"name"`
			Slash []slashCommand `json:"slash"`
		} `json:"components"`
	}
	if err := requestInto(comp, "core", "catalog", map[string]any{"op": "snapshot"}, &snapshot); err != nil {
		return nil, err
	}
	var commands []slashCommand
	for _, comp := range snapshot.Components {
		for _, cmd := range comp.Slash {
			cmd.Component = comp.Name
			commands = append(commands, cmd)
		}
	}
	return commands, nil
}

func loadSlashTableCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		commands, err := loadSlashTable(comp)
		return slashTableMsg{Commands: commands, Err: err}
	}
}

// catalogUpdatedMsg signals ev.catalog.updated: components registered or
// departed, so the slash registry (and its store checkpoint) changed.
type catalogUpdatedMsg struct{}

func (m *model) mergeSlashRegistry(commands []slashCommand) {
	reg := newSlashRegistry()
	for _, cmd := range commands {
		if cmd.Component == "" || cmd.Tool == "" || cmd.Name == "" {
			continue // core-validated, but never trust the wire blindly
		}
		reg.add(cmd)
	}
	m.slash = reg
}

// ---- completion ------------------------------------------------------------

// slashCompleteState is the Tab-completion UI: the partial word being
// completed, the candidates, and the highlighted one. Rendered as a dim
// line above the input; Tab cycles, anything else dismisses.
type slashCompleteState struct {
	active     bool
	loading    bool
	prefix     string   // input text before the token being completed
	token      string   // partial word being completed
	candidates []string // full completion strings for the token
	index      int      // highlighted candidate
}

func (m *model) dismissSlashComplete() {
	m.slashComp = slashCompleteState{}
	m.layout()
}

// handleSlashTab processes Tab in chat mode while the input starts with
// "/". It advances the completion: first press computes and shows
// candidates (filling the first), further presses cycle. A single
// candidate fills immediately without a list. Returns whether the key was
// consumed (false: the textarea owns the Tab).
func (m model) handleSlashTab(backward bool) (model, tea.Cmd) {
	value := m.input.Value()
	if !strings.HasPrefix(strings.TrimSpace(value), "/") {
		return m, nil
	}
	if m.slashComp.active {
		if len(m.slashComp.candidates) == 0 {
			return m, nil
		}
		if backward {
			m.slashComp.index = (m.slashComp.index - 1 + len(m.slashComp.candidates)) % len(m.slashComp.candidates)
		} else {
			m.slashComp.index = (m.slashComp.index + 1) % len(m.slashComp.candidates)
		}
		m.applySlashCandidate()
		return m, nil
	}

	prefix, token, candidates, source, loading := m.slashCandidates(value)
	if loading {
		// Value candidates come from a source tool; show a placeholder
		// while the request is in flight.
		m.slashComp = slashCompleteState{active: true, loading: true, prefix: prefix, token: token}
		m.layout()
		return m, m.slashSourceCmd(source, token)
	}
	if len(candidates) == 0 {
		return m, nil
	}
	filtered := filterSlashCandidates(candidates, token)
	if len(filtered) == 0 {
		return m, nil
	}
	if len(filtered) == 1 {
		// A single candidate: fill it directly, no list.
		m.input.SetValue(prefix + filtered[0])
		m.input.CursorEnd()
		return m, nil
	}
	m.slashComp = slashCompleteState{active: true, prefix: prefix, token: token, candidates: filtered}
	m.applySlashCandidate()
	m.layout()
	return m, nil
}

func (m *model) applySlashCandidate() {
	if m.slashComp.index < 0 || m.slashComp.index >= len(m.slashComp.candidates) {
		return
	}
	m.input.SetValue(m.slashComp.prefix + m.slashComp.candidates[m.slashComp.index])
	m.input.CursorEnd()
}

func filterSlashCandidates(candidates []string, token string) []string {
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, token) {
			out = append(out, c)
		}
	}
	return out
}

// slashCandidates computes what a Tab press should complete for the
// current input: the command word, or an argument of a known command.
// It returns the text before the token being completed (prefix), the
// partial token, and either inline candidates or a source to fetch
// them from (loading = source fetch needed).
func (m model) slashCandidates(value string) (prefix, token string, candidates []string, source *slashSource, loading bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return value, "", nil, nil, false
	}
	word := fields[0]
	if len(fields) == 1 && !strings.HasSuffix(value, " ") {
		// Completing the command name itself.
		partial := strings.TrimPrefix(word, "/")
		names := make([]string, 0, len(m.slash.order))
		names = append(names, m.slash.order...)
		sort.Strings(names)
		return "/", partial, names, nil, false
	}

	cmd, ok := m.slash.lookup(word)
	if !ok {
		return "", "", nil, nil, false
	}
	// Complete an argument: the current token is the trailing partial word
	// (empty when the user just typed a space). Walk the preceding fields
	// to find which declared param this token addresses.
	consumed := map[string]bool{}
	positional := 0
	for _, f := range fields[1:] {
		if name, _, named := strings.Cut(f, "="); named {
			consumed[name] = true
		} else if p, isBool := m.slashParamByName(cmd, f); isBool && p.Kind == "bool" {
			consumed[f] = true // bare bool flag
		} else {
			positional++
		}
	}
	// The current token: trailing text after the last space. Completing a
	// fresh argument (trailing space) advances one positional slot.
	lastSpace := strings.LastIndex(value, " ")
	if lastSpace < 0 {
		return "", "", nil, nil, false
	}
	token = value[lastSpace+1:]
	prefix = value[:lastSpace+1]
	if token == "" && strings.HasSuffix(value, " ") {
		positional++
	}

	// Positional: the n-th (1-based) non-bool param.
	for _, p := range cmd.Params {
		if p.Kind == "bool" {
			continue
		}
		if positional == 1 {
			if p.Source != nil {
				return prefix, token, nil, p.Source, true
			}
			if len(p.Values) > 0 {
				return prefix, token, p.Values, nil, false
			}
			return "", "", nil, nil, false
		}
		positional--
	}
	return "", "", nil, nil, false
}

func (m model) slashParamByName(cmd slashCommand, name string) (slashParam, bool) {
	for _, p := range cmd.Params {
		if p.Name == name {
			return p, true
		}
	}
	return slashParam{}, false
}

// slashSourceMsg carries completion values fetched from a param's source
// tool. Stale results (the user kept typing, or restarted completion) are
// dropped by matching the token they were requested for.
type slashSourceMsg struct {
	Token  string
	Values []string
	Err    error
}

// applySlashSource folds a fetched completion-value list into the active
// completion state: a single candidate fills directly, several open the
// candidate list with the first highlighted, errors dismiss with a note.
func (m *model) applySlashSource(msg slashSourceMsg) {
	if !m.slashComp.active || m.slashComp.token != msg.Token {
		return // stale result for an abandoned completion
	}
	m.slashComp.loading = false
	if msg.Err != nil {
		m.contextNote = msg.Err.Error()
		m.dismissSlashComplete()
		return
	}
	sort.Strings(msg.Values) // deterministic cycling regardless of source order
	m.slashComp.candidates = filterSlashCandidates(msg.Values, msg.Token)
	if len(m.slashComp.candidates) == 0 {
		m.dismissSlashComplete()
		return
	}
	if len(m.slashComp.candidates) == 1 {
		m.input.SetValue(m.slashComp.prefix + m.slashComp.candidates[0])
		m.input.CursorEnd()
		m.dismissSlashComplete()
		return
	}
	m.slashComp.index = 0
	m.applySlashCandidate()
}

func (m model) slashSourceCmd(source *slashSource, token string) tea.Cmd {
	return func() tea.Msg {
		var raw json.RawMessage
		err := requestInto(m.comp, "core", "invoke", map[string]any{
			"tool":      source.Tool,
			"arguments": source.Args,
		}, &raw)
		if err != nil {
			return slashSourceMsg{Token: token, Err: err}
		}
		return slashSourceMsg{Token: token, Values: extractCompletionValues(raw, source.Field)}
	}
}

// extractCompletionValues turns a source tool's result into candidate
// strings: an object's items (id or a chosen field), an array of strings
// or {id} objects, a plain value, or nothing.
func extractCompletionValues(raw json.RawMessage, field string) []string {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil
	}
	var out []string
	add := func(v string) {
		if v != "" {
			out = append(out, v)
		}
	}
	switch n := node.(type) {
	case []any:
		for _, item := range n {
			switch it := item.(type) {
			case string:
				add(it)
			case map[string]any:
				if field != "" {
					if v, ok := it[field].(string); ok {
						add(v)
					}
				} else if v, ok := it["id"].(string); ok {
					add(v)
				}
			}
		}
	case map[string]any:
		if items, ok := n["items"].([]any); ok {
			return extractCompletionValues(mustJSON(items), field)
		}
		if field != "" {
			if v, ok := n[field].(string); ok {
				add(v)
			}
		}
	case string:
		add(n)
	}
	sort.Strings(out)
	return out
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

// slashCompletionView renders the candidate list above the input, like
// the reverse-i-search line: dim candidates, active one highlighted.
func (m model) slashCompletionView() string {
	state := m.slashComp
	if state.loading {
		return metaStyle.Render(truncate(t(m.loc, "slash.completing", state.token), max(1, m.width-1)))
	}
	if !state.active || len(state.candidates) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range state.candidates {
		if i > 0 {
			b.WriteString("  ")
		}
		if i == state.index {
			b.WriteString(activeSlashStyle.Render(c))
		} else {
			b.WriteString(metaStyle.Render(c))
		}
	}
	return metaStyle.Render("▸ ") + truncate(b.String(), max(1, m.width-3))
}

// ---- execution -------------------------------------------------------------

// executeSlashCommand runs a registered slash command: parse the command
// line against the declared params and issue the target tool call. Errors
// are rendered as error blocks; the result lands as a meta block.
func (m model) executeSlashCommand(cmd slashCommand, rawArgs string) (tea.Model, tea.Cmd) {
	args, err := parseSlashArgs(cmd, rawArgs)
	if err != nil {
		m.addBlock(blockError, "/"+cmd.Name+": "+err.Error())
		m.syncViewport(true)
		return m, nil
	}
	m.addBlock(blockMeta, "→ /"+cmd.Name+slashArgText(rawArgs))
	m.syncViewport(true)
	return m, m.slashCallCmd(cmd, args)
}

// slashArgText renders the argument part of the "→ /cmd …" meta line with
// a separating space when arguments are present.
func slashArgText(rawArgs string) string {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" {
		return ""
	}
	return " " + trimmed
}

func (m model) slashCallCmd(cmd slashCommand, args map[string]any) tea.Cmd {
	return func() tea.Msg {
		var result json.RawMessage
		err := requestInto(m.comp, cmd.Component, cmd.Tool, args, &result)
		return slashResultMsg{Name: cmd.Name, Result: result, Err: err}
	}
}

// slashResultMsg carries a registered command's tool result for display.
type slashResultMsg struct {
	Name   string
	Result json.RawMessage
	Err    error
}

func (m *model) applySlashResult(msg slashResultMsg) {
	if msg.Err != nil {
		m.addBlock(blockError, "/"+msg.Name+" failed: "+msg.Err.Error())
		m.syncViewport(true)
		return
	}
	m.addBlock(blockMeta, "/"+msg.Name+" → "+compactJSON(msg.Result))
	m.syncViewport(true)
}

// parseSlashArgs builds the tool-call arguments object from a command
// line: positional values fill non-bool params in declaration order,
// name=value sets params by name, a bare bool-param name is a true flag.
// Declared defaults apply to params the user omitted.
func parseSlashArgs(cmd slashCommand, rawArgs string) (map[string]any, error) {
	args := map[string]any{}
	byName := map[string]slashParam{}
	for _, p := range cmd.Params {
		byName[p.Name] = p
	}
	positional := 0
	nextPositional := func() (slashParam, bool) {
		for positional < len(cmd.Params) {
			p := cmd.Params[positional]
			positional++
			if p.Kind != "bool" {
				return p, true
			}
		}
		return slashParam{}, false
	}
	for _, tok := range strings.Fields(rawArgs) {
		var name, val string
		if i := strings.Index(tok, "="); i >= 0 {
			name, val = tok[:i], tok[i+1:]
			if _, ok := byName[name]; !ok {
				return nil, fmt.Errorf("unknown argument %q", name)
			}
		} else if p, ok := byName[tok]; ok && p.Kind == "bool" {
			name, val = tok, "true"
		} else {
			p, ok := nextPositional()
			if !ok {
				return nil, fmt.Errorf("too many arguments (declared: %d)", len(cmd.Params))
			}
			name, val = p.Name, tok
		}
		p := byName[name]
		value, err := coerceSlashValue(p, val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		args[p.Name] = value
	}
	for _, p := range cmd.Params {
		if _, ok := args[p.Name]; ok || len(p.Default) == 0 {
			continue
		}
		var def any
		if err := json.Unmarshal(p.Default, &def); err == nil {
			args[p.Name] = def
		}
	}
	return args, nil
}

func coerceSlashValue(p slashParam, val string) (any, error) {
	switch p.Kind {
	case "bool":
		switch strings.ToLower(val) {
		case "true", "on", "yes", "1":
			return true, nil
		case "false", "off", "no", "0":
			return false, nil
		}
		return nil, fmt.Errorf("expected on/off")
	case "int":
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("expected a number")
		}
		return n, nil
	default:
		if len(p.Values) > 0 && !containsStr(p.Values, val) {
			return nil, fmt.Errorf("expected one of %s", strings.Join(p.Values, ", "))
		}
		return val, nil
	}
}

func containsStr(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// suggestSlash builds a "did you mean" hint from registry names: prefix
// matches first, then near-misses by edit distance (typos like /deply).
// Empty when nothing is close.
func suggestSlash(reg slashRegistry, name string) string {
	var matches []string
	seen := map[string]bool{}
	add := func(candidate string) {
		if !seen[candidate] && len(matches) < 3 {
			seen[candidate] = true
			matches = append(matches, candidate)
		}
	}
	for _, candidate := range reg.order {
		if strings.HasPrefix(candidate, name) {
			add(candidate)
		}
	}
	if len(matches) < 3 {
		for _, candidate := range reg.order {
			if candidate != name && editDistance(candidate, name) <= 2 {
				add(candidate)
			}
		}
	}
	if len(matches) == 0 {
		return " (try /help)"
	}
	for i := range matches {
		matches[i] = "/" + matches[i]
	}
	return " — did you mean " + strings.Join(matches, ", ")
}

// editDistance is the classic Levenshtein distance (insert/delete/substitute
// at cost 1). Small command names make this cheap.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
