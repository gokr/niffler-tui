package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testFleetActivity(seq int) fleetActivity {
	return fleetActivity{ID: "same", SessionID: "same", TurnID: "turn-" + string(rune('0'+seq)), Seq: seq, Outcome: "done", At: float64(seq)}
}
func TestActivityUnreadIsInstanceLocalAndReplaySafe(t *testing.T) {
	l := newActivityLedger()
	a := testFleetActivity(1)
	if !l.merge("instance-a", a, false) || !l.merge("instance-b", a, false) {
		t.Fatal("first completion not observed")
	}
	if len(l.unread("instance-a")) != 1 || len(l.unread("instance-b")) != 1 {
		t.Fatal("completion not unread")
	}
	l.markRead("instance-a", "same")
	if len(l.unread("instance-a")) != 0 || len(l.unread("instance-b")) != 1 {
		t.Fatal("reading one instance cleared another")
	}
	if l.merge("instance-a", a, false) || len(l.unread("instance-a")) != 0 {
		t.Fatal("replay made old completion unread")
	}
	newer := testFleetActivity(2)
	l.merge("instance-a", newer, false)
	l.merge("instance-a", a, true)
	if len(l.unread("instance-a")) != 1 {
		t.Fatal("stale visible snapshot acknowledged newer completion")
	}
	l.merge("instance-a", newer, true)
	if len(l.unread("instance-a")) != 0 {
		t.Fatal("viewing completion did not clear badge")
	}
	if l.Latest["instance-b"]["same"].Seq != 1 {
		t.Fatal("instance isolation lost")
	}
}
func TestActivityLedgerRetainsUnreadAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "activity.json")
	l := newActivityLedger()
	l.merge("stable-instance-id", testFleetActivity(1), false)
	if err := l.save(path); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state not private")
	}
	loaded, err := loadActivityLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.unread("stable-instance-id")) != 1 {
		t.Fatal("unread update lost on restart")
	}
	loaded.markRead("stable-instance-id", "same")
	if err = loaded.save(path); err != nil {
		t.Fatal(err)
	}
	again, err := loadActivityLedger(path)
	if err != nil || len(again.unread("stable-instance-id")) != 0 {
		t.Fatalf("read marker lost: %v", err)
	}
	if loaded.merge("stable-instance-id", fleetActivity{}, false) {
		t.Fatal("invalid activity accepted")
	}
}
func TestActivityLedgerRejectsSymlinksAndMalformedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	target := filepath.Join(dir, "keep")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip(err)
	}
	if _, err := loadActivityLedger(path); err == nil {
		t.Fatal("symlink read accepted")
	}
	if err := newActivityLedger().save(path); err == nil {
		t.Fatal("symlink replaced")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "untouched" {
		t.Fatal("target touched")
	}
	os.Remove(path)
	for _, data := range []string{"{", `{"version":2}`, `{"version":1,"latest":{"i":{"x":{"id":"wrong"}}}}`} {
		os.WriteFile(path, []byte(data), 0600)
		if _, err := loadActivityLedger(path); err == nil {
			t.Fatalf("accepted malformed %q", data)
		}
	}
}
