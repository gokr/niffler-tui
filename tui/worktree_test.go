package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorktreeAddSelectsWorkspaceBeforeGit(t *testing.T) {
	for _, session := range []string{"startup", "ownership-fresh"} {
		t.Run(session, func(t *testing.T) {
			cwd, pinned, infos := "/harness", false, 0
			request := func(component, tool string, args map[string]any, out any) error {
				if args["sessionId"] != session {
					t.Fatalf("wrong session: %v", args)
				}
				switch tool {
				case "session_info":
					infos++
					info := out.(*worktreeInfo)
					if infos == 1 {
						info.Error = "no conversation"
					} else {
						info.Workspace.Primary = cwd
						info.Workspace.Branch = "main"
					}
				case "session":
					if dir, ok := args["cwd"].(string); ok {
						cwd, pinned = dir, true
					}
					*out.(*sessionRootsResponse) = sessionRootsResponse{Cwd: cwd, CwdPinned: pinned}
				default:
					t.Fatalf("unexpected request %s.%s", component, tool)
				}
				return nil
			}
			invoke := func(id, tool string, args map[string]any, out any) error {
				if id != session || !pinned || cwd != "/project" || args["repo"] != "/project" {
					t.Fatalf("git before workspace selection: %s %s %v", id, cwd, args)
				}
				if tool == "git_worktree" {
					if args["base"] != "main" {
						t.Fatalf("stale branch: %v", args)
					}
					v := out.(*struct{ Path, Branch string })
					v.Path, v.Branch = "/tree", "feature"
				}
				return nil
			}
			msg := worktreeCommandWith(session, "/project", "add", "feature", false, request, invoke, nil)().(worktreeMsg)
			if msg.Err != nil || msg.Cwd != "/tree" || cwd != "/tree" || infos != 2 {
				t.Fatalf("result=%+v cwd=%s infos=%d", msg, cwd, infos)
			}
		})
	}
}

// A dirty tree no longer refuses: `git worktree add` cuts a sibling tree from a
// commit, so uncommitted changes never block it. What they change is whether the
// CONVERSATION can follow — a transcript that describes edits which stayed
// behind must not silently claim the new tree.
func TestWorktreeBirthDecision(t *testing.T) {
	for _, tc := range []struct {
		pristine, pinned, dirty, fork bool
		want                          string
		name                          string
	}{
		{true, false, false, false, "move", "fresh conversation"},
		{true, true, false, false, "move", "fresh, workspace chosen"},
		{true, false, true, false, "move", "fresh conversation in a dirty tree"},
		{true, false, false, true, "fork", "fresh, but keep the original"},
		{false, false, false, false, "move", "turns, workspace never chosen"},
		{false, true, false, false, "fork", "turns, workspace chosen"},
		{false, false, true, false, "fork", "turns, dirty tree"},
		{false, true, true, false, "fork", "turns, chosen and dirty"},
	} {
		if got := worktreeBirthDecision(tc.pristine, tc.pinned, tc.dirty, tc.fork); got != tc.want {
			t.Fatalf("%s: %+v → %s, want %s", tc.name, tc, got, tc.want)
		}
		// Whatever the inputs, a tree request is never refused.
		if got := worktreeBirthDecision(tc.pristine, tc.pinned, tc.dirty, tc.fork); got == "refuse" {
			t.Fatalf("%s: refused", tc.name)
		}
	}
}

