// Header usage chips and the slimmed bottom row: session token in/out and
// cache hit rate live next to the context gauge, the activity spinner is
// embedded in the input divider, and the bottom row only carries the
// conversation workspace and transient notes (command/key hints moved to
// /help and completion).
package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestDisplayPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if got := displayPath(home+"/git/niffler-tui", 80); got != "~/git/niffler-tui" {
		t.Fatalf("home prefix = %q", got)
	}
	if got := displayPath(home, 80); got != "~" {
		t.Fatalf("home = %q", got)
	}
	if got := displayPath("/tmp/x", 80); got != "/tmp/x" {
		t.Fatalf("short path = %q", got)
	}
	long := "/very/long/path/to/some/deeply/nested/project"
	short := displayPath(long, 20)
	if w := ansi.StringWidth(short); w > 20 {
		t.Fatalf("truncated width = %d (%q)", w, short)
	}
	if !strings.HasPrefix(short, "…") || !strings.HasSuffix(short, "project") {
		t.Fatalf("truncated path should keep its tail: %q", short)
	}
}

func TestBootstrapLoadsWorkspaceAndCacheStats(t *testing.T) {
	m := newTestModel()
	m.session = "console"
	m.cwd = "/startup/dir"

	updated, _ := m.Update(bootstrapMsg{
		Session: "console",
		Conversation: conversationState{
			Cwd: "/home/gokr/git/nifflerprod", CachePrompt: 1_000, CacheRead: 900,
		},
	})
	got := updated.(model)
	if got.cwd != "/home/gokr/git/nifflerprod" {
		t.Fatalf("cwd = %q, want the persisted workspace", got.cwd)
	}
	if got.cacheHits != 900 || got.cachePrompt != 1_000 {
		t.Fatalf("cache stats = %d/%d, want 900/1000", got.cacheHits, got.cachePrompt)
	}
	if chip := got.usageChip(); !strings.Contains(chip, "cache 90.0%") {
		t.Fatalf("usage chip = %q, want the restored hit rate", chip)
	}

	// A conversation with no stored workspace (brand-new) keeps the startup
	// fallback directory.
	fresh := newTestModel()
	fresh.session = "new"
	fresh.cwd = "/startup/dir"
	updated, _ = fresh.Update(bootstrapMsg{Session: "new", Conversation: conversationState{}})
	if got := updated.(model); got.cwd != "/startup/dir" {
		t.Fatalf("fresh conversation cwd = %q, want the fallback", got.cwd)
	}
}

func TestUsageChipAndRuntimeLine(t *testing.T) {
	m := newTestModel()
	if chip := m.usageChip(); chip != "" {
		t.Fatalf("empty usage chip = %q", chip)
	}

	m.inputTokens, m.outputTokens = 1_200_000, 45_000
	m.cacheHits, m.cachePrompt = 1_100_000, 1_200_000
	chip := m.usageChip()
	if !strings.Contains(chip, "↑ 1.2M") || !strings.Contains(chip, "↓ 45.0k") ||
		!strings.Contains(chip, "cache 91.7%") {
		t.Fatalf("usage chip = %q", chip)
	}

	runtime := runtimeResolution{Provider: "default", ProviderSource: "store",
		Model: "deepseek-v4-flash", Context: 1_000_000, OK: true}
	m.contextUsed = 264_000
	m.runtime = runtime

	// The header keeps only the provider/model selection — the live
	// measures moved to the bottom bar.
	line := ansi.Strip(runtimeStatusLine(LocaleEN, runtime, "", 200))
	if strings.Contains(line, "cache") || strings.Contains(line, "ctx") ||
		!strings.Contains(line, "deepseek-v4-flash") {
		t.Fatalf("runtime line should be selection-only: %q", line)
	}

	// Tight header: the selection shrinks before truncating, no measures.
	line = ansi.Strip(runtimeStatusLine(LocaleEN, runtime, "", 12))
	if w := ansi.StringWidth(line); w > 11 {
		t.Fatalf("tight line too wide: %d (%q)", w, line)
	}

	// Bottom bar: cwd │ ctx gauge │ usage chip, in that order.
	bottom := ansi.Strip(m.bottomLine())
	if !strings.Contains(bottom, "ctx") || !strings.Contains(bottom, "↑ 1.2M ↓ 45.0k") ||
		!strings.Contains(bottom, "cache 91.7%") {
		t.Fatalf("bottom bar missing the live measures: %q", bottom)
	}
	if strings.Index(bottom, "ctx") > strings.Index(bottom, "cache") {
		t.Fatalf("bottom bar ordering: %q", bottom)
	}
}

func TestActivityLabelAndInputRule(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.width = 60
	m.connected = true
	if got := m.activityLabel(); got != "" {
		t.Fatalf("idle activity = %q, want empty", got)
	}

	m.busy = true
	label := m.activityLabel()
	if !strings.Contains(label, "Working") {
		t.Fatalf("busy activity = %q", label)
	}
	rule := ansi.Strip(m.inputRule(label))
	if !strings.Contains(rule, "── ") || !strings.Contains(rule, "Working") {
		t.Fatalf("input rule = %q", rule)
	}
	if w := ansi.StringWidth(rule); w != m.width-1 {
		t.Fatalf("input rule width = %d, want %d", w, m.width-1)
	}

	// A label too wide for the terminal degrades to a plain rule.
	m.width = 6
	if rule := ansi.Strip(m.inputRule(label)); strings.Contains(rule, "Working") {
		t.Fatalf("narrow rule kept the label: %q", rule)
	}
}

