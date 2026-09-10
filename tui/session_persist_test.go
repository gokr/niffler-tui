// Last-session persistence: the TUI remembers the conversation it was last
// switched to (per harness, keyed by the bus URL) and resumes it on the next
// launch unless -session or NIF_SESSION says otherwise.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain isolates per-user state (theme, locale, sent-message history,
// last session) from the developer's machine for the whole package.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "niffler-tui-test-state-")
	if err != nil {
		os.Exit(m.Run())
	}
	_ = os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestLastSessionPersistRoundtrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	if got := loadLastSession(url); got != "" {
		t.Fatalf("empty state dir returned %q", got)
	}
	persistSession(url, "conv-123")
	if got := loadLastSession(url); got != "conv-123" {
		t.Fatalf("roundtrip = %q", got)
	}

	// A different harness (bus URL) keeps its own last session.
	if got := loadLastSession("nats://127.0.0.1:49999"); got != "" {
		t.Fatalf("other harness resumed %q", got)
	}

	// Recorded ids are sanitized on load, so a hand-edited file can never
	// inject a bus-unsafe session id.
	path := sessionFilePath(url)
	if err := os.WriteFile(path, []byte("weird/../id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadLastSession(url)
	if got == "" || strings.ContainsAny(got, "/.") {
		t.Fatalf("unsanitized session id loaded: %q", got)
	}

	// The file lives in the shared state dir next to theme/locale.
	if dir := filepath.Dir(path); !strings.HasSuffix(dir, filepath.Join("niffler-tui")) {
		t.Fatalf("session file dir = %q", dir)
	}
}

func TestSwitchSessionPersistsLastSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newTestModel()
	m.natsURL = "nats://persist-test:4222"

	switched := m.switchSession("conv-later")
	if switched.session != "conv-later" {
		t.Fatalf("session = %q", switched.session)
	}
	if got := loadLastSession(m.natsURL); got != "conv-later" {
		t.Fatalf("switch did not persist: %q", got)
	}
}
