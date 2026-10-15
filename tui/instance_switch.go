package main

import "strings"

// Re-exec only explicit remote targets. Drop session flags: session identifiers
// belong to the instance being left, not to the target's independent store.
func instanceSwitchArgs(args []string, name string) []string {
	out := []string{args[0]}
	for i := 1; i < len(args); i++ {
		key, _, eq := strings.Cut(args[i], "=")
		switch key {
		case "--instance", "-session", "--session":
			if !eq && i+1 < len(args) {
				i++
			}
		default:
			out = append(out, args[i])
		}
	}
	return append(out, "--instance", name)
}

func nextInstanceName(order []string, active string) string {
	if len(order) == 0 {
		return ""
	}
	for i, name := range order {
		if name == active {
			return order[(i+1)%len(order)]
		}
	}
	return order[0]
}
