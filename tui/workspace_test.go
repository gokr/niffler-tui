// Tests for the /roots command and the working-set display chips
// (workspace.go). The bus-facing paths are exercised through the same
// requestInto-shaped helpers the other commands use, so these cover the pure
// logic: path resolution, the readback text, the chips and the stale-reply
// guards.

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// mustGetwd is the process working directory, which resolveRootPath falls back
// to when the conversation workspace is not known yet.
func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("no working directory: %v", err)
	}
	return wd
}

func TestResolveRootPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	ws := filepath.Join(home, "git", "niffler-tui")

	tests := []struct {
		name      string
		argument  string
		workspace string
		want      string
		wantErr   bool
	}{
		{
			// The case the user asked for: core refuses a bare "~", so the
			// client expands it rather than bouncing the human's own shorthand
			// back as an error.
			name:     "tilde with a path expands to home",
			argument: "~/git/xxx",
			want:     filepath.Join(home, "git", "xxx"),
		},
		{
			name:     "bare tilde is home itself",
			argument: "~",
			want:     home,
		},
		{
			// core requires absolute; a relative path means "inside the
			// conversation", which is the base the model's own relative paths
			// resolve against.
			name:      "relative path resolves against the workspace",
			argument:  "../sibling",
			workspace: ws,
			want:      filepath.Join(home, "git", "sibling"),
		},
		{
			name:     "absolute path is left alone",
			argument: "/tmp/other",
			want:     "/tmp/other",
		},
		{
			name:     "trailing separator is tidied",
			argument: "/tmp/other/",
			want:     "/tmp/other",
		},
		{
			name:     "inner dot segments collapse",
			argument: "/tmp/./a/../b",
			want:     "/tmp/b",
		},
		{
			name:     "whitespace is trimmed",
			argument: "  /tmp/x  ",
			want:     "/tmp/x",
		},
		{
			// The fallback matches initialCwd(): a conversation with no header
			// yet is pinned to the launch directory, so a relative path means
			// the same thing before and after that pin lands.
			name:     "relative without a workspace falls back to the launch dir",
			argument: "sibling",
			want:     filepath.Join(mustGetwd(t), "sibling"),
		},
		{
			name:     "empty is refused",
			argument: "   ",
			wantErr:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRootPath(tc.argument, tc.workspace)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveRootPath(%q) = %q, want an error", tc.argument, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRootPath(%q) error: %v", tc.argument, err)
			}
			if got != tc.want {
				t.Fatalf("resolveRootPath(%q) = %q, want %q", tc.argument, got, tc.want)
			}
		})
	}
}

// TestBootstrapHeaderRootsAreFiltered pins the startup path. The conversation
// header mirrors core's stored policy, so it carries the primary workspace in
// grantedRoots even when the conversation has no grants — and it keeps carrying
// it after every grant is removed (verified against a live harness). Reading it
// unfiltered left a permanent "+1" on a conversation with nothing granted.
func TestBootstrapHeaderRootsAreFiltered(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.session = "sess-1"

	// The exact shape a live harness returned for a conversation whose only
	// grant had just been removed: the primary workspace, alone.
	updated, _ := m.Update(bootstrapMsg{
		Session: "sess-1",
		Conversation: conversationState{
			Cwd:          "/tmp/nif-probe-ws",
			GrantedRoots: []string{"/tmp/nif-probe-ws"},
		},
	})
	m = updated.(model)
	if got := m.rootsChip(); got != "" {
		t.Fatalf("a header carrying only the primary workspace showed a chip: %q (roots %v)",
			got, m.grantedRoots)
	}

	// With a real grant the chip counts just that one, not the primary too.
	m2 := newTestModel()
	m2.loc = LocaleEN
	m2.session = "sess-1"
	updated, _ = m2.Update(bootstrapMsg{
		Session: "sess-1",
		Conversation: conversationState{
			Cwd:          "/tmp/nif-probe-ws",
			GrantedRoots: []string{"/tmp/nif-probe-ws", "/tmp/other"},
		},
	})
	m2 = updated.(model)
	if got := m2.rootsChip(); got != "+1" {
		t.Fatalf("chip = %q, want +1 (primary must not be counted)", got)
	}
}

