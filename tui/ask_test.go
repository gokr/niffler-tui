package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSlashAskParse(tt *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{"?/status", "/status", true},
		{"?/doctor deep", "/doctor deep", true},
		{"  ? /recall retry  ", "/recall retry", true},
		{"?", "", false},
		{"? ", "", false},
		{"?/", "", false},
		{"?ls", "", false}, // a shell escape, not a slash ask
		{"!/status", "", false},
		{"/status", "", false},
		{"hello", "", false},
	}
	for _, c := range cases {
		got, ok := slashAsk(c.line)
		if got != c.want || ok != c.ok {
			tt.Errorf("slashAsk(%q) = (%q, %v), want (%q, %v)", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestAskPrompt(tt *testing.T) {
	prompt := askPrompt("/status", "provider: deepseek")
	for _, want := range []string{"`/status`", "provider: deepseek"} {
		if !strings.Contains(prompt, want) {
			tt.Fatalf("ask prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(askPrompt("/nothing", "  \n"), "(no output)") {
		tt.Fatal("empty command output must still produce a readable prompt")
	}
}

// askViaEnter drives "?/command" through the Enter key and returns the model
// plus the command the ask dispatched.
func askViaEnter(tt *testing.T, m model, line string) (model, tea.Cmd) {
	tt.Helper()
	m.input.SetValue(line)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(model), cmd
}

func TestSlashAskSendsSynchronousOutput(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m.session = "s1"
	m, cmd := askViaEnter(tt, m, "?/status")
	if _, wrapped := unwrapAsk(cmd); wrapped {
		tt.Fatal("a synchronous local command was wrapped; it prints before returning")
	}
	if !m.busy {
		tt.Fatal("the ask must start a turn")
	}
	if m.ask.active {
		tt.Fatal("the ask must be cleared once it is sent")
	}
	// The command's own output AND the prompt are both in the transcript.
	var report, prompt string
	for _, b := range m.blocks {
		if b.kind == blockMeta && len(b.text) > len(report) {
			report = b.text
		}
		if b.kind == blockUser {
			prompt = b.text
		}
	}
	if report == "" {
		tt.Fatal("no report block")
	}
	if !strings.Contains(prompt, "`/status`") {
		tt.Fatalf("no ask prompt rendered: %q", prompt)
	}
	if first := firstLine(report); first == "" || !strings.Contains(prompt, first) {
		tt.Fatalf("prompt does not carry the command's output:\n%q", prompt)
	}
}

// unwrapAsk reports whether a command is still a pending ask wrapper.
func unwrapAsk(cmd tea.Cmd) (askResultMsg, bool) {
	if cmd == nil {
		return askResultMsg{}, false
	}
	msg, ok := cmd().(askResultMsg)
	return msg, ok
}

func TestSlashAskWrapsAsyncCommand(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m.session = "s1"
	// /recall answers over the bus: its output lands in a later message, so
	// the ask wraps that command (askWrapCmd) and closes when it arrives.
	m, cmd := askViaEnter(tt, m, "?/recall retry policy")
	if cmd == nil {
		tt.Fatal("an asynchronous command must be wrapped")
	}
	if !m.ask.active {
		tt.Fatal("the ask must stay open until the result lands")
	}
	// Deliver the command's own result as the runtime would — through the
	// wrapper, so the ask sees it after the command has rendered.
	hits := json.RawMessage(`{"count":1,"via":"store.search","text":"1 hit(s) via store.search:",
		"matches":[{"id":"conv-a:000003","role":"tool","snippet":"the [hit]"}]}`)
	updated, _ := m.Update(askResultMsg{inner: slashResultMsg{Name: "recall", Session: "s1", Result: hits}})
	m = updated.(model)
	if m.ask.active {
		tt.Fatal("the ask must close once the result has been rendered")
	}
	if !m.busy {
		tt.Fatal("the ask must start a turn")
	}
	var sawHits, sawPrompt bool
	for _, b := range m.blocks {
		if b.kind == blockMeta && strings.Contains(b.text, "1 hit(s) via store.search") {
			sawHits = true
		}
		if b.kind == blockUser && strings.Contains(b.text, "`/recall retry policy`") &&
			strings.Contains(b.text, "1 hit(s) via store.search") {
			sawPrompt = true
		}
	}
	if !sawHits || !sawPrompt {
		tt.Fatalf("hits=%v prompt=%v blocks=%#v", sawHits, sawPrompt, m.blocks)
	}
}

func TestSlashAskRefusesUIBrowsers(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m.session = "s1"
	// /session opens a browser: there is no output text to ask about.
	m, cmd := askViaEnter(tt, m, "?/session")
	if cmd != nil || m.busy {
		tt.Fatalf("a UI-opening command must not ask (cmd=%v busy=%v)", cmd, m.busy)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockError {
		tt.Fatalf("last block = %#v", last)
	}
}

func TestSlashAskRefusesUnknownCommand(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m, cmd := askViaEnter(tt, m, "?/nosuchcommand")
	if cmd != nil || m.busy {
		tt.Fatalf("unknown command = (cmd=%v busy=%v)", cmd, m.busy)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockError {
		tt.Fatalf("last block = %#v", last)
	}
}

func TestSlashAskDisconnectedKeepsOutput(tt *testing.T) {
	m := newTestModel()
	m.connected = false
	m.session = "s1"
	m, cmd := askViaEnter(tt, m, "?/status")
	if cmd != nil || m.busy {
		tt.Fatalf("disconnected ask = (cmd=%v busy=%v)", cmd, m.busy)
	}
	var sawReport, sawError bool
	for _, b := range m.blocks {
		sawReport = sawReport || b.kind == blockMeta
		sawError = sawError || b.kind == blockError
	}
	if !sawReport || !sawError {
		tt.Fatalf("report=%v error=%v blocks=%#v", sawReport, sawError, m.blocks)
	}
}

func TestSlashAskRefusesShellMarker(tt *testing.T) {
	m := newTestModel()
	m.connected = true
	m, cmd := askViaEnter(tt, m, "!/status")
	if cmd != nil || m.busy {
		tt.Fatalf("!/status = (cmd=%v busy=%v)", cmd, m.busy)
	}
	// The one thing "!/status" must never do is reach the model as prompt text.
	m.input.SetValue("!/status")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.input.Value() != "" {
		tt.Fatalf("input kept %q", m.input.Value())
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockError || !strings.Contains(last.text, "!/status") {
		tt.Fatalf("last block = %#v", last)
	}
}

// TestHelpListsShellMarkers pins the marker lines into /help. They are the one
// pair of input syntaxes that are not registry commands, so nothing else would
// notice if their lines stopped being rendered (they were dead catalog keys
// until this test existed).
func TestHelpListsShellMarkers(tt *testing.T) {
	for _, loc := range []Locale{LocaleEN, LocaleZH, LocaleZHTW} {
		m := newTestModel()
		m.loc = loc
		updated, _ := m.executeLocalCommand("/help")
		m = updated.(model)
		last := m.blocks[len(m.blocks)-1]
		for _, key := range []string{"help.bang", "help.slashAsk"} {
			want := t(loc, key)
			if !strings.Contains(last.text, want) {
				tt.Errorf("%s: /help does not render %s:\n%s", loc, key, last.text)
			}
		}
	}
}
