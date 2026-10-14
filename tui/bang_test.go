package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// errTest is a stand-in for a failed process start (exec.Command error).
var errTest = errString("exec: file does not exist")

type errString string

func (e errString) Error() string { return string(e) }

func TestShellEscapeParse(tt *testing.T) {
	cases := []struct {
		line    string
		command string
		ask     bool
		ok      bool
	}{
		{"!ls", "ls", false, true},
		{"?ls", "ls", true, true},
		{"! git status", "git status", false, true},
		{"   ?  git status  ", "git status", true, true},
		// "!!" reaches the shell as "!…" (its own history expansion), the way
		// a terminal would pass it.
		{"!!ls", "!ls", false, true},
		// A bare marker is an ordinary prompt; a marker in front of a slash
		// command belongs to the slash registry, not to the shell.
		{"!", "", false, false},
		{"?", "", false, false},
		{"! /status", "", false, false},
		{"?/status", "", false, false},
		{"hello", "", false, false},
		{"/help", "", false, false},
		{"", "", false, false},
	}
	for _, c := range cases {
		command, ask, ok := shellEscape(c.line)
		if ok != c.ok || command != c.command || ask != c.ask {
			tt.Errorf("shellEscape(%q) = (%q, %v, %v), want (%q, %v, %v)",
				c.line, command, ask, ok, c.command, c.ask, c.ok)
		}
	}
}

func TestCapPromptOutput(tt *testing.T) {
	short := "hello\n"
	if got := capPromptOutput(short); got != short {
		tt.Fatalf("short output changed: %q", got)
	}
	long := strings.Repeat("x", promptOutputLimit+500)
	got := capPromptOutput(long)
	if !strings.Contains(got, "500 bytes truncated") {
		tt.Fatalf("missing truncation note: %q", got[len(got)-40:])
	}
	if len(got) > promptOutputLimit+64 {
		tt.Fatalf("cap exceeded: %d bytes", len(got))
	}
	// A multi-byte rune straddling the cut must not be sliced in half.
	wide := strings.Repeat("ä", promptOutputLimit)
	if out := capPromptOutput(wide); !utf8.ValidString(out) {
		tt.Fatal("truncation produced invalid UTF-8")
	}
}

func TestShellBlockText(tt *testing.T) {
	// "!" — local output, no model involvement.
	local := shellBlockText(LocaleEN, shellOutputMsg{Command: "ls", Output: "a\nb\n"})
	if !strings.HasPrefix(local, "! ls\n") || !strings.Contains(local, "a\nb") {
		tt.Fatalf("local block = %q", local)
	}
	// "?" — the head line says the output also went to the model.
	ask := shellBlockText(LocaleEN, shellOutputMsg{Command: "ls", Output: "a", Ask: true})
	if !strings.HasPrefix(ask, "? ls\n") {
		tt.Fatalf("ask block head = %q", ask)
	}
	// Failure states are stated, and silence is never rendered as blank.
	failed := shellBlockText(LocaleEN, shellOutputMsg{Command: "false", Exit: 2})
	if !strings.Contains(failed, "exit status 2") || !strings.Contains(failed, "(no output)") {
		tt.Fatalf("failed block = %q", failed)
	}
	broken := shellBlockText(LocaleEN, shellOutputMsg{Command: "nope", Err: errTest})
	if !strings.Contains(broken, errTest.Error()) {
		tt.Fatalf("error block = %q", broken)
	}
}

func TestShellAskPrompt(tt *testing.T) {
	prompt := shellAskPrompt(shellOutputMsg{Command: "make test", Output: "FAIL tests/t_x\n", Exit: 1})
	for _, want := range []string{"make test", "FAIL tests/t_x", "exited with status 1"} {
		if !strings.Contains(prompt, want) {
			tt.Errorf("ask prompt missing %q:\n%s", want, prompt)
		}
	}
	// A silent, failing command still produces a usable prompt.
	silent := shellAskPrompt(shellOutputMsg{Command: "false", Exit: 1})
	if !strings.Contains(silent, "(no output)") {
		tt.Fatalf("silent prompt = %q", silent)
	}
}

// shellEscapeViaEnter drives a line through the Enter key, the way a human
// submits it, and returns the command it dispatched (nil when the line was
// handled without one).
func shellEscapeViaEnter(tt *testing.T, m model, line string) (model, tea.Cmd) {
	tt.Helper()
	m.input.SetValue(line)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(model), cmd
}

