// Shell escape: "!command" runs the command locally and shows its output in
// the transcript — the equivalent of typing it in another terminal. "?command"
// runs the same command and hands its output to the model as a turn.
//
// Both are client-side: core never sees the command line, the harness's bash
// tool is not involved, and no approval gate applies — approvals gate the
// *model's* tool calls, not the human's own keystroke. The trust boundary is
// the one a human editing a file in their editor already crosses while a turn
// runs: the command runs on whatever machine this TUI runs on, which under a
// remote NIF_NATS_URL is not necessarily the harness's machine. It also runs
// beside the agent's write lease rather than under it (cooperative
// coordination only). Interactive programs (vim, less, ssh) are not supported:
// the command has no terminal attached.
//
// A marker in front of a slash command ("?/status") is not a shell escape — it
// asks about a local command's output, which the slash path handles.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// shellMarkerLocal runs a command for the human only.
	shellMarkerLocal = '!'
	// shellMarkerAsk runs a command and sends its output to the model.
	shellMarkerAsk = '?'
	// promptOutputLimit caps the bytes kept from one command's output, for the
	// transcript and for the prompt alike: output that would flood either is
	// worse than output the human can re-run narrowly, and the truncation is
	// always stated (never silent). Shell escapes (bang.go) and "?/command"
	// asks (ask.go) share it — both end up in a prompt.
	promptOutputLimit = 8192
	// shellTimeout bounds one command so a hung program cannot leave the
	// command's goroutine pending forever.
	shellTimeout = 10 * time.Minute
)

// shellOutputMsg carries one finished shell escape back to the Update loop.
type shellOutputMsg struct {
	Command string // the command line as typed, marker stripped
	Output  string // combined stdout+stderr, capped
	Exit    int    // process exit status (0 when it never started)
	Err     error  // start/wait/timeout failure — not a non-zero status
	Ask     bool   // "?" — the output goes to the model
	Session string // conversation id at invocation time (ask guard)
}

// shellEscape splits an input line into a shell escape. ok is false for
// anything that is not one: no marker, a bare marker (so a lone "!" or "?"
// stays an ordinary prompt), or a slash command behind the marker (that
// belongs to the slash registry).
func shellEscape(line string) (command string, ask, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false, false
	}
	switch trimmed[0] {
	case shellMarkerLocal:
		ask = false
	case shellMarkerAsk:
		ask = true
	default:
		return "", false, false
	}
	rest := strings.TrimSpace(trimmed[1:])
	if rest == "" || strings.HasPrefix(rest, "/") {
		return "", false, false
	}
	return rest, ask, true
}

// shellEscapeCmd runs one shell escape off the UI goroutine. The command runs
// in the conversation workspace (the directory the agent's own tools see),
// through the user's shell, with this process's environment.
func (m model) shellEscapeCmd(command string, ask bool) tea.Cmd {
	dir := m.cwd
	session := m.session
	return func() tea.Msg {
		if dir == "" {
			if cwd, err := os.Getwd(); err == nil {
				dir = cwd
			}
		}
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, shell, "-c", command)
		cmd.Dir = dir
		// Let scripts detect the origin (gemini-cli's GEMINI_CLI precedent).
		cmd.Env = append(os.Environ(), "NIF_TUI=1")
		out, err := cmd.CombinedOutput()
		msg := shellOutputMsg{
			Command: command,
			Output:  capPromptOutput(string(out)),
			Ask:     ask,
			Session: session,
		}
		switch {
		case err == nil:
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			msg.Err = fmt.Errorf("timed out after %s", shellTimeout)
		default:
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				msg.Exit = exit.ExitCode()
			} else {
				msg.Err = err
			}
		}
		return msg
	}
}

// capPromptOutput truncates one command's output to promptOutputLimit bytes,
// keeping the head — the first lines are what names the failure — and stating
// how much was dropped, so a prompt never loses the tail silently.
func capPromptOutput(out string) string {
	if len(out) <= promptOutputLimit {
		return out
	}
	kept := out[:promptOutputLimit]
	// Never cut inside a UTF-8 rune: drop trailing continuation bytes and a
	// dangling lead byte.
	for len(kept) > 0 && kept[len(kept)-1]&0xC0 == 0x80 {
		kept = kept[:len(kept)-1]
	}
	if len(kept) > 0 && kept[len(kept)-1]&0xC0 == 0xC0 {
		kept = kept[:len(kept)-1]
	}
	return kept + "\n" + fmt.Sprintf("… %d bytes truncated", len(out)-len(kept))
}

