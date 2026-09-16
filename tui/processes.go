package main

// Background processes control plane (/processes + the status-line badge).
// The processes component owns detached children (servers, watchers —
// started via bash's run_in_background or the model's process_start); this
// is the human's window onto them: see what runs, peek output, kill a
// runaway. Read-only calls and kill go straight to the component over the
// bus — the TUI is the operator's hands, the same reasoning as /lsp's
// registry writes; the two-stage confirm in the panel is the operator's
// approval. Peeking uses process_poll {tail: "1"}, which re-reads the
// bounded raw tail WITHOUT advancing the component's drain cursor — the
// human looking never steals output the model has not seen yet.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	sdk "niffler.dev/sdk"
)

// processSummary is one entry of the processes component's process_list.
// Status strings are the component's contract: "running", "exited(code N)",
// "killed(signal N)".
type processSummary struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Command  string `json:"command"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	// StartedAt is the spawn time (epoch seconds); older components omit
	// it and the badge/cycle ages degrade to the plain count/status.
	StartedAt float64 `json:"started_at"`
}

func (p processSummary) running() bool { return p.Status == "running" }

// processesListMsg carries the registry snapshot for the badge and the
// /processes panel.
type processesListMsg struct {
	Processes []processSummary
	Err       error
}

func loadProcesses(comp *sdk.Component) ([]processSummary, error) {
	var response struct {
		Processes []processSummary `json:"processes"`
	}
	err := requestInto(comp, "processes", "process_list", map[string]any{}, &response)
	return response.Processes, err
}

func processesListCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		processes, err := loadProcesses(comp)
		return processesListMsg{Processes: processes, Err: err}
	}
}

// processesPeekMsg carries a raw output tail for the panel's peek view.
type processesPeekMsg struct {
	ID   string
	Text string
	Err  error
}

func processesPeekCmd(comp *sdk.Component, id string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			Text string `json:"text"`
		}
		err := requestInto(comp, "processes", "process_poll",
			map[string]any{"id": id, "tail": "1"}, &response)
		if err != nil {
			err = fmt.Errorf("processes.process_poll: %w", err)
		}
		return processesPeekMsg{ID: id, Text: response.Text, Err: err}
	}
}

// processesActionMsg reports a completed kill; the transcript shows the
// outcome and the panel reloads.
type processesActionMsg struct {
	ID     string
	Status string // terminal status reported by process_kill
	Err    error
}

func processesKillCmd(comp *sdk.Component, id string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			Status string `json:"status"`
		}
		err := requestInto(comp, "processes", "process_kill",
			map[string]any{"id": id}, &response)
		if err != nil {
			err = fmt.Errorf("processes.process_kill: %w", err)
		}
		return processesActionMsg{ID: id, Status: response.Status, Err: err}
	}
}

// bgTickMsg drives the badge's slow refresh while any process is running —
// one live timer per purpose (the armSpinner lesson): armed when a snapshot
// shows running processes, disarmed when the count reaches zero.
type bgTickMsg struct{}

const bgTickInterval = 10 * time.Second

func bgTickCmd() tea.Cmd {
	return tea.Tick(bgTickInterval, func(time.Time) tea.Msg { return bgTickMsg{} })
}

// runningCount counts entries still running.
func runningCount(processes []processSummary) int {
	count := 0
	for _, p := range processes {
		if p.running() {
			count++
		}
	}
	return count
}

// processesBadgeText is the status-line segment: "bg 2 (7m)" while anything
// runs, empty otherwise (most conversations never start one). The age is
// the longest-running entry's; an unknown timestamp (older component)
// degrades to the plain count.
func processesBadgeText(loc Locale, processes []processSummary, now ...float64) string {
	count := runningCount(processes)
	if count == 0 {
		return ""
	}
	oldest := float64(0)
	for _, p := range processes {
		if p.running() && p.StartedAt > oldest {
			oldest = p.StartedAt
		}
	}
	if oldest > 0 && len(now) > 0 {
		return t(loc, "processes.badgeAge", strconv.Itoa(count), humanAge(now[0]-oldest))
	}
	return t(loc, "processes.badge", strconv.Itoa(count))
}

// sortProcesses puts running entries first, then by numeric id (p2 < p10).
func sortProcesses(processes []processSummary) {
	numeric := func(p processSummary) int {
		n, err := strconv.Atoi(strings.TrimPrefix(p.ID, "p"))
		if err != nil {
			return -1
		}
		return n
	}
	sort.SliceStable(processes, func(i, j int) bool {
		ri, rj := processes[i].running(), processes[j].running()
		if ri != rj {
			return ri
		}
		return numeric(processes[i]) < numeric(processes[j])
	})
}

// processSelectorItems builds the /processes list: running first, each with
// id/label/status/command. A confirm-armed entry prefixes the kill prompt
// (two-stage, same shape as /lsp's two-stage remove). An empty registry
// shows a note instead of bubbles' bare "no items".
func processSelectorItems(loc Locale, processes []processSummary, confirmKill string) []list.Item {
	sortProcesses(processes)
	items := make([]list.Item, 0, len(processes)+1)
	for _, p := range processes {
		title := p.ID + " " + p.Label
		description := p.Status + " · " + p.Command
		if p.ID == confirmKill {
			title = t(loc, "processes.confirmKillShort") + " " + title
		} else if p.running() {
			title = "● " + title
		}
		items = append(items, selectorItem{
			kind: selectorProcess,
			id:   p.ID, title: title, description: description,
			payload: p,
		})
	}
	if len(items) == 0 {
		items = append(items, selectorItem{
			kind: selectorProcessNote,
			id:   "__none__", title: t(loc, "processes.noteTitle"),
			description: t(loc, "processes.noteDesc"),
		})
	}
	return items
}

// peekTitle is the peek view's header line.
func peekTitle(loc Locale, p processSummary) string {
	return t(loc, "processes.peekTitle", p.ID, p.Label, p.Status)
}

// peekView renders the peek mode: header, the clamped raw tail, footer
// hint. Pure so the tail clamping is testable.
func peekView(loc Locale, p processSummary, text string, width, height int, loading bool) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(peekTitle(loc, p)))
	b.WriteString("\n\n")
	body := text
	if loading {
		body = t(loc, "processes.peekLoading")
	} else if strings.TrimSpace(body) == "" {
		body = t(loc, "processes.peekEmpty")
	} else {
		body = clampTail(body, max(3, height-6))
	}
	b.WriteString(body)
	b.WriteString("\n\n" + t(loc, "processes.peekHelp"))
	return b.String()
}

// clampTail keeps the last maxLines lines of a raw output tail. The
// component already bounds the tail at ~64KB; this keeps the control area
// from scrolling a big block out of view.
func clampTail(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
