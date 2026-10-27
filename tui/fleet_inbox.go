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
	// A coding artifact, as the worker produced it: the change itself, not a
	// description of it. There is no media/size/digest here because a coding
	// artifact is a git observation — the commit lives in the worker's repo,
	// which is where reviewable code belongs.
	Artifact *fleetArtifact `json:"artifact"`
}

// inboxRequestArgs is the inbox read's payload. No `human` flag: the inbox is a
// human read by construction on the harness side.
func inboxRequestArgs(limit int, after string) map[string]any {
	args := map[string]any{"limit": limit}
	if after != "" {
		args["after"] = after
	}
	return args
}

// detailRequestArgs is the report read's payload. `human: true` is required —
// without it the harness demands a conversation owner and refuses, which is how
// /tasks <jobId> first failed against a real client.
func detailRequestArgs(jobID string) map[string]any {
	return map[string]any{"jobId": jobID, "human": true}
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
			args := inboxRequestArgs(limit, after)
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
		lines = append(lines, item.Artifact.render()...)
	}
	lines = append(lines, "", "Detail lives on the peer: fleet_task_result {jobId}. "+
		"A job whose instance is gone keeps its verdict here and loses its detail.")
	return strings.Join(lines, "\n")
}

// fleetArtifact mirrors sdk/niffler/codingartifact.nim's shape. Every field
// degrades honestly there (`observed: false` plus a reason), so rendering must
// never imply success from a missing one.
type fleetArtifact struct {
	Version        int    `json:"version"`
	Observed       bool   `json:"observed"`
	Reason         string `json:"reason"`
	ProducedCommit bool   `json:"producedCommit"`
	HeadMoved      bool   `json:"headMoved"`
	OnBase         bool   `json:"onBase"`
	Dirty          bool   `json:"dirty"`
	Untracked      int    `json:"untracked"`
	Base           *struct {
		Commit string `json:"commit"`
		Short  string `json:"short"`
	} `json:"base"`
	Head *struct {
		Commit  string `json:"commit"`
		Short   string `json:"short"`
		Subject string `json:"subject"`
		Branch  string `json:"branch"`
		URL     string `json:"url"`
	} `json:"head"`
	Diffstat string `json:"diffstat"`
	Summary  string `json:"summary"`
}

