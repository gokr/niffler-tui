package main

// The fleet inbox (docs/FLEET-INBOX.md in the harness repo): what the remote
// Nifflers this harness submitted work to have reported.
//
// Two guarantees shape this file, and both come from the harness side:
//
//   - The list is a LOCAL read (fleet_task_inbox over the fleetjob kind), so a
//     job whose instance is down, rebuilt or removed still shows a verdict.
//     Detail is a separate call that needs the peer to answer; this file never
//     pretends otherwise.
//   - Reading acknowledges nothing. Seen/unread is this client's state, keyed
//     by (peer, jobId, activation) exactly so a model cannot mark its own work
//     as read by a human, and so a second activation of the same job is new
//     news rather than a silent overwrite.
import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// fleetInboxItem mirrors fleet_task_inbox's row shape (components/fleet).
type fleetInboxItem struct {
	JobID     string  `json:"jobId"`
	Peer      string  `json:"peer"`
	State     string  `json:"state"`
	Mode      string  `json:"mode"`
	Objective string  `json:"objective"`
	CreatedAt float64 `json:"createdAt"`
	CheckedAt float64 `json:"checkedAt"`
	Notified  bool    `json:"notified"`
	Cwd       string  `json:"cwd"`
	Outcome   string  `json:"outcome"`
	Settled   *struct {
		Activation          int    `json:"activation"`
		Status              string `json:"status"`
		Summary             string `json:"summary"`
		TranscriptCommitted bool   `json:"transcriptCommitted"`
	} `json:"settled"`
	Artifact *struct {
		Name      string  `json:"name"`
		MediaType string  `json:"mediaType"`
		Size      float64 `json:"size"`
		Digest    string  `json:"digest"`
		Summary   string  `json:"summary"`
	} `json:"artifact"`
}

type fleetInboxMsg struct {
	Items []fleetInboxItem
	Err   error
}

// seenKey is the ledger key for one settlement. Activation is part of it: a
// re-run of the same job is a NEW event, and folding it into the old key would
// let a second activation hide behind a "read" verdict.
func (i fleetInboxItem) seenKey() string {
	activation := 0
	if i.Settled != nil {
		activation = i.Settled.Activation
	}
	return fmt.Sprintf("%s|%s|%d", i.Peer, i.JobID, activation)
}

// settledOnly: only a settled job is news. A queued or running job is a
// thing to look at when you choose, not a notification.
func (i fleetInboxItem) settledOnly() bool {
	return i.State == "settled" && i.Settled != nil
}

// fleetInboxCmd reads the inbox. It pages by the harness's stable job-id
// cursor, so a long history cannot hide a settlement behind a fuller page.
func fleetInboxCmd(comp *sdk.Component, limit int) tea.Cmd {
	return func() tea.Msg {
		if comp == nil {
			return fleetInboxMsg{Err: fmt.Errorf("not connected")}
		}
		var all []fleetInboxItem
		after := ""
		for page := 0; page < 100; page++ {
			var reply struct {
				Items     []fleetInboxItem `json:"items"`
				HasMore   bool             `json:"hasMore"`
				NextAfter string           `json:"nextAfter"`
			}
			args := map[string]any{"limit": limit}
			if after != "" {
				args["after"] = after
			}
			if err := requestInto(comp, "fleet", "fleet_task_inbox", args, &reply); err != nil {
				return fleetInboxMsg{Err: err}
			}
			all = append(all, reply.Items...)
			if !reply.HasMore || reply.NextAfter == "" || reply.NextAfter <= after {
				return fleetInboxMsg{Items: all}
			}
			after = reply.NextAfter
		}
		return fleetInboxMsg{Err: fmt.Errorf("inbox cursor did not converge")}
	}
}

// renderFleetInbox is the human-readable view: objective first (it is what the
// row is about), then the verdict, then where the detail lives. Deliberately
// plain text in a meta block, like /processes and /niffler: a job list is a
// glance, not a mode.
func renderFleetInbox(items []fleetInboxItem, seen map[string]bool) string {
	if len(items) == 0 {
		return "Fleet inbox: no tasks submitted to remote instances yet.\n" +
			"Delegate with fleet_task_submit (docs/FLEET-INBOX.md)."
	}
	sorted := append([]fleetInboxItem{}, items...)
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].CreatedAt == sorted[b].CreatedAt {
			return sorted[a].JobID < sorted[b].JobID
		}
		return sorted[a].CreatedAt > sorted[b].CreatedAt
	})
	var unread int
	for _, item := range sorted {
		if item.settledOnly() && !seen[item.seenKey()] {
			unread++
		}
	}
	var lines []string
	head := fmt.Sprintf("Fleet inbox: %d task(s)", len(sorted))
	if unread > 0 {
		head += fmt.Sprintf(", %d unread", unread)
	}
	lines = append(lines, head)
	for _, item := range sorted {
		marker := "  "
		if item.settledOnly() && !seen[item.seenKey()] {
			marker = "● "
		}
		state := item.State
		if item.Settled != nil && item.Settled.Status != "" {
			state = item.Settled.Status
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s — %s",
			marker, item.Peer, state, oneLine(item.Objective)))
		if item.Settled != nil && item.Settled.Summary != "" {
			lines = append(lines, "    "+oneLine(item.Settled.Summary))
		}
		if item.Artifact != nil {
			what := item.Artifact.Summary
			if what == "" {
				what = item.Artifact.Name
			}
			if what != "" {
				lines = append(lines, "    artifact: "+oneLine(what))
			}
		}
	}
	lines = append(lines, "", "Detail lives on the peer: fleet_task_result {jobId}. "+
		"A job whose instance is gone keeps its verdict here and loses its detail.")
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	const max = 100
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// localTasks renders the fleet inbox into the meta block.
//
// It marks what it shows as seen (that is what "reading" means for this
// client), but only AFTER rendering: a settle that arrives mid-render must not
// be silently swallowed as already-read.
func localTasks(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	return m, func() tea.Msg {
		msg := fleetInboxCmd(m.comp, 100)()
		batch, ok := msg.(fleetInboxMsg)
		if !ok {
			return fleetInboxMsg{Err: fmt.Errorf("inbox read returned no result")}
		}
		if batch.Err != nil {
			return fleetTasksRenderedMsg{Err: batch.Err}
		}
		return fleetTasksRenderedMsg{Items: batch.Items}
	}
}

// fleetTasksRenderedMsg carries a completed inbox read back to the UI loop,
// where the seen markers can be updated and the block appended.
type fleetTasksRenderedMsg struct {
	Items []fleetInboxItem
	Err   error
}

// fleetSettledMsg is one live settle event. The payload already carries a
// bounded verdict, so no store read is needed to notify.
type fleetSettledMsg struct{ Item fleetInboxItem }
