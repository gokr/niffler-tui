package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// The conversation's working set: the primary workspace core pinned at birth
// (`cwd`), the extra roots a human granted on top of it, and the git branch
// that names where the work currently sits. All three are *display* state for
// the bottom row and /roots — the authoritative set lives in core, which
// computes the primary plus linked worktrees and same-origin siblings
// (core/workspace.nim) and fences writes with it. This client never invents a
// root; it only reports what core accepted.
//
// Why the display exists at all: a grant is a security decision (it widens
// what tools accept and what the write fence admits), so a grant the human
// cannot see is a grant they cannot audit or take back. The bottom row is
// where the workspace already lives, so "+N" and the branch ride there and
// /roots is the full readback plus the control.

// grantedRootsMsg carries the conversation's granted roots, read from the
// session status. Roots is nil when the conversation has none, which is the
// ordinary case.
type grantedRootsMsg struct {
	Session string
	Roots   []string
	Err     error
}

// rootChangeMsg reports the outcome of a /roots add|remove. Roots is the set
// core reports after the change, so the display follows core's own view
// (normalized spellings included) rather than the string the user typed.
type rootChangeMsg struct {
	Session string
	Action  string // "add" or "remove"
	Path    string // the resolved path the client asked for
	Roots   []string
	Err     error
}

// branchMsg carries the primary workspace's git facts: the current branch,
// whether this checkout is a LINKED worktree rather than the primary one, and
// where the primary checkout is. Empty Branch means "not a git checkout, or git
// is unavailable" — the row then shows no branch rather than a placeholder, the
// same omission rule the context gauge uses when no limit is known.
type branchMsg struct {
	Dir      string
	Branch   string
	Worktree bool   // linked worktree, not the primary checkout
	Primary  string // the primary checkout's path, when derivable
	Err      error
}

// sessionRootsResponse is the slice of core's session status this file needs.
// grantedRoots is only present once a human granted something, so an absent
// field and an empty array mean the same thing: no grants.
//
// Error matters: core reports a refused grant as a RESULT carrying an "error"
// field (not an error envelope), and requestInto does not inspect it. Without
// this field a refusal would unmarshal as a successful change with an empty
// root set, and the client would report a grant that never happened.
type sessionRootsResponse struct {
	GrantedRoots []string `json:"grantedRoots"`
	Cwd          string   `json:"cwd"`
	CwdPinned    bool     `json:"cwdPinned"`
	Error        string   `json:"error"`
}

// grantedRoots returns the grant list for display, dropping the primary
// workspace.
//
// Both read paths need this. core's granted-root document is a sandbox policy
// whose writeRoots it seeds with the primary workspace (initialSandboxPolicy)
// and then adds grants to, and the conversation header mirrors that same
// unfiltered list. So a readback built from either one lists the primary
// alongside the real grants — and the header keeps carrying it even after every
// grant is removed, which would leave a permanent "+1" on a conversation with no
// grants at all. The primary is not a grant: it is immutable, shown separately,
// and cannot be removed.
func grantedRoots(roots []string, primary string) []string {
	var out []string
	for _, root := range roots {
		if root == primary {
			continue
		}
		out = append(out, root)
	}
	return out
}

// grantedFromResponse applies grantedRoots to a session status reply.
func grantedFromResponse(response sessionRootsResponse) []string {
	return grantedRoots(response.GrantedRoots, response.Cwd)
}

// loadRootsCmd reads the conversation's granted roots. It is the same call
// /approvals makes for its mode: a session status read, no turn.
func loadRootsCmd(comp *sdk.Component, session string) tea.Cmd {
	return func() tea.Msg {
		var response sessionRootsResponse
		if err := requestInto(comp, "core", "session",
			map[string]any{"sessionId": session}, &response); err != nil {
			return grantedRootsMsg{Session: session, Err: err}
		}
		if response.Error != "" {
			return grantedRootsMsg{Session: session, Err: errors.New(response.Error)}
		}
		return grantedRootsMsg{Session: session, Roots: grantedFromResponse(response)}
	}
}