// The report has to explain the one non-obvious consequence: work that was
// uncommitted stays behind, and merge will refuse for the files it touches until
// it is committed. Silence there is what made the old flow confusing.
func TestWorktreeAddReportExplainsWhatStayedBehind(t *testing.T) {
	clean := worktreeAddReport("fork", "/repo", "/repo.wt/feat", "feat", false)
	if strings.Contains(clean, "stayed there") {
		t.Fatalf("a clean source tree claimed work stayed behind:\n%s", clean)
	}
	if !strings.Contains(clean, "new conversation has that history") {
		t.Fatalf("a fork did not say where the transcript went:\n%s", clean)
	}
	if strings.Contains(clean, "continues in the new tree") {
		t.Fatalf("a fork claimed this conversation moved:\n%s", clean)
	}

	dirty := worktreeAddReport("fork", "/repo", "/repo.wt/feat", "feat", true)
	for _, want := range []string{"stayed there", "/worktree merge refuses"} {
		if !strings.Contains(dirty, want) {
			t.Fatalf("dirty report is missing %q:\n%s", want, dirty)
		}
	}

	moved := worktreeAddReport("move", "/repo", "/repo.wt/feat", "feat", false)
	if !strings.Contains(moved, "continues in the new tree") {
		t.Fatalf("a move did not say the conversation moved:\n%s", moved)
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

// /worktree status asked for `git_worktree_list {status: true}` — the whole
// point of the view — and rendered it through the human-facing report, so the
// report must carry the facts that decision needs: this conversation's place in
// the graph, its granted roots, and per tree the dirt and the divergence.
func TestWorktreeStatusAsksForStatusAndRendersIt(t *testing.T) {
	var asked map[string]any
	request := func(_ string, tool string, args map[string]any, out any) error {
		switch tool {
		case "session_info":
			info := out.(*worktreeInfo)
			info.Workspace.Root = "/repo"
			info.Workspace.Primary = "/repo"
			info.Workspace.Branch = "main"
			info.Origin.Session = "conv-origin"
		case "session":
			*out.(*sessionRootsResponse) = sessionRootsResponse{
				Cwd:          "/repo",
				CwdPinned:    true,
				GrantedRoots: []string{"/repo", "/shared/lib"},
			}
		default:
			t.Fatalf("unexpected request %s", tool)
		}
		return nil
	}
	invoke := func(_ string, tool string, args map[string]any, out any) error {
		if tool != "git_worktree_list" {
			t.Fatalf("unexpected invoke %s", tool)
		}
		asked = args
		clean, ahead, behind := true, 2, 0
		dirty := false
		*out.(*worktreeListInfo) = worktreeListInfo{
			Container: "/repo.wt",
			Compare:   "main",
			Trees: []worktreeEntry{
				{Path: "/repo", Branch: "main", Current: true, Clean: &dirty,
					Changed: 1, Untracked: 3},
				{Path: "/repo.wt/feat", Branch: "feat", Clean: &clean,
					Ahead: &ahead, Behind: &behind},
			},
		}
		return nil
	}
	msg := worktreeCommandWith("conv-1", "/repo", "status", "", false,
		request, invoke, nil)().(worktreeMsg)
	if msg.Err != nil {
		t.Fatalf("/worktree status failed: %v", msg.Err)
	}
	if asked["status"] != true {
		t.Fatalf("status was not requested from git_worktree_list: %v", asked)
	}
	for _, want := range []string{
		"workspace: /repo (primary checkout, branch main)",
		"origin:    conv-origin",
		"roots:     1 granted",
		"/shared/lib",
		"/repo.wt/feat",
		"clean, 2 ahead of main",
		"dirty (1 changed, 3 untracked)",
	} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("report is missing %q:\n%s", want, msg.Text)
		}
	}
	// The primary is not a grant, and no JSON blob is rendered.
	if strings.Contains(msg.Text, `"trees"`) || strings.Contains(msg.Text, "roots:     2 granted") {
		t.Fatalf("report leaked the raw listing or counted the primary as a grant:\n%s", msg.Text)
	}
}

// A merge report says where the work went and what to do next, instead of
// printing the receipt JSON the tool returns.
func TestWorktreeMergeReport(t *testing.T) {
	got := worktreeMergeReport("feat", "main",
		"0123456789abcdef0123456789abcdef01234567", 2,
		[]string{"a.go", "b.go"})
	for _, want := range []string{
		"merged feat into main (2 commits)",
		"2 file(s): a.go, b.go",
		"main now at 0123456",
		"/worktree close retires it",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "0123456789abcdef") {
		t.Fatalf("full oid printed:\n%s", got)
	}

	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, fmt.Sprintf("f%d.go", i))
	}
	long := worktreeMergeReport("feat", "main", "", 1, many)
	if !strings.Contains(long, "+14 more") {
		t.Fatalf("a long file list was not bounded:\n%s", long)
	}
	if strings.Contains(long, "now at") {
		t.Fatalf("invented a head for an empty oid:\n%s", long)
	}
	// One commit is singular.
	if !strings.Contains(worktreeMergeReport("f", "m", "", 1, nil), "(1 commit)") {
		t.Fatal("singular commit not rendered")
	}
}

// A tree that was never measured must not be called clean, and a listing with
// no compare ref must not invent "level with". Both are what the pointer fields
// exist for.
func TestWorktreeReportOmitsUnmeasuredFacts(t *testing.T) {
	list := worktreeListInfo{Trees: []worktreeEntry{
		{Path: "/repo.wt/x", Branch: "x"},
	}}
	got := worktreeStatusReport("/repo", "/repo", worktreeInfo{}, nil, list)
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "/repo.wt/x") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("tree row missing:\n%s", got)
	}
	if strings.Contains(line, "clean") || strings.Contains(line, "ahead") ||
		strings.Contains(line, "level with") {
		t.Fatalf("unmeasured facts were rendered: %q", line)
	}
	if !strings.Contains(got, "roots:     none granted") {
		t.Fatalf("grant line missing:\n%s", got)
	}
}
