package main

// One client preferences file: ~/.niffler/config.json.
//
// Theme, locale and user aliases are the same kind of fact — how THIS client
// is set up — so they share one file instead of three single-purpose files
// (theme, locale, aliases). Reading is total and forgiving (a missing, corrupt
// or half-written file yields defaults; a preferences file must never block the
// UI), and writing is read-modify-write so changing the theme cannot lose your
// aliases.
//
// Aliases are kept as raw JSON so the on-disk entry form survives a
// read/write cycle losslessly: a bare string is a prompt alias, an object
// carries {"call": …} or {"prompt": …} (decodeAlias owns that contract).
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const clientConfigName = "config.json"

// clientConfig is the on-disk shape. Fields are omitempty so a client that
// only ever set a theme does not write empty maps and aliases.
type clientConfig struct {
	Version int                        `json:"version"`
	Theme   string                     `json:"theme,omitempty"`
	Locale  string                     `json:"locale,omitempty"`
	Aliases map[string]json.RawMessage `json:"aliases,omitempty"`
}

func clientConfigPath() string {
	return statePath(clientConfigName)
}

// loadClientConfig reads the preferences file. Never returns an error: an
// unreadable file is "no preferences", which is the safe direction for
// something the UI consults before it can report anything.
func loadClientConfig() clientConfig {
	cfg := clientConfig{Version: 1}
	path := clientConfigPath()
	if path == "" {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var raw clientConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return cfg
	}
	if raw.Version != 0 && raw.Version != 1 {
		// A future version is not ours to interpret; keep the defaults rather
		// than guessing at a shape that may mean something else.
		return cfg
	}
	cfg.Theme = raw.Theme
	cfg.Locale = raw.Locale
	cfg.Aliases = raw.Aliases
	return cfg
}

// updateClientConfig applies one change and writes the whole file atomically.
// Read-modify-write is the point: three features share the file, and a
// targeted write is what keeps one from clobbering another.
func updateClientConfig(mutate func(*clientConfig)) error {
	path := clientConfigPath()
	if path == "" {
		return fmt.Errorf("no state directory")
	}
	cfg := loadClientConfig()
	cfg.Version = 1
	mutate(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return err
	}
	staged := file.Name()
	defer os.Remove(staged)
	if err := file.Chmod(0o600); err == nil {
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
		return fmt.Errorf("refusing non-regular %s", clientConfigName)
	}
	return os.Rename(staged, path)
}

// migrateLegacyPreferences folds the three single-purpose files into
// config.json ONCE, then removes them. Each is read the way its own loader
// read it (theme/locale as a trimmed single line, aliases as a JSON map), and
// an existing config.json value always wins — a user who has already set a
// preference must not have it overwritten by a stale legacy file.
//
// Idempotent by construction: after the first run the legacy files are gone.
func migrateLegacyPreferences() {
	if clientConfigPath() == "" {
		return
	}
	themeVal := readLegacyLine("theme")
	localeVal := readLegacyLine("locale")
	aliasVal := readLegacyAliases()

	if themeVal == "" && localeVal == "" && aliasVal == nil {
		return
	}
	var migrated []string
	err := updateClientConfig(func(cfg *clientConfig) {
		if cfg.Theme == "" && themeVal != "" && validTheme(themeVal) {
			cfg.Theme = themeVal
			migrated = append(migrated, "theme")
		}
		if cfg.Locale == "" && localeVal != "" {
			if _, ok := validLocale(localeVal); ok {
				cfg.Locale = localeVal
				migrated = append(migrated, "locale")
			}
		}
		if len(cfg.Aliases) == 0 && len(aliasVal) > 0 {
			cfg.Aliases = aliasVal
			migrated = append(migrated, "aliases")
		}
	})
	if err != nil {
		// Leave the legacy files alone: the next run retries rather than
		// dropping preferences the user still has.
		return
	}
	// Only remove a legacy file whose value actually landed in the config.
	for _, name := range migrated {
		_ = os.Remove(statePath(name))
	}
}

func readLegacyLine(name string) string {
	path := statePath(name)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readLegacyAliases() map[string]json.RawMessage {
	path := statePath("aliases")
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return raw
}
