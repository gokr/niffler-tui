package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"regexp"
)

// blankLineRe matches whitespace-only lines (lipgloss pads empty lines
// inside styled blocks with spaces) so structural tests can normalize them.
var blankLineRe = regexp.MustCompile(`(?m)^[ \t]+$`)

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
	if !strings.Contains(stripANSI(out), "Title") {
		t.Fatal("transcript missing assistant markdown content")
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
	// Production appends go through the streaming path, which marks the
	// transcript cache dirty; the block cache also keys on the text itself
	// and must re-render on the changed value.
	idx := 0
	m.appendStreamingText(blockAssistant, &idx, " and more")
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
		// Mirror the production default (newModel): tracking on so the wheel
		// scrolls the transcript and click expands tool cards.
		mouse: true,
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

func TestSteerWhileBusy(t *testing.T) {
	m := newTestModel()
	m.connected = true
	m.busy = true
	m.input.SetValue("steer me now")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("input after steer = %q, want empty", got)
	}
	if !m.busy {
		t.Fatal("steer cleared busy; the turn is still working until done")
	}
	want := "Steer: steer me now"
	if len(m.blocks) != 1 || m.blocks[0].kind != blockUser || m.blocks[0].text != want {
		t.Fatalf("blocks after steer = %#v, want one block with %q", m.blocks, want)
	}
	// Busy-Enter must not be treated as a fresh send: no history entry.
	if len(m.history) != 0 {
		t.Fatalf("steer added a history entry: %#v", m.history)
	}
}

func TestSteerClearedByDone(t *testing.T) {
	m := newTestModel()
	m.connected = true
	m.busy = true
	m.applySessionEvent(sessionEventMsg{kind: "done", event: sessionEvent{SessionID: "game", Reply: "final"}})
	if m.busy {
		t.Fatal("done did not clear busy")
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
	line := runtimeStatusLine(LocaleEN, m.runtime, "deepseek-v4-pro", m.contextUsed, 80)
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
	form := newProviderForm(&template, runtimeResolution{Catalog: "deepseek", Model: "deepseek-chat"}, 80, LocaleEN)
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

func TestEditProviderFormPrefillsAndAllowsEmptyKey(t *testing.T) {
	form := newEditProviderForm(providerSummary{
		Nickname: "deepseek", BaseURL: "https://api.deepseek.com",
		Catalog: "deepseek", Model: "deepseek-chat", Context: 65536,
	}, 80, LocaleEN)
	if form.inputs[providerFieldNickname].Value() != "deepseek" {
		t.Fatal("nickname not prefilled")
	}
	if form.inputs[providerFieldBaseURL].Value() != "https://api.deepseek.com" {
		t.Fatal("base URL not prefilled")
	}
	if form.inputs[providerFieldModel].Value() != "deepseek-chat" {
		t.Fatal("model not prefilled")
	}
	if form.inputs[providerFieldContext].Value() != "65536" {
		t.Fatalf("context = %q, want 65536", form.inputs[providerFieldContext].Value())
	}
	if !form.edit {
		t.Fatal("edit form not flagged as edit")
	}
	// No API key required for an edit (stored credential is preserved).
	values, err := form.values()
	if err != nil {
		t.Fatal(err)
	}
	if values.Nickname != "deepseek" || values.APIKey != "" {
		t.Fatalf("edit values = %#v", values)
	}
	// An explicitly-entered key is kept for rotation.
	form.inputs[providerFieldAPIKey].SetValue("sk-new")
	values, err = form.values()
	if err != nil {
		t.Fatal(err)
	}
	if values.APIKey != "sk-new" {
		t.Fatalf("rotated key = %q", values.APIKey)
	}
}

func TestProviderSelectorEditAndDeleteKeys(t *testing.T) {
	m := newTestModel()
	m.providers = []providerSummary{{Nickname: "deepseek", Active: true, BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"}}
	m.openProviderSelector()
	m.selector.list.Select(1) // deepseek (0=environment, 1=deepseek, 2=connect)

	// e opens the pre-filled edit form.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'e'})
	got := updated.(model)
	if got.mode != modeConnectForm {
		t.Fatalf("after e mode = %v, want modeConnectForm", got.mode)
	}
	if got.providerForm.edit != true {
		t.Fatal("after e the form is not in edit mode")
	}
	if got.providerForm.inputs[providerFieldNickname].Value() != "deepseek" {
		t.Fatalf("edit form nickname = %q", got.providerForm.inputs[providerFieldNickname].Value())
	}

	// Back in the provider selector, the first d press arms the delete.
	m.mode = modeProviders
	m.openProviderSelector()
	m.selector.list.Select(1)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd'})
	got = updated.(model)
	if got.providerConfirmDelete != "deepseek" {
		t.Fatalf("first d confirm = %q, want deepseek", got.providerConfirmDelete)
	}

	// A second d on the same provider confirms and re-arms control pending.
	updated, _ = got.Update(tea.KeyPressMsg{Code: 'd'})
	got = updated.(model)
	if got.providerConfirmDelete != "" {
		t.Fatalf("confirm left armed = %q", got.providerConfirmDelete)
	}
	if !got.controlPending {
		t.Fatal("confirmed delete did not set controlPending")
	}
}

func TestPasteRoutesToProviderFormNotChatInput(t *testing.T) {
	m := newTestModel()
	m.mode = modeConnectForm
	m.providerForm = newProviderForm(nil, runtimeResolution{}, 80, LocaleEN)
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
	items := catalogProviderItems(LocaleEN, []catalogProvider{
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
	providerItems := providerSelectorItems(LocaleEN, []providerSummary{{Nickname: "__environment__"}}, providerStatusResponse{})
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
	m.providerForm = newProviderForm(nil, runtimeResolution{}, 80, LocaleEN)
	m.providerForm.inputs[providerFieldAPIKey].SetValue("sk-never-retain")
	updated, _ := m.Update(providerActionMsg{Action: "switch", Nickname: "work"})
	got := updated.(model)
	if got.modelOverride != "conversation-model" {
		t.Fatalf("global provider switch erased session model: %q", got.modelOverride)
	}
	if got.providerForm.inputs[providerFieldAPIKey].Value() != "" {
		t.Fatal("provider action retained the API key in UI state")
	}

	updated, _ = got.Update(providerBusEventMsg{})
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
			ModelOverride: "chosen", Provider: "old-work",
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

func TestParseApprovalPayload(t *testing.T) {
	req, ok := parseApprovalPayload(json.RawMessage(
		`{"id":"a1","tool":"bash","args":{"cmd":"make"},"sessionId":"sess-1"}`))
	if !ok {
		t.Fatal("valid payload not accepted")
	}
	if req.id != "a1" || req.tool != "bash" || req.sessionID != "sess-1" {
		t.Fatalf("parsed req = %+v", req)
	}
	if _, ok := parseApprovalPayload(json.RawMessage(`{"id":"a1"}`)); ok {
		t.Fatal("payload without tool accepted")
	}
	if _, ok := parseApprovalPayload(json.RawMessage(`not json`)); ok {
		t.Fatal("malformed payload accepted")
	}
}

func TestApprovalDirectedAckAndAutoApprove(t *testing.T) {
	m := newTestModel()

	// Broadcast (no ack expected) request is queued.
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "b1", tool: "bash", args: json.RawMessage(`{"cmd":"ls"}`)},
	})
	if len(m.approvals) != 1 || m.approvals[0].id != "b1" {
		t.Fatalf("broadcast request not queued: %+v", m.approvals)
	}

	// An auto-approved tool skips the queue entirely.
	m.rememberAutoApprove("sess-1", "bash")
	m.applyApprovalEvent(approvalEventMsg{
		req:      approvalRequest{id: "b2", tool: "bash", sessionID: "sess-1"},
		directed: true,
	})
	if len(m.approvals) != 1 {
		t.Fatalf("auto-approved request was queued: %+v", m.approvals)
	}

	// A directed (non-auto) request is queued behind existing ones.
	m.applyApprovalEvent(approvalEventMsg{
		req:      approvalRequest{id: "d1", tool: "bash", sessionID: "sess-2"},
		directed: true,
	})
	if len(m.approvals) != 2 || m.approvals[1].id != "d1" {
		t.Fatalf("directed request not queued: %+v", m.approvals)
	}
}

func TestApprovalAnswerPopsAndAutoRemembers(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{
		req: approvalRequest{id: "c1", tool: "core.spawn", sessionID: "sess-9"},
	})
	m.answerApproval(true, true)
	if len(m.approvals) != 0 {
		t.Fatalf("queue not empty after answer: %+v", m.approvals)
	}
	if !m.isAutoApproved("sess-9", "core.spawn") {
		t.Fatal("auto-approve not remembered")
	}
	if m.isAutoApproved("sess-9", "bash") {
		t.Fatal("unrelated tool auto-approved")
	}

	m.answerApproval(true, true) // no-op on empty queue
	if len(m.approvals) != 0 {
		t.Fatalf("empty-queue answer mutated state: %+v", m.approvals)
	}
}

func TestApprovalResolvedDismisses(t *testing.T) {
	m := newTestModel()
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "x1", tool: "bash"}})
	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "x2", tool: "bash"}})
	m.applyApprovalResolved("x1")
	if len(m.approvals) != 1 || m.approvals[0].id != "x2" {
		t.Fatalf("resolved did not dismiss: %+v", m.approvals)
	}
}

