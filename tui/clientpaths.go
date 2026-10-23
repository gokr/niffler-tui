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
	// Migration input: the old state directory only. A config-dir source is
	// no longer consulted (the 0.8 consolidation moved every preference into
	// config.json in the real home), so a stale file there cannot be mistaken
	// for current state.
	oldDir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if oldDir == "" {
		oldDir = filepath.Join(home, ".local", "state")
	}
	oldDir = filepath.Join(oldDir, "niffler-tui")
	// Migrated files were private client state or preferences; both were
	// 0600-worthy, so every migration target keeps that mode.
	if err := migrateClientFile(filepath.Join(oldDir, name), path, true); err != nil {
		return "", fmt.Errorf("migrate client file %s: %w", name, err)
	}
	return path, nil
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
