// Asking about a local command's output: "?/status" runs /status and hands
// what it printed to the model, the slash-command counterpart of "?command"
// (bang.go). It replaces the per-command `ask` arguments the TUI used to
// carry (/status ask, /recall … ask, /doctor … ask): one marker, one
// mechanism, every command.
//
// The mechanism is not per-command: the ask opens before the command runs,
// records where its output starts in the transcript, and closes once that
// output has landed — synchronously for a command that prints and returns,
// or through askWrapCmd for one whose result arrives later as a message.
// A result that already carries the userMessage convention (MCP prompt
// templates, tools that craft their own phrasing) closes the ask itself:
// applySlashResult sends that prompt and clears the flag, so the generic
// wrapper never fires and the command's wording wins.
package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// askState is the pending "?/command" ask.
type askState struct {
	active  bool
	line    string // the command as typed, including its leading "/"
	mark    int    // transcript index the command's output starts at
	session string // conversation id at invocation time (switch guard)
}

// slashAsk splits "?/command args" into the slash command it asks about. ok is
// false for a bare "?" (an ordinary prompt), for "?" in front of something
// that is not a slash command, and for "!" (the local-only marker).
func slashAsk(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != shellMarkerAsk {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[1:])
	if !strings.HasPrefix(rest, "/") || strings.TrimSpace(strings.TrimPrefix(rest, "/")) == "" {
		return "", false
	}
	return rest, true
}

// knownLocalCommand reports whether a command name resolves to a built-in
// local command or a registered plugin command, and whether it can open a
// picker or form (which has no output text to ask about). Aliases are excluded
// on purpose: running one already sends its own prompt, so there is nothing
// left to ask about.
func (m model) knownLocalCommand(name string) (known, interactive bool) {
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	if cmd, ok := builtinCommand(name); ok && cmd.run != nil {
		return true, cmd.interactive
	}
	if cmd, ok := m.slash.lookup(name); ok && !cmd.builtin {
		return true, false
	}
	return false, false
}

// executeSlashAsk runs a "?/command" ask: it reports the command, dispatches
// it exactly as a typed slash command, and closes the ask over whatever the
// command printed. Commands that open a selector or a form are refused up
// front (registry-declared `interactive`): they have no output text, and
// asking must not leave the user staring at a picker.
func (m model) executeSlashAsk(line string) (tea.Model, tea.Cmd) {
	name := ""
	if fields := strings.Fields(strings.TrimPrefix(line, "/")); len(fields) > 0 {
		name = fields[0]
	}
	known, interactive := m.knownLocalCommand(name)
	if !known {
		m.addBlock(blockError, t(m.loc, "ask.notAskable", line))
		m.syncViewport(true)
		return m, nil
	}
	if interactive {
		m.addBlock(blockError, t(m.loc, "ask.uiCommand", line))
		m.syncViewport(true)
		return m, nil
	}
	m.ask = askState{active: true, line: line, mark: len(m.blocks), session: m.session}
	updated, cmd := m.executeLocalCommand(line)
	m = updated.(model)
	if !m.ask.active {
		// The command already sent its own prompt (userMessage convention).
		return m, cmd
	}
	if cmd == nil {
		return m.finishAsk()
	}
	return m, askWrapCmd(cmd)
}

// askWrapCmd wraps a command's own result command so the ask can close once
// the result has been rendered: the command's message is handled first, then
// the ask reports. One hook covers every command whose output arrives
// asynchronously, whatever message type it uses.
func askWrapCmd(cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg { return askResultMsg{inner: cmd()} }
}

// askResultMsg carries a wrapped command's own message (askWrapCmd).
type askResultMsg struct{ inner tea.Msg }

// handleAskResult processes a wrapped command result, then closes the ask.
// The inner message is handled by the same Update dispatch that would have
// seen it unwrapped, so the command's own rendering is unchanged. A batch is
// expanded member by member and re-wrapped: the runtime runs the members
// concurrently, each reports separately, and finishAsk is idempotent, so the
// first one to arrive closes the ask.
func (m model) handleAskResult(msg askResultMsg, cmds *[]tea.Cmd) (tea.Model, tea.Cmd) {
	switch inner := msg.inner.(type) {
	case nil:
	case tea.BatchMsg:
		members := make([]tea.Cmd, 0, len(inner))
		for _, member := range inner {
			if member != nil {
				members = append(members, askWrapCmd(member))
			}
		}
		if len(members) > 0 {
			// Still waiting for the batch's own messages: they close the ask.
			return m, tea.Batch(members...)
		}
	default:
		updated, cmd := m.Update(inner)
		m = updated.(model)
		*cmds = append(*cmds, cmd)
	}
	return m.finishAsk()
}

// finishAsk sends the command's output to the model: an ordinary turn when the
// conversation is idle, a steer when a turn is live. It is a no-op without a
// pending ask, and refuses a conversation that changed while the command ran
// (the output stays in the transcript, so it can be asked again there).
func (m model) finishAsk() (tea.Model, tea.Cmd) {
	if !m.ask.active {
		return m, nil
	}
	state := m.ask
	m.ask = askState{}
	output := m.askOutput(state.mark)
	if !m.connected {
		m.addBlock(blockError, t(m.loc, "shell.notConnected"))
		m.syncViewport(true)
		return m, nil
	}
	if state.session != m.session {
		m.addBlock(blockError, t(m.loc, "slash.conversationChanged"))
		m.syncViewport(true)
		return m, nil
	}
	return m.askSend(askPrompt(state.line, output))
}

// askOutput collects the transcript text a command produced, from its output
// mark to the end. Rendering (not raw results) is what the model reads: it is
// the same text the human saw. The cap is the shell escapes' cap — output that
// would flood the context window is worse than output the human can re-run
// narrowly.
func (m model) askOutput(mark int) string {
	if mark >= len(m.blocks) {
		return ""
	}
	var parts []string
	for _, block := range m.blocks[mark:] {
		if strings.TrimSpace(block.text) == "" {
			continue
		}
		parts = append(parts, block.text)
	}
	return capPromptOutput(strings.Join(parts, "\n\n"))
}

// askPrompt frames a local command's output for the model. Model-facing text
// stays English (the i18n boundary in i18n.go).
func askPrompt(line, output string) string {
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return "I ran the local command `" + line + "` in my terminal client (it never " +
		"reached the harness). Its output:\n\n```\n" + output + "\n```"
}
