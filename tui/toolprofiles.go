package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type toolRef struct {
	Component string `json:"component"`
	Name      string `json:"name"`
}
type exposureDoc struct {
	Direct     []toolRef `json:"direct"`
	Discovered []toolRef `json:"discovered"`
}
type visibilityTool struct {
	Name   string `json:"name"`
	Schema struct {
		Harness struct {
			Hidden bool `json:"hidden"`
		} `json:"x-harness"`
	} `json:"schema"`
}
type visibilityComponent struct {
	Name    string           `json:"name"`
	Running bool             `json:"running"`
	Tools   []visibilityTool `json:"tools"`
}

func renderComponents(comps []visibilityComponent, exposure *exposureDoc, filter string) (string, error) {
	if filter == "" {
		filter = "all"
	}
	if filter != "all" && filter != "direct" && filter != "discovered" && filter != "undiscovered" {
		return "", fmt.Errorf("expected all, direct, discovered or undiscovered")
	}
	direct, seen := map[string]bool{}, map[string]bool{}
	key := func(c, n string) string { return c + "\x00" + n }
	if exposure != nil {
		for _, t := range exposure.Direct {
			direct[key(t.Component, t.Name)] = true
		}
		for _, t := range exposure.Discovered {
			seen[key(t.Component, t.Name)] = true
		}
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].Name < comps[j].Name })
	var lines []string
	if exposure == nil {
		lines = append(lines, "No conversation tool snapshot yet; exposure unknown.")
	}
	for _, c := range comps {
		if !c.Running {
			continue
		}
		sort.Slice(c.Tools, func(i, j int) bool { return c.Tools[i].Name < c.Tools[j].Name })
		var tools []string
		for _, t := range c.Tools {
			state := "undiscovered"
			switch {
			case t.Schema.Harness.Hidden:
				state = "hidden"
			case exposure == nil:
				state = "unknown"
			case direct[key(c.Name, t.Name)]:
				state = "direct"
			case seen[key(c.Name, t.Name)]:
				state = "discovered"
			}
			if filter == "all" || filter == state {
				tools = append(tools, fmt.Sprintf("  %s [%s]", t.Name, state))
			}
		}
		if filter != "all" && len(tools) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s (%d/%d tools)", c.Name, len(tools), len(c.Tools)))
		lines = append(lines, tools...)
	}
	return strings.Join(lines, "\n"), nil
}

func (m model) toolVisibilityCmd(name, arg string) tea.Cmd {
	return func() tea.Msg {
		msg := slashResultMsg{Name: name, Session: m.session}
		var result any
		switch name {
		case "profile":
			if arg != "" {
				selected := arg
				if arg == "default" {
					selected = ""
				} else {
					var preview json.RawMessage
					msg.Err = requestInto(m.comp, "core", "profile", map[string]any{"op": "get", "name": arg}, &preview)
					if msg.Err != nil {
						return msg
					}
				}
				msg.Profile = &selected
				result = map[string]any{"text": "Profile for new conversations: " + arg}
			} else {
				var listing json.RawMessage
				msg.Err = requestInto(m.comp, "core", "profile", map[string]any{"op": "list"}, &listing)
				result = map[string]any{"current": m.toolProfile, "available": listing}
			}
		case "discover":
			if arg == "" {
				msg.Err = fmt.Errorf("usage: /discover COMPONENT or /discover tool=NAME")
				return msg
			}
			discovery := map[string]any{"component": arg}
			if strings.HasPrefix(arg, "tool=") {
				discovery = map[string]any{"tools": []string{strings.TrimPrefix(arg, "tool=")}}
			}
			var found json.RawMessage
			msg.Err = requestInto(m.comp, "core", "session", map[string]any{"sessionId": m.session, "profile": m.toolProfile, "discovery": discovery}, &found)
			result = found
		case "components":
			var status struct {
				Components []visibilityComponent `json:"components"`
			}
			msg.Err = requestInto(m.comp, "core", "status", map[string]any{}, &status)
			if msg.Err != nil {
				return msg
			}
			var stored struct {
				Value *exposureDoc `json:"value"`
			}
			msg.Err = requestInto(m.comp, "store", "get", map[string]any{"kind": "session", "id": m.session + ":tools"}, &stored)
			if msg.Err != nil {
				return msg
			}
			var text string
			text, msg.Err = renderComponents(status.Components, stored.Value, arg)
			result = map[string]any{"text": text}
		}
		if msg.Err == nil {
			msg.Result, msg.Err = json.Marshal(result)
		}
		return msg
	}
}