func TestApprovalKeysRouteWhilePending(t *testing.T) {
	m := newTestModel()
	// Enter with no pending approval must NOT be consumed.
	if _, consumed := m.approvalKey(tea.KeyPressMsg{}); consumed {
		t.Fatal("empty key consumed without pending approval")
	}

	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "k1", tool: "bash", sessionID: "s1"}})
	if _, consumed := m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !consumed {
		t.Fatal("enter not consumed by pending approval")
	}
	if len(m.approvals) != 0 {
		t.Fatalf("enter did not answer: %+v", m.approvals)
	}

	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "k2", tool: "bash", sessionID: "s1"}})
	if _, consumed := m.approvalKey(tea.KeyPressMsg{Code: tea.KeyEsc}); !consumed {
		t.Fatal("esc not consumed by pending approval")
	}
	if len(m.approvals) != 0 {
		t.Fatalf("esc did not deny: %+v", m.approvals)
	}

	m.applyApprovalEvent(approvalEventMsg{req: approvalRequest{id: "k3", tool: "bash", sessionID: "s1"}})
	if _, consumed := m.approvalKey(tea.KeyPressMsg{Code: 'a'}); !consumed {
		t.Fatal("'a' not consumed by pending approval")
	}
	if !m.isAutoApproved("s1", "bash") {
		t.Fatal("'a' did not remember auto-approve")
	}
}

func TestApprovalPrettyArgs(t *testing.T) {
	got := prettyApprovalArgs(json.RawMessage(`{"cmd":"make","dir":"src"}`))
	if !strings.Contains(got, "\"cmd\": \"make\"") {
		t.Fatalf("args not indented: %q", got)
	}
	if prettyApprovalArgs(nil) != "{}" {
		t.Fatal("empty args should render {}")
	}
}

func TestToolCallGroupingIntoCard(t *testing.T) {
	m := newTestModel()
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":"make"}`)})
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":"git"}`)})
	m.appendToolCall(toolCall{name: "core.spawn", args: json.RawMessage(`{"name":"x"}`)})
	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 grouped card, got %d blocks", len(m.blocks))
	}
	run := m.blocks[0].run
	if run == nil || len(run.calls) != 3 {
		t.Fatalf("card run = %+v, want 3 calls", run)
	}
	if !run.collapsed {
		t.Fatal("new card should be collapsed by default")
	}

	// Assistant text starts a fresh card (third block) rather than folding
	// into the first.
	m.addBlock(blockAssistant, "ok")
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":true}`)})
	if len(m.blocks) != 3 || m.blocks[0].run == nil || len(m.blocks[0].run.calls) != 3 ||
		m.blocks[2].run == nil || len(m.blocks[2].run.calls) != 1 {
		t.Fatalf("assistant break did not start a new card: %d blocks", len(m.blocks))
	}
}

