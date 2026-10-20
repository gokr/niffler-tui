package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
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
	t.Setenv("HOME", t.TempDir())
	m := newTestModel()
	m.instanceState = "instance:old"
	m.launchDir = "/remote"
	updated, cmd := m.Update(instanceSwitchMsg{name: "other", claim: &instanceClaim{}})
	got := updated.(model)
	if cmd == nil || got.switchInstance != "other" {
		t.Fatal("no switch")
	}
	if loadLastSession(m.instanceState, m.launchDir) != m.session {
		t.Fatal("old session lost")
	}
}

// A switch answer without a reservation must never quit into a relaunch: main
// has nothing to hand over and would release this client for a target it does
// not hold.
func TestSwitchWithoutReservationIsRefused(t *testing.T) {
	m := newTestModel()
	updated, cmd := m.Update(instanceSwitchMsg{name: "other"})
	got := updated.(model)
	if cmd != nil || got.switchInstance != "" || got.switchClaim != nil {
		t.Fatal("switch without a reservation was accepted")
	}
}

// TestRelaunchCarriesTheReservation: the successor is a new process, so the
// reserved target claim can only reach it through the environment of the exec
// — with the interface target as the last argument and the stale session
// dropped. Success never returns, which is why the caller may release the
// reservation only when this reports an error.
func TestRelaunchCarriesTheReservation(t *testing.T) {
	old := instanceExec
	defer func() { instanceExec = old }()
	var gotPath string
	var gotArgs, gotEnv []string
	var calls int
	instanceExec = func(path string, argv, env []string) error {
		calls++
		gotPath, gotArgs, gotEnv = path, argv, env
		return errors.New("exec refused")
	}
	// The claim must not be inherited by a launch that never happened.
	os.Setenv(instanceClaimEnv, "stale")
	defer os.Unsetenv(instanceClaimEnv)

	h := instanceClaimHandoff{Scope: "instance:abc", Workspace: "/worker", UI: "tui-111122223333", Session: "conv-1"}
	err := relaunchInstance([]string{"niffler-tui", "--instance=old", "-session", "foreign"}, "worker", h)
	if err == nil || calls != 1 {
		t.Fatalf("relaunch = %v (%d execs)", err, calls)
	}
	if gotPath == "" || gotArgs[0] != gotPath || gotArgs[len(gotArgs)-2] != "--instance" || gotArgs[len(gotArgs)-1] != "worker" {
		t.Fatalf("args = %v", gotArgs)
	}
	found := ""
	for _, entry := range gotEnv {
		if strings.HasPrefix(entry, instanceClaimEnv+"=") {
			found = strings.TrimPrefix(entry, instanceClaimEnv+"=")
		}
	}
	if found == "stale" || !strings.Contains(found, `"ui":"tui-111122223333"`) || !strings.Contains(found, `"session":"conv-1"`) {
		t.Fatalf("claim not handed over: %q", found)
	}
	for _, entry := range gotEnv {
		if strings.HasPrefix(entry, "NIF_SESSION=") {
			t.Fatalf("stale session flag leaked into the successor: %q", entry)
		}
	}
}
