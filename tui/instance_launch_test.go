package main

import (
	"testing"
)

func TestParseInstanceOptions(t *testing.T) {
	opts, rest, err := parseInstanceOptions([]string{"tui", "--instance=worker", "-session", "conversation"})
	if err != nil || opts.Name != "worker" || len(rest) != 3 || rest[1] != "-session" {
		t.Fatalf("%+v %v %v", opts, rest, err)
	}
	for _, args := range [][]string{{"tui", "--instance"}, {"tui", "--instance", "a", "--instance=b"}, {"tui", "--instance-import", "receipt"}} {
		if _, _, err := parseInstanceOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestRemoteSessionSwitchKeepsRemoteWorkspaceAndState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newTestModel()
	m.remoteInstance = true
	m.instanceState = "instance:stable"
	m.launchDir = "/remote/work"
	m.natsURL = "nats://127.0.0.1:12345"
	m = m.switchSession("remote-session")
	if m.cwd != "/remote/work" {
		t.Fatal("local workspace leaked", m.cwd)
	}
	if loadLastSession(m.instanceState, m.launchDir) != "remote-session" {
		t.Fatal("state not instance scoped")
	}
	if loadLastSession(m.natsURL, m.launchDir) != "" {
		t.Fatal("state persisted under tunnel port")
	}
	m.input.SetValue("@some-path")
	_, cmd := m.handleFileTab(false)
	if cmd != nil {
		t.Fatal("remote file completion enumerated local disk")
	}
}
