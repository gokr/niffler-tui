package main

import (
	"os"
	"strings"
	"testing"
)

func TestFleetRegistryCannotBeAskedAbout(t *testing.T) {
	m := newTestModel()
	known, interactive := m.knownLocalCommand("niffler")
	if !known || !interactive {
		t.Fatal("fleet registry can leak through ?/niffler")
	}
	updated, cmd := m.executeSlashAsk("/niffler")
	if cmd != nil || updated.(model).ask.active {
		t.Fatal("private registry ask accepted")
	}
}

func TestRemoteShellEscapeUsesLocalLaunchDirectory(t *testing.T) {
	m := newTestModel()
	m.remoteInstance = true
	m.cwd = "/nonexistent-remote-workspace"
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	msg := m.shellEscapeCmd("pwd", false)().(shellOutputMsg)
	if msg.Err != nil || strings.TrimSpace(msg.Output) != cwd {
		t.Fatalf("shell escaped into remote workspace: %+v", msg)
	}
}