// setRootsCmd grants or revokes roots through core's session `roots` op and
// reports the resulting set. core validates (absolute, existing, not the
// primary workspace, not "/") and persists before answering, so the reply's
// grantedRoots is the policy that will survive a restart — a refusal leaves
// the previous set in place, which the error carries.
func setRootsCmd(comp *sdk.Component, session, action string, paths []string) tea.Cmd {
	return func() tea.Msg {
		path := ""
		if len(paths) > 0 {
			path = paths[0]
		}
		change := map[string]any{action: paths}
		var response sessionRootsResponse
		args := map[string]any{"sessionId": session, "roots": change}
		if err := requestInto(comp, "core", "session", args, &response); err != nil {
			return rootChangeMsg{Session: session, Action: action, Path: path, Err: err}
		}
		// core persists before answering, so a refusal here means the previous
		// set is still in force — report it as a failure, never as a change.
		if response.Error != "" {
			return rootChangeMsg{Session: session, Action: action, Path: path,
				Err: errors.New(response.Error)}
		}
		return rootChangeMsg{Session: session, Action: action, Path: path,
			Roots: grantedFromResponse(response)}
	}
}

// resolveRootPath turns a typed root into the absolute path core requires.
// `~` is expanded because that is what a user means by it, and a relative
// path resolves against the conversation workspace — the same base the
// model's own workspace-relative paths use. Both are reported back resolved
// in the confirmation, so a mis-resolved path is visible immediately instead
// of becoming a silent grant.
func resolveRootPath(argument, workspace string) (string, error) {
	path := strings.TrimSpace(argument)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	switch {
	case path == "~":
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("cannot resolve ~: no home directory")
		}
		path = home
	case strings.HasPrefix(path, "~/"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("cannot resolve ~: no home directory")
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		base := workspace
		if base == "" {
			if wd, err := os.Getwd(); err == nil {
				base = wd
			}
		}
		if base == "" {
			return "", fmt.Errorf("%s is relative and the conversation workspace is unknown — use an absolute path", path)
		}
		path = filepath.Join(base, path)
	}
	// Tidy without resolving symlinks: core normalizes authoritatively, and
	// a path shown back to the user should be the one they can recognize.
	return filepath.Clean(path), nil
}

// loadBranchCmd reads the workspace's git branch and whether this checkout is
// a linked worktree. Local-only on purpose: for a remote instance `cwd` names a
// directory on the other machine, so exec'ing git here would inspect an
// unrelated (or missing) local path. The row omits the branch there rather than
// showing a wrong one.
//
// A linked worktree is told apart from the primary checkout by comparing git's
// own two answers: `--git-dir` names this checkout's git directory while
// `--git-common-dir` names the shared one. They are the same path in the primary
// checkout (`./.git`) and differ in a worktree (`.git/worktrees/<name>` vs
// `.git`), which is exactly the distinction — no filesystem guessing, and a
// plain, non-repo directory simply fails the command and shows nothing.
func loadBranchCmd(dir string, remote bool) tea.Cmd {
	if dir == "" || remote {
		return nil
	}
	return func() tea.Msg {
		// One git invocation for all three answers. --abbrev-ref prints "HEAD"
		// on a detached head; report it as-is rather than inventing a name.
		cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD",
			"--git-dir", "--git-common-dir")
		out, err := cmd.Output()
		if err != nil {
			return branchMsg{Dir: dir, Err: err}
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) < 3 {
			return branchMsg{Dir: dir, Err: fmt.Errorf("unexpected git output: %q", out)}
		}
		msg := branchMsg{Dir: dir, Branch: strings.TrimSpace(lines[0])}
		gitDir := strings.TrimSpace(lines[1])
		commonDir := strings.TrimSpace(lines[2])
		if gitDir != commonDir {
			msg.Worktree = true
			// The common dir is the primary checkout's .git directory, so its
			// parent is the primary checkout itself. Only when git reports an
			// absolute path: a relative one names the querying checkout.
			if filepath.IsAbs(commonDir) {
				msg.Primary = filepath.Dir(commonDir)
			}
		}
		return msg
	}
}