func TestToolRunRenderingCollapsedAndExpanded(t *testing.T) {
	run := &toolRun{collapsed: true, calls: []toolCall{
		{name: "bash", args: json.RawMessage(`{"cmd":"make"}`)},
		{name: "bash", args: json.RawMessage(`{"cmd":"git"}`)},
		{name: "core.spawn", args: json.RawMessage(`{"name":"x"}`), err: "denied"},
	}}
	// Collapsed: one summary line with count + chips.
	got := renderToolRun(run, false)
	if strings.Contains(got, "\n") {
		t.Fatalf("collapsed card should be one line:\n%q", got)
	}
	if !strings.Contains(got, "3 tool calls") || !strings.Contains(got, "core.spawn") {
		t.Fatalf("collapsed summary missing count/chips: %q", got)
	}
	if strings.Contains(got, "denied") {
		t.Fatal("collapsed card leaked error text")
	}

	// Expanded: per-call args + error.
	got = renderToolRun(run, true)
	if !strings.Contains(got, "make") || !strings.Contains(got, "denied") {
		t.Fatalf("expanded card missing detail: %q", got)
	}
}

func TestToolRunSingleCallAndGlyph(t *testing.T) {
	ok := renderToolRun(&toolRun{collapsed: true, calls: []toolCall{{name: "bash"}}}, false)
	if !strings.Contains(ok, "bash") || strings.Contains(ok, "tool calls") {
		t.Fatalf("single-call summary wrong: %q", ok)
	}
	bad := renderToolRun(&toolRun{collapsed: true, calls: []toolCall{{name: "bash", err: "boom"}}}, true)
	if strings.Contains(bad, "✓") {
		t.Fatal("errored call rendered the ok glyph")
	}
}

func TestToolCardHitTestAndToggle(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":"make"}`)})
	m.appendToolCall(toolCall{name: "bash", args: json.RawMessage(`{"cmd":"git"}`)})
	m.addBlock(blockUser, "hello")
	m.appendToolCall(toolCall{name: "core.spawn"})

	// Content line 0 is the first (collapsed) card; line 1 is the blank
	// separator; line 2 is the user block; line 4 is the second card.
	if idx := m.blockAtContentLine(0); idx != 0 {
		t.Fatalf("content line 0 -> block %d, want 0", idx)
	}
	if idx := m.blockAtContentLine(4); idx != 2 {
		t.Fatalf("content line 4 -> block %d, want 2", idx)
	}

	// Toggling via hit-test: collapse/expand the clicked card. Mouse tracking
	// must be on for clicks to reach the app.
	m.blocks[0].run.collapsed = true
	m.mouse = true
	m.handleMouseClick(tea.MouseClickMsg{Button: tea.MouseLeft, Y: 0 + 2})
	if m.blocks[0].run.collapsed {
		t.Fatal("click did not expand the first card")
	}
	// Non-left button is ignored: the card stays expanded.
	m.handleMouseClick(tea.MouseClickMsg{Button: tea.MouseRight, Y: 0 + 2})
	if m.blocks[0].run.collapsed {
		t.Fatal("right-click collapsed the card")
	}
}

// TestThinkingEffortCycle covers the ctrl+g rotation and its persistence
// message handling: a save for the current session applies; a stale one
// (session switched) is dropped.
func TestThinkingEffortCycle(t *testing.T) {
	m := newTestModel()
	m.session = "game"
	if got := m.effortLabel(); got != "auto" {
		t.Fatalf("empty effort label = %q, want auto", got)
	}
	want := []string{"low", "medium", "high", ""}
	for i, level := range want {
		next := m.nextThinkingEffort()
		if next != level {
			t.Fatalf("nextThinkingEffort step %d = %q, want %q", i, next, level)
		}
		m.thinkingEffort = next
	}
	if got := m.effortLabel(); got != "auto" {
		t.Fatalf("back at empty effort label = %q, want auto", got)
	}
	m.thinkingEffort = "high"
	if got := m.effortLabel(); got != "high" {
		t.Fatalf("high effort label = %q", got)
	}
}

func TestThinkingEffortMessageAppliesOrDrops(t *testing.T) {
	m := newTestModel()
	m.session = "game"
	m.controlPending = true
	m.applyThinkingEffort(thinkingEffortMsg{Session: "game", Effort: "high"})
	if m.thinkingEffort != "high" || m.controlPending {
		t.Fatalf("effort save not applied: effort=%q pending=%v", m.thinkingEffort, m.controlPending)
	}
	if len(m.blocks) != 1 || m.blocks[0].kind != blockMeta ||
		!strings.Contains(m.blocks[0].text, "thinking effort: high") {
		t.Fatalf("blocks = %+v, want a meta confirmation", m.blocks)
	}

	// A completion for the old session must not leak into the new one.
	m.session = "other"
	m.thinkingEffort = ""
	m.applyThinkingEffort(thinkingEffortMsg{Session: "game", Effort: "high"})
	if m.thinkingEffort != "" {
		t.Fatalf("stale effort save applied: %q", m.thinkingEffort)
	}
}

func TestToolVisibilityCycle(t *testing.T) {
	m := newTestModel()
	m.appendToolCall(toolCall{name: "a"})
	m.addBlock(blockUser, "x")
	m.appendToolCall(toolCall{name: "b"})

	brief := m.renderTranscript()
	if !strings.Contains(brief, "▸") {
		t.Fatalf("brief level should render collapsed cards: %q", brief)
	}

	m.cycleToolVisibility() // brief → full
	full := m.renderTranscript()
	if !strings.Contains(full, "▾") || strings.Contains(full, "▸") {
		t.Fatalf("full level should expand every card: %q", full)
	}

	m.cycleToolVisibility() // full → off
	off := m.renderTranscript()
	if strings.Contains(off, "tool") || strings.Contains(off, "▸") || strings.Contains(off, "▾") {
		t.Fatalf("off level should hide cards: %q", off)
	}
	// Hidden cards must not shift mouse hit-testing: the user block owns
	// content line 0 now.
	if idx := m.blockAtContentLine(0); idx != 1 {
		t.Fatalf("hit-test line 0 = block %d, want the user block (1)", idx)
	}

	m.cycleToolVisibility() // off → brief
	if got := m.renderTranscript(); got != brief {
		t.Fatalf("cycle back to brief changed rendering: %q", got)
	}
}