func TestChatBottomRowIsWorkspaceAndNote(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.width, m.height = 100, 30
	m.connected = true
	m.cwd = "/home/gokr/git/niffler-tui"
	m.layout()

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "niffler-tui") {
		t.Fatalf("bottom row missing the workspace:\n%s", view)
	}
	// The command list and key hints no longer occupy the bottom row.
	for _, stale := range []string{"/provider /model", "alt+enter: newline", "ctrl+r: history"} {
		if strings.Contains(view, stale) {
			t.Fatalf("bottom row still shows %q:\n%s", stale, view)
		}
	}

	// While busy, "Working" sits in the divider above the input, not in the
	// bottom row: exactly one line carries both the rule glyphs and the label.
	m.busy = true
	view = ansi.Strip(m.View().Content)
	ruleLines, bottomLines := 0, 0
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Working") {
			if strings.Contains(line, "──") {
				ruleLines++
			} else {
				bottomLines++
			}
		}
	}
	if ruleLines != 1 || bottomLines != 0 {
		t.Fatalf("working label placement: rule=%d other=%d\n%s", ruleLines, bottomLines, view)
	}

	// A transient note joins the workspace, context gauge and usage chip on
	// the same row (no usage chip yet: no tokens counted).
	m.busy = false
	m.contextNote = "context trimmed"
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "niffler-tui  │  ctx —  │  context trimmed") {
		t.Fatalf("note not on the bottom row:\n%s", view)
	}
}

// TestBusyLabelStates walks the busy label's signal ladder: a running tool
// names itself, fresh tokens say thinking or responding, a provider silent
// past the stall threshold says waiting, and working is the fallback. The
// elapsed suffix and mid-turn steer count ride on every variant.
func TestBusyLabelStates(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.busy = true

	// No signals at all: plain Working, no suffixes (no turn timestamp).
	if got := m.busyLabel(); got != "Working" {
		t.Fatalf("bare busy label = %q, want %q", got, "Working")
	}

	// A turn in progress always carries the elapsed time. In the first
	// seconds the fresh timestamp reads as still working.
	m.busySince = time.Now().Add(-3 * time.Second)
	m.lastTokenAt = m.busySince
	if got := m.busyLabel(); got != "Working (3s)" {
		t.Fatalf("elapsed label = %q, want %q", got, "Working (3s)")
	}

	// The same turn, still silent 42 seconds in: the provider has produced
	// nothing, so the label admits waiting — the elapsed keeps counting.
	m.busySince = time.Now().Add(-42 * time.Second)
	m.lastTokenAt = m.busySince
	if got := m.busyLabel(); got != "Waiting (42s)" {
		t.Fatalf("stalled label = %q, want %q", got, "Waiting (42s)")
	}

	// Fresh reasoning tokens say Thinking; content says Responding.
	m.lastTokenAt = time.Now()
	m.streamKind = streamThinking
	if got := m.busyLabel(); got != "Thinking (42s)" {
		t.Fatalf("thinking label = %q, want %q", got, "Thinking (42s)")
	}
	m.streamKind = streamResponding
	if got := m.busyLabel(); got != "Responding (42s)" {
		t.Fatalf("responding label = %q, want %q", got, "Responding (42s)")
	}

	// Stale stream tokens do not keep the streaming label: the freshness
	// check drops it and the silence reads as Waiting.
	m.lastTokenAt = time.Now().Add(-2 * activityStallAfter)
	if got := m.busyLabel(); got != "Waiting (42s)" {
		t.Fatalf("stale stream label = %q, want %q", got, "Waiting (42s)")
	}

	// A pending tool call dominates everything, naming itself with its
	// identifying argument.
	m.appendToolCall(toolCall{
		name:    "bash",
		args:    json.RawMessage(`{"command": "go build ./..."}`),
		pending: true,
	})
	if got := m.busyLabel(); got != "bash go build ./... (42s)" {
		t.Fatalf("tool label = %q, want %q", got, "bash go build ./... (42s)")
	}

	// File tools shorten the path to their basename; the scan also skips
	// finished calls and older pending ones — the latest pending call wins.
	m.appendToolCall(toolCall{name: "read", args: json.RawMessage(`{"path": "/x/y.go"}`)})
	m.appendToolCall(toolCall{
		name:    "edit",
		args:    json.RawMessage(`{"path": "/home/gokr/deep/repo/main.go"}`),
		pending: true,
	})
	if got := m.busyLabel(); got != "edit main.go (42s)" {
		t.Fatalf("edit label = %q, want %q", got, "edit main.go (42s)")
	}

	// Accepted mid-turn steers ride along as +N.
	m.steered = 2
	if got := m.busyLabel(); got != "edit main.go +2 (42s)" {
		t.Fatalf("steered label = %q, want %q", got, "edit main.go +2 (42s)")
	}
}

// TestFmtElapsed covers the compact turn-age shapes used by the label.
func TestFmtElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		42 * time.Second: "42s",
		65 * time.Second: "1m05s",
		(2*time.Hour + 3*time.Minute + 4*time.Second): "2h03m04s",
	} {
		if got := fmtElapsed(d); got != want {
			t.Errorf("fmtElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}