// branchChip renders the branch for the bottom row, or "" when there is
// nothing truthful to show (no repo, git unavailable; a detached HEAD keeps
// "HEAD" because that is the real state).
//
// A linked worktree is marked, because the row's path alone cannot say it: two
// worktrees of one repo are different directories with different branches and
// nothing on screen distinguishes "my checkout" from "a worktree of my
// checkout" — which matters when deciding where an edit lands. The mark is a
// glyph rather than a word to keep the row narrow; /workspace and /roots spell
// it out.
func (m model) branchChip() string {
	if m.branch == "" {
		return ""
	}
	if m.worktree {
		return t(m.loc, "workspace.branchWorktree", m.branch)
	}
	return t(m.loc, "workspace.branch", m.branch)
}

// rootsChip renders the "+N" suffix for granted roots. N counts the grants,
// not the whole workspace set: the set's worktree and sibling members are
// core's own computation and are not the human's decision to audit here.
// Empty when nothing was granted, so an ordinary conversation pays no width.
func (m model) rootsChip() string {
	if len(m.grantedRoots) == 0 {
		return ""
	}
	return t(m.loc, "workspace.grantedRoots", fmt.Sprint(len(m.grantedRoots)))
}

// ---- /roots ---------------------------------------------------------------

// rootsSubcommands are /roots' declared subcommands. The entry references
// this table for its `subcommand` enum, for dispatch and for Tab completion,
// so the accepted set cannot drift from the documented one.
var rootsSubcommands = []slashSubcommand{
	{name: "add", run: rootsAdd},
	{name: "remove", aliases: []string{"rm"}, run: rootsRemove},
}

// localRoots reports the conversation's workspace and granted roots, and
// adds or removes a grant. A bare /roots is the readback that makes the
// bottom row's "+N" auditable; the subcommands are the control, mirroring
// core's session `roots` op with the client-side conveniences core cannot
// have (tilde expansion, workspace-relative resolution).
func localRoots(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		return m, loadRootsCmd(m.comp, m.session)
	}
	first, rest := splitCommand(argument)
	sub, ok := cmd.localSubcommand(first)
	if !ok {
		// A bare path is the common case for a readback-shaped command, so
		// treat "/roots ~/git/x" as a grant instead of an unknown subcommand.
		if looksLikePath(first) {
			return rootsApply(m, "add", argument)
		}
		m.addBlock(blockError, t(m.loc, "roots.unknownSubcommand", first))
		m.syncViewport(true)
		return m, nil
	}
	return sub.run(m, cmd, rest)
}

// looksLikePath decides whether an unrecognized first word is meant as a
// path (so "/roots ~/git/x" grants) or a mistyped subcommand (so
// "/roots ad" is reported). A path carries a separator, a dot prefix or a
// home marker; a bare word does not.
func looksLikePath(word string) bool {
	return strings.ContainsAny(word, "/~") || strings.HasPrefix(word, ".")
}

func rootsAdd(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return rootsApply(m, "add", argument)
}

func rootsRemove(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return rootsApply(m, "remove", argument)
}

// rootsApply resolves the argument and sends the grant or revocation. One
// path per call: core accepts arrays, but a slash command's argument is a
// single token after the subcommand, and a one-at-a-time control keeps the
// confirmation line unambiguous about what changed.
func rootsApply(m model, action, argument string) (tea.Model, tea.Cmd) {
	argument = strings.TrimSpace(argument)
	if argument == "" {
		m.addBlock(blockError, t(m.loc, "roots.usage"))
		m.syncViewport(true)
		return m, nil
	}
	path, err := resolveRootPath(argument, m.cwd)
	if err != nil {
		m.addBlock(blockError, t(m.loc, "roots.badPath", argument, err.Error()))
		m.syncViewport(true)
		return m, nil
	}
	m.addBlock(blockMeta, "→ /roots "+action+" "+displayPath(path, 0))
	m.syncViewport(true)
	return m, setRootsCmd(m.comp, m.session, action, []string{path})
}