// TestThinkingBlocksDoNotStackAcrossRounds covers the bug where every
// later round's reasoning was appended to the first round's never-finalized
// thinking block, stacking all thinking above the conversation. Each round
// must own its thinking block.
func TestThinkingBlocksDoNotStackAcrossRounds(t *testing.T) {
	m := newTestModel()

	// Round 1: reasoning streamed, then the assistant event closes the round.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "first thoughts", Content: "one"}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "one"}})

	// Round 2 opens after a tool call.
	m.applySessionEvent(sessionEventMsg{kind: "toolcall", event: sessionEvent{
		SessionID: "game", Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}})
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "second thoughts", Content: "two"}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "two"}})

	var thinkTexts []string
	for _, b := range m.blocks {
		if b.kind == blockThinking {
			thinkTexts = append(thinkTexts, b.text)
		}
	}
	if len(thinkTexts) != 2 {
		t.Fatalf("got %d thinking blocks (%q), want 2 separate", len(thinkTexts), thinkTexts)
	}
	if thinkTexts[0] != "first thoughts" || thinkTexts[1] != "second thoughts" {
		t.Fatalf("thinking stacked across rounds: %q", thinkTexts)
	}
}

// TestTurnDoneFinalizesThinking covers the done-without-assistant path: an
// error turn must not leave an unfinalized thinking block behind for the
// next turn to append into.
func TestTurnDoneFinalizesThinking(t *testing.T) {
	m := newTestModel()
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "doomed thoughts"}})
	m.finishTurn("", "backend exploded")

	// New turn (like the enter handler): roundClosed must be false before
	// the next round's tokens arrive or they are dropped as stragglers.
	m.roundClosed = false
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Reasoning: "fresh thoughts"}})
	var thinkTexts []string
	for _, b := range m.blocks {
		if b.kind == blockThinking {
			thinkTexts = append(thinkTexts, b.text)
		}
	}
	if len(thinkTexts) != 2 {
		t.Fatalf("got %d thinking blocks (%q), want 2 separate", len(thinkTexts), thinkTexts)
	}
}

func TestThinkingLevelRendering(t *testing.T) {
	m := newTestModel()
	m.addBlock(blockThinking, "deep reasoning")
	m.addBlock(blockUser, "hello")
	m.addBlock(blockAssistant, "hi")

	full := m.renderTranscript()
	if !strings.Contains(full, "deep reasoning") {
		t.Fatalf("full level hides reasoning: %q", full)
	}

	m.thinkLevel = thinkBrief
	m.markTranscriptDirty()
	brief := m.renderTranscript()
	if strings.Contains(brief, "deep reasoning") {
		t.Fatalf("brief level shows full reasoning: %q", brief)
	}
	if !strings.Contains(brief, "thinking") {
		t.Fatalf("brief level lost its collapsed marker: %q", brief)
	}

	m.thinkLevel = thinkOff
	m.markTranscriptDirty()
	off := m.renderTranscript()
	if strings.Contains(off, "reasoning") || strings.Contains(off, "thinking") {
		t.Fatalf("off level still renders thinking: %q", off)
	}
	// Hidden blocks must not shift mouse hit-testing: the user block is the
	// first rendered piece and owns content line 0.
	if idx := m.blockAtContentLine(0); idx != 1 {
		t.Fatalf("hit-test line 0 = block %d, want the user block (1)", idx)
	}
}

func TestLateTokenAfterAssistantDoesNotDuplicate(t *testing.T) {
	m := newTestModel()
	// Stream the full reply, then the final assistant event with the complete
	// content, then a straggler token frame carrying the tail — NATS does not
	// order across subjects, so this is the duplicate-output race.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: "Everything works and the suite passing."}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "Everything works and the suite passing."}})
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: " suite passing."}})

	if len(m.blocks) != 1 {
		t.Fatalf("late token created %d blocks, want 1:\n%+v", len(m.blocks), m.blocks)
	}
	got := m.blocks[0].text
	if strings.Count(got, "suite passing.") != 1 {
		t.Fatalf("duplicate content in assistant block: %q", got)
	}
	if got != "Everything works and the suite passing." {
		t.Fatalf("assistant block text = %q", got)
	}
}

func TestLateTokenAfterDoneDoesNotCreateBlock(t *testing.T) {
	m := newTestModel()
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "done reply"}})
	m.finishTurn("done reply", "")

	// A token frame that arrives after the turn finished must be ignored.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: " suite passing."}})
	if len(m.blocks) != 1 {
		t.Fatalf("post-done token created %d blocks, want 1:\n%+v", len(m.blocks), m.blocks)
	}
	if m.blocks[0].text != "done reply" {
		t.Fatalf("assistant block text = %q", m.blocks[0].text)
	}
}

func TestMultiRoundStillStreams(t *testing.T) {
	m := newTestModel()
	// Round 1: stream + assistant + toolcall (opens a fresh round).
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: "let me check"}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "let me check"}})
	m.applySessionEvent(sessionEventMsg{kind: "toolcall", event: sessionEvent{
		SessionID: "game", Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}})
	// Round 2 tokens must create a fresh block, not be swallowed.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: "the answer is 42"}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "the answer is 42"}})

	if len(m.blocks) != 3 { // thinking? no — user? no — assistant, tool card, assistant
		t.Fatalf("multi-round turn produced %d blocks, want 3:\n%+v", len(m.blocks), m.blocks)
	}
	if m.blocks[2].text != "the answer is 42" {
		t.Fatalf("round 2 assistant = %q", m.blocks[2].text)
	}
}

func TestMouseDefaultAndToggle(t *testing.T) {
	m := newTestModel()
	if !m.mouse {
		t.Fatal("mouse tracking should default to on so the wheel scrolls the transcript")
	}

	// /mouse off (no argument = toggle) disables tracking.
	updated, _ := m.executeLocalCommand("/mouse")
	m = updated.(model)
	if m.mouse {
		t.Fatal("/mouse did not toggle tracking off")
	}

	// Explicit on/off forms.
	updated, _ = m.executeLocalCommand("/mouse on")
	if !updated.(model).mouse {
		t.Fatal("/mouse on did not enable tracking")
	}
	updated, _ = m.executeLocalCommand("/mouse off")
	if updated.(model).mouse {
		t.Fatal("/mouse off did not disable tracking")
	}
}

