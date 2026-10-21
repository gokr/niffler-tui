package main

// Remote instance selection at LAUNCH is a command-line decision, never a file.
// The client always has its local harness; named peers come from the harness's
// own fleet registry once connected, so nothing has to be remembered locally
// before the first connection. A peer that is not registered yet (or a tunnel
// the verifier cannot derive) is named on the command line for that launch.
//
//	--instance NAME --ssh user@host             (root defaults to /home/ubuntu/niffler)
//	--instance NAME --incus container [--host operator@hypervisor]
//	--instance NAME --lima vm [--host operator@hypervisor]
//	--instance NAME --bus nats://127.0.0.1:PORT
//
// Every route is validated and probed before it is used: the launch path is a
// convenience over "connect to this address", not a trust decision.
import (
	"context"
	"fmt"
	"strings"
)

// instanceOptions is the launch-time route description. Name alone is resolved
// from the harness registry instead (see selectInstanceFromRegistry).
type instanceOptions struct {
	Name  string
	SSH   string
	Lima  string
	Incus string
	Host  string
	Bus   string
	Root  string
	Probe bool
}

func parseInstanceOptions(args []string) (instanceOptions, []string, error) {
	var opts instanceOptions
	rest := []string{args[0]}
	seen := map[string]bool{}
	for i := 1; i < len(args); i++ {
		key, value, eq := strings.Cut(args[i], "=")
		if key == "--instance-probe" {
			if opts.Name == "" {
				return opts, nil, fmt.Errorf("--instance-probe requires --instance NAME")
			}
			opts.Probe = true
			continue
		}
		switch key {
		case "--instance", "--ssh", "--lima", "--incus", "--host", "--bus", "--instance-root":
		default:
			rest = append(rest, args[i])
			continue
		}
		if seen[key] {
			return opts, nil, fmt.Errorf("duplicate %s", key)
		}
		seen[key] = true
		if !eq {
			i++
			if i >= len(args) {
				return opts, nil, fmt.Errorf("%s requires a value", key)
			}
			value = args[i]
		}
		if value == "" {
			return opts, nil, fmt.Errorf("%s requires a non-empty value", key)
		}
		switch key {
		case "--instance":
			opts.Name = value
		case "--ssh":
			opts.SSH = value
		case "--lima":
			opts.Lima = value
		case "--incus":
			opts.Incus = value
		case "--host":
			opts.Host = value
		case "--bus":
			opts.Bus = value
		case "--instance-root":
			opts.Root = value
		}
	}
	transports := 0
	for _, t := range []string{opts.SSH, opts.Lima, opts.Incus, opts.Bus} {
		if t != "" {
			transports++
		}
	}
	if transports > 1 {
		return opts, nil, fmt.Errorf("--ssh, --lima, --incus and --bus are mutually exclusive")
	}
	if transports > 0 && opts.Name == "" {
		return opts, nil, fmt.Errorf("a route requires --instance NAME (the local harness needs no route)")
	}
	if opts.Host != "" && (opts.SSH != "" || opts.Bus != "" || (opts.Lima == "" && opts.Incus == "")) {
		return opts, nil, fmt.Errorf("--host requires --lima or --incus")
	}
	if opts.Root != "" && opts.Name == "" {
		return opts, nil, fmt.Errorf("--instance-root requires --instance NAME")
	}
	return opts, rest, nil
}

// profileFromOptions turns the launch flags into a validated profile. The
// identity is unknown at this point; openInstanceTransport proves it and the
// caller replaces Expect with the live identity before anything is trusted.
func profileFromOptions(opts instanceOptions) (instanceProfile, error) {
	root := opts.Root
	if root == "" {
		root = "/home/ubuntu/niffler"
	}
	p := instanceProfile{Host: opts.Host, Workspace: root,
		Expect: instanceExpectation{Name: opts.Name, Root: root}}
	switch {
	case opts.SSH != "":
		p.SSH = opts.SSH
	case opts.Lima != "":
		p.Lima = opts.Lima
	case opts.Incus != "":
		p.Incus = opts.Incus
	case opts.Bus != "":
		p.URL = opts.Bus
	default:
		return p, fmt.Errorf("no route given")
	}
	if err := validateRoute(p); err != nil {
		return p, err
	}
	return p, nil
}

// validateRoute checks a launch-time route, whose identity pin is not known
// until the peer answers.
func validateRoute(p instanceProfile) error { return validateRouteShape(p, false) }

// selectInstance resolves a named peer: a launch-time route if one was given,
// otherwise the harness's fleet registry on the local harness.
func selectInstance(ctx context.Context, opts instanceOptions) (instanceProfile, *instanceTransport, error) {
	var p instanceProfile
	var err error
	if opts.SSH != "" || opts.Lima != "" || opts.Incus != "" || opts.Bus != "" {
		if p, err = profileFromOptions(opts); err != nil {
			return p, nil, err
		}
	} else {
		p, err = instanceFromRegistry(opts.Name)
		if err != nil {
			return p, nil, err
		}
	}
	transport, identity, err := openInstanceTransport(ctx, p)
	if err != nil {
		return p, nil, err
	}
	// Trust the LIVE identity, never the launch flags: the pin is what later
	// reconnects and cache scoping key off, so it must describe what answered.
	if p.Expect.ID == "" {
		p.Expect = instanceExpectation{ID: identity.InstanceID, Name: opts.Name,
			Root: p.Expect.Root, GitHash: identity.GitHash}
	} else if err = verifyInstance(p, identity); err != nil {
		transport.Close()
		return p, nil, err
	}
	return p, transport, nil
}

func (m model) instanceStateScope() string {
	if m.instanceState != "" {
		return m.instanceState
	}
	return m.natsURL
}
func (m model) initialWorkspace() string {
	if m.remoteInstance {
		return m.launchDir
	}
	return initialCwd()
}
