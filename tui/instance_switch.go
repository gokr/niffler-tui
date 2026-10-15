package main

import (
	"os"
	"strings"
	"syscall"
)

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

// instanceExec is syscall.Exec, indirected so the relaunch contract (args,
// environment, and that success is fatal to this process) is testable without
// replacing the test binary.
var instanceExec = syscall.Exec

// relaunchInstance replaces this process with the same binary pointed at
// another instance, carrying the reserved target claim in the environment
// (instance_claim.go). It returns only when the exec never happened, so the
// caller may still roll that reservation back; on success it does not return
// and the successor owns the claim.
func relaunchInstance(originalArgs []string, name string, handoff instanceClaimHandoff) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := instanceSwitchArgs(originalArgs, name)
	args[0] = executable
	return instanceExec(executable, args, instanceClaimEnvironment(os.Environ(), handoff))
}
