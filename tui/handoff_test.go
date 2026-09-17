// Restart handoff: /restart leaves the successor its registry identity so the
// conversation (and the "Niffler N" label) survive a restart whose release
// never reached core. These tests pin the record's lifecycle without a bus:
// round trip, one-shot consumption, staleness, keying, and sanitizing.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandoffRoundtripIsConsumedOnce(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	writeHandoff(url, "cwd-x", "tui-6f1a9c02b7d4", "conv-carried")
	ui, session := consumeHandoff(url, "cwd-x", time.Now())
	if ui != "tui-6f1a9c02b7d4" || session != "conv-carried" {
		t.Fatalf("handoff = (%q, %q)", ui, session)
	}
	// One restart, one inheritance: a later start must not adopt the id again.
	if ui, session := consumeHandoff(url, "cwd-x", time.Now()); ui != "" || session != "" {
		t.Fatalf("handoff was inherited twice: (%q, %q)", ui, session)
	}
}

func TestHandoffIsKeyedByHarnessAndWorkspace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	writeHandoff(url, "cwd-a", "tui-1a2b3c4d5e6f", "conv-a")
	// Another harness (its own store) and another project each have their own
	// handoff slot, so neither can resurrect this identity.
	if ui, _ := consumeHandoff("nats://127.0.0.1:49999", "cwd-a", time.Now()); ui != "" {
		t.Fatalf("other harness adopted %q", ui)
	}
	if ui, _ := consumeHandoff(url, "cwd-b", time.Now()); ui != "" {
		t.Fatalf("other workspace adopted %q", ui)
	}
	if ui, session := consumeHandoff(url, "cwd-a", time.Now()); ui != "tui-1a2b3c4d5e6f" || session != "conv-a" {
		t.Fatalf("own handoff lost: (%q, %q)", ui, session)
	}
}

func TestHandoffStaleness(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	writeHandoff(url, "cwd-x", "tui-deadbeef0001", "conv-old")
	// The restart never came (run without the wrapper, or the rebuilt binary
	// would not start): the identity must not be resurrected later.
	late := time.Now().Add(handoffTTL + time.Second)
	if ui, session := consumeHandoff(url, "cwd-x", late); ui != "" || session != "" {
		t.Fatalf("stale handoff adopted: (%q, %q)", ui, session)
	}
	// ... and it is gone, not merely ignored.
	if _, err := os.Stat(handoffFilePath(url, "cwd-x")); !os.IsNotExist(err) {
		t.Fatalf("stale handoff file survived: %v", err)
	}
}

func TestHandoffSanitizesRecordedIDs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	writeHandoff(url, "cwd-x", "../../etc/passwd", "weird/../session")
	ui, session := consumeHandoff(url, "cwd-x", time.Now())
	if ui == "" || session == "" {
		t.Fatalf("handoff dropped instead of sanitized: (%q, %q)", ui, session)
	}
	if strings.ContainsAny(ui, "/.") || strings.ContainsAny(session, "/.") {
		t.Fatalf("unsanitized handoff: (%q, %q)", ui, session)
	}
}

func TestHandoffIgnoresMissingAndCorruptRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	if ui, session := consumeHandoff(url, "cwd-x", time.Now()); ui != "" || session != "" {
		t.Fatalf("missing record produced (%q, %q)", ui, session)
	}
	path := handoffFilePath(url, "cwd-x")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ui, session := consumeHandoff(url, "cwd-x", time.Now()); ui != "" || session != "" {
		t.Fatalf("corrupt record produced (%q, %q)", ui, session)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt record survived: %v", err)
	}
}

func TestWriteHandoffWithoutIdentityIsNoOp(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	writeHandoff("nats://127.0.0.1:4222", "cwd-x", "", "conv-x")
	if ui, _ := consumeHandoff("nats://127.0.0.1:4222", "cwd-x", time.Now()); ui != "" {
		t.Fatalf("empty uiID produced a handoff %q", ui)
	}
}