func TestToolcallReorderBetweenStreamAndFinalNoDuplicate(t *testing.T) {
	m := newTestModel()
	// Round 1 narration streams, then finishes.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: "Let me check the docs"}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "Let me check the docs"}})
	// A tool-call event follows (round 1 closes); it resets round state.
	m.applySessionEvent(sessionEventMsg{kind: "toolcall", event: sessionEvent{
		SessionID: "game", Tool: "fetch", Args: json.RawMessage(`{"url":"x"}`)}})

	// Round 2 streams a *partial*, then — because NATS does not order across
	// subjects — a stray tool-call event from an earlier round lands BETWEEN
	// the stream and the round-2 assistant event, resetting assistantIdx.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: "storage as a"}})
	m.applySessionEvent(sessionEventMsg{kind: "toolcall", event: sessionEvent{
		SessionID: "game", Tool: "fetch", Args: json.RawMessage(`{"url":"y"}`)}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "storage as a Docker container? Yes, MinIO."}})

	// Expect exactly one block per piece: round-1 assistant, tool card,
	// round-2 partial (finalized with the full content), and the stray tool
	// card — no duplicate assistant block for round 2.
	var assistantBlocks []string
	for i := range m.blocks {
		if m.blocks[i].kind == blockAssistant {
			assistantBlocks = append(assistantBlocks, m.blocks[i].text)
		}
	}
	if len(assistantBlocks) != 2 {
		t.Fatalf("got %d assistant blocks, want 2:\n%+v", len(assistantBlocks), assistantBlocks)
	}
	if assistantBlocks[1] != "storage as a Docker container? Yes, MinIO." {
		t.Fatalf("round-2 assistant = %q, want the full finalized content", assistantBlocks[1])
	}
}

func TestAssistantFinalCoalescesIntoStreamedBlock(t *testing.T) {
	m := newTestModel()
	// A round that only participates via the token stream; the assistant
	// event must finalize the SAME block instead of opening a duplicate.
	m.applySessionEvent(sessionEventMsg{kind: "token", event: sessionEvent{
		SessionID: "game", Content: "an answer"}})
	m.applySessionEvent(sessionEventMsg{kind: "assistant", event: sessionEvent{
		SessionID: "game", Content: "an extended answer"}})

	if len(m.blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (no duplicate):\n%+v", len(m.blocks), m.blocks)
	}
	if m.blocks[0].text != "an extended answer" {
		t.Fatalf("block text = %q, want the full finalized content", m.blocks[0].text)
	}
	if !m.blocks[0].finalized {
		t.Fatal("block should be marked finalized after the assistant event")
	}
}

func TestRuntimeOutputLimitSurfaced(t *testing.T) {
	m := newTestModel()
	m.runtime = runtimeResolution{
		OK: true, Provider: "deepseek", ProviderSource: "store",
		Model: "deepseek-chat", Catalog: "deepseek",
		Context: 1_000_000, ContextSource: "catalog",
		Output: 32768, OutputSource: "fallback",
	}
	status := m.detailedRuntimeStatus()
	if !strings.Contains(status, "output: 32.8k (fallback)") {
		t.Fatalf("detailed status missing output limit: %q", status)
	}
	line := runtimeStatusLine(LocaleEN, m.runtime, "", 0, 80)
	if ansi.StringWidth(line) > 79 {
		t.Fatalf("runtime line too wide: %d", ansi.StringWidth(line))
	}
}

func TestRememberAutoApprovePersistsToStore(t *testing.T) {
	// With a live component, rememberAutoApprove returns the command that
	// writes the decision to the store so the core gate honors it for every
	// client (no dialog at all). In tests comp is nil, so no command is
	// returned but the in-memory list still records it.
	m := newTestModel()
	if cmd := m.rememberAutoApprove("sess-1", "bash"); cmd != nil {
		t.Fatal("persist command expected only with a live component")
	}
	if !m.isAutoApproved("sess-1", "bash") {
		t.Fatal("in-memory auto-approve not recorded")
	}
	// Idempotent: a second remember does not duplicate.
	if cmd := m.rememberAutoApprove("sess-1", "bash"); cmd != nil {
		t.Fatal("persist command expected only with a live component")
	}
	if len(m.autoApproved["sess-1"]) != 1 {
		t.Fatalf("auto-approve duplicated: %v", m.autoApproved["sess-1"])
	}
	// Empty session is ignored (no persistence, no memory).
	m.rememberAutoApprove("", "bash")
	if m.isAutoApproved("", "bash") {
		t.Fatal("empty session auto-approved")
	}
}

func TestTurnDoneFromOtherSessionIgnored(t *testing.T) {
	m := newTestModel()
	m.session = "new"
	m.busy = true // a turn is in flight for the session we switched away from

	// The old session's turn completion must not land in the new transcript
	// or clear the new session's busy flag.
	updated, _ := m.Update(turnDoneMsg{session: "old", reply: "stale reply"})
	got := updated.(model)
	if len(got.blocks) != 0 || !got.busy {
		t.Fatalf("foreign turn leaked into the new session: blocks=%d busy=%v", len(got.blocks), got.busy)
	}

	// The matching session's completion still finishes the turn.
	updated, _ = got.Update(turnDoneMsg{session: "new", reply: "hello"})
	got = updated.(model)
	if got.busy {
		t.Fatal("matching turn did not clear busy")
	}
	if len(got.blocks) != 1 || got.blocks[0].kind != blockAssistant || got.blocks[0].text != "hello" {
		t.Fatalf("matching turn reply not rendered: %+v", got.blocks)
	}
}

func TestSessionListMsgPopulatesSelector(t *testing.T) {
	m := newTestModel()
	m.mode = modeSessions
	updated, _ := m.Update(sessionListMsg{
		Sessions: []sessionSummary{
			{ID: "conv-2", Title: "Second", CreatedAt: 200},
			{ID: "conv-1", Title: "First", CreatedAt: 100},
		},
	})
	got := updated.(model)
	if got.mode != modeSessions {
		t.Fatalf("mode = %v, want sessions selector", got.mode)
	}
	items := got.selector.list.Items()
	if len(items) != 3 { // + new session entry
		t.Fatalf("selector items = %d, want 3", len(items))
	}

	// A store error falls back to chat mode with a note.
	got.mode = modeSessions
	updated, _ = got.Update(sessionListMsg{Err: errors.New("store unavailable")})
	got = updated.(model)
	if got.mode != modeChat {
		t.Fatalf("error mode = %v, want chat", got.mode)
	}
	if !strings.Contains(got.contextNote, "store unavailable") {
		t.Fatalf("error note = %q", got.contextNote)
	}
}

