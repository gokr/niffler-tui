// /restart: quit with restartExitCode so the launcher re-runs the binary. The
// command can only arm the flag, write the identity handoff (handoff.go) and
// request the quit — the restart itself is the wrapper script's job (it loops
// on the exit code), so these tests pin the client-side half of that contract:
// the flag is set, a real quit is returned, the successor can adopt this
// client's identity and conversation, and nothing else arms it.
package main

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// isQuit runs a returned command and reports whether it produces a quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// TestRestartArmsQuitFlag pins the command's contract: /restart marks the
// model and returns an actual tea.Quit (not some other message), so the
// program ends and main() can read the flag off Run()'s final model.
func TestRestartArmsQuitFlag(t *testing.T) {
	m := newTestModel()
	updated, cmd := m.executeLocalCommand("/restart")
	got := updated.(model)
	if !got.restart {
		t.Fatal("/restart did not arm the restart flag")
	}
	if !isQuit(cmd) {
		t.Fatalf("/restart returned %v, want a tea.Quit command", cmd)
	}
	// The confirmation block renders for the final frame; without it a slow
	// exit looks like the client merely hung.
	if n := len(got.blocks); n == 0 || got.blocks[n-1].kind != blockMeta {
		t.Fatalf("no restart confirmation block: %+v", got.blocks)
	}
}

// TestRestartHandsOverIdentity pins the other half of the restart contract:
// the successor must be able to adopt this client's registry identity, or a
// claim that was never released — a lost release, a crash, a predecessor too
// old to release at all — leaves it refused the conversation and opening an
// empty one instead.
func TestRestartHandsOverIdentity(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const url = "nats://127.0.0.1:4222"

	m := newTestModel()
	m.natsURL = url
	m.launchDir = "cwd-restart"
	m.uiID = "tui-5c3b1f9a0e42"
	m.session = "conv-carried-over"

	updated, cmd := m.executeLocalCommand("/restart")
	if !updated.(model).restart {
		t.Fatal("/restart did not arm the restart flag")
	}
	if !isQuit(cmd) {
		t.Fatalf("/restart returned %v, want a tea.Quit command", cmd)
	}
	ui, session := consumeHandoff(url, m.launchDir, time.Now())
	if ui != m.uiID || session != m.session {
		t.Fatalf("handoff = (%q, %q), want (%q, %q)", ui, session, m.uiID, m.session)
	}
}

// TestOnlyRestartHandsOverIdentity keeps the handoff to the restart path: a
// normal command must not leave an identity for the next start to adopt.
func TestOnlyRestartHandsOverIdentity(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newTestModel()
	m.natsURL = "nats://127.0.0.1:4222"
	m.launchDir = "cwd-restart"
	m.uiID = "tui-5c3b1f9a0e42"

	for _, line := range []string{"/status", "/help", "/mouse on", "/profile"} {
		_, _ = m.executeLocalCommand(line)
	}
	if ui, session := consumeHandoff(m.natsURL, m.launchDir, time.Now()); ui != "" || session != "" {
		t.Fatalf("a non-restart command left a handoff (%q, %q)", ui, session)
	}
}

// TestRestartExitCodeIsReservedForTheWrapper guards the cross-repo contract
// with the installed wrapper script: it must be non-zero (distinguishable from
// a clean quit), outside the shell's own codes, and outside the sysexits range
// so a future wrapper change cannot confuse a failure with a restart request.
func TestRestartExitCodeIsReservedForTheWrapper(t *testing.T) {
	switch {
	case restartExitCode == 0:
		t.Fatal("restart code 0 would loop forever on a clean exit")
	case restartExitCode < 85 || restartExitCode > 120:
		t.Fatalf("restart code %d collides with sysexits (64-78) or signal exits (128+n)",
			restartExitCode)
	}
}

// TestRestartIsOnlyFromRestartCommand keeps /restart from firing by accident:
// other commands must leave the flag alone, and it survives neither a fresh
// model (a restarted client starts clean) nor is it settable by /mouse-style
// toggles.
func TestRestartIsOnlyFromRestartCommand(t *testing.T) {
	// /profile instead of /theme: the theme switch mutates the package-level
	// styles that TestDefaultThemeMatchesCompiledInStyles asserts on.
	for _, line := range []string{"/status", "/help", "/mouse on", "/cards", "/profile"} {
		m := newTestModel()
		updated, _ := m.executeLocalCommand(line)
		if updated.(model).restart {
			t.Errorf("%s armed the restart flag", line)
		}
	}
	// An argument is not a session id to restart into; it is ignored.
	m := newTestModel()
	updated, _ := m.executeLocalCommand("/restart somewhere-else")
	if got := updated.(model); !got.restart || got.session != m.session {
		t.Fatalf("/restart with an argument changed state: restart=%v session=%q",
			got.restart, got.session)
	}
}