func TestShellEscapeRunsLocallyWithoutConnection(tt *testing.T) {
	m := newTestModel()
	// Deliberately disconnected: "!" is the human's own command line and must
	// work regardless of the bus.
	m, cmd := shellEscapeViaEnter(tt, m, "!echo niffler-shell-escape")
	if cmd == nil {
		t := m.blocks
		tt.Fatalf("no command dispatched; blocks = %#v", t)
	}
	msg, ok := cmd().(shellOutputMsg)
	if !ok {
		tt.Fatalf("command returned %T", cmd())
	}
	if msg.Ask || msg.Command != "echo niffler-shell-escape" {
		tt.Fatalf("msg = %#v", msg)
	}
	if !strings.Contains(msg.Output, "niffler-shell-escape") {
		tt.Fatalf("output = %q", msg.Output)
	}
	// The result renders as a shell block, and "!" never starts a turn.
	updated, turnCmd := m.Update(msg)
	m = updated.(model)
	if turnCmd != nil {
		tt.Error("\"!\" must not dispatch a turn")
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockShell || !strings.Contains(last.text, "! echo niffler-shell-escape") {
		tt.Fatalf("last block = %#v", last)
	}
	if m.busy {
		tt.Error("busy after a local shell escape")
	}
}

func TestShellEscapeAskSendsOutputToModel(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m.session = "s1"
	m, cmd := shellEscapeViaEnter(tt, m, "?echo niffler-ask")
	if cmd == nil {
		tt.Fatal("no command dispatched")
	}
	msg := cmd().(shellOutputMsg)
	if !msg.Ask || msg.Session != "s1" {
		tt.Fatalf("msg = %#v", msg)
	}
	updated, turnCmd := m.Update(msg)
	m = updated.(model)
	if turnCmd == nil || !m.busy {
		t := m.blocks
		tt.Fatalf("ask must start a turn (busy=%v, blocks=%#v)", m.busy, t)
	}
	// The output is visible AND the prompt enters the conversation.
	var sawShell, sawUser bool
	for _, b := range m.blocks {
		if b.kind == blockShell && strings.Contains(b.text, "niffler-ask") {
			sawShell = true
		}
		if b.kind == blockUser && strings.Contains(b.text, "echo niffler-ask") {
			sawUser = true
		}
	}
	if !sawShell || !sawUser {
		tt.Fatalf("shell=%v user=%v blocks=%#v", sawShell, sawUser, m.blocks)
	}
}

func TestShellEscapeAskWhileBusySteers(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m.session = "s1"
	m.busy = true
	updated, cmd := m.handleShellOutput(shellOutputMsg{
		Command: "ls", Output: "a", Ask: true, Session: "s1",
	})
	m = updated.(model)
	if cmd == nil {
		tt.Fatal("mid-turn \"?\" must steer, not drop the output")
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockUser || !strings.HasPrefix(last.text, "Steer: ") {
		tt.Fatalf("last block = %#v", last)
	}
}

func TestShellEscapeAskRefusesMovedConversation(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m.session = "s2" // the output belongs to s1
	updated, cmd := m.handleShellOutput(shellOutputMsg{
		Command: "ls", Output: "a", Ask: true, Session: "s1",
	})
	m = updated.(model)
	if cmd != nil {
		tt.Error("a conversation switch must drop the prompt, not send it")
	}
	// The output itself stays readable so the human can re-ask it.
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockError {
		tt.Fatalf("last block = %#v", last)
	}
}

func TestShellEscapeAskDisconnectedKeepsOutput(tt *testing.T) {
	m := newTestModel()
	m.connected = false
	m.session = "s1"
	updated, cmd := m.handleShellOutput(shellOutputMsg{
		Command: "ls", Output: "a", Ask: true, Session: "s1",
	})
	m = updated.(model)
	if cmd != nil || m.busy {
		tt.Fatalf("disconnected \"?\" must not start a turn (cmd=%v busy=%v)", cmd, m.busy)
	}
	var sawShell, sawError bool
	for _, b := range m.blocks {
		sawShell = sawShell || b.kind == blockShell
		sawError = sawError || b.kind == blockError
	}
	if !sawShell || !sawError {
		tt.Fatalf("shell=%v error=%v blocks=%#v", sawShell, sawError, m.blocks)
	}
}