func TestSecondEscapeCancelsAndArmsStopping(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.session = "game"

	// First ESC arms the stop prompt; nothing is cancelled yet.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := updated.(model)
	if cmd != nil || !got.stopArmed || !got.busy {
		t.Fatalf("first esc: armed=%v busy=%v cmd=%v", got.stopArmed, got.busy, cmd != nil)
	}

	// Second ESC force-cancels: the prompt disarms, stopping is recorded on
	// the live model (regression: cancelTurnCmd used to mutate a copy).
	updated, cmd = got.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	got = updated.(model)
	if cmd == nil {
		t.Fatal("second esc returned no cancel command")
	}
	if got.stopArmed || !got.stopping || !got.busy {
		t.Fatalf("second esc: armed=%v stopping=%v busy=%v", got.stopArmed, got.stopping, got.busy)
	}
}

func TestTranscriptCacheInvalidatedByMutations(t *testing.T) {
	m := newTestModel()
	m.addBlock(blockUser, "first")
	first := m.renderTranscript()
	if first == "" {
		t.Fatal("empty transcript for one block")
	}
	if m.transcriptDirty {
		t.Fatal("render did not clear the dirty flag")
	}
	if again := m.renderTranscript(); again != first {
		t.Fatal("cached transcript changed without mutation")
	}

	m.addBlock(blockMeta, "second")
	second := m.renderTranscript()
	if second == first || !strings.Contains(second, "second") {
		t.Fatalf("transcript did not reflect appended block: %q", second)
	}

	// A tool-visibility change must invalidate the cached transcript.
	m.blocks = append(m.blocks, transcriptBlock{kind: blockTool, run: &toolRun{calls: []toolCall{{name: "bash"}}, collapsed: true}})
	collapsed := m.renderTranscript()
	m.cycleToolVisibility()
	expanded := m.renderTranscript()
	if expanded == collapsed {
		t.Fatal("visibility cycle did not invalidate the transcript cache")
	}
}

func TestSessionSelectorEnterSwitchesOrDismisses(t *testing.T) {
	m := newTestModel()
	m.session = "current"
	m.mode = modeSessions
	m.selector = newSelector("Sessions", sessionSelectorItems(LocaleEN, "current", []sessionSummary{
		{ID: "current", Title: "Current"},
		{ID: "other", Title: "Other"},
	}), 80, 20)
	// sessionSelectorItems prepends "+ New session"; select the current
	// session entry (index 1).
	m.selector.list.Select(1)

	// Enter on the current session just dismisses the browser: switching
	// would wipe the live transcript.
	updated, cmd := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeChat || cmd != nil {
		t.Fatalf("enter on current session: mode=%v cmd=%v, want chat with no command", got.mode, cmd != nil)
	}
	if got.session != "current" || len(got.blocks) != 0 {
		t.Fatalf("current session state was reset: session=%q blocks=%d", got.session, len(got.blocks))
	}

	// Re-open the browser (as a user navigating the list would) and select
	// the other session; Enter switches to it and re-bootstraps.
	got.mode = modeSessions
	got.selector = newSelector("Sessions", sessionSelectorItems(LocaleEN, "current", []sessionSummary{
		{ID: "current", Title: "Current"},
		{ID: "other", Title: "Other"},
	}), 80, 20)
	got.selector.list.Select(2)
	updated, cmd = got.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = updated.(model)
	if cmd == nil {
		t.Fatal("switching sessions returned no bootstrap command")
	}
	if got.session != "other" {
		t.Fatalf("enter on other session stayed on %q", got.session)
	}
	if got.mode != modeChat || got.historyFile != historyFilePath("other") {
		t.Fatalf("switch did not reset session state: mode=%v file=%q", got.mode, got.historyFile)
	}
}

func TestSwitchSessionClearsViewport(t *testing.T) {
	m := newTestModel()
	m.session = "old"
	m.addBlock(blockUser, "previous conversation")
	m.syncViewport(true)
	if m.viewportContent == "" {
		t.Fatal("precondition: viewport should hold the old transcript")
	}

	got := m.switchSession("conv-new")
	if len(got.blocks) != 0 {
		t.Fatalf("blocks not cleared after switch: %d", len(got.blocks))
	}
	if got.viewportContent != "" {
		t.Fatalf("viewport still shows the old transcript after switch: %q", got.viewportContent)
	}
}

func TestThinkingRenderingTrimsEdgeNewlines(t *testing.T) {
	m := newTestModel()
	m.addBlock(blockUser, "hi there")
	// Gateway-style reasoning deltas: reasoning opens with blank lines,
	// paragraphs are separated by runs of newlines (often several, since
	// each delta carries its own trailing breaks), and it closes with
	// blank lines before the content starts.
	for _, delta := range []string{"\n\nOkay, let's think", " about this.\n\n\n",
		"First step.\n\n\n\n\n\n\nSecond step.\n\n"} {
		m.appendStreamingText(blockThinking, &m.thinkingIdx, delta)
	}
	m.appendStreamingText(blockAssistant, &m.assistantIdx, "\n\nHere is the answer.")

	// Strip ANSI so structural assertions don't trip over style codes
	// (lipgloss wraps each block, inserting resets around newlines).
	out := ansi.Strip(m.renderTranscript())
	// lipgloss pads interior empty lines with spaces; normalize them back
	// to truly empty lines for the structure checks.
	out = blankLineRe.ReplaceAllString(out, "")
	// Interior blank-line runs collapse to a single newline: paragraphs
	// flow directly after one another. Only the block separator's single
	// blank line (\n\n) may appear anywhere.
	if strings.Contains(out, "\n\n\n") {
		t.Fatalf("rendered transcript kept blank lines:\n%q", out)
	}
	// The paragraph flow survives (lines may carry trailing padding spaces).
	if !regexp.MustCompile(`First step\. +\nSecond step\.`).MatchString(out) {
		t.Fatalf("paragraph flow lost:\n%q", out)
	}
	if !strings.Contains(out, "Here is the answer.") {
		t.Fatalf("assistant content lost:\n%q", out)
	}

	// Hidden levels: brief collapses to a single dim line, off drops the
	// block entirely.
	m.thinkLevel = thinkBrief
	m.markTranscriptDirty()
	if out := ansi.Strip(m.renderTranscript()); !strings.Contains(out, "thinking…") || strings.Contains(out, "Second step.") {
		t.Fatalf("brief level should collapse reasoning:\n%q", out)
	}
	m.thinkLevel = thinkOff
	m.markTranscriptDirty()
	if out := ansi.Strip(m.renderTranscript()); strings.Contains(out, "thinking…") {
		t.Fatalf("off level should hide reasoning:\n%q", out)
	}
}

