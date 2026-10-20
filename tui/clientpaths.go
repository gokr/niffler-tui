package main

// Client-local files share one cross-platform home. XDG paths are migration
// inputs only; harness/store configuration and skill discovery are unchanged.
import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func clientPath(name string, config bool) (string, error) {
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
	var oldDir string
	if config {
		oldDir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
		oldDir = filepath.Join(oldDir, "niffler")
	} else {
		oldDir = strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
		if oldDir == "" {
			oldDir = filepath.Join(home, ".local", "state")
		}
		oldDir = filepath.Join(oldDir, "niffler-tui")
	}
	if err := migrateClientFile(filepath.Join(oldDir, name), path, config); err != nil {
		return "", fmt.Errorf("migrate client file %s: %w", name, err)
	}
	return path, nil
}

func statePath(name string) string {
	path, err := clientPath(name, false)
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
