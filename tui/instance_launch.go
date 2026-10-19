package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Consume fleet options before the legacy flag parser without changing session
// flag precedence. The installed wrapper must pass fleet launches through before
// its local-harness discovery (remote selection must never boot a local core).
type instanceOptions struct{ Name, Import, Probe string }

func parseInstanceOptions(args []string) (instanceOptions, []string, error) {
	var opts instanceOptions
	rest := []string{args[0]}
	for i := 1; i < len(args); i++ {
		key, value, eq := strings.Cut(args[i], "=")
		var field *string
		switch key {
		case "--instance":
			field = &opts.Name
		case "--instance-import":
			field = &opts.Import
		case "--instance-probe":
			field = &opts.Probe
		default:
			rest = append(rest, args[i])
			continue
		}
		if !eq {
			i++
			if i >= len(args) {
				return opts, nil, fmt.Errorf("%s requires a value", key)
			}
			value = args[i]
		}
		if value == "" || *field != "" {
			return opts, nil, fmt.Errorf("invalid or duplicate %s", key)
		}
		*field = value
	}
	if opts.Import != "" && opts.Name == "" {
		return opts, nil, fmt.Errorf("--instance-import requires --instance NAME")
	}
	return opts, rest, nil
}

func selectInstance(ctx context.Context, name string) (instanceProfile, *instanceTransport, error) {
	path, err := instanceRegistryPath()
	if err != nil {
		return instanceProfile{}, nil, err
	}
	registry, err := loadInstanceRegistry(path)
	if err != nil {
		return instanceProfile{}, nil, err
	}
	p, ok := registry.Instances[name]
	if !ok {
		return p, nil, fmt.Errorf("unknown instance %q", name)
	}
	if name == "local" && p.URL == "" && p.SSH == "" && p.Multipass == "" && p.Incus == "" {
		return p, nil, fmt.Errorf("implicit local profile: launch without --instance")
	}
	transport, _, err := openInstanceTransport(ctx, p)
	return p, transport, err
}

func importInstanceReceipt(ctx context.Context, name, path string) error {
	if !instanceNamePattern.MatchString(name) || name == "local" {
		return fmt.Errorf("invalid remote instance name")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("receipt must be a private regular file, at most 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Parse/validate without granting trust; a live probe below is mandatory.
	var receipt struct {
		Expect instanceExpectation `json:"expect"`
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	p, err := profileFromReceipt(data, harnessIdentity{InstanceID: receipt.Expect.ID})
	if err != nil {
		return err
	}
	registryPath, err := instanceRegistryPath()
	if err != nil {
		return err
	}
	registry, err := loadInstanceRegistry(registryPath)
	if err != nil {
		return err
	}
	if old, ok := registry.Instances[name]; ok && old.Expect.ID != p.Expect.ID {
		return fmt.Errorf("name already pins another identity; refusing replacement")
	}
	transport, id, err := openInstanceTransport(ctx, p)
	if err != nil {
		return err
	}
	defer transport.Close()
	if _, err = profileFromReceipt(data, id); err != nil {
		return err
	}
	if _, ok := registry.Instances[name]; !ok {
		registry.Order = append(registry.Order, name)
	}
	registry.Instances[name] = p
	return saveInstanceRegistry(registryPath, registry)
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