// shellBlockText renders a finished escape as the transcript block: the marker
// head line (so a scrolled-back transcript says whether the output went to the
// model), the output, and the exit status when the command failed.
func shellBlockText(loc Locale, msg shellOutputMsg) string {
	marker := "!"
	if msg.Ask {
		marker = "?"
	}
	var b strings.Builder
	b.WriteString(marker + " " + msg.Command)
	switch {
	case msg.Err != nil:
		b.WriteString("\n" + msg.Err.Error())
	case msg.Exit != 0:
		b.WriteString("\n" + t(loc, "shell.exit", strconv.Itoa(msg.Exit)))
	}
	out := strings.TrimRight(msg.Output, "\n")
	if strings.TrimSpace(out) == "" {
		out = t(loc, "shell.noOutput")
	}
	b.WriteString("\n" + out)
	return b.String()
}

// shellEscapeStart reports the escape and starts the command. "?" runs the
// same local command as "!" — only its output's destination differs — so both
// markers can run while a turn is live and neither needs a connection.
func (m model) shellEscapeStart(command string, ask bool) (tea.Model, tea.Cmd) {
	marker := "!"
	if ask {
		marker = "?"
	}
	m.addBlock(blockMeta, "→ "+marker+" "+command)
	m.syncViewport(true)
	return m, m.shellEscapeCmd(command, ask)
}

// handleShellOutput renders a finished escape, and for "?" hands its output to
// the model. The conversation is guarded exactly as a slash result's
// userMessage is: a session switch while the command ran drops the prompt
// (the output stays visible in the transcript, so it can be re-asked).
func (m model) handleShellOutput(msg shellOutputMsg) (tea.Model, tea.Cmd) {
	m.addBlock(blockShell, shellBlockText(m.loc, msg))
	if !msg.Ask {
		m.syncViewport(true)
		return m, nil
	}
	if msg.Session != m.session {
		m.addBlock(blockError, t(m.loc, "slash.conversationChanged"))
		m.syncViewport(true)
		return m, nil
	}
	return m.askSend(shellAskPrompt(msg))
}

// askSend puts locally produced text into the conversation: an ordinary turn
// when the conversation is idle, a steer when a turn is live (the same lane a
// typed busy message takes — the human asked for an answer, not for the turn
// to be interrupted). Shared by the shell escapes (bang.go) and the
// "?/command" asks (ask.go).
func (m model) askSend(prompt string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.addBlock(blockError, t(m.loc, "shell.notConnected"))
		m.syncViewport(true)
		return m, nil
	}
	if m.busy {
		m.addBlock(blockUser, "Steer: "+prompt)
		m.layout()
		m.syncViewport(true)
		return m, m.sendSteer(prompt)
	}
	if m.controlPending {
		m.contextNote = t(m.loc, "note.controlPending")
		return m, nil
	}
	m.busy = true
	m.busySince = time.Now()
	m.lastTokenAt = m.busySince
	m.streamKind = streamNone
	m.steered = 0
	m.hadAssistant = false
	m.assistantIdx = -1
	m.thinkingIdx = -1
	m.setStreaming(false)
	m.roundClosed = false
	m.turnEndRendered = false
	m.addBlock(blockUser, prompt)
	m.layout()
	m.syncViewport(true)
	return m, tea.Batch(m.sendTurn(prompt, nil), m.armSpinner())
}

// shellAskPrompt wraps a command's output as the prompt "?" sends. Model-facing
// text stays English (the i18n boundary in i18n.go).
func shellAskPrompt(msg shellOutputMsg) string {
	out := strings.TrimRight(msg.Output, "\n")
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "I ran `%s` in the conversation workspace. Its output:\n\n```\n%s\n```", msg.Command, out)
	if msg.Exit != 0 {
		fmt.Fprintf(&b, "\n\nThe command exited with status %d.", msg.Exit)
	}
	if msg.Err != nil {
		fmt.Fprintf(&b, "\n\nThe command did not complete: %s.", msg.Err)
	}
	return b.String()
}
