package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientPathsMigration(t *testing.T) {
	home, state, config := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", config)
	// On macOS UserConfigDir intentionally ignores XDG_CONFIG_HOME.
	legacyConfig, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name, dir string
		config    bool
	}{
		{"theme", filepath.Join(state, "niffler-tui"), false},
		{"locale", filepath.Join(state, "niffler-tui"), false},
		{"aliases", filepath.Join(state, "niffler-tui"), false},
		{"history-conv.jsonl", filepath.Join(state, "niffler-tui"), false},
		{"session", filepath.Join(state, "niffler-tui"), false},
		{"instances.json", filepath.Join(legacyConfig, "niffler"), true},
		{"activity.json", filepath.Join(legacyConfig, "niffler"), true},
	} {
		old := filepath.Join(item.dir, item.name)
		if err := os.MkdirAll(item.dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(old, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := clientPath(item.name, item.config)
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
		if _, err := clientPath(item.name, item.config); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(got)
		if string(data) != "original" {
			t.Fatal("overwrote destination")
		}
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
	old := filepath.Join(state, "niffler-tui", "handoff")
	os.MkdirAll(filepath.Dir(old), 0700)
	os.WriteFile(old, []byte(`{"ui":"ui-one","session":"conv-one","at":0}`), 0600)
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