// TestGrantedFromResponseDropsPrimary pins the readback filter. core's stored
// policy seeds writeRoots with the primary workspace and adds grants to it, so
// a readback straight from the field lists the primary as if it were a grant —
// which would inflate the "+N" count and offer a removal that core refuses.
func TestGrantedFromResponseDropsPrimary(t *testing.T) {
	tests := []struct {
		name     string
		response sessionRootsResponse
		want     []string
	}{
		{
			name:     "no grants at all",
			response: sessionRootsResponse{Cwd: "/ws"},
			want:     nil,
		},
		{
			// The shape a live harness returned after a remove: the policy's
			// writeRoots still carries the primary.
			name:     "primary alone is not a grant",
			response: sessionRootsResponse{Cwd: "/ws", GrantedRoots: []string{"/ws"}},
			want:     nil,
		},
		{
			name: "primary plus a real grant",
			response: sessionRootsResponse{Cwd: "/ws",
				GrantedRoots: []string{"/ws", "/other"}},
			want: []string{"/other"},
		},
		{
			name: "a grant that merely shares a prefix is kept",
			response: sessionRootsResponse{Cwd: "/ws",
				GrantedRoots: []string{"/ws-other", "/other"}},
			want: []string{"/ws-other", "/other"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := grantedFromResponse(tc.response)
			if len(got) != len(tc.want) {
				t.Fatalf("grantedFromResponse = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("grantedFromResponse = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestRootsReport(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	primary := filepath.Join(home, "git", "niffler-tui")

	t.Run("no grants says how to make one", func(t *testing.T) {
		got := rootsReport(LocaleEN, primary, nil)
		if !strings.Contains(got, "~/git/niffler-tui") {
			t.Fatalf("primary workspace missing from readback: %q", got)
		}
		// The empty state is the discoverability surface for the command: it
		// must name the subcommand that grants one.
		if !strings.Contains(got, "/roots add") {
			t.Fatalf("empty readback does not teach the command: %q", got)
		}
	})

	t.Run("grants are listed with the count", func(t *testing.T) {
		granted := []string{filepath.Join(home, "other-repo"), "/tmp/abs"}
		got := rootsReport(LocaleEN, primary, granted)
		if !strings.Contains(got, "2") {
			t.Fatalf("grant count missing: %q", got)
		}
		for _, want := range []string{"~/other-repo", "/tmp/abs", "~/git/niffler-tui"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%q missing from readback: %q", want, got)
			}
		}
		// The readback is also where removal is taught.
		if !strings.Contains(got, "/roots remove") {
			t.Fatalf("readback does not name the removal path: %q", got)
		}
	})
}

func TestWorkspaceChips(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN

	// An ordinary conversation pays no width: no branch, no roots.
	if got := m.rootsChip(); got != "" {
		t.Fatalf("rootsChip with no grants = %q, want empty", got)
	}
	if got := m.branchChip(); got != "" {
		t.Fatalf("branchChip with no branch = %q, want empty", got)
	}

	m.grantedRoots = []string{"/a", "/b"}
	if got := m.rootsChip(); got != "+2" {
		t.Fatalf("rootsChip with 2 grants = %q, want +2", got)
	}
	m.grantedRoots = nil

	m.branch = "main"
	if got := m.branchChip(); got != "(main)" {
		t.Fatalf("branchChip = %q, want (main)", got)
	}
	// A detached HEAD is reported as git spells it, not invented.
	m.branch = "HEAD"
	if got := m.branchChip(); got != "(HEAD)" {
		t.Fatalf("detached branchChip = %q, want (HEAD)", got)
	}
}

// TestBottomLineCarriesWorkspaceChips pins the row layout: the workspace is
// the anchor and keeps its existing position, with the branch and the grant
// count as qualifiers immediately after it.
func TestBottomLineCarriesWorkspaceChips(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.width = 200
	m.cwd = "/home/someone/git/niffler-tui"
	m.branch = "feat/roots"
	m.grantedRoots = []string{"/a", "/b", "/c"}

	line := m.bottomLine()
	ws := strings.Index(line, "niffler-tui")
	branch := strings.Index(line, "(feat/roots)")
	roots := strings.Index(line, "+3")
	if ws < 0 || branch < 0 || roots < 0 {
		t.Fatalf("bottom row missing a chip: %q", line)
	}
	if !(ws < branch && branch < roots) {
		t.Fatalf("chip order = workspace %d, branch %d, roots %d (want that order): %q",
			ws, branch, roots, line)
	}
}

func TestLooksLikePath(t *testing.T) {
	// A path is a grant; a bare word is a mistyped subcommand.
	for _, word := range []string{"/tmp/x", "~/git/x", "./rel", "../rel", "a/b"} {
		if !looksLikePath(word) {
			t.Errorf("looksLikePath(%q) = false, want true", word)
		}
	}
	for _, word := range []string{"ad", "remove", "rm", "xyz"} {
		if looksLikePath(word) {
			t.Errorf("looksLikePath(%q) = true, want false", word)
		}
	}
}

// TestRootsDispatch covers the argument shapes a human actually types,
// including the sugar where a bare path means add.
func TestRootsDispatch(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.cwd = t.TempDir()
	cmd, _ := builtinCommand("roots")

	t.Run("bare path is a grant, not an unknown subcommand", func(t *testing.T) {
		updated, _ := localRoots(m, cmd, "~/git/xxx")
		got := updated.(model)
		blocks := got.blocks
		if len(blocks) == 0 {
			t.Fatal("no block rendered for a bare-path grant")
		}
		last := blocks[len(blocks)-1]
		if last.kind == blockError {
			t.Fatalf("bare path was rejected: %q", last.text)
		}
		if !strings.Contains(last.text, "add") {
			t.Fatalf("grant echo does not name the action: %q", last.text)
		}
	})

	t.Run("a mistyped subcommand is reported with the valid ones", func(t *testing.T) {
		updated, _ := localRoots(m, cmd, "ad /tmp/x")
		got := updated.(model)
		last := got.blocks[len(got.blocks)-1]
		if last.kind != blockError {
			t.Fatalf("mistyped subcommand did not error: %q", last.text)
		}
		if !strings.Contains(last.text, "ad") {
			t.Fatalf("error does not quote the bad word: %q", last.text)
		}
	})

	t.Run("empty add reports usage instead of sending", func(t *testing.T) {
		updated, _ := localRoots(m, cmd, "add")
		got := updated.(model)
		last := got.blocks[len(got.blocks)-1]
		if last.kind != blockError || !strings.Contains(last.text, "add") {
			t.Fatalf("empty add = %q (kind %v), want a usage error", last.text, last.kind)
		}
	})

	t.Run("remove accepts its rm alias", func(t *testing.T) {
		updated, _ := localRoots(m, cmd, "rm /tmp/x")
		got := updated.(model)
		last := got.blocks[len(got.blocks)-1]
		if last.kind == blockError {
			t.Fatalf("rm alias rejected: %q", last.text)
		}
	})

	t.Run("disconnected client refuses instead of sending", func(t *testing.T) {
		off := m
		off.connected = false
		before := len(off.blocks)
		updated, _ := localRoots(off, cmd, "add /tmp/x")
		if got := updated.(model); len(got.blocks) != before {
			t.Fatalf("block rendered while disconnected: %+v", got.blocks[before:])
		}
	})
}

// TestRootsMessagesApplyAndGuardStale covers the message handlers: a grant
// updates the chip, and a reply for another conversation is dropped so a
// switch cannot show the previous conversation's roots.
func TestRootsMessagesApplyAndGuardStale(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.session = "sess-1"

	updated, _ := m.Update(rootChangeMsg{
		Session: "sess-1", Action: "add", Path: "/tmp/granted",
		Roots: []string{"/tmp/granted"},
	})
	m = updated.(model)
	if got := m.rootsChip(); got != "+1" {
		t.Fatalf("chip after a grant = %q, want +1", got)
	}
	last := m.blocks[len(m.blocks)-1]
	if !strings.Contains(last.text, "/tmp/granted") {
		t.Fatalf("grant confirmation = %q", last.text)
	}

	// A stale reply (another conversation) must not touch the current view.
	updated, _ = m.Update(rootChangeMsg{
		Session: "sess-other", Action: "add", Path: "/tmp/elsewhere",
		Roots: []string{"/tmp/elsewhere", "/tmp/granted"},
	})
	m = updated.(model)
	if got := m.rootsChip(); got != "+1" {
		t.Fatalf("stale roots reply changed the chip: %q", got)
	}

	// A refusal keeps the previous set and reports core's reason, which is
	// the whole story for the human (relative, missing, primary, "/").
	updated, _ = m.Update(rootChangeMsg{
		Session: "sess-1", Action: "add",
		Err: errors.New("root must be absolute (got sibling)"),
	})
	m = updated.(model)
	if got := m.rootsChip(); got != "+1" {
		t.Fatalf("refusal changed the chip: %q", got)
	}
	last = m.blocks[len(m.blocks)-1]
	if last.kind != blockError || !strings.Contains(last.text, "absolute") {
		t.Fatalf("refusal block = %q (kind %v)", last.text, last.kind)
	}
}

// TestBranchMsgGuardsStaleDir: a branch read answers for one directory, so a
// reply for a workspace we have left must be dropped rather than label the
// new one with another repo's branch.
func TestBranchMsgGuardsStaleDir(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.cwd = "/ws/current"

	updated, _ := m.Update(branchMsg{Dir: "/ws/other", Branch: "wrong-branch"})
	m = updated.(model)
	if m.branch != "" {
		t.Fatalf("stale branch reply applied: %q", m.branch)
	}

	updated, _ = m.Update(branchMsg{Dir: "/ws/current", Branch: "main"})
	m = updated.(model)
	if m.branch != "main" {
		t.Fatalf("branch reply not applied: %q", m.branch)
	}

	// A failure is not a note — no repo and no git simply omit the chip.
	before := len(m.blocks)
	m.contextNote = ""
	updated, _ = m.Update(branchMsg{Dir: "/ws/current", Err: errors.New("not a repo")})
	m = updated.(model)
	if m.contextNote != "" {
		t.Fatalf("a git failure raised a note: %q", m.contextNote)
	}
	if len(m.blocks) != before {
		t.Fatalf("a git failure rendered blocks: %+v", m.blocks[before:])
	}
}

func TestLoadBranchCmdSkipsRemoteAndEmpty(t *testing.T) {
	// Remote: cwd names a directory on another machine, so a local git read
	// would report an unrelated repo's branch.
	if cmd := loadBranchCmd("/ws", true); cmd != nil {
		t.Fatal("loadBranchCmd ran for a remote instance")
	}
	if cmd := loadBranchCmd("", false); cmd != nil {
		t.Fatal("loadBranchCmd ran without a workspace")
	}
	if cmd := loadBranchCmd(t.TempDir(), false); cmd == nil {
		t.Fatal("loadBranchCmd skipped a local workspace")
	}
}

// ---- worktree detection and /workspace ------------------------------------

// TestLoadBranchCmdDetectsWorktreeShape runs against real git repositories. The
// distinction is the point: a linked worktree and the primary checkout are both
// just directories with a branch, and only git's own two answers tell them
// apart, so this drives git rather than mocking it.
func TestLoadBranchCmdDetectsWorktreeShape(t *testing.T) {
	if _, err := execLookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	primary := t.TempDir()
	gitRun(t, primary, "init", "-q", "-b", "main")
	gitRun(t, primary, "config", "user.email", "t@example.invalid")
	gitRun(t, primary, "config", "user.name", "workspace test")
	osWrite(t, filepath.Join(primary, "README.md"), "hello\n")
	gitRun(t, primary, "add", "README.md")
	gitRun(t, primary, "commit", "-q", "-m", "init")

	linked := filepath.Join(t.TempDir(), "wt")
	gitRun(t, primary, "worktree", "add", "-q", "-b", "feat/x", linked)

	t.Run("primary checkout", func(t *testing.T) {
		msg, ok := loadBranchCmd(primary, false)().(branchMsg)
		if !ok {
			t.Fatal("not a branchMsg")
		}
		if msg.Err != nil {
			t.Fatalf("err: %v", msg.Err)
		}
		if msg.Branch != "main" {
			t.Fatalf("branch = %q, want main", msg.Branch)
		}
		if msg.Worktree {
			t.Fatalf("primary checkout reported as a linked worktree (primary=%q)", msg.Primary)
		}
		if msg.Primary != "" {
			t.Fatalf("primary checkout named a primary: %q", msg.Primary)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		msg, ok := loadBranchCmd(linked, false)().(branchMsg)
		if !ok {
			t.Fatal("not a branchMsg")
		}
		if msg.Err != nil {
			t.Fatalf("err: %v", msg.Err)
		}
		if msg.Branch != "feat/x" {
			t.Fatalf("branch = %q, want feat/x", msg.Branch)
		}
		if !msg.Worktree {
			t.Fatal("linked worktree not detected")
		}
		// The primary is derivable because git reports an absolute common dir
		// for a linked worktree; losing it would leave the report unable to
		// name what the worktree belongs to.
		if filepath.Base(msg.Primary) != filepath.Base(primary) {
			t.Fatalf("primary = %q, want the primary checkout %q", msg.Primary, primary)
		}
	})

	t.Run("plain directory is not a repo", func(t *testing.T) {
		msg, ok := loadBranchCmd(t.TempDir(), false)().(branchMsg)
		if !ok {
			t.Fatal("not a branchMsg")
		}
		if msg.Err == nil {
			t.Fatalf("a non-repo reported success: branch=%q worktree=%v", msg.Branch, msg.Worktree)
		}
		if msg.Branch != "" || msg.Worktree {
			t.Fatalf("a non-repo carried git facts: %+v", msg)
		}
	})
}

func TestBranchChipMarksWorktree(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN

	m.branch = "feat/x"
	m.worktree = false
	if got := m.branchChip(); got != "(feat/x)" {
		t.Fatalf("primary chip = %q, want (feat/x)", got)
	}
	// A linked worktree is marked so two checkouts of one repo are
	// distinguishable on screen.
	m.worktree = true
	got := m.branchChip()
	if got != "(feat/x ⧉)" {
		t.Fatalf("worktree chip = %q, want (feat/x ⧉)", got)
	}
	if ansi.StringWidth(got) != ansi.StringWidth("(feat/x ⧉)") {
		t.Fatalf("marker width is not single-column: %q", got)
	}
}

func TestWorkspaceReport(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	cwd := filepath.Join(home, "git", "niffler-tui-fleet")

	t.Run("primary checkout", func(t *testing.T) {
		got := workspaceReport(LocaleEN, cwd, "main", false, "", nil, true)
		if !strings.Contains(got, "~/git/niffler-tui-fleet") {
			t.Fatalf("path missing: %q", got)
		}
		if !strings.Contains(got, "main") {
			t.Fatalf("branch missing: %q", got)
		}
		if strings.Contains(got, "LINKED") {
			t.Fatalf("primary checkout claimed to be a worktree: %q", got)
		}
	})

	t.Run("linked worktree names the primary", func(t *testing.T) {
		got := workspaceReport(LocaleEN, cwd, "feat/fleet", true,
			filepath.Join(home, "git", "niffler-tui"), nil, true)
		if !strings.Contains(got, "LINKED") {
			t.Fatalf("worktree not called out: %q", got)
		}
		if !strings.Contains(got, "~/git/niffler-tui") {
			t.Fatalf("primary checkout missing: %q", got)
		}
	})

	t.Run("not a repo shows no branch line", func(t *testing.T) {
		got := workspaceReport(LocaleEN, cwd, "", false, "", nil, true)
		if strings.Contains(got, "branch") {
			t.Fatalf("branch line rendered without a branch: %q", got)
		}
	})

	t.Run("grant count is reported", func(t *testing.T) {
		got := workspaceReport(LocaleEN, cwd, "main", false, "", []string{"/a", "/b"}, true)
		if !strings.Contains(got, "2") {
			t.Fatalf("grant count missing: %q", got)
		}
	})

	t.Run("an unchosen workspace says so", func(t *testing.T) {
		got := workspaceReport(LocaleEN, cwd, "main", false, "", nil, false)
		if !strings.Contains(got, "not chosen") {
			t.Fatalf("default workspace not called out: %q", got)
		}
		chosen := workspaceReport(LocaleEN, cwd, "main", false, "", nil, true)
		if strings.Contains(chosen, "not chosen") {
			t.Fatalf("a chosen workspace claimed to be a default: %q", chosen)
		}
	})
}

// TestWorkspaceChangeAppliesAndGuardsStale mirrors the roots handlers: a repin
// updates the row, and a reply for another conversation is dropped.
func TestWorkspaceChangeAppliesAndGuardsStale(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.session = "sess-1"
	m.cwd = "/old"
	// Stale git facts must not survive a repin: they describe the old path.
	m.branch, m.worktree, m.primary = "old-branch", true, "/primary"

	updated, _ := m.Update(workspaceChangeMsg{
		Session: "sess-1", Path: "/new", Cwd: "/new",
	})
	m = updated.(model)
	if m.cwd != "/new" {
		t.Fatalf("cwd = %q, want /new", m.cwd)
	}
	if m.branch != "" || m.worktree || m.primary != "" {
		t.Fatalf("stale git facts survived a repin: branch=%q worktree=%v primary=%q",
			m.branch, m.worktree, m.primary)
	}

	// A refusal (the immutability case) keeps the current workspace and
	// surfaces core's reason verbatim.
	before := m.cwd
	updated, _ = m.Update(workspaceChangeMsg{
		Session: "sess-1", Path: "/nope",
		Err: errors.New("conversation cwd is immutable once turns exist (currently /new); start a new conversation to work elsewhere"),
	})
	m = updated.(model)
	if m.cwd != before {
		t.Fatalf("a refused repin changed the workspace: %q", m.cwd)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockError || !strings.Contains(last.text, "immutable") {
		t.Fatalf("refusal block = %q (kind %v)", last.text, last.kind)
	}

	// A stale reply from another conversation must not move this one.
	updated, _ = m.Update(workspaceChangeMsg{
		Session: "sess-other", Path: "/elsewhere", Cwd: "/elsewhere",
	})
	m = updated.(model)
	if m.cwd != before {
		t.Fatalf("stale repin reply moved the workspace: %q", m.cwd)
	}
}

func TestLocalWorkspaceReportsWithoutArgument(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.cwd = "/ws"
	m.branch = "main"
	cmd, _ := builtinCommand("workspace")

	updated, got := localWorkspace(m, cmd, "")
	out := updated.(model)
	if got != nil {
		t.Fatal("a bare /workspace returned a command (it is a local readback)")
	}
	last := out.blocks[len(out.blocks)-1]
	if !strings.Contains(last.text, "/ws") {
		t.Fatalf("readback = %q", last.text)
	}
}

func TestLocalWorkspaceRepinsWithArgument(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = true
	m.cwd = t.TempDir()
	cmd, _ := builtinCommand("workspace")

	updated, got := localWorkspace(m, cmd, "/tmp")
	out := updated.(model)
	if got == nil {
		t.Fatal("a repin returned no command")
	}
	if len(out.blocks) == 0 {
		t.Fatal("no echo block for the repin")
	}
	if !strings.Contains(out.blocks[len(out.blocks)-1].text, "/tmp") {
		t.Fatalf("repin echo = %q", out.blocks[len(out.blocks)-1].text)
	}
}

func TestLocalWorkspaceRefusesWhenDisconnected(t *testing.T) {
	m := newTestModel()
	m.loc = LocaleEN
	m.connected = false
	cmd, _ := builtinCommand("workspace")

	updated, got := localWorkspace(m, cmd, "")
	out := updated.(model)
	if got != nil {
		t.Fatal("returned a command while disconnected")
	}
	if out.contextNote == "" {
		t.Fatal("no note explaining the refusal")
	}
}

// gitRun runs one git command in dir, failing the test on a nonzero exit.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

func osWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// execLookPath is os/exec.LookPath behind a tiny wrapper so the test file's
// imports stay obvious.
func execLookPath(name string) (string, error) { return exec.LookPath(name) }