// render describes what changed, in the order a reviewer reads it: what it
// built on, what it produced, whether the tree was left messy, and where to
// look. It never says the change is good — that stays the reviewer's call.
func (a *fleetArtifact) render() []string {
	if a == nil {
		return nil
	}
	if !a.Observed {
		reason := a.Reason
		if reason == "" {
			reason = "workspace was not observable"
		}
		return []string{"    artifact: " + oneLine(reason) + " (nothing to review)"}
	}
	var out []string
	if a.ProducedCommit {
		head := "?"
		if a.Head != nil && a.Head.Short != "" {
			head = a.Head.Short
		}
		base := "?"
		if a.Base != nil && a.Base.Short != "" {
			base = a.Base.Short
		}
		line := fmt.Sprintf("    changed: %s → %s", base, head)
		if a.Head != nil && a.Head.Subject != "" {
			line += " \"" + oneLine(a.Head.Subject) + "\""
		}
		out = append(out, line)
		if !a.OnBase {
			// The difference between "built on the work you approved" and
			// "replaced it" — the one thing a reviewer must not have to infer.
			out = append(out, "    WARNING: not on the base it started from (history replaced)")
		}
		if a.Head != nil && a.Head.URL != "" {
			out = append(out, "    "+a.Head.URL)
		}
	} else {
		out = append(out, "    changed: no commit")
	}
	if a.Dirty || a.Untracked > 0 {
		mess := fmt.Sprintf("    tree left dirty (%d untracked)", a.Untracked)
		if !a.Dirty {
			mess = fmt.Sprintf("    %d untracked file(s)", a.Untracked)
		}
		out = append(out, mess)
	}
	if a.Diffstat != "" {
		for _, line := range strings.Split(strings.TrimRight(a.Diffstat, "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				out = append(out, "    "+oneLine(line))
			}
		}
	}
	return out
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
	// `/tasks <jobId>` opens that job's report; a bare `/tasks` lists the inbox.
	// Both are local reads; requesting detail explicitly avoids dumping every
	// full report into the inbox.
	if job := strings.TrimSpace(argument); job != "" {
		return m, fleetTaskDetailCmd(m.comp, job)
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

// fleetTaskDetailMsg carries one job's remote report back to the UI loop.
type fleetTaskDetailMsg struct {
	JobID string
	Reply string
	Err   error
}

// fleetTaskDetailCmd reads one locally cached report via fleet_task_result.
// Newer harnesses page full prose; older ones return summaries or bounded rows.
func fleetTaskDetailCmd(comp *sdk.Component, jobID string) tea.Cmd {
	return fleetTaskDetailReadCmd(jobID, func(args map[string]any, reply any) error {
		if comp == nil {
			return fmt.Errorf("not connected")
		}
		return requestInto(comp, "fleet", "fleet_task_result", args, reply)
	})
}

// The request seam lets tests exercise pagination without a live bus.
func fleetTaskDetailReadCmd(jobID string, read func(map[string]any, any) error) tea.Cmd {
	return func() tea.Msg {
		var reply struct {
			JobID string `json:"jobId"`
			State string `json:"state"`
			Items []struct {
				ID      string `json:"id"`
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"items"`
			Bounded    bool    `json:"bounded"`
			HasMore    bool    `json:"hasMore"`
			Report     *string `json:"report"`
			Summary    string  `json:"summary"`
			Detail     string  `json:"detail"`
			NextOffset int     `json:"nextOffset"`
			Error      string  `json:"error"`
		}
		// `human: true` is the human-scope read: the owner check keeps
		// CONVERSATIONS apart, and a person at a keyboard is not a conversation.
		// Without it the inbox could list every job and open none of them.
		if err := read(detailRequestArgs(jobID), &reply); err != nil {
			return fleetTaskDetailMsg{JobID: jobID, Err: err}
		}
		if reply.Error != "" {
			return fleetTaskDetailMsg{JobID: jobID, Err: fmt.Errorf("%s", reply.Error)}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Fleet task %s", jobID)
		if reply.State != "" {
			fmt.Fprintf(&b, " (%s)", reply.State)
		}
		b.WriteString("\n")
		if reply.Report != nil {
			b.WriteString(*reply.Report)
			// Reports are local pages on newer harnesses. Preserve paragraphs
			// and links, rather than collapsing a detailed report to one line.
			offset := 0
			for reply.HasMore {
				if reply.NextOffset <= offset {
					return fleetTaskDetailMsg{JobID: jobID, Err: fmt.Errorf("invalid report cursor")}
				}
				offset = reply.NextOffset
				args := detailRequestArgs(jobID)
				args["offset"] = offset
				reply.Report, reply.HasMore = nil, false
				if err := read(args, &reply); err != nil {
					return fleetTaskDetailMsg{JobID: jobID, Err: err}
				}
				if reply.Error != "" || reply.Report == nil {
					return fleetTaskDetailMsg{JobID: jobID, Err: fmt.Errorf("report page unavailable: %s", reply.Error)}
				}
				b.WriteString(*reply.Report)
				if b.Len() > 1024*1024 {
					b.WriteString("\n… display capped at 1 MiB; retrieve further pages with fleet_task_result")
					break
				}
			}
			if reply.Detail != "" {
				b.WriteString("\n" + reply.Detail)
			}
			return fleetTaskDetailMsg{JobID: jobID, Reply: b.String()}
		}
		// Older harnesses may return only a cached summary or transcript rows.
		if reply.Summary != "" {
			b.WriteString(reply.Summary)
			if reply.Detail != "" {
				b.WriteString("\n" + reply.Detail)
			}
			return fleetTaskDetailMsg{JobID: jobID, Reply: b.String()}
		}
		if len(reply.Items) == 0 {
			b.WriteString("  the peer returned no transcript entries")
		}
		for _, item := range reply.Items {
			fmt.Fprintf(&b, "  %-9s %s\n", item.Role+":", oneLine(item.Content))
		}
		if reply.HasMore {
			// Honest about truncation: this is a bounded report, and the full
			// canonical transcript stays on the worker.
			b.WriteString("  … more entries on the peer (the transcript stays canonical there)\n")
		}
		return fleetTaskDetailMsg{JobID: jobID, Reply: strings.TrimRight(b.String(), "\n")}
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
