// Leaving the bus cleanly: core drops a ui registration and its conversation
// claims when the client asks, and otherwise only when the lease expires
// (20s). An exit whose release never reaches core leaves the conversation
// looking owned for that window — long enough for a relaunch to be refused it
// and open an empty conversation instead. The restart path carries its own
// identity over (handoff.go); every other exit releases the registration, so
// the request is retried a couple of times and a total failure is reported
// instead of swallowed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	sdk "niffler.dev/sdk"
)

const (
	releaseAttempts = 3
	releaseTimeout  = 700 * time.Millisecond
	releaseBackoff  = 150 * time.Millisecond
)

// requester is the slice of *sdk.Component the release needs, so the retry
// policy is testable without a bus.
type requester func(component, tool string, args any, timeout time.Duration) (json.RawMessage, error)

// releaseUI drops this client's registry entry and its conversation claims.
// Best effort, but bounded: about 2.5s of retries at worst, then one warning
// on stderr — the user should know the conversation keeps looking owned until
// the lease runs out, because a relaunch inside that window is refused it.
func releaseUI(comp *sdk.Component, uiID string) {
	if comp == nil || uiID == "" {
		return
	}
	if err := releaseUIWith(comp.Request, uiID, releaseAttempts); err != nil {
		fmt.Fprintln(os.Stderr, "niffler-tui: warning: could not release this client's "+
			"conversation claim ("+err.Error()+"); it expires with the lease in ~20s")
	}
}

// releaseUIWith retries the release: core answers in microseconds when it is
// healthy, so the extra attempts only matter for a busy or restarting bus. An
// answer of any kind ends the retries — the registry's release op is
// unconditional, so the only failure worth retrying is a request that never
// came back.
func releaseUIWith(req requester, uiID string, attempts int) error {
	if req == nil || uiID == "" || attempts < 1 {
		return nil
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(releaseBackoff)
		}
		if _, err = req("core", "ui",
			map[string]any{"op": "release", "ui": uiID}, releaseTimeout); err == nil {
			return nil
		}
	}
	return fmt.Errorf("release not acknowledged after %d attempts: %w", attempts, err)
}
