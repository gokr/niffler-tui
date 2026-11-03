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
	return m, worktreeCommand(m.comp, m.session, m.cwd, action, name, explicitFork)
}

func worktreeCommand(comp *sdk.Component, session, cwd, action, name string, explicitFork bool) tea.Cmd {
	return func() tea.Msg {
		msg := worktreeMsg{Session: session}
		fail := func(err error) tea.Msg { msg.Err = err; return msg }
		var info worktreeInfo
		if err := requestInto(comp, "core", "session_info", map[string]any{"sessionId": session}, &info); err != nil {
			return fail(err)
		}
		if err := infoFailure(info.Error); err != nil {
			return fail(err)
		}
		if info.Workspace.Root != "" {
			cwd = info.Workspace.Root
		}
		primary := info.Workspace.Primary
		if primary == "" {
			primary = cwd
		}
		msg.Isolation = info.Workspace.Isolation
		switch action {
		case "status":
			var trees json.RawMessage
			if err := worktreeInvoke(comp, session, "git_worktree_list", map[string]any{"repo": cwd}, &trees); err != nil {
				return fail(err)
			}
			msg.Text = fmt.Sprintf("workspace: %s\nprimary: %s\nbranch: %s\nisolation: %s\norigin: %s\n%s", cwd, primary, info.Workspace.Branch, info.Workspace.Isolation, info.Origin.Session, trees)
		case "add":
			if name == "" {
				return fail(fmt.Errorf("/worktree add needs NAME"))
			}
			var status sessionRootsResponse
			if err := requestInto(comp, "core", "session", map[string]any{"sessionId": session}, &status); err != nil {
				return fail(err)
			}
			if status.Error != "" {
				return fail(fmt.Errorf("%s", status.Error))
			}
			var dirty struct {
				Text string `json:"text"`
			}
			if err := worktreeInvoke(comp, session, "git_status", map[string]any{"repo": cwd}, &dirty); err != nil {
				return fail(err)
			}
			decision := worktreeBirthDecision(info.MessageCount == 0, status.CwdPinned, worktreeStatusDirty(dirty.Text), explicitFork)
			if decision == "refuse" {
				return fail(fmt.Errorf("current tree has uncommitted changes:\n%s\nCommit first, or /worktree add %s --fork to leave them here", dirty.Text, name))
			}
			var created struct{ Path, Branch string }
			if err := worktreeInvoke(comp, session, "git_worktree", map[string]any{"op": "add", "repo": cwd, "name": name, "branch": name, "base": info.Workspace.Branch}, &created); err != nil {
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
				if err := requestInto(comp, "agent", "agent_fork", map[string]any{"source": session, "cwd": created.Path}, &fork); err != nil {
					return fail(fmt.Errorf("tree created at %s; fork failed: %w", created.Path, err))
				}
				if fork.Error != "" || fork.Session == "" {
					return fail(fmt.Errorf("tree created at %s; fork refused: %s", created.Path, fork.Error))
				}
				target = fork.Session
			}
			var selected sessionRootsResponse
			if err := requestInto(comp, "core", "session", map[string]any{"sessionId": target, "cwd": created.Path, "isolation": "worktree", "checkout": map[string]any{"primaryRoot": primary, "branch": created.Branch}}, &selected); err != nil {
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
			if err := worktreeInvoke(comp, session, "git_merge_preview", map[string]any{"repo": primary, "from_ref": info.Workspace.Branch}, &preview); err != nil {
				return fail(err)
			}
			if !preview.Clean {
				return fail(fmt.Errorf("merge conflicts: %s", strings.Join(preview.Conflicts, ", ")))
			}
			var receipt json.RawMessage
			if err := worktreeInvoke(comp, session, "git_merge", map[string]any{"op": "start", "repo": primary, "from_ref": info.Workspace.Branch}, &receipt); err != nil {
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
						if err := requestInto(comp, "core", "session",
							map[string]any{"sessionId": id, "cwd": dir}, &response); err != nil {
							return err
						}
						if response.Error != "" {
							return fmt.Errorf("%s", response.Error)
						}
						return nil
					},
					func() error {
						return worktreeInvoke(comp, session, "git_worktree",
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
			if err := worktreeInvoke(comp, session, "git_worktree", map[string]any{"op": "remove", "repo": primary, "path": path}, &receipt); err != nil {
				return fail(err)
			}
			msg.Text = "worktree removed; branch kept"
			msg.Isolation = ""
		}
		return msg
	}
}
