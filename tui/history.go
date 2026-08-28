// Sent-message history: in-memory navigation and reverse search, plus a
// per-session persistent JSONL store (one sent message per line). All
// persistence is best effort; errors are swallowed.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxHistory = 200

var invalidSessionID = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func sanitizeSessionID(id string) string {
	return invalidSessionID.ReplaceAllString(id, "-")
}

// historyFilePath returns the per-session persistent history file
// (JSONL, one sent message per line). Empty when the user state directory
// is unavailable.
func historyFilePath(session string) string {
	// Go has no os.UserStateDir; resolve $XDG_STATE_HOME ourselves with the
	// conventional fallback.
	dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "niffler-tui", "history-"+sanitizeSessionID(session)+".jsonl")
}

// readHistoryLines reads a history file into trimmed non-empty lines. The
// dirty return reports that the file should be rewritten (blank lines were
// encountered). Errors are swallowed: history is best effort.
func readHistoryLines(path string) (lines []string, dirty bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			lines = append(lines, string(line))
		} else if readErr == nil {
			dirty = true
		}
		if readErr != nil {
			break
		}
	}
	return lines, dirty
}

// parseHistoryEntries decodes history lines into sent messages, dropping
// malformed entries and consecutive duplicates; dirty reports content that
// should be rewritten away.
func parseHistoryEntries(lines []string) (entries []string, dirty bool) {
	for _, line := range lines {
		var entry string
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry == "" {
			dirty = true
		} else if len(entries) > 0 && entries[len(entries)-1] == entry {
			dirty = true
		} else {
			entries = append(entries, entry)
		}
	}
	return entries, dirty
}

// loadHistory reads the persistent history file, keeping at most maxHistory
// entries. Malformed/empty lines and consecutive duplicates are dropped; a
// changed file is rewritten trimmed. Errors are swallowed: history is best
// effort.
func loadHistory(path string) []string {
	if path == "" {
		return nil
	}
	lines, dirty := readHistoryLines(path)
	entries, parsedDirty := parseHistoryEntries(lines)
	dirty = dirty || parsedDirty
	if len(entries) > maxHistory {
		entries = entries[len(entries)-maxHistory:]
		dirty = true
	}
	if dirty {
		writeHistory(path, entries)
	}
	return entries
}

// appendHistoryEntry appends one sent message to the persistent history
// file, creating it if needed, and trims the file when it has outgrown
// maxHistory so it stays small over long-running sessions. Errors are
// ignored.
func appendHistoryEntry(path, content string) {
	if path == "" || content == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	line, err := json.Marshal(content)
	if err == nil {
		_, _ = f.Write(append(line, '\n'))
	}
	_ = f.Chmod(0o600)
	_ = f.Close()
	trimHistoryFile(path)
}

// trimHistoryFile rewrites the file when it has more than maxHistory lines,
// keeping the most recent entries (decoded so lines are not re-encoded).
func trimHistoryFile(path string) {
	lines, _ := readHistoryLines(path)
	if len(lines) <= maxHistory {
		return
	}
	entries, _ := parseHistoryEntries(lines)
	if len(entries) > maxHistory {
		entries = entries[len(entries)-maxHistory:]
	}
	writeHistory(path, entries)
}

// writeHistory rewrites the history file with the given entries.
func writeHistory(path string, entries []string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Chmod(0o600)
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		_, _ = f.Write(append(line, '\n'))
	}
}

// addHistory records a sent message, skipping empty entries and consecutive
// duplicates, and caps the stored history. It reports whether an entry was
// added so persistent history stays in sync with the in-memory list.
func (m *model) addHistory(content string) bool {
	if content == "" {
		return false
	}
	if len(m.history) > 0 && m.history[len(m.history)-1] == content {
		return false
	}
	m.history = append(m.history, content)
	if len(m.history) > maxHistory {
		m.history = m.history[len(m.history)-maxHistory:]
	}
	return true
}

// histPrev walks back through sent messages, snapshotting the in-progress
// draft the first time so histNext can restore it.
func (m *model) histPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.histIdx == -1 {
		m.draft = m.input.Value()
		m.histIdx = len(m.history)
	}
	if m.histIdx > 0 {
		m.histIdx--
		m.input.SetValue(m.history[m.histIdx])
	}
}

// histNext walks forward through sent messages, returning to the draft after
// the newest entry.
func (m *model) histNext() {
	if m.histIdx == -1 {
		return
	}
	if m.histIdx < len(m.history)-1 {
		m.histIdx++
		m.input.SetValue(m.history[m.histIdx])
	} else {
		m.histIdx = -1
		m.input.SetValue(m.draft)
	}
}

// searchStart enters reverse history search. Subsequent key presses are
// handled by handleSearchKey until the search is accepted or cancelled.
func (m *model) searchStart() {
	m.searchActive = true
	m.searchQuery = ""
	m.searchIdx = len(m.history)
	m.draft = m.input.Value()
	m.searchFindPrev()
}

// searchFindPrev moves to the most recent history entry (older than the
// current match, or from the end when the query just changed) containing the
// query. Returns false when there is no (further) match.
func (m *model) searchFindPrev() bool {
	query := strings.ToLower(m.searchQuery)
	if query == "" {
		m.searchIdx = -1
		m.input.SetValue(m.draft)
		return false
	}
	for i := m.searchIdx - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(m.history[i]), query) {
			m.searchIdx = i
			m.input.SetValue(m.history[i])
			return true
		}
	}
	m.searchIdx = -1
	m.input.SetValue(m.draft)
	return false
}

func (m *model) searchAccept() {
	m.searchActive = false
	m.searchQuery = ""
	if m.searchIdx >= 0 {
		m.histIdx = m.searchIdx
	} else {
		m.histIdx = -1
	}
	m.searchIdx = -1
}

func (m *model) searchCancel() {
	m.searchActive = false
	m.searchQuery = ""
	m.searchIdx = -1
	m.histIdx = -1
	m.input.SetValue(m.draft)
}
