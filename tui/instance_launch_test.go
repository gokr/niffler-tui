package main

import (
	"testing"
)

func TestParseInstanceOptions(t *testing.T) {
	opts, rest, err := parseInstanceOptions([]string{"tui", "--instance=worker", "-session", "conversation"})
	if err != nil || opts.Name != "worker" || len(rest) != 3 || rest[1] != "-session" {
		t.Fatalf("%+v %v %v", opts, rest, err)
	}
}

// A launch-time route is named on the command line; nothing is remembered in a
// file. Exactly one route, and every route needs a name to select it by.
func TestParseInstanceOptionsRoutes(t *testing.T) {
	valid := map[string]instanceOptions{
		"--ssh":   {Name: "worker", SSH: "ubuntu@worker"},
		"--incus": {Name: "worker", Incus: "worker"},
		"--lima":  {Name: "worker", Lima: "worker"},
		"--bus":   {Name: "worker", Bus: "nats://127.0.0.1:4222"},
	}
	for flag, want := range valid {
		got, _, err := parseInstanceOptions([]string{"tui", "--instance", "worker", flag, want.lookupValue()})
		if err != nil {
			t.Fatalf("%s refused: %v", flag, err)
		}
		if got.routeCount() != 1 {
			t.Fatalf("%s produced %d routes", flag, got.routeCount())
		}
	}
	if got, _, err := parseInstanceOptions([]string{"tui", "--instance", "worker",
		"--incus", "worker", "--host", "operator@hypervisor"}); err != nil || got.Host != "operator@hypervisor" {
		t.Fatalf("guest host refused: %+v %v", got, err)
	}
	if _, _, err := parseInstanceOptions([]string{"tui", "--instance", "worker",
		"--incus", "worker", "--ssh", "ubuntu@x"}); err == nil {
		t.Fatal("two routes accepted")
	}
	if _, _, err := parseInstanceOptions([]string{"tui", "--ssh", "ubuntu@x"}); err == nil {
		t.Fatal("a route without a name accepted")
	}
	if _, _, err := parseInstanceOptions([]string{"tui", "--instance", "worker", "--ssh", "ubuntu@x", "--host", "h"}); err == nil {
		t.Fatal("--host accepted for a direct SSH route")
	}
	if _, _, err := parseInstanceOptions([]string{"tui", "--instance", "worker", "--instance-probe"}); err != nil {
		t.Fatalf("--instance-probe refused: %v", err)
	}
	if _, _, err := parseInstanceOptions([]string{"tui", "--instance-probe"}); err == nil {
		t.Fatal("--instance-probe without a name accepted")
	}
	// The removed import flag is not consumed here; it reaches flag.Parse and
	// fails loudly as an unknown flag instead of silently importing nothing.
	_, rest, err := parseInstanceOptions([]string{"tui", "--instance-import", "receipt"})
	if err != nil {
		t.Fatalf("parse refused before the flag parser could report it: %v", err)
	}
	hasImport := false
	for _, arg := range rest {
		if arg == "--instance-import" {
			hasImport = true
		}
	}
	if !hasImport {
		t.Fatalf("removed flag was swallowed instead of failing: %v", rest)
	}
}

func (o instanceOptions) routeCount() int {
	n := 0
	for _, route := range []string{o.SSH, o.Lima, o.Incus, o.Bus} {
		if route != "" {
			n++
		}
	}
	return n
}

func (o instanceOptions) lookupValue() string {
	for _, route := range []string{o.SSH, o.Lima, o.Incus, o.Bus} {
		if route != "" {
			return route
		}
	}
	return ""
}

func TestProfileFromOptionsUsesGuestRootDefault(t *testing.T) {
	p, err := profileFromOptions(instanceOptions{Name: "worker", Incus: "worker"})
	if err != nil {
		t.Fatalf("guest route refused: %v", err)
	}
	if p.Expect.Root != "/home/ubuntu/niffler" {
		t.Fatalf("guest root default = %q", p.Expect.Root)
	}
	if p.Expect.ID != "" {
		t.Fatal("a launch-time route invented an identity pin")
	}
	if _, err := profileFromOptions(instanceOptions{Name: "worker",
		Incus: "worker", Root: "/srv/niffler"}); err == nil {
		t.Fatal("guest route accepted a non-standard root")
	}
}

func TestRemoteSessionSwitchKeepsRemoteWorkspaceAndState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
