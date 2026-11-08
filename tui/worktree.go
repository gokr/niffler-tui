package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// Porcelain --branch has only ## metadata lines in a clean tree. Treat
// any other nonempty output as dirt, including capped/unrecognized output.
func worktreeStatusDirty(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "##") && line != "(exit 0)" {
			return true
		}
	}
	return false
}

func worktreeBirthDecision(pristine, pinned, dirty, explicitFork bool) string {
	if dirty && !explicitFork {
		return "refuse"
	}
	if explicitFork || (!pristine && pinned) {
		return "fork"
	}
	return "move"
}

type worktreeInfo struct {
	Error        string `json:"error"`
	Cwd          string `json:"cwd"`
	MessageCount int    `json:"messageCount"`
	Origin       struct {
		Session string `json:"session"`
	} `json:"origin"`
	Workspace struct {
		Root      string `json:"root"`
		Primary   string `json:"primaryRoot"`
		Branch    string `json:"branch"`
		Linked    bool   `json:"linked"`
		Isolation string `json:"isolation"`
	} `json:"workspace"`
}

// worktreeListInfo is the part of `git_worktree_list {status: true}` the status
// view renders. The dirt/divergence fields are POINTERS on purpose: a listing
// asks for status only when a human needs it, and a tree nobody measured must
// not be reported as clean.
type worktreeListInfo struct {
	Container string          `json:"container"`
	Compare   string          `json:"compare"`
	Trees     []worktreeEntry `json:"trees"`
}

type worktreeEntry struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Head       string `json:"head"`
	Detached   bool   `json:"detached"`
	Current    bool   `json:"isCurrent"`
	Locked     bool   `json:"locked"`
	LockReason string `json:"lockReason"`
	Prunable   bool   `json:"prunable"`
	Clean      *bool  `json:"clean"`
	Changed    int    `json:"changed"`
	Untracked  int    `json:"untracked"`
	Ahead      *int   `json:"ahead"`
	Behind     *int   `json:"behind"`
	Owner      *struct {
		SessionID string `json:"sessionId"`
		State     string `json:"state"`
	} `json:"owner"`
}

// worktreeDirt is one tree's cleanliness in words. A tree that was not measured
// (no status asked for, or the directory is gone) says nothing at all.
func worktreeDirt(e worktreeEntry) string {
	if e.Clean == nil {
		return ""
	}
	if *e.Clean {
		return "clean"
	}
	var parts []string
	if e.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", e.Changed))
	}
	if e.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", e.Untracked))
	}
	if len(parts) == 0 {
		return "dirty"
	}
	return "dirty (" + strings.Join(parts, ", ") + ")"
}

// worktreeDivergence is how far a tree sits from the branch the listing compared
// against (the primary's current branch — the one a tree is cut from). Silence
// when there is nothing to compare: a detached primary has no branch name, and a
// tree git could not read has no counts.
func worktreeDivergence(e worktreeEntry, compare string) string {
	if compare == "" || e.Ahead == nil || e.Behind == nil {
		return ""
	}
	ahead, behind := *e.Ahead, *e.Behind
	switch {
	case ahead == 0 && behind == 0:
		return "level with " + compare
	case behind == 0:
		return fmt.Sprintf("%d ahead of %s", ahead, compare)
	case ahead == 0:
		return fmt.Sprintf("%d behind %s", behind, compare)
	}
	return fmt.Sprintf("%d ahead, %d behind %s", ahead, behind, compare)
}

// worktreeFactsLine is the comma-joined tail of one tree's row: what a human
// deciding "can I retire this, should I merge it" needs, in reading order.
func worktreeFactsLine(e worktreeEntry, compare string) string {
	var parts []string
	if dirt := worktreeDirt(e); dirt != "" {
		parts = append(parts, dirt)
	}
	if div := worktreeDivergence(e, compare); div != "" {
		parts = append(parts, div)
	}
	if e.Detached {
		parts = append(parts, "detached HEAD")
	}
	if e.Prunable {
		parts = append(parts, "prunable (directory gone — rm or prune)")
	} else if e.Locked {
		parts = append(parts, "locked")
	}
	if e.Owner != nil && e.Owner.SessionID != "" {
		parts = append(parts, "owner "+e.Owner.SessionID)
	}
	return strings.Join(parts, ", ")
}

