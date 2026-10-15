package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type instanceSwitchMsg struct {
	name  string
	claim *instanceClaim
	err   error
}

func localNiffler(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	// A switch owns the UI until it answers: a second one would race the same
	// claim, and a quit with a reservation nobody handed over.
	if m.instanceSwitchPending || m.switchInstance != "" {
		return m, nil
	}
	path, err := instanceRegistryPath()
	if err != nil {
		m.addBlock(blockError, err.Error())
		m.syncViewport(true)
		return m, nil
	}
	registry, err := loadInstanceRegistry(path)
	if err != nil {
		m.addBlock(blockError, err.Error())
		m.syncViewport(true)
		return m, nil
	}
	name := strings.TrimSpace(argument)
	if name == "" {
		lines := []string{"Instances (use /niffler NAME or /niffler next):"}
		for _, n := range registry.Order {
			p := registry.Instances[n]
			marker := "  "
			if n == m.instanceName {
				marker = "* "
			}
			lines = append(lines, fmt.Sprintf("%s%s — %s", marker, n, p.Expect.Name))
		}
		m.addBlock(blockMeta, strings.Join(lines, "\n"))
		m.syncViewport(true)
		return m, nil
	}
	if name == "next" {
		order := []string{}
		for _, candidate := range registry.Order {
			p := registry.Instances[candidate]
			if p.URL != "" || p.SSH != "" || p.Multipass != "" {
				order = append(order, candidate)
			}
		}
		name = nextInstanceName(order, m.instanceName)
		if name == "" {
			return m, nil
		}
	}
	if name == m.instanceName {
		return m, nil
	}
	p, ok := registry.Instances[name]
	if !ok {
		m.addBlock(blockError, "Unknown instance: "+name)
		m.syncViewport(true)
		return m, nil
	}
	if name == "local" && p.URL == "" && p.SSH == "" && p.Multipass == "" {
		m.addBlock(blockError, "Return to local by quitting and launching without --instance; local autostart is not part of remote switching.")
		m.syncViewport(true)
		return m, nil
	}
	// Secure the target before main is allowed to release the current UI.
	m.instanceSwitchPending = true
	return m, func() tea.Msg {
		claim, err := reserveInstance(m.ctx, p)
		return instanceSwitchMsg{name: name, claim: claim, err: err}
	}
}
