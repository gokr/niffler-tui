package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestInstanceSwitchArgsDropsForeignSession(t *testing.T) {
	got := instanceSwitchArgs([]string{"tui", "--instance=old", "-session", "foreign", "--session=other"}, "worker")
	want := []string{"tui", "--instance", "worker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestInstanceRotation(t *testing.T) {
	order := []string{"a", "b", "c"}
	if nextInstanceName(order, "b") != "c" || nextInstanceName(order, "c") != "a" || nextInstanceName(nil, "a") != "" {
		t.Fatal("rotation")
	}
}

func TestFailedInstanceSwitchKeepsCurrentSession(t *testing.T) {
	m := newTestModel()
	m.instanceName = "old"
	before := m.session
	updated, cmd := m.Update(instanceSwitchMsg{name: "other", err: errors.New("identity mismatch")})
	got := updated.(model)
	if cmd != nil || got.switchInstance != "" || got.session != before || got.instanceName != "old" {
		t.Fatal("failed switch changed instance")
	}
}

func TestSuccessfulSwitchPersistsOldSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newTestModel()
	m.instanceState = "instance:old"
	m.launchDir = "/remote"
	updated, cmd := m.Update(instanceSwitchMsg{name: "other"})
	got := updated.(model)
	if cmd == nil || got.switchInstance != "other" {
		t.Fatal("no switch")
	}
	if loadLastSession(m.instanceState, m.launchDir) != m.session {
		t.Fatal("old session lost")
	}
}
