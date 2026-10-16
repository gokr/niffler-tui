package main

// Human read state is client-local. Nothing in this file acknowledges an agent
// notice, starts a turn, or changes a model's frozen prompt/tools.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

type fleetActivity struct {
	ID        string  `json:"id"`
	SessionID string  `json:"sessionId"`
	TurnID    string  `json:"turnId"`
	Seq       int     `json:"seq"`
	Outcome   string  `json:"outcome"`
	At        float64 `json:"at"`
}
type fleetActivityMsg struct{ Activity fleetActivity }
type fleetActivitySnapshotMsg struct {
	Items []fleetActivity
	Err   error
}

type activityLedger struct {
	Version int                                 `json:"version"`
	Latest  map[string]map[string]fleetActivity `json:"latest"`
	Seen    map[string]map[string]int           `json:"seen"`
}

func newActivityLedger() *activityLedger {
	return &activityLedger{Version: 1, Latest: map[string]map[string]fleetActivity{}, Seen: map[string]map[string]int{}}
}
func validFleetActivity(a fleetActivity) bool {
	if a.SessionID == "" || a.ID != a.SessionID || a.TurnID == "" || a.Seq < 1 || a.At <= 0 {
		return false
	}
	switch a.Outcome {
	case "done", "failed", "cancelled", "needs-attention":
		return true
	}
	return false
}

// merge returns true for a newly observed completion, not for duplicate replay.
// Session IDs are never reused after deletion (WIRE activity snapshot contract).
func (l *activityLedger) merge(scope string, a fleetActivity, viewed bool) bool {
	if scope == "" || !validFleetActivity(a) {
		return false
	}
	if l.Latest == nil {
		l.Latest = map[string]map[string]fleetActivity{}
	}
	if l.Seen == nil {
		l.Seen = map[string]map[string]int{}
	}
	if l.Latest[scope] == nil {
		l.Latest[scope] = map[string]fleetActivity{}
	}
	previous, exists := l.Latest[scope][a.SessionID]
	if exists && a.Seq <= previous.Seq {
		// A stale snapshot must never acknowledge a newer, unread push.
		if viewed && a.Seq == previous.Seq && a.TurnID == previous.TurnID {
			l.markRead(scope, a.SessionID)
		}
		return false
	}
	l.Latest[scope][a.SessionID] = a
	if viewed {
		l.markRead(scope, a.SessionID)
	}
	return true
}
func (l *activityLedger) markRead(scope, session string) {
	if a, ok := l.Latest[scope][session]; ok {
		if l.Seen == nil {
			l.Seen = map[string]map[string]int{}
		}
		if l.Seen[scope] == nil {
			l.Seen[scope] = map[string]int{}
		}
		if a.Seq > l.Seen[scope][session] {
			l.Seen[scope][session] = a.Seq
		}
	}
}
func (l *activityLedger) unread(scope string) []fleetActivity {
	var result []fleetActivity
	for session, a := range l.Latest[scope] {
		if a.Seq > l.Seen[scope][session] {
			result = append(result, a)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].At == result[j].At {
			return result[i].SessionID < result[j].SessionID
		}
		return result[i].At > result[j].At
	})
	return result
}
func activityLedgerPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "niffler", "activity.json"), nil
}
func loadActivityLedger(path string) (*activityLedger, error) {
	l := newActivityLedger()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("activity state must be a private regular file, at most 8 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, l); err != nil {
		return nil, err
	}
	if l.Version != 1 {
		return nil, fmt.Errorf("unsupported activity state version")
	}
	if l.Latest == nil {
		l.Latest = map[string]map[string]fleetActivity{}
	}
	if l.Seen == nil {
		l.Seen = map[string]map[string]int{}
	}
	for scope, items := range l.Latest {
		if scope == "" {
			return nil, fmt.Errorf("empty activity scope")
		}
		for session, a := range items {
			if session != a.SessionID || !validFleetActivity(a) {
				return nil, fmt.Errorf("invalid saved activity")
			}
		}
	}
	for _, items := range l.Seen {
		for _, seq := range items {
			if seq < 0 {
				return nil, fmt.Errorf("invalid activity read sequence")
			}
		}
	}
	return l, nil
}
func (l *activityLedger) save(path string) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular activity state")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("activity state exceeds 8 MiB")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".activity-*")
	if err != nil {
		return err
	}
	staged := file.Name()
	defer os.Remove(staged)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Re-check after staging: don't replace a newly introduced symlink.
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular activity state")
	}
	return os.Rename(staged, path)
}

// Full latest-per-conversation snapshot only on connect/reconnect, never a
// timestamp watermark or a periodic task-status poll. Caller subscribes first.
func loadFleetActivityCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		var items []fleetActivity
		after := ""
		seen := map[string]bool{}
		for {
			var page struct {
				Items     []fleetActivity `json:"items"`
				HasMore   bool            `json:"hasMore"`
				NextAfter string          `json:"nextAfter"`
			}
			err := requestInto(comp, "core", "activity_list", map[string]any{"after": after, "limit": 100}, &page)
			if err != nil {
				return fleetActivitySnapshotMsg{Err: err}
			}
			for _, a := range page.Items {
				if !validFleetActivity(a) {
					return fleetActivitySnapshotMsg{Err: fmt.Errorf("invalid activity item")}
				}
				items = append(items, a)
			}
			if !page.HasMore {
				return fleetActivitySnapshotMsg{Items: items}
			}
			if page.NextAfter == "" || page.NextAfter <= after || seen[page.NextAfter] {
				return fleetActivitySnapshotMsg{Err: fmt.Errorf("activity page cursor did not advance")}
			}
			seen[page.NextAfter] = true
			after = page.NextAfter
		}
	}
}