func TestBootstrapMsgFromOtherSessionIgnored(t *testing.T) {
	m := newTestModel()
	m.session = "new"
	m.runtime = runtimeResolution{Provider: "keep", Model: "keep", OK: true}
	m.modelOverride = "keep"

	updated, _ := m.Update(bootstrapMsg{
		Session:      "old",
		Conversation: conversationState{ModelOverride: "stale"},
		Runtime:      runtimeResolution{Provider: "stale", Model: "stale"},
	})
	got := updated.(model)
	if got.runtime.Provider != "keep" || got.modelOverride != "keep" {
		t.Fatalf("stale bootstrap clobbered the new session: %#v", got.runtime)
	}

	// A matching bootstrap still populates the header state.
	updated, _ = got.Update(bootstrapMsg{
		Session:      "new",
		Conversation: conversationState{ModelOverride: "chosen"},
		Runtime:      runtimeResolution{Provider: "work", Model: "m1"},
	})
	got = updated.(model)
	if got.runtime.Provider != "work" || got.modelOverride != "chosen" {
		t.Fatalf("matching bootstrap not applied: %#v override=%q", got.runtime, got.modelOverride)
	}
}

func TestModelActionFromOtherSessionIgnored(t *testing.T) {
	m := newTestModel()
	m.session = "new"
	m.modelOverride = "old-override"

	// A conversation model save fired for the old session must not touch
	// the new session's override. (In practice a stale completion can only
	// arrive after switchSession, which already cleared controlPending; a
	// stale message is ignored entirely.)
	updated, _ := m.Update(modelActionMsg{
		Session: "old", Selected: "m2", Previous: "old-override",
		Runtime: runtimeResolution{OK: true, Model: "m2"},
	})
	got := updated.(model)
	if got.modelOverride != "old-override" {
		t.Fatalf("stale model action changed override: %q", got.modelOverride)
	}

	// A matching save commits the override and clears the pending flag.
	got.controlPending = true
	updated, _ = got.Update(modelActionMsg{
		Session: "new", Selected: "m2", Previous: "old-override",
		Runtime: runtimeResolution{OK: true, Model: "m2"},
	})
	got = updated.(model)
	if got.modelOverride != "m2" || got.controlPending {
		t.Fatalf("matching model action not applied: override=%q pending=%v", got.modelOverride, got.controlPending)
	}
}

func TestSessionListMsgDroppedAfterLeavingBrowser(t *testing.T) {
	m := newTestModel()
	m.mode = modeSessions
	// The user leaves the browser (esc -> chat) while the list is loading.
	m.mode = modeChat
	updated, _ := m.Update(sessionListMsg{
		Sessions: []sessionSummary{{ID: "x", Title: "X"}},
	})
	got := updated.(model)
	if got.mode != modeChat {
		t.Fatalf("late session list yanked mode to %v", got.mode)
	}
}

// TestThinkingToggleDoesNotAccumulateNewlines guards the ctrl+t path:
// cycling the display level must never mutate block text or grow the
// rendered transcript — toggling re-renders the same blocks, it cannot
// add newlines.
func TestThinkingToggleDoesNotAccumulateNewlines(t *testing.T) {
	m := newTestModel()
	m.addBlock(blockUser, "hi")
	m.addBlock(blockThinking, "\n\nStep one.\n\n\nStep two.\n\n")
	m.addBlock(blockAssistant, "answer")

	first := ansi.Strip(m.renderTranscript())
	for i := 0; i < 9; i++ { // three full cycles
		m.cycleThinkingLevel()
		rendered := ansi.Strip(m.renderTranscript())
		if (i+1)%3 == 0 { // back at full (full → brief → off → full)
			if rendered != first {
				t.Fatalf("toggle %d changed the full rendering:\n%q\nvs\n%q", i, first, rendered)
			}
		}
	}
	// The block text itself must be untouched by any number of toggles.
	if m.blocks[1].text != "\n\nStep one.\n\n\nStep two.\n\n" {
		t.Fatalf("toggling mutated block text: %q", m.blocks[1].text)
	}
}

func TestProviderSelectorOffersSubscriptionLogins(tt *testing.T) {
	m := newTestModel()
	m.openProviderSelector()
	titles := map[string]bool{}
	for _, item := range m.selector.list.Items() {
		si := item.(selectorItem)
		titles[si.title] = true
		if si.kind == selectorOAuthOpenAIBrowser || si.kind == selectorOAuthOpenAIDevice ||
			si.kind == selectorOAuthAnthropic {
			continue
		}
		if strings.HasPrefix(si.id, "__oauth_") {
			tt.Fatalf("unexpected oauth item %q", si.id)
		}
	}
	for _, want := range []string{
		t(LocaleEN, "oauth.option.openai.browser"),
		t(LocaleEN, "oauth.option.openai.device"),
		t(LocaleEN, "oauth.option.anthropic"),
	} {
		if !titles[want] {
			tt.Fatalf("provider selector missing %q", want)
		}
	}
	// Choosing the Anthropic entry (last item) dispatches the start command:
	// the mode only changes once oauthStartMsg returns, but the control
	// roundtrip is flagged immediately.
	m.selector.list.Select(len(m.selector.list.Items()) - 1)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if cmd == nil {
		tt.Fatal("OAuth entry produced no start command")
	}
	if !got.controlPending {
		tt.Fatal("OAuth start did not set controlPending")
	}
}

