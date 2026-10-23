package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// There is exactly ONE client home: ~/.niffler. The old state directory is a
// migration input; a config-directory file is NOT (two candidate homes once
// let a stale ~/.config/niffler/instances.json look authoritative).
func TestClientPathsMigration(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", state)
	for _, item := range []struct {
		name, dir string
	}{
		{"theme", filepath.Join(state, "niffler-tui")},
		{"locale", filepath.Join(state, "niffler-tui")},
		{"aliases", filepath.Join(state, "niffler-tui")},
		{"history-conv.jsonl", filepath.Join(state, "niffler-tui")},
		{"session", filepath.Join(state, "niffler-tui")},
		{"activity.json", filepath.Join(state, "niffler-tui")},
	} {
		old := filepath.Join(item.dir, item.name)
		if err := os.MkdirAll(item.dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(old, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := clientPath(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(home, ".niffler", item.name) {
			t.Fatalf("path %s", got)
		}
		data, err := os.ReadFile(got)
		if err != nil || string(data) != "original" {
			t.Fatalf("migration: %q %v", data, err)
		}
		info, _ := os.Stat(got)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("permissions %v", info.Mode())
		}
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Fatalf("legacy source remains: %v", err)
		}
		// New file wins even when an older client creates another legacy file.
		os.WriteFile(old, []byte("stale"), 0600)
		if _, err := clientPath(item.name); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(got)
		if string(data) != "original" {
			t.Fatal("overwrote destination")
		}
	}
}

// A file in the old CONFIG directory is not state: the client must not read
// it, and must not migrate it into the real home either.
func TestClientIgnoresConfigDirState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	legacyConfig, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(legacyConfig, "niffler")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "instances.json")
	if err := os.WriteFile(stale, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := clientPath("instances.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("a config-dir file was migrated into the client home: %v", err)
	}
	if data, err := os.ReadFile(stale); err != nil || len(data) == 0 {
		t.Fatalf("the config-dir file was consumed: %v", err)
	}
}

func TestClientMigrationRefusesSymlinksAndPublicRegistry(t *testing.T) {
	dir := t.TempDir()
	old, dest := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	os.WriteFile(old, []byte("private"), 0644)
	if err := migrateClientFile(old, dest, true); err == nil {
		t.Fatal("accepted public registry")
	}
	os.Remove(old)
	os.Symlink(dest, old)
	if err := migrateClientFile(old, dest, false); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestLegacyHandoffConsumedWithoutResurrection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	// A handoff from a pre-0.10 install sits flat in the old state directory;
	// it is a MIGRATION input, adopted into sessions/ by migrateSessionFiles.
	old := filepath.Join(state, "niffler-tui", "handoff")
	os.MkdirAll(filepath.Dir(old), 0700)
	os.WriteFile(old, []byte(`{"ui":"ui-one","session":"conv-one","at":0}`), 0600)
	migrateSessionFiles()
	ui, session := consumeHandoff("", "", time.Now())
	if ui != "ui-one" || session != "conv-one" {
		t.Fatalf("handoff %q %q", ui, session)
	}
	ui, _ = consumeHandoff("", "", time.Now())
	if ui != "" {
		t.Fatal("handoff replayed")
	}
}

func TestClientMigrationFailurePreservesSource(t *testing.T) {
	dir := t.TempDir()
	old, blocker := filepath.Join(dir, "old"), filepath.Join(dir, "blocker")
	os.WriteFile(old, []byte("keep"), 0600)
	os.WriteFile(blocker, []byte("x"), 0600)
	if err := migrateClientFile(old, filepath.Join(blocker, "new"), false); err == nil {
		t.Fatal("expected error")
	}
	data, err := os.ReadFile(old)
	if err != nil || string(data) != "keep" {
		t.Fatal("lost legacy file")
	}
}
