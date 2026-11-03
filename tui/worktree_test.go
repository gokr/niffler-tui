package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorktreeBirthDecision(t *testing.T) {
	for _, tc := range []struct {
		pristine, pinned, dirty, fork bool
		want                          string
	}{
		{true, true, false, false, "move"}, {false, false, false, false, "move"},
		{false, true, false, false, "fork"}, {true, false, true, false, "refuse"},
		{false, true, true, false, "refuse"}, {false, true, true, true, "fork"},
	} {
		if got := worktreeBirthDecision(tc.pristine, tc.pinned, tc.dirty, tc.fork); got != tc.want {
			t.Fatalf("%+v: %s", tc, got)
		}
	}
}
func TestWorktreePorcelainDirt(t *testing.T) {
	if worktreeStatusDirty("(exit 0)\n## main\n") {
		t.Fatal("clean branch counted as dirty")
	}
	for _, s := range []string{" D gone", "R  old -> new", "UU conflict", "?? new", " M edited", "[capped]"} {
		if !worktreeStatusDirty("## main\n" + s) {
			t.Fatal("missed dirt: " + s)
		}
	}
}
func TestWorktreeCloseSwitchesToOriginAndIgnoresStale(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newTestModel()
	m.session = "fork"
	m.controlPending = true
	updated, _ := m.Update(worktreeMsg{Session: "fork", Target: "origin", Text: "returned to origin"})
	got := updated.(model)
	if got.session != "origin" {
		t.Fatalf("close switched to %q", got.session)
	}
	updated, _ = got.Update(worktreeMsg{Session: "fork", Target: "wrong"})
	if updated.(model).session != "origin" {
		t.Fatal("stale operation switched session")
	}
}
func TestWorktreeIsolationChip(t *testing.T) {
	m := newTestModel()
	m.branch = "feat"
	m.worktree = true
	m.isolation = "worktree"
	if !strings.Contains(m.branchChip(), "isolated") {
		t.Fatal(m.branchChip())
	}
}

// A MOVED conversation has no origin, and its workspace is immutable. close
// must therefore open a fresh conversation in the primary — and must do it
// BEFORE removing anything, so a failure leaves the tree and transcript intact.
func TestCloseWorktreeMovedConversationOpensPrimary(t *testing.T) {
	info := worktreeInfo{}
	info.Workspace.Linked = true
	info.Workspace.Primary = "/repo"
	prepared := ""
	removed := false
	target, cwd, detail, err := closeWorktree(info, "fresh-1", func() string { return "" },
		func(id, dir string) error { prepared = id + "@" + dir; return nil },
		func() error { removed = true; return nil })
	if err != nil {
		t.Fatalf("moved close failed: %v", err)
	}
	if prepared != "fresh-1@/repo" {
		t.Fatalf("prepared %q", prepared)
	}
	if target != "fresh-1" || cwd != "/repo" {
		t.Fatalf("target=%q cwd=%q", target, cwd)
	}
	if !removed {
		t.Fatal("worktree was not removed")
	}
	if !strings.Contains(detail, "preserved") {
		t.Fatalf("detail does not say the transcript survived: %q", detail)
	}
}

// A FORK still returns to its origin, and does not mint a conversation.
func TestCloseWorktreeForkReturnsToOrigin(t *testing.T) {
	info := worktreeInfo{}
	info.Workspace.Linked = true
	info.Workspace.Primary = "/repo"
	info.Origin.Session = "origin-1"
	minted := false
	target, _, detail, err := closeWorktree(info, "", func() string { return "" },
		func(id, dir string) error { minted = true; return nil },
		func() error { return nil })
	if err != nil {
		t.Fatalf("fork close failed: %v", err)
	}
	if minted || target != "origin-1" || !strings.Contains(detail, "origin") {
		t.Fatalf("minted=%v target=%q detail=%q", minted, target, detail)
	}
}

// If the primary conversation cannot be prepared, NOTHING is removed: the tree
// and the transcript a human is still looking at must survive a failed close.
func TestCloseWorktreeFailureRemovesNothing(t *testing.T) {
	info := worktreeInfo{}
	info.Workspace.Linked = true
	info.Workspace.Primary = "/repo"
	removed := false
	_, _, _, err := closeWorktree(info, "fresh-1", func() string { return "" },
		func(id, dir string) error { return fmt.Errorf("store down") },
		func() error { removed = true; return nil })
	if err == nil {
		t.Fatal("a failed prepare must be reported")
	}
	if removed {
		t.Fatal("the worktree was removed even though the destination failed")
	}
}

// Not a worktree at all: refuse rather than removing something unrelated.
func TestCloseWorktreeRefusesOutsideWorktree(t *testing.T) {
	info := worktreeInfo{}
	info.Workspace.Primary = "/repo"
	removed := false
	if _, _, _, err := closeWorktree(info, "x", func() string { return "/repo" },
		func(id, dir string) error { return nil },
		func() error { removed = true; return nil }); err == nil || removed {
		t.Fatalf("err=%v removed=%v", err, removed)
	}
}

// The reported failure: /worktree add on a conversation that has not run a turn
// yet. session_info has no header to read and answers "no conversation '<id>'",
// which must not block the command — that is exactly when creating a tree is
// most useful.
func TestInfoFailureAllowsAFreshConversation(t *testing.T) {
	if err := infoFailure("core.session_info: no conversation 'conv-e5760bb7a9ba'"); err != nil {
		t.Fatalf("a fresh conversation was refused: %v", err)
	}
	if err := infoFailure(""); err != nil {
		t.Fatalf("no error was treated as one: %v", err)
	}
	if err := infoFailure("core.session_info: store unavailable"); err == nil {
		t.Fatal("a real failure must still be reported")
	}
}
