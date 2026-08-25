package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPluginManifestIncludesAllProductionGoSources(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "niffler.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Components []struct {
			Name    string   `json:"name"`
			Main    string   `json:"main"`
			Sources []string `json:"sources"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	mainFile := ""
	for _, component := range manifest.Components {
		if component.Name != "tui" {
			continue
		}
		mainFile = filepath.Base(component.Main)
		for _, source := range component.Sources {
			declared[filepath.Base(source)] = true
		}
	}
	if mainFile != "main.go" {
		t.Fatalf("plugin main = %q", mainFile)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || name == mainFile {
			continue
		}
		if !declared[name] {
			t.Errorf("production source %s is missing from niffler.json sources", name)
		}
		delete(declared, name)
	}
	for stale := range declared {
		t.Errorf("niffler.json names missing production source %s", stale)
	}
}

func TestSanitizeSessionID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "game", want: "game"},
		{name: "preserves allowed separators", in: "game_dev-2", want: "game_dev-2"},
		{name: "replaces subject punctuation", in: "game.dev/web", want: "game-dev-web"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeSessionID(test.in); got != test.want {
				t.Fatalf("sanitizeSessionID(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestStreamingBlocksStayStable(t *testing.T) {
	m := model{
		session:      "game",
		viewport:     viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		assistantIdx: -1,
		thinkingIdx:  -1,
	}

	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "think ", Content: "Hello",
	}})
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "more", Content: " world",
	}})

	if len(m.blocks) != 2 {
		t.Fatalf("got %d blocks, want thinking + assistant", len(m.blocks))
	}
	if got := m.blocks[0].text; got != "think more" {
		t.Fatalf("thinking block = %q", got)
	}
	if got := m.blocks[1].text; got != "Hello world" {
		t.Fatalf("assistant block = %q", got)
	}

	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "Hello world!",
	}})
	if got := m.blocks[1].text; got != "Hello world!" {
		t.Fatalf("final assistant block = %q", got)
	}
	m.finishTurn("Hello world!", "")
	if len(m.blocks) != 2 {
		t.Fatalf("request reply duplicated the done event: got %d blocks", len(m.blocks))
	}

	m.applySessionEvent(sessionEventMsg{kind: "toolcall", event: sessionEvent{
		SessionID: "game", Tool: "bash", Args: json.RawMessage(`{"cmd":"make"}`),
	}})
	if m.assistantIdx != -1 || m.thinkingIdx != -1 {
		t.Fatal("tool call did not close the current LLM round")
	}
}

func TestAddHistory(t *testing.T) {
	m := model{}
	m.addHistory("hello")
	m.addHistory("hello") // consecutive duplicate skipped
	m.addHistory("")
	if len(m.history) != 1 {
		t.Fatalf("history = %v, want 1 entry", m.history)
	}
	m.addHistory("world")
	if len(m.history) != 2 {
		t.Fatalf("history = %v, want 2 entries", m.history)
	}
	if m.history[0] != "hello" || m.history[1] != "world" {
		t.Fatalf("history order wrong: %v", m.history)
	}
}

func TestHistoryNavigation(t *testing.T) {
	m := model{input: textarea.New(), history: []string{"one", "two"}, histIdx: -1}
	m.input.SetValue("draft text")

	m.histPrev()
	if got := m.input.Value(); got != "two" {
		t.Fatalf("after first up: input = %q, want %q", got, "two")
	}
	if m.histIdx != 1 {
		t.Fatalf("histIdx = %d, want 1", m.histIdx)
	}

	m.histPrev()
	if got := m.input.Value(); got != "one" {
		t.Fatalf("after second up: input = %q, want %q", got, "one")
	}

	m.histPrev() // already at the oldest entry
	if got := m.input.Value(); got != "one" {
		t.Fatalf("up at oldest entry changed input to %q", got)
	}

	m.histNext()
	if got := m.input.Value(); got != "two" {
		t.Fatalf("after down: input = %q, want %q", got, "two")
	}

	m.histNext() // past the newest entry: back to the draft
	if got := m.input.Value(); got != "draft text" {
		t.Fatalf("after down past newest: input = %q, want draft %q", got, "draft text")
	}
	if m.histIdx != -1 {
		t.Fatalf("histIdx = %d after returning to draft, want -1", m.histIdx)
	}

	m.histNext() // no-op in draft mode
	if m.histIdx != -1 {
		t.Fatalf("histNext in draft mode changed histIdx to %d", m.histIdx)
	}
}

func TestHistorySearchFindAndCycle(t *testing.T) {
	m := model{
		input:   textarea.New(),
		history: []string{"build the server", "run the tests", "build the ui"},
		histIdx: -1,
	}
	m.input.SetValue("")
	m.searchStart()
	if !m.searchActive {
		t.Fatal("searchStart did not activate search")
	}

	// Typing a query finds the most recent match, case-insensitively.
	m.searchQuery = "BUILD"
	m.searchIdx = len(m.history)
	if !m.searchFindPrev() {
		t.Fatal("expected a match for BUILD")
	}
	if m.searchIdx != 2 || m.input.Value() != "build the ui" {
		t.Fatalf("first match = idx %d value %q, want idx 2 %q", m.searchIdx, m.input.Value(), "build the ui")
	}

	// Ctrl+R again moves to the next older match.
	if !m.searchFindPrev() {
		t.Fatal("expected an older match for BUILD")
	}
	if m.searchIdx != 0 || m.input.Value() != "build the server" {
		t.Fatalf("older match = idx %d value %q, want idx 0 %q", m.searchIdx, m.input.Value(), "build the server")
	}

	// No older match.
	if m.searchFindPrev() {
		t.Fatal("expected no older match for BUILD")
	}
	if m.searchIdx != -1 {
		t.Fatalf("searchIdx = %d after exhausting matches, want -1", m.searchIdx)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input after exhausting matches = %q, want draft", got)
	}
}

func TestHistorySearchAcceptAndCancel(t *testing.T) {
	m := model{input: textarea.New(), history: []string{"alpha", "beta"}, histIdx: -1}
	m.input.SetValue("")
	m.searchStart()
	m.searchQuery = "alp"
	m.searchIdx = len(m.history)
	m.searchFindPrev()
	m.searchAccept()
	if m.searchActive {
		t.Fatal("searchAccept left search active")
	}
	if m.histIdx != 0 {
		t.Fatalf("histIdx = %d after accepting match, want 0", m.histIdx)
	}
	if got := m.input.Value(); got != "alpha" {
		t.Fatalf("input = %q after accept, want %q", got, "alpha")
	}
	// Down from the accepted entry walks forward to the draft.
	m.histNext()
	if got := m.input.Value(); got != "beta" {
		t.Fatalf("input = %q after down, want %q", got, "beta")
	}
	m.histNext()
	if got := m.input.Value(); got != "" {
		t.Fatalf("input = %q after down to draft, want empty", got)
	}

	m2 := model{input: textarea.New(), history: []string{"alpha"}, histIdx: -1}
	m2.input.SetValue("my draft")
	m2.searchStart()
	m2.searchQuery = "al"
	m2.searchIdx = len(m2.history)
	m2.searchFindPrev()
	m2.searchCancel()
	if m2.searchActive {
		t.Fatal("searchCancel left search active")
	}
	if got := m2.input.Value(); got != "my draft" {
		t.Fatalf("input = %q after cancel, want draft %q", got, "my draft")
	}
	if m2.histIdx != -1 {
		t.Fatalf("histIdx = %d after cancel, want -1", m2.histIdx)
	}

	m3 := model{input: textarea.New(), history: []string{"alpha"}, histIdx: -1}
	m3.input.SetValue("draft")
	m3.searchStart()
	m3.searchQuery = "no match"
	m3.searchIdx = len(m3.history)
	if m3.searchFindPrev() {
		t.Fatal("unexpected search match")
	}
	if got := m3.input.Value(); got != "draft" {
		t.Fatalf("no-match search left stale input %q", got)
	}
	m3.searchAccept()
	if got := m3.input.Value(); got != "draft" || m3.histIdx != -1 {
		t.Fatalf("accept no-match = value:%q idx:%d", got, m3.histIdx)
	}
}

func TestMarkdownRendering(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "dark")
	m := model{
		input:    textarea.New(),
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
	}
	m.width = 80
	m.height = 24
	m.layout() // builds the glamour renderer

	m.addBlock(blockAssistant, "# Title\n\n**bold** and `code`\n\n```go\nfunc main() {}\n```")
	m.addBlock(blockUser, "plain user text")

	out := m.renderTranscript()
	if !strings.Contains(out, "niffler>") {
		t.Fatal("transcript missing assistant label")
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("expected ANSI-styled markdown output from glamour")
	}
	if !strings.Contains(out, "plain user text") {
		t.Fatal("user block missing from transcript")
	}
	if !strings.Contains(stripANSI(out), "func main()") {
		t.Fatal("fenced code block missing from rendered transcript")
	}

	// The cached render is reused unchanged on the next pass.
	again := m.renderTranscript()
	if again != out {
		t.Fatal("second render differs from cached render")
	}
}

func TestMarkdownBlockCacheInvalidatedOnAppend(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "dark")
	m := model{
		input:    textarea.New(),
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
	}
	m.width = 80
	m.height = 24
	m.layout()

	m.addBlock(blockAssistant, "first")
	m.renderTranscript()
	if !m.blocks[0].renderedOK {
		t.Fatal("block was not cached after render")
	}
	m.blocks[0].text += " and more"
	m.renderTranscript()
	if !m.blocks[0].renderedOK {
		t.Fatal("block cache flag lost after re-render")
	}
	// Glamour wraps each styled span in ANSI codes, so strip them before
	// checking for the appended text.
	if !strings.Contains(stripANSI(m.blocks[0].rendered), "and more") {
		t.Fatalf("cached render stale after append: %q", m.blocks[0].rendered)
	}
}

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) {
					c := s[j]
					if (c >= '0' && c <= '9') || c == ';' || c == ':' || c == '?' || c == '<' || c == '=' || c == '>' {
						j++
						continue
					}
					if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
						j++
					}
					break
				}
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// newTestModel builds a model with properly constructed input and viewport
// components and the same keymaps as newModel.
func newTestModel() model {
	input := textarea.New()
	input.Prompt = "> "
	input.DynamicHeight = true
	input.MaxHeight = maxInputHeight
	input.ShowLineNumbers = false
	_ = input.Focus()
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	configureKeymaps(&input, &view)
	return model{
		input:        input,
		viewport:     view,
		assistantIdx: -1,
		thinkingIdx:  -1,
		histIdx:      -1,
		searchIdx:    -1,
	}
}

func TestMultilineEditingAndSend(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("first line")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	m = updated.(model)
	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("Alt+Enter value = %q, want trailing newline", got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = updated.(model)
	if got := m.input.Value(); got != "first line\n\n" {
		t.Fatalf("Ctrl+J value = %q, want second newline", got)
	}

	m = newTestModel()
	m.connected = true
	m.input.SetValue("send me")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("input after send = %q, want empty", got)
	}
	if !m.busy || len(m.blocks) != 1 || m.blocks[0].kind != blockUser || m.blocks[0].text != "send me" {
		t.Fatalf("send state = busy:%v blocks:%#v", m.busy, m.blocks)
	}
	if len(m.history) != 1 || m.history[0] != "send me" {
		t.Fatalf("history after send = %#v", m.history)
	}
}

func TestWrappedInputEdges(t *testing.T) {
	m := newTestModel()
	m.width = 14 // 12 columns after the prompt allowance in layout.
	m.height = 24
	m.layout()
	m.history = []string{"history item"}
	m.input.SetValue(strings.Repeat("x", 40))

	if info := m.input.LineInfo(); info.Height < 2 {
		t.Fatalf("test input did not soft-wrap: %#v", info)
	}
	if m.inputAtTop() {
		t.Fatal("cursor at end of wrapped line reported at top")
	}
	if !m.inputAtBottom() {
		t.Fatal("cursor at end of wrapped line not reported at bottom")
	}

	m.input.SetCursorColumn(0)
	if !m.inputAtTop() || m.inputAtBottom() {
		t.Fatalf("wrapped start edge = top:%v bottom:%v", m.inputAtTop(), m.inputAtBottom())
	}

	// Down at the first visual row moves within the wrapped input instead of
	// being stolen by history navigation.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(model)
	if m.histIdx != -1 || m.input.LineInfo().RowOffset == 0 {
		t.Fatalf("Down did not move within wrapped input: idx=%d info=%#v", m.histIdx, m.input.LineInfo())
	}

	// Return to the visual top; Up there invokes history.
	m.input.SetCursorColumn(0)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(model)
	if got := m.input.Value(); got != "history item" || m.histIdx != 0 {
		t.Fatalf("Up at visual top = value:%q idx:%d", got, m.histIdx)
	}
}

func TestViewportKeyRouting(t *testing.T) {
	m := newTestModel()

	press := func(keystroke string) tea.KeyPressMsg {
		switch keystroke {
		case "ctrl+up":
			return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl}
		case "ctrl+down":
			return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl}
		case "pgup":
			return tea.KeyPressMsg{Code: tea.KeyPgUp}
		case "pgdown":
			return tea.KeyPressMsg{Code: tea.KeyPgDown}
		case "up":
			return tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			return tea.KeyPressMsg{Code: tea.KeyDown}
		case "left":
			return tea.KeyPressMsg{Code: tea.KeyLeft}
		case "right":
			return tea.KeyPressMsg{Code: tea.KeyRight}
		case "enter":
			return tea.KeyPressMsg{Code: tea.KeyEnter}
		case "home":
			return tea.KeyPressMsg{Code: tea.KeyHome}
		case "end":
			return tea.KeyPressMsg{Code: tea.KeyEnd}
		case "ctrl+u":
			return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
		case "ctrl+d":
			return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
		case "alt+enter":
			return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
		case "space":
			return tea.KeyPressMsg{Text: " "}
		default:
			return tea.KeyPressMsg{Text: keystroke}
		}
	}

	for _, s := range []string{"ctrl+up", "ctrl+down", "pgup", "pgdown"} {
		if !m.isViewportKey(press(s)) {
			t.Fatalf("viewport key %q not routed to viewport", s)
		}
	}
	// Editing keys, including ones the viewport also binds by default,
	// must stay with the textarea.
	for _, s := range []string{"up", "down", "left", "right", "enter", "k", "j", "f", "b", "u", "d", "h", "l", "space", "ctrl+u", "ctrl+d", "home", "end", "alt+enter", "a"} {
		if m.isViewportKey(press(s)) {
			t.Fatalf("editing key %q routed to viewport", s)
		}
	}
}

func TestMarkdownRenderDeferredWhileStreaming(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "dark")
	m := newTestModel()
	m.width = 80
	m.height = 24
	m.layout() // builds the glamour renderer

	m.addBlock(blockAssistant, "# Title\n\n**bold**")
	out := m.renderBlock(0)
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("expected styled markdown after settle")
	}
	if !m.blocks[0].renderedOK {
		t.Fatal("block not cached after render")
	}

	// A token arrives: output is still streaming, so the render is deferred
	// and the block shows as plain text until the settle tick fires.
	m.blocks[0].text += " and `code`"
	m.lastTokenAt = time.Now()
	m.streaming = true
	if got := m.renderBlock(0); strings.Contains(got, "\x1b[") || got != m.blocks[0].text {
		t.Fatalf("streaming render should be plain text, got %q", got)
	}

	// The settle tick has fired: the block renders as markdown again.
	m.streaming = false
	out = m.renderBlock(0)
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("expected styled markdown after settle tick")
	}
	if !strings.Contains(stripANSI(out), "and") {
		t.Fatalf("settled render missing streamed content: %q", out)
	}
}

func TestHistoryFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")

	appendHistoryEntry(path, "hello")
	appendHistoryEntry(path, "multi\nline message")
	appendHistoryEntry(path, "multi\nline message") // consecutive duplicate
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not-json\n\n")
	_ = f.Close()

	got := loadHistory(path)
	if len(got) != 2 || got[0] != "hello" || got[1] != "multi\nline message" {
		t.Fatalf("round trip/cleanup = %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history mode = %v, want 0600", info.Mode().Perm())
	}

	// Loading more than maxHistory entries trims the file to the newest.
	for i := 0; i < maxHistory+5; i++ {
		appendHistoryEntry(path, "entry"+strings.Repeat("x", i))
	}
	got = loadHistory(path)
	if len(got) != maxHistory {
		t.Fatalf("trimmed history has %d entries, want %d", len(got), maxHistory)
	}
	if got[0] != "entry"+strings.Repeat("x", 5) {
		t.Fatalf("trimmed history kept wrong entries: first = %q", got[0])
	}

	// Missing file loads as empty history.
	if got := loadHistory(filepath.Join(t.TempDir(), "nope.jsonl")); len(got) != 0 {
		t.Fatalf("missing file loaded %d entries", len(got))
	}
}

func TestTruncateDisplayWidthSafe(t *testing.T) {
	if got := truncate("héllo wörld", 6); got != "héllo…" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncate("wide界text", 6); got != "wide…" {
		t.Fatalf("wide truncate = %q", got)
	}
}

func TestRuntimeStatusAndContextUsage(t *testing.T) {
	m := newTestModel()
	m.applySessionEvent(sessionEventMsg{kind: "status", event: sessionEvent{
		SessionID: "", Provider: "deepseek", ProviderSource: "store",
		Model: "deepseek-v4-pro", Context: 1_000_000,
		ContextSource: "catalog", UsedTokens: 250_000,
	}})
	if m.runtime.Provider != "deepseek" || m.runtime.Model != "deepseek-v4-pro" || m.runtime.Context != 1_000_000 {
		t.Fatalf("runtime = %#v", m.runtime)
	}
	if m.contextUsed != 250_000 || contextPercent(m.contextUsed, m.runtime.Context) != 0.25 {
		t.Fatalf("context = used:%d limit:%d", m.contextUsed, m.runtime.Context)
	}
	line := runtimeStatusLine(m.runtime, "deepseek-v4-pro", m.contextUsed, 80)
	if ansi.StringWidth(line) > 79 || !strings.Contains(ansi.Strip(line), "25%") {
		t.Fatalf("runtime line width/content = %d %q", ansi.StringWidth(line), ansi.Strip(line))
	}

	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		Provider: "deepseek", Model: "deepseek-v4-pro", Context: 1_000_000,
		Usage:   usageStats{PromptTokens: 300_000, CompletionTokens: 25_000, TotalTokens: 325_000},
		Content: "done",
	}})
	if m.promptTokens != 300_000 || m.contextUsed != 325_000 {
		t.Fatalf("assistant usage = prompt:%d used:%d", m.promptTokens, m.contextUsed)
	}
}

func TestProviderFormMasksSecretAndValidates(t *testing.T) {
	template := catalogProvider{ID: "deepseek", Name: "DeepSeek", API: "https://api.deepseek.com"}
	form := newProviderForm(&template, runtimeResolution{Catalog: "deepseek", Model: "deepseek-chat"}, 80)
	form.inputs[providerFieldAPIKey].SetValue("sk-super-secret")
	view := form.view(80)
	if strings.Contains(view, "sk-super-secret") {
		t.Fatal("provider form rendered the API key")
	}
	values, err := form.values()
	if err != nil {
		t.Fatal(err)
	}
	if values.Nickname != "deepseek" || values.Catalog != "deepseek" || values.Model != "deepseek-chat" {
		t.Fatalf("form values = %#v", values)
	}
}

func TestPasteRoutesToProviderFormNotChatInput(t *testing.T) {
	m := newTestModel()
	m.mode = modeConnectForm
	m.providerForm = newProviderForm(nil, runtimeResolution{}, 80)
	m.providerForm.focusField(providerFieldAPIKey)

	updated, _ := m.Update(tea.PasteMsg{Content: "sk-pasted-key"})
	got := updated.(model)
	if got.providerForm.inputs[providerFieldAPIKey].Value() != "sk-pasted-key" {
		t.Fatalf("API key field = %q, want sk-pasted-key",
			got.providerForm.inputs[providerFieldAPIKey].Value())
	}
	if m.input.Value() != "" {
		t.Fatalf("chat input absorbed paste = %q", m.input.Value())
	}
}

func TestPasteRoutesToChatInputInChatMode(t *testing.T) {
	m := newTestModel()
	m.mode = modeChat
	updated, _ := m.Update(tea.PasteMsg{Content: "hello pasted"})
	got := updated.(model)
	if got.input.Value() != "hello pasted" {
		t.Fatalf("chat input = %q, want hello pasted", got.input.Value())
	}
}

func TestPasteIgnoredInSelectorMode(t *testing.T) {
	m := newTestModel()
	m.mode = modeProviders
	updated, _ := m.Update(tea.PasteMsg{Content: "ignored"})
	got := updated.(model)
	if got.input.Value() != "" {
		t.Fatalf("chat input absorbed paste in selector mode = %q", got.input.Value())
	}
}

func TestCatalogProviderSelectorOnlyOffersCompatibleTemplates(t *testing.T) {
	items := catalogProviderItems([]catalogProvider{
		{ID: "deepseek", Name: "DeepSeek", NPM: "@ai-sdk/openai-compatible"},
		{ID: "anthropic", Name: "Anthropic", NPM: "@ai-sdk/anthropic"},
		{ID: "openrouter", Name: "OpenRouter", NPM: "@openrouter/ai-sdk-provider"},
	})
	var ids []string
	for _, raw := range items {
		ids = append(ids, raw.(selectorItem).id)
	}
	joined := strings.Join(ids, ",")
	if joined != "__custom__,deepseek,openrouter" {
		t.Fatalf("selector ids = %q", joined)
	}

	// Internal actions are typed, so even an awkward but valid provider
	// nickname cannot be mistaken for the environment action.
	providerItems := providerSelectorItems([]providerSummary{{Nickname: "__environment__"}}, providerStatusResponse{})
	foundProviderCollision := false
	for _, raw := range providerItems {
		item := raw.(selectorItem)
		if item.id == "__environment__" && item.kind == selectorProvider {
			foundProviderCollision = true
		}
	}
	if !foundProviderCollision {
		t.Fatal("provider nickname collided with an internal selector action")
	}
}

func TestStoredProvidersMergeIntoCatalogConnectionState(t *testing.T) {
	m := newTestModel()
	m.providers = []providerSummary{{Nickname: "work", Catalog: "deepseek"}}
	m.catalogProviders = []catalogProvider{{ID: "deepseek"}, {ID: "openrouter"}}
	merged := m.configuredCatalogProviders()
	if !merged[0].Configured || merged[1].Configured {
		t.Fatalf("merged catalog providers = %#v", merged)
	}
}

func TestLocalModelCommandSetsConversationOverride(t *testing.T) {
	m := newTestModel()
	m.connected = true
	updated, cmd := m.executeLocalCommand("/model deepseek-v4-pro")
	got := updated.(model)
	if got.modelOverride != "deepseek-v4-pro" || got.runtime.Model != "deepseek-v4-pro" {
		t.Fatalf("model command state = override:%q runtime:%#v", got.modelOverride, got.runtime)
	}
	if cmd == nil {
		t.Fatal("model command did not schedule effective-context resolution")
	}
}

func TestRuntimeRefreshKeepsSuccessfulResolutionOnOptionalFailure(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(runtimeRefreshedMsg{
		Runtime: runtimeResolution{OK: true, Provider: "deepseek", Model: "deepseek-chat", Context: 1_000_000},
		ListErr: errors.New("provider list temporarily unavailable"),
	})
	got := updated.(model)
	if got.runtime.Provider != "deepseek" || got.runtime.Context != 1_000_000 {
		t.Fatalf("successful runtime resolution was discarded: %#v", got.runtime)
	}
	if !strings.Contains(got.contextNote, "temporarily unavailable") {
		t.Fatalf("partial failure was not surfaced: %q", got.contextNote)
	}
}

func TestProviderChangeRetainsConversationModelAndClearsFormSecret(t *testing.T) {
	m := newTestModel()
	m.modelOverride = "conversation-model"
	m.providerForm = newProviderForm(nil, runtimeResolution{}, 80)
	m.providerForm.inputs[providerFieldAPIKey].SetValue("sk-never-retain")
	updated, _ := m.Update(providerActionMsg{Action: "switch", Nickname: "work"})
	got := updated.(model)
	if got.modelOverride != "conversation-model" {
		t.Fatalf("global provider switch erased session model: %q", got.modelOverride)
	}
	if got.providerForm.inputs[providerFieldAPIKey].Value() != "" {
		t.Fatal("provider action retained the API key in UI state")
	}

	updated, _ = got.Update(providerBusEventMsg{Kind: "switch"})
	got = updated.(model)
	if got.modelOverride != "conversation-model" {
		t.Fatalf("provider event erased session model: %q", got.modelOverride)
	}
}

func TestBootstrapFallsBackToPersistedRuntimeMetadata(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(bootstrapMsg{
		ProviderStatus: providerStatusResponse{Source: "store", Provider: providerSummary{Nickname: "work"}},
		Conversation: conversationState{
			Found: true, ModelOverride: "chosen", Provider: "old-work",
			Model: "chosen", Context: 200_000, ContextUsed: 50_000,
		},
		Warnings: []string{"llm.llm_resolve unavailable"},
	})
	got := updated.(model)
	if got.runtime.Provider != "work" || got.runtime.Model != "chosen" || got.runtime.Context != 200_000 {
		t.Fatalf("persisted runtime fallback = %#v", got.runtime)
	}
	if got.modelOverride != "chosen" || got.contextUsed != 50_000 {
		t.Fatalf("persisted conversation selection/usage lost: %#v", got)
	}
}

func TestModelActionCommitsOrRollsBackSelection(t *testing.T) {
	m := newTestModel()
	m.controlPending = true
	m.modelOverride = "new-model"
	updated, _ := m.Update(modelActionMsg{
		Selected: "new-model", Previous: "old-model",
		Runtime: runtimeResolution{
			OK: true, Provider: "work", ProviderSource: "store",
			Model: "new-model", Catalog: "deepseek", Context: 200_000,
			ContextSource: "catalog",
		},
	})
	got := updated.(model)
	if got.controlPending || got.modelOverride != "new-model" || got.runtime.Model != "new-model" {
		t.Fatalf("successful model action = pending:%v override:%q runtime:%#v", got.controlPending, got.modelOverride, got.runtime)
	}

	got.controlPending = true
	got.modelOverride = "bad-model"
	updated, _ = got.Update(modelActionMsg{
		Selected: "bad-model", Previous: "new-model", Err: errors.New("selection failed"),
	})
	got = updated.(model)
	if got.controlPending || got.modelOverride != "new-model" {
		t.Fatalf("failed model action did not roll back: pending:%v override:%q", got.controlPending, got.modelOverride)
	}
}

func TestStatusWarningAcceptsStringWhileContextWarningUsesBool(t *testing.T) {
	m := newTestModel()
	m.applySessionEvent(sessionEventMsg{kind: "status", event: sessionEvent{
		Warning: json.RawMessage(`"catalog temporarily unavailable"`),
	}})
	if m.contextNote != "catalog temporarily unavailable" {
		t.Fatalf("status warning = %q", m.contextNote)
	}
	m.runtime.Context = 100
	m.contextUsed = 80
	m.applySessionEvent(sessionEventMsg{kind: "context", event: sessionEvent{
		Warning: json.RawMessage(`true`),
	}})
	if !strings.Contains(m.contextNote, "80%") {
		t.Fatalf("context warning = %q", m.contextNote)
	}
}
