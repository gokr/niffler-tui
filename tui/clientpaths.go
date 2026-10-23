package main

// Client-local files share ONE home: ~/.niffler. There is deliberately no
// fallback to an XDG config directory any more — two candidate homes meant a
// file could be read from a location nothing wrote to (a stale
// ~/.config/niffler/instances.json looked authoritative long after the client
// stopped using it). XDG paths are MIGRATION INPUTS only, tried once while the
// file is still absent from the real home.
import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func clientPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".niffler", name)
	if _, err := os.Lstat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	oldDir := legacyStateDir()
	// Migrated files were private client state or preferences; both were
	// 0600-worthy, so every migration target keeps that mode.
	if err := migrateClientFile(filepath.Join(oldDir, name), path, true); err != nil {
		return "", fmt.Errorf("migrate client file %s: %w", name, err)
	}
	return path, nil
}

// stateDir returns ~/.niffler/<sub>, creating it 0700. Client state that
// accumulates one file per conversation lives in a subdirectory rather than
// beside the preferences: ~/.niffler holds a handful of things a human might
// read, and dozens of history files turn it into noise.
// legacyStateDir is the pre-0.8 per-user state directory: a MIGRATION input
// only. A config-directory source is deliberately absent — two candidate homes
// once let a file nothing wrote keep looking authoritative.
func legacyStateDir() string {
	oldDir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if oldDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		oldDir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(oldDir, "niffler-tui")
}

// stateDir returns ~/.niffler/<sub>, creating it 0700. Client state that
// accumulates one file per conversation lives in a subdirectory rather than
// beside the preferences: ~/.niffler holds a handful of things a human might
// read, and dozens of per-conversation files turn it into noise.
func stateDir(sub string) string {
	path, err := clientPath(sub)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ""
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return ""
	}
	return path
}

func statePath(name string) string {
	path, err := clientPath(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ""
	}
	return path
}

func migrateClientFile(old, path string, private bool) error {
	info, err := os.Lstat(old)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("legacy source is not a regular file")
	}
	if private && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("legacy source must be private (0600)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	src, err := os.Open(old)
	if err != nil {
		return err
	}
	defer src.Close()
	file, err := os.CreateTemp(filepath.Dir(path), ".migrate-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = io.Copy(file, src); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	// Atomic publication with no overwrite, including concurrent first launches.
	if err = os.Link(file.Name(), path); err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	// Remove the old source so consumed handoffs/deleted aliases cannot reappear.
	return os.Remove(old)
}

// migrateSessionFiles moves pre-0.10 `session-*` / `handoff*` files from the
// state root into sessions/. Same rule as history: a name already present
// wins, and the root copy goes only after (or because) that is settled.
func migrateSessionFiles() {
	dir := stateDir("sessions")
	if dir == "" {
		return
	}
	// BOTH roots: a pre-0.10 install may still have its files flat in the real
	// home, or untouched in the legacy XDG directory (where an upgrade without
	// a launch in between leaves them).
	if root, err := clientPath("."); err == nil && root != "" {
		migrateSessionFilesFrom(root, dir)
	}
	if legacy := legacyStateDir(); legacy != "" {
		migrateSessionFilesFrom(legacy, dir)
	}
}

func migrateSessionFilesFrom(root, dir string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if !strings.HasPrefix(name, "session") && !strings.HasPrefix(name, "handoff") {
			continue
		}
		old := filepath.Join(root, name)
		target := filepath.Join(dir, name)
		if _, err := os.Lstat(target); err == nil {
			_ = os.Remove(old)
			continue
		}
		if err := os.Rename(old, target); err == nil {
			// Pre-0.10 session files were written 0644 in a 0755 dir.
			_ = os.Chmod(target, 0o600)
		}
	}
}