func TestOAuthStartMessageOpensLoginPanel(tt *testing.T) {
	m := newTestModel()
	m.width = 100
	msg := oauthStartMsg{state: func() oauthLoginState {
		state := newOAuthLoginState(LocaleEN)
		state.flowID = "flow-1"
		state.provider = "OpenAI Codex (ChatGPT Plus/Pro)"
		state.url = "https://auth.openai.com/oauth/authorize?x=1"
		state.callbackAvailable = true
		state.status = t(LocaleEN, "oauth.waiting")
		return state
	}()}
	updated, _ := m.Update(msg)
	got := updated.(model)
	if got.mode != modeOAuth || got.oauthLogin == nil {
		tt.Fatalf("mode = %v, oauthLogin = %#v", got.mode, got.oauthLogin)
	}
	view := ansi.Strip(got.View().Content)
	if !strings.Contains(view, "Sign in to OpenAI Codex") || !strings.Contains(view, "auth.openai.com") {
		tt.Fatalf("login panel missing provider/URL:\n%s", view)
	}
}

func TestOAuthManualCodeSubmitsAndPolls(t *testing.T) {
	m := newTestModel()
	m.width = 100
	m.mode = modeOAuth
	state := newOAuthLoginState(LocaleEN)
	state.flowID = "flow-2"
	state.provider = "Anthropic (Claude Pro/Max)"
	state.url = "https://claude.ai/oauth/authorize?x=1"
	state.retryAfterMs = 0
	m.oauthLogin = &state

	// Typing lands in the manual input (Text mirrors real terminal input).
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	got := updated.(model)
	if got.oauthLogin.input.Value() != "a" {
		t.Fatalf("manual input = %q", got.oauthLogin.input.Value())
	}
	// Enter with a code present submits it and restarts the poll chain.
	got.oauthLogin.input.SetValue("abc#state")
	updated, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = updated.(model)
	if cmd == nil {
		t.Fatal("manual submit produced no poll command")
	}
	if got.oauthLogin.manualPending != "abc#state" {
		t.Fatalf("manualPending = %q", got.oauthLogin.manualPending)
	}
}

func TestOAuthPollCompletionSwitchesToChatAndRefreshes(t *testing.T) {
	m := newTestModel()
	m.width = 100
	m.mode = modeOAuth
	state := newOAuthLoginState(LocaleEN)
	state.flowID = "flow-3"
	state.provider = "OpenAI Codex (ChatGPT Plus/Pro)"
	state.url = "https://auth.openai.com/oauth/authorize?x=1"
	m.oauthLogin = &state

	updated, _ := m.Update(oauthPollMsg{
		state:    state,
		done:     true,
		provider: providerSummary{Nickname: "openai-codex", AuthType: "oauth"},
	})
	got := updated.(model)
	if got.mode != modeChat || got.oauthLogin != nil {
		t.Fatalf("after completion mode = %v, oauthLogin = %#v", got.mode, got.oauthLogin)
	}
	// The View renders the transcript; assert via it like other tests do.
	if !strings.Contains(ansi.Strip(got.View().Content), "signed in: openai-codex") {
		t.Fatalf("transcript missing signed-in note:\n%s", ansi.Strip(got.renderTranscript()))
	}
}

func TestOAuthPollFromStaleFlowIsDropped(t *testing.T) {
	m := newTestModel()
	m.mode = modeOAuth
	state := newOAuthLoginState(LocaleEN)
	state.flowID = "flow-old"
	m.oauthLogin = &state
	late := state
	late.flowID = "flow-other"

	updated, _ := m.Update(oauthPollMsg{state: late})
	got := updated.(model)
	if got.oauthLogin == nil || got.oauthLogin.flowID != "flow-old" {
		t.Fatalf("stale poll replaced the live flow: %#v", got.oauthLogin)
	}
}

func TestProviderSummaryShowsOAuthBadge(t *testing.T) {
	items := providerSelectorItems(LocaleEN, []providerSummary{
		{Nickname: "claude", AuthType: "oauth", Active: true, BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4-6"},
		{Nickname: "deepseek", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"},
	}, providerStatusResponse{Source: "store"})
	var oauthDescription, apiKeyDescription string
	for _, item := range items {
		si := item.(selectorItem)
		if si.id == "claude" {
			oauthDescription = si.description
		}
		if si.id == "deepseek" {
			apiKeyDescription = si.description
		}
	}
	if !strings.Contains(oauthDescription, "OAuth") {
		t.Fatalf("oauth provider description = %q", oauthDescription)
	}
	if strings.Contains(apiKeyDescription, "OAuth") {
		t.Fatalf("api-key provider description = %q", apiKeyDescription)
	}
}

func TestOAuthPollFromSupersededChainIsDropped(t *testing.T) {
	m := newTestModel()
	m.mode = modeOAuth
	state := newOAuthLoginState(LocaleEN)
	state.flowID = "flow-4"
	state.seq = 2 // a manual submit bumped the chain
	m.oauthLogin = &state

	late := state
	late.seq = 1 // result from the chain that was sleeping before the submit
	updated, cmd := m.Update(oauthPollMsg{state: late})
	got := updated.(model)
	if got.oauthLogin == nil || got.oauthLogin.seq != 2 {
		t.Fatalf("superseded poll mutated the live chain: %#v", got.oauthLogin)
	}
	if cmd != nil {
		t.Fatal("superseded poll rescheduled a chain")
	}
}

func TestOAuthPollErrorKeepsPanelForRetry(tt *testing.T) {
	m := newTestModel()
	m.width = 100
	m.mode = modeOAuth
	state := newOAuthLoginState(LocaleEN)
	state.flowID = "flow-5"
	state.url = "https://claude.ai/oauth/authorize"
	state.status = t(LocaleEN, "oauth.waiting")
	m.oauthLogin = &state

	updated, cmd := m.Update(oauthPollMsg{state: state, done: true, err: fmt.Errorf("flow expired")})
	got := updated.(model)
	if got.mode != modeOAuth || got.oauthLogin == nil {
		tt.Fatalf("error path left the login panel: mode=%v oauth=%#v", got.mode, got.oauthLogin)
	}
	if got.oauthLogin.err == "" || got.oauthLogin.status != "" {
		tt.Fatalf("error not surfaced: err=%q status=%q", got.oauthLogin.err, got.oauthLogin.status)
	}
	if cmd != nil {
		tt.Fatal("terminal error rescheduled the poll chain")
	}
	if !strings.Contains(ansi.Strip(got.View().Content), "flow expired") {
		tt.Fatal("error text missing from the login panel")
	}
}
