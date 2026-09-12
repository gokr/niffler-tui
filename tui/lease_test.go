package main

import (
	"fmt"
	"regexp"
	"testing"
)

// The TUI's unique identity: hex, stable per call site, unique across
// processes. It becomes the component-name suffix ("tui-<id>"), which is
// what makes approval routing private per terminal.
func TestNewUIID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newUIID()
		if !re.MatchString(id) {
			t.Fatalf("newUIID = %q, want 12 lowercase hex chars", id)
		}
		if seen[id] {
			t.Fatalf("newUIID repeated %q across %d draws", id, i+1)
		}
		seen[id] = true
	}
}

// Conversation ids must never collide on wall-clock seconds: two /new
// presses in the same second used to pick the same "conv-<unixtime>".
func TestNewSessionIDUniqueAndShaped(t *testing.T) {
	re := regexp.MustCompile(`^conv-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newSessionID()
		if !re.MatchString(id) {
			t.Fatalf("newSessionID = %q, want conv-<12 hex>", id)
		}
		if seen[id] {
			t.Fatalf("newSessionID repeated %q across %d draws", id, i+1)
		}
		seen[id] = true
	}
}

// The resume-state file is keyed by bus URL AND workspace: starting the TUI
// in a different project must not resume the previous project's
// conversation.
func TestSessionFilePathWorkspaceKeyed(t *testing.T) {
	a := sessionFilePath("nats://127.0.0.1:4222", "/home/me/alpha")
	b := sessionFilePath("nats://127.0.0.1:4222", "/home/me/beta")
	c := sessionFilePath("nats://127.0.0.1:4222", "/home/me/alpha")
	if a == b {
		t.Fatalf("different workspaces share a state file: %q", a)
	}
	if a != c {
		t.Fatalf("same harness+workspace must be stable: %q vs %q", a, c)
	}
}

// uiStartupDecision is the pure ownership rule: keep the conversation when
// we own it (or coordination is unavailable); start a fresh one and say why
// when another live UI holds it.
func TestUIStartupDecision(t *testing.T) {
	const newID = "conv-fresh"

	owned := uiAttachMsg{session: "console", registered: true, number: 1, claimed: true}
	if switchTo, note := uiStartupDecision(owned, newID); switchTo != "" || note != "" {
		t.Fatalf("owned conversation should be kept, got %q / %q", switchTo, note)
	}

	// Registry unreachable: coordination degrades silently, chat works.
	degraded := uiAttachMsg{session: "console", err: fmt.Errorf("no route")}
	if switchTo, note := uiStartupDecision(degraded, newID); switchTo != "" || note != "" {
		t.Fatalf("registry outage should keep the session, got %q / %q", switchTo, note)
	}

	// Held by Niffler 2: start fresh and say who holds it.
	held := uiAttachMsg{session: "console", registered: true, number: 3,
		claimed: false, ownerNumber: 2}
	switchTo, note := uiStartupDecision(held, newID)
	if switchTo != newID {
		t.Fatalf("held conversation should switch to %q, got %q", newID, switchTo)
	}
	if want := "This conversation is open in Niffler 2 — started a new conversation."; note != want {
		t.Fatalf("note = %q, want %q", note, want)
	}

	// Refused without an owner number (registry quirk): still start fresh,
	// with the generic wording.
	quirk := uiAttachMsg{session: "console", registered: true, claimed: false}
	switchTo, note = uiStartupDecision(quirk, newID)
	if switchTo != newID {
		t.Fatalf("refused claim should switch to %q, got %q", newID, switchTo)
	}
	if want := "This conversation is open in another UI — started a new conversation."; note != want {
		t.Fatalf("note = %q, want %q", note, want)
	}
}