// worktreeName is a tree's label in the status view: its branch, or an honest
// marker when it has none (a detached HEAD is a state, not a missing name).
func worktreeName(e worktreeEntry) string {
	if e.Branch == "" {
		return "(detached)"
	}
	return e.Branch
}

// worktreeStatusReport renders /worktree: where this conversation works, the
// roots a human granted on top of it, and every tree of the repository with the
// dirt and divergence that decide what to do next.
//
// It replaces a raw JSON dump. The JSON was complete and unreadable: the one
// question a human opens this for — "what is in my other trees, can I retire
// them" — could only be answered by parsing it by eye. Tools keep the JSON
// (git_worktree_list is the model's view); this is the human's.
func worktreeStatusReport(workspace, primary string, info worktreeInfo,
	granted []string, list worktreeListInfo) string {
	var b strings.Builder
	branch := info.Workspace.Branch
	if branch == "" {
		branch = "none"
	}
	kind := "primary checkout"
	if info.Workspace.Linked {
		kind = "linked worktree"
	}
	fmt.Fprintf(&b, "workspace: %s (%s, branch %s)\n", workspace, kind, branch)
	if primary != "" {
		fmt.Fprintf(&b, "primary:   %s\n", primary)
	}
	isolation := info.Workspace.Isolation
	if isolation == "" {
		isolation = "shared"
	}
	fmt.Fprintf(&b, "isolation: %s\n", isolation)
	if info.Origin.Session != "" {
		fmt.Fprintf(&b, "origin:    %s (fork; close returns here)\n", info.Origin.Session)
	} else {
		b.WriteString("origin:    none (moved conversation: its workspace is immutable)\n")
	}
	// Grants are a security decision — they widen what this conversation may
	// write — so they belong beside the workspace they widen, not only in
	// /roots.
	if len(granted) == 0 {
		b.WriteString("roots:     none granted\n")
	} else {
		fmt.Fprintf(&b, "roots:     %d granted\n", len(granted))
		for _, root := range granted {
			fmt.Fprintf(&b, "             %s\n", root)
		}
	}
	if list.Container != "" {
		fmt.Fprintf(&b, "container: %s\n", list.Container)
	}
	if len(list.Trees) == 0 {
		b.WriteString("trees:     none reported (is this a git checkout?)\n")
		return b.String()
	}
	if list.Compare != "" {
		fmt.Fprintf(&b, "trees (compared with %s; * = this conversation):\n", list.Compare)
	} else {
		b.WriteString("trees (* = this conversation):\n")
	}
	// Column widths from the real values, so the rows line up in a terminal
	// without padding guesses.
	nameWidth, pathWidth := 0, 0
	for _, e := range list.Trees {
		nameWidth = max(nameWidth, len(worktreeName(e)))
		pathWidth = max(pathWidth, len(e.Path))
	}
	for _, e := range list.Trees {
		marker := " "
		if e.Current {
			marker = "*"
		}
		line := fmt.Sprintf("  %s %-*s  %-*s", marker, nameWidth, worktreeName(e), pathWidth, e.Path)
		if facts := worktreeFactsLine(e, list.Compare); facts != "" {
			line += "  " + facts
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// closeWorktree decides where the client goes BEFORE anything is removed, so a
// failure leaves the tree and this transcript intact.
//
// Two cases, and the second was missing until a user hit it: a FORK returns to
// its origin; a MOVED conversation has no origin and its workspace is immutable,
// so the only honest destination is a fresh conversation in the primary. The old
// transcript stays as the record — it is not repinned, which core would refuse
// anyway.
func closeWorktree(info worktreeInfo, freshID string, primaryCwd func() string,
	prepare func(id, dir string) error, remove func() error) (target, cwd, detail string, err error) {
	if !info.Workspace.Linked {
		return "", "", "", fmt.Errorf("close needs a linked worktree; nothing removed")
	}
	primary := info.Workspace.Primary
	if primary == "" {
		primary = primaryCwd()
	}
	if primary == "" {
		return "", "", "", fmt.Errorf("cannot find the primary checkout; nothing removed")
	}
	target = info.Origin.Session
	if target != "" {
		detail = "returned to origin"
	} else {
		if freshID == "" {
			return "", "", "", fmt.Errorf("cannot mint a conversation id; nothing removed")
		}
		if err = prepare(freshID, primary); err != nil {
			return "", "", "", fmt.Errorf("cannot open a conversation in the primary; nothing removed: %w", err)
		}
		target, cwd = freshID, primary
		detail = "new conversation opened in the primary; this transcript is preserved"
	}
	if err = remove(); err != nil {
		return "", "", "", err
	}
	return target, cwd, "worktree removed; branch kept; " + detail, nil
}

// infoFailure decides whether a session_info error blocks the operation.
//
// A conversation that has not run a turn yet has NO stored header, so
// session_info answers "no conversation '<id>'". That is not a failure:
// /worktree add is most useful BEFORE the first turn, and refusing there made
// the command unusable on a fresh conversation ("/worktree: core.session_info:
// no conversation 'conv-…'"). Everything else is a real failure.
func infoFailure(msg string) error {
	if msg == "" || strings.Contains(msg, "no conversation") {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

type worktreeMsg struct {
	Session, Target, Cwd, Text, Isolation string
	Err                                   error
}

// git mutations go through core's dispatch/approval gate, not a direct
// svc.git request. Private session context lets git attribute receipts.
func worktreeInvoke(comp *sdk.Component, session, tool string, args map[string]any, out any) error {
	raw, err := comp.Request("core", "session", map[string]any{
		"sessionId": session, "invoke": map[string]any{"tool": tool, "arguments": args}}, 6*time.Minute)
	if err != nil {
		return err
	}
	var wrapper struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err = json.Unmarshal(raw, &wrapper); err != nil {
		return err
	}
	if wrapper.Error != "" {
		return fmt.Errorf("%s", wrapper.Error)
	}
	if len(wrapper.Result) == 0 {
		return fmt.Errorf("harness lacks session-scoped tool controls; update the harness")
	}
	raw = wrapper.Result
	var verdict struct {
		Error string `json:"error"`
		Text  string `json:"text"`
		Exit  int    `json:"exit_code"`
	}
	if err = json.Unmarshal(raw, &verdict); err != nil {
		return err
	}
	if verdict.Error != "" {
		return fmt.Errorf("%s", verdict.Error)
	}
	if verdict.Exit != 0 {
		return fmt.Errorf("%s", verdict.Text)
	}
	return json.Unmarshal(raw, out)
}

func localWorktree(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected || m.busy || m.controlPending {
		m.addBlock(blockError, "/worktree: connect and wait for the current turn/control to finish")
		m.syncViewport(true)
		return m, nil
	}
	fields := strings.Fields(argument)
	action, name := "status", ""
	if len(fields) > 0 {
		action = fields[0]
	}
	if len(fields) > 1 {
		name = fields[1]
	}
	explicitFork := len(fields) == 3 && fields[2] == "--fork"
	if len(fields) > 3 || (len(fields) == 3 && !explicitFork) {
		m.addBlock(blockError, "usage: /worktree [add NAME [--fork]|merge|rm PATH|close]")
		m.syncViewport(true)
		return m, nil
	}
	if (action == "merge" || action == "close" || action == "status") && len(fields) > 1 {
		m.addBlock(blockError, "/worktree: unexpected arguments")
		m.syncViewport(true)
		return m, nil
	}
	if action == "new" {
		action = "add"
	}
	if action != "status" && action != "add" && action != "merge" && action != "rm" && action != "close" {
		m.addBlock(blockError, "unknown /worktree command: "+action)
		m.syncViewport(true)
		return m, nil
	}
	m.controlPending = true
	return m, worktreeCommand(m.comp, m.session, m.cwd, action, name, explicitFork, m.grantedRoots)
}

func worktreeCommand(comp *sdk.Component, session, cwd, action, name string,
	explicitFork bool, granted []string) tea.Cmd {
	return worktreeCommandWith(session, cwd, action, name, explicitFork,
		func(component, tool string, args map[string]any, out any) error {
			return requestInto(comp, component, tool, args, out)
		}, func(session, tool string, args map[string]any, out any) error {
			return worktreeInvoke(comp, session, tool, args, out)
		}, granted)
}

func worktreeCommandWith(session, cwd, action, name string, explicitFork bool,
	request func(string, string, map[string]any, any) error,
	invoke func(string, string, map[string]any, any) error, granted []string) tea.Cmd {
	return func() tea.Msg {
		msg := worktreeMsg{Session: session}
		fail := func(err error) tea.Msg { msg.Err = err; return msg }
		var info worktreeInfo
		if err := request("core", "session_info", map[string]any{"sessionId": session}, &info); err != nil {
			return fail(err)
		}
		if err := infoFailure(info.Error); err != nil {
			return fail(err)
		}
		// A fresh session's launch directory has not necessarily reached core:
		// the first chat turn normally supplies it. Establish it before any
		// session-scoped Git call, whose write fence uses core's workspace.
		// Never move an existing conversation with a chosen workspace or turns.
		var status sessionRootsResponse
		if err := request("core", "session", map[string]any{"sessionId": session}, &status); err != nil {
			return fail(err)
		}
		if status.Error != "" {
			return fail(fmt.Errorf("%s", status.Error))
		}
		if !status.CwdPinned && info.MessageCount == 0 {
			if cwd == "" {
				return fail(fmt.Errorf("workspace is unknown"))
			}
			if err := request("core", "session", map[string]any{"sessionId": session, "cwd": cwd}, &status); err != nil {
				return fail(err)
			}
			if status.Error != "" {
				return fail(fmt.Errorf("cannot select workspace: %s", status.Error))
			}
			// Refresh checkout facts after the selection, not the default root.
			info = worktreeInfo{}
			if err := request("core", "session_info", map[string]any{"sessionId": session}, &info); err != nil {
				return fail(err)
			}
			if err := infoFailure(info.Error); err != nil {
				return fail(err)
			}
		}
		if status.Cwd != "" {
			cwd = status.Cwd
		}
		// The grants the human made are core's answer, not this client's
		// memory: the status readback carries them, and the primary is filtered
		// out (it is the workspace, not a grant). m.grantedRoots is only a
		// fallback for a core too old to report them.
		if status.GrantedRoots != nil {
			granted = grantedRoots(status.GrantedRoots, status.Cwd)
		}
		primary := info.Workspace.Primary
		if primary == "" {
			primary = cwd
		}
		msg.Isolation = info.Workspace.Isolation
		switch action {
		case "status":
			// status: true — the dirt and divergence are what a human opens
			// this view to decide on, and they cost two git calls per tree.
			var list worktreeListInfo
			if err := invoke(session, "git_worktree_list",
				map[string]any{"repo": cwd, "status": true}, &list); err != nil {
				return fail(err)
			}
			msg.Text = worktreeStatusReport(cwd, primary, info, granted, list)
		case "add":
			if name == "" {
				return fail(fmt.Errorf("/worktree add needs NAME"))
			}
			var dirty struct {
				Text string `json:"text"`
			}
			if err := invoke(session, "git_status", map[string]any{"repo": cwd}, &dirty); err != nil {
				return fail(err)
			}
			decision := worktreeBirthDecision(info.MessageCount == 0, status.CwdPinned, worktreeStatusDirty(dirty.Text), explicitFork)
			if decision == "refuse" {
				return fail(fmt.Errorf("current tree has uncommitted changes:\n%s\nCommit first, or /worktree add %s --fork to leave them here", dirty.Text, name))
			}
			var created struct{ Path, Branch string }
			if err := invoke(session, "git_worktree", map[string]any{"op": "add", "repo": cwd, "name": name, "branch": name, "base": info.Workspace.Branch}, &created); err != nil {
				return fail(err)
			}
			if created.Path == "" {
				return fail(fmt.Errorf("git did not return a created tree path"))
			}
			target := session
			if decision == "fork" {
				var fork struct {
					Session string `json:"sessionId"`
					Error   string `json:"error"`
				}
				if err := request("agent", "agent_fork", map[string]any{"source": session, "cwd": created.Path}, &fork); err != nil {
					return fail(fmt.Errorf("tree created at %s; fork failed: %w", created.Path, err))
				}
				if fork.Error != "" || fork.Session == "" {
					return fail(fmt.Errorf("tree created at %s; fork refused: %s", created.Path, fork.Error))
				}
				target = fork.Session
			}
			var selected sessionRootsResponse
			if err := request("core", "session", map[string]any{"sessionId": target, "cwd": created.Path, "isolation": "worktree", "checkout": map[string]any{"primaryRoot": primary, "branch": created.Branch}}, &selected); err != nil {
				return fail(fmt.Errorf("tree created at %s; selection failed: %w", created.Path, err))
			}
			if selected.Error != "" {
				return fail(fmt.Errorf("tree created at %s; selection refused: %s", created.Path, selected.Error))
			}
			msg.Target = target
			msg.Cwd = created.Path
			msg.Isolation = "worktree"
			msg.Text = decision + " → " + created.Path + " (branch " + created.Branch + ")"
			if decision == "fork" {
				msg.Text += "; original conversation preserved. Raw completed history copied, not compaction checkpoints."
			}
		case "merge":
			if !info.Workspace.Linked {
				return fail(fmt.Errorf("merge needs a linked worktree"))
			}
			var preview struct {
				Clean     bool     `json:"clean"`
				Conflicts []string `json:"conflicts"`
			}
			if err := invoke(session, "git_merge_preview", map[string]any{"repo": primary, "from_ref": info.Workspace.Branch}, &preview); err != nil {
				return fail(err)
			}
			if !preview.Clean {
				return fail(fmt.Errorf("merge conflicts: %s", strings.Join(preview.Conflicts, ", ")))
			}
			var receipt json.RawMessage
			if err := invoke(session, "git_merge", map[string]any{"op": "start", "repo": primary, "from_ref": info.Workspace.Branch}, &receipt); err != nil {
				return fail(err)
			}
			msg.Text = "merged: " + string(receipt) + "\nWorktree kept; /worktree close to retire it and return to origin."
		case "rm", "close":
			path := name
			if action == "rm" && path == cwd {
				return fail(fmt.Errorf("refusing to remove the active workspace; use close to return to its origin"))
			}
			if action == "close" {
				var receipt json.RawMessage
				target, dest, detail, err := closeWorktree(info, newSessionID(),
					func() string { return info.Workspace.Primary },
					func(id, dir string) error {
						var response sessionRootsResponse
						if err := request("core", "session",
							map[string]any{"sessionId": id, "cwd": dir}, &response); err != nil {
							return err
						}
						if response.Error != "" {
							return fmt.Errorf("%s", response.Error)
						}
						return nil
					},
					func() error {
						return invoke(session, "git_worktree",
							map[string]any{"op": "remove", "repo": primary, "path": cwd}, &receipt)
					})
				if err != nil {
					return fail(err)
				}
				msg.Target, msg.Cwd, msg.Text, msg.Isolation = target, dest, detail, ""
				break
			}
			if path == "" {
				return fail(fmt.Errorf("/worktree rm needs an absolute PATH"))
			}
			var receipt json.RawMessage
			if err := invoke(session, "git_worktree", map[string]any{"op": "remove", "repo": primary, "path": path}, &receipt); err != nil {
				return fail(err)
			}
			msg.Text = "worktree removed; branch kept"
			msg.Isolation = ""
		}
		return msg
	}
}
