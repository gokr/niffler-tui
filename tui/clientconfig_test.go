package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The three preferences share one file, and each update must leave the others
// alone: a /theme must not revert your aliases.
func TestClientConfigUpdatesAreTargeted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := loadClientConfig(); got.Theme != "" || len(got.Aliases) != 0 {
		t.Fatalf("fresh config = %#v", got)
	}
	if err := updateClientConfig(func(c *clientConfig) { c.Theme = "nord" }); err != nil {
		t.Fatal(err)
	}
	if err := updateClientConfig(func(c *clientConfig) { c.Locale = "zh" }); err != nil {
		t.Fatal(err)
	}
	if err := updateClientConfig(func(c *clientConfig) {
		c.Aliases = map[string]json.RawMessage{"commit": json.RawMessage(`"do it"`)}
	}); err != nil {
		t.Fatal(err)
	}
	got := loadClientConfig()
	if got.Theme != "nord" || got.Locale != "zh" || len(got.Aliases) != 1 {
		t.Fatalf("a later update clobbered an earlier one: %#v", got)
	}
}

// Reading preferences must never fail the UI: a corrupt or future-version file
// yields defaults, and nothing is overwritten in the process.
func TestClientConfigTolerance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := clientConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"garbage": "not json",
		"array":   `["nope"]`,
		"future":  `{"version":99,"theme":"nord"}`,
		"empty":   "",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got := loadClientConfig()
		if got.Theme != "" || len(got.Aliases) != 0 {
			t.Fatalf("%s: loadClientConfig = %#v, want defaults", name, got)
		}
	}
	// The file mode matters: these are private preferences.
	persistTheme("nord")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

// The one-time fold: legacy theme/locale/aliases land in config.json and are
// removed, an existing preference is never overwritten, and a second run is a
// no-op (idempotent by construction).
func TestLegacyPreferenceMigration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(os.Getenv("HOME"), ".niffler")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("theme", "nord\n")
	write("locale", "zh\n")
	write("aliases", `{"commit":"do it","pr":{"call":"cli call git '{}'"}}`)

	migrateLegacyPreferences()
	cfg := loadClientConfig()
	if cfg.Theme != "nord" || cfg.Locale != "zh" || len(cfg.Aliases) != 2 {
		t.Fatalf("migration lost a preference: %#v", cfg)
	}
	for _, name := range []string{"theme", "locale", "aliases"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy %s was not removed", name)
		}
	}

	// An existing config wins over a stale legacy file that reappears.
	write("theme", "sepia\n")
	persistTheme("light")
	migrateLegacyPreferences()
	if got := loadClientConfig().Theme; got != "light" {
		t.Fatalf("migration overwrote an existing choice with %q", got)
	}
	// The stale file survives, because its value never landed: the user's
	// current choice is not silently replaced, and nothing is half-removed.
	if _, err := os.Stat(filepath.Join(dir, "theme")); err != nil {
		t.Fatal("removed a legacy file whose value was not adopted")
	}
	// A second run remains a no-op for everything already migrated.
	migrateLegacyPreferences()
	if got := loadClientConfig().Theme; got != "light" {
		t.Fatalf("second migration changed the theme to %q", got)
	}
}
