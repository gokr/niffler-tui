package main

import (
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