// rootsReport renders the readback: the primary workspace first (it is the
// one relative paths resolve against), then each grant. A grant is marked as
// such because the distinction is the point — the primary is immutable, the
// grants are the human's to remove.
func rootsReport(loc Locale, cwd string, granted []string) string {
	var b strings.Builder
	b.WriteString(t(loc, "roots.primary", displayPath(cwd, 0)))
	if len(granted) == 0 {
		b.WriteString("\n")
		b.WriteString(t(loc, "roots.none"))
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString(t(loc, "roots.granted", fmt.Sprint(len(granted))))
	for _, root := range granted {
		b.WriteString("\n  ")
		b.WriteString(displayPath(root, 0))
	}
	b.WriteString("\n")
	b.WriteString(t(loc, "roots.hint"))
	return b.String()
}

// ---- /workspace -----------------------------------------------------------

// workspaceReport describes where a conversation works: the path, its git
// shape (branch, and whether this is a linked worktree of another checkout),
// and the granted roots. The worktree line is what the bottom row cannot say —
// a worktree and a primary checkout look identical as paths.
func workspaceReport(loc Locale, cwd, branch string, worktree bool, primary string, granted []string, pinned bool) string {
	var b strings.Builder
	b.WriteString(t(loc, "workspace.current", displayPath(cwd, 0)))
	if !pinned {
		b.WriteString("\n")
		b.WriteString(t(loc, "workspace.default"))
	}
	if branch != "" {
		b.WriteString("\n")
		if worktree {
			b.WriteString(t(loc, "workspace.reportWorktree", branch))
			if primary != "" {
				b.WriteString("\n")
				b.WriteString(t(loc, "workspace.reportPrimary", displayPath(primary, 0)))
			}
		} else {
			b.WriteString(t(loc, "workspace.reportBranch", branch))
		}
	}
	if len(granted) > 0 {
		b.WriteString("\n")
		b.WriteString(t(loc, "workspace.reportRoots", fmt.Sprint(len(granted))))
	}
	return b.String()
}

// localWorkspace reports or repins the conversation's workspace. It is the
// control for the one thing the client pins on the user's behalf at birth
// (sendTurn sends the launch directory as `cwd`), so "I started the client in
// the wrong directory" has an answer inside the client rather than requiring a
// restart.
//
// A repin is possible while the conversation is pristine (no turn yet) and,
// once turns exist, only while its workspace was never actually CHOSEN — a
// header whose cwd is the harness-root default that a control call baked in.
// After that core refuses, because a resume must resolve context and paths
// exactly as the original turn did. The refusal is surfaced verbatim, and the
// report states which case applies, so the rule is visible before it is hit.
func localWorkspace(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		m.addBlock(blockMeta, workspaceReport(m.loc, m.cwd, m.branch,
			m.worktree, m.primary, m.grantedRoots, m.cwdPinned))
		m.syncViewport(true)
		return m, nil
	}
	path, err := resolveRootPath(argument, m.cwd)
	if err != nil {
		m.addBlock(blockError, t(m.loc, "workspace.badPath", argument, err.Error()))
		m.syncViewport(true)
		return m, nil
	}
	m.addBlock(blockMeta, "→ /workspace "+displayPath(path, 0))
	m.syncViewport(true)
	return m, setWorkspaceCmd(m.comp, m.session, path)
}

// setWorkspaceCmd asks core to repin the workspace. core validates (an existing
// absolute directory) and refuses once turns exist; on success it also
// re-resolves the constitution against the new root and publishes
// ev.workspace.opened, so nothing here has to recompute the prompt.
func setWorkspaceCmd(comp *sdk.Component, session, path string) tea.Cmd {
	return func() tea.Msg {
		var response sessionRootsResponse
		if err := requestInto(comp, "core", "session",
			map[string]any{"sessionId": session, "cwd": path}, &response); err != nil {
			return workspaceChangeMsg{Session: session, Path: path, Err: err}
		}
		if response.Error != "" {
			return workspaceChangeMsg{Session: session, Path: path,
				Err: errors.New(response.Error)}
		}
		return workspaceChangeMsg{Session: session, Path: path,
			Cwd: response.Cwd, Pinned: response.CwdPinned}
	}
}

// workspaceChangeMsg reports the outcome of a /workspace repin. Cwd is core's
// own answer, so the row follows core's normalization rather than the typed
// string.
type workspaceChangeMsg struct {
	Session string
	Path    string
	Cwd     string
	Pinned  bool
	Err     error
}
