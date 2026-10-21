package main

// Shared fixtures and the route/pin validator, now that the client has no
// instance registry of its own.
import (
	"strings"
	"testing"
)

func testInstance() instanceProfile {
	return instanceProfile{SSH: "ubuntu@worker", Workspace: "/srv/work",
		Expect: instanceExpectation{ID: strings.Repeat("a", 32),
			Root: "/home/ubuntu/niffler", Name: "worker"}}
}

func TestValidateProfileRefusals(t *testing.T) {
	if err := validateProfile(testInstance()); err != nil {
		t.Fatalf("valid profile refused: %v", err)
	}
	cases := map[string]func(*instanceProfile){
		"no transport":         func(p *instanceProfile) { p.SSH = "" },
		"two transports":       func(p *instanceProfile) { p.URL = "nats://127.0.0.1:4222" },
		"missing identity":     func(p *instanceProfile) { p.Expect.ID = "" },
		"bad identity":         func(p *instanceProfile) { p.Expect.ID = "zz" },
		"relative root":        func(p *instanceProfile) { p.Expect.Root = "srv" },
		"relative workspace":   func(p *instanceProfile) { p.Workspace = "work" },
		"ssh injection":        func(p *instanceProfile) { p.SSH = "-oProxyCommand=bad" },
		"host on direct ssh":   func(p *instanceProfile) { p.Host = "user@host" },
		"guest root not fixed": func(p *instanceProfile) { p.SSH = ""; p.Incus = "worker"; p.Expect.Root = "/srv" },
		"public bus":           func(p *instanceProfile) { p.SSH = ""; p.URL = "nats://example.com:4222" },
		"bus with path":        func(p *instanceProfile) { p.SSH = ""; p.URL = "nats://127.0.0.1:4222/x" },
	}
	for name, mutate := range cases {
		p := testInstance()
		mutate(&p)
		if err := validateProfile(p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestValidateRouteAllowsUnknownPin(t *testing.T) {
	p := testInstance()
	p.Expect.ID = ""
	if err := validateRoute(p); err != nil {
		t.Fatalf("a launch-time route without a pin must validate: %v", err)
	}
}
