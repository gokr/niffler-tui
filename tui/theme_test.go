package main

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// restoreDefaultTheme re-applies the compiled-in theme after a test that
// switched the package-global styles, so state never leaks between tests.
func restoreDefaultTheme(t *testing.T) {
	t.Cleanup(func() { applyTheme(ThemeDefault) })
}

// TestThemeRegistryCoverage pins the theme surface: at least nine
// non-default themes (the feature exists to offer real choice), names unique
// across registry and picker order, and every theme fully specified with a
// known glamour markdown style.
func TestThemeRegistryCoverage(t *testing.T) {
	if len(themeNames) < 10 {
		t.Fatalf("themeNames has %d entries, want >= 10 (default + 9 alternatives)", len(themeNames))
	}
	seen := map[string]bool{}
	for _, name := range themeNames {
		if seen[name] {
			t.Fatalf("duplicate theme name %q in themeNames", name)
		}
		seen[name] = true
		th, ok := themeRegistry[name]
		if !ok {
			t.Fatalf("themeNames lists %q but the registry has no entry", name)
		}
		for _, role := range []struct{ label, color string }{
			{"header", th.header}, {"inputBorder", th.inputBorder},
			{"user", th.user}, {"assistant", th.assistant},
			{"thinking", th.thinking}, {"tool", th.tool},
			{"meta", th.meta}, {"error", th.error},
			{"barOK", th.barOK}, {"barCrit", th.barCrit},
		} {
			if role.color == "" {
				t.Errorf("theme %q leaves role %s empty", name, role.label)
			}
		}
		switch th.glamour {
		case "", "light", "dark", "dracula", "tokyo-night":
			// "" means GLAMOUR_STYLE keeps controlling markdown (default).
		default:
			t.Errorf("theme %q names unknown glamour style %q", name, th.glamour)
		}
		if themeDescription(name) == "" {
			t.Errorf("theme %q has no picker description", name)
		}
	}
	if _, ok := themeRegistry[ThemeDefault]; !ok {
		t.Fatal("registry is missing the compiled-in default theme")
	}
}

// TestDefaultThemeMatchesCompiledInStyles checks the registry's default
// palette against the compiled-in style var block: applying it must not
// change what the styles render.
func TestDefaultThemeMatchesCompiledInStyles(t *testing.T) {
	restoreDefaultTheme(t)
	render := func() string {
		return headerStyle.Render("h") + userStyle.Render("u") + toolStyle.Render("t") +
			errorStyle.Render("e") + activeSlashStyle.Render("s") + approvalBoxStyle.Render("a")
	}
	before := render()
	if !applyTheme(ThemeDefault) {
		t.Fatal("applyTheme(default) failed")
	}
	if got := render(); got != before {
		t.Fatalf("default theme changed the compiled-in styles:\n%q != %q", got, before)
	}
}

// TestApplyThemeSwitchesStyles verifies a theme switch actually recolors
// the chrome (the light theme must not render like the dark-oriented
// default) and that unknown names are rejected without touching state.
func TestApplyThemeSwitchesStyles(t *testing.T) {
	restoreDefaultTheme(t)
	before := headerStyle.Render("Niffler") + userStyle.Render("you")
	if applyTheme("definitely-not-a-theme") {
		t.Fatal("unknown theme accepted")
	}
	if got := headerStyle.Render("Niffler") + userStyle.Render("you"); got != before {
		t.Fatal("failed applyTheme mutated the styles")
	}
	if !applyTheme("light") {
		t.Fatal("applyTheme(light) failed")
	}
	after := headerStyle.Render("Niffler") + userStyle.Render("you")
	if after == before {
		t.Fatal("light theme rendered identically to the default theme")
	}
	if currentTheme.glamour != "light" {
		t.Fatalf("light theme glamour = %q, want light", currentTheme.glamour)
	}
}

// TestThemeDetectionAndPersistence covers the /theme persistence contract:
// file first, then NIF_TUI_THEME, then default; corrupt names fall through.
func TestThemeDetectionAndPersistence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	os.Unsetenv("NIF_TUI_THEME")

	if got := detectTheme(); got != ThemeDefault {
		t.Fatalf("bare detectTheme = %q, want %q", got, ThemeDefault)
	}

	persistTheme("nord")
	if got := detectTheme(); got != "nord" {
		t.Fatalf("after persist, detectTheme = %q, want nord", got)
	}

	// An invalid persisted name falls back through the chain.
	path := themeFilePath()
	if err := os.WriteFile(path, []byte("bogus-theme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectTheme(); got != ThemeDefault {
		t.Fatalf("corrupt file detectTheme = %q, want default", got)
	}

	// Environment fallback when no file exists.
	os.Remove(path)
	t.Setenv("NIF_TUI_THEME", "solarized-dark")
	if got := detectTheme(); got != "solarized-dark" {
		t.Fatalf("env detectTheme = %q, want solarized-dark", got)
	}
	// A persisted choice wins over the environment (same precedence as /locale).
	persistTheme("sepia")
	if got := detectTheme(); got != "sepia" {
		t.Fatalf("file-over-env detectTheme = %q, want sepia", got)
	}
	// persistTheme must not store junk: the file keeps its previous content.
	persistTheme("not-a-theme")
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "sepia" {
		t.Fatalf("persistTheme overwrote a valid choice: %q err %v", string(data), err)
	}
}

// TestThemeCommand covers /theme <name> (applies + persists + confirms) and
// its error path.
func TestThemeCommand(t *testing.T) {
	restoreDefaultTheme(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	os.Unsetenv("NIF_TUI_THEME")

	m := newTestModel()
	updated, _ := m.executeLocalCommand("/theme bogus")
	got := updated.(model)
	if got.theme != "" || len(got.blocks) != 1 || got.blocks[0].kind != blockError {
		t.Fatalf("unknown theme = theme:%q blocks:%+v", got.theme, got.blocks)
	}
	if _, err := os.Stat(themeFilePath()); !os.IsNotExist(err) {
		t.Fatal("unknown theme was persisted")
	}

	updated, _ = m.executeLocalCommand("/theme light")
	got = updated.(model)
	if got.theme != "light" {
		t.Fatalf("/theme light left theme = %q", got.theme)
	}
	if currentTheme.glamour != "light" {
		t.Fatalf("glamour style after /theme = %q, want light", currentTheme.glamour)
	}
	if len(got.blocks) != 1 || got.blocks[0].kind != blockMeta ||
		!strings.Contains(got.blocks[0].text, "light") {
		t.Fatalf("confirmation block = %+v", got.blocks)
	}
	data, err := os.ReadFile(themeFilePath())
	if err != nil || strings.TrimSpace(string(data)) != "light" {
		t.Fatalf("persisted theme file = %q err %v, want light", string(data), err)
	}
}

// TestThemeSelectorAppliesAndPersists drives the /theme picker end to end:
// open, select, enter — the theme applies, persists, and returns to chat.
func TestThemeSelectorAppliesAndPersists(t *testing.T) {
	restoreDefaultTheme(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	os.Unsetenv("NIF_TUI_THEME")

	m := newTestModel()
	m.openThemeSelector()
	if m.mode != modeThemes {
		t.Fatalf("openThemeSelector mode = %v, want modeThemes", m.mode)
	}
	// The mode must render (a mode missing from View()'s switch shows only
	// the header — the /mcp regression class).
	m.width = 80
	m.height = 24
	m.layout()
	if view := m.View().Content; !strings.Contains(view, "default") ||
		!strings.Contains(stripANSI(view), "Color themes") {
		t.Fatalf("theme picker did not render its list:\n%q", stripANSI(view))
	}
	items := m.selector.list.Items()
	if len(items) != len(themeNames) {
		t.Fatalf("picker items = %d, want %d", len(items), len(themeNames))
	}
	// themeNames order marks the active entry first.
	if items[0].(selectorItem).id != ThemeDefault {
		t.Fatalf("first picker item = %v, want the default theme", items[0])
	}

	// Select nord and confirm with enter.
	nordIndex := -1
	for i, item := range items {
		if item.(selectorItem).id == "nord" {
			nordIndex = i
		}
	}
	if nordIndex < 0 {
		t.Fatal("nord missing from the picker")
	}
	m.selector.list.Select(nordIndex)
	updated, _ := m.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeChat {
		t.Fatalf("enter did not return to chat: mode = %v", got.mode)
	}
	if got.theme != "nord" || currentTheme.glamour != "dark" {
		t.Fatalf("selection not applied: theme=%q glamour=%q", got.theme, currentTheme.glamour)
	}
	if len(got.blocks) == 0 || got.blocks[len(got.blocks)-1].kind != blockMeta {
		t.Fatalf("no confirmation block: %+v", got.blocks)
	}
	data, err := os.ReadFile(themeFilePath())
	if err != nil || strings.TrimSpace(string(data)) != "nord" {
		t.Fatalf("persisted theme file = %q err %v, want nord", string(data), err)
	}

	// Esc leaves the theme untouched.
	m2 := newTestModel()
	m2.openThemeSelector()
	updated, _ = m2.handleControlKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	got = updated.(model)
	if got.mode != modeChat || got.theme != "" {
		t.Fatalf("esc = mode:%v theme:%q, want chat with no change", got.mode, got.theme)
	}
}

// TestThemeChangeInvalidatesRenderCaches ensures a live theme switch
// repaints the transcript: block render caches and the joined-transcript
// cache must be dropped and the glamour renderer rebuilt (its style is
// theme-dependent).
func TestThemeChangeInvalidatesRenderCaches(t *testing.T) {
	restoreDefaultTheme(t)
	t.Setenv("GLAMOUR_STYLE", "dark")
	m := newTestModel()
	m.width = 80
	m.height = 24
	m.layout()
	m.addBlock(blockAssistant, "# Title\n\n**bold**")
	if got := m.renderTranscript(); !strings.Contains(got, "Title") {
		t.Fatalf("precondition: transcript did not render: %q", got)
	}

	if !m.setTheme("light") {
		t.Fatal("setTheme(light) failed")
	}
	if !m.transcriptDirty {
		t.Fatal("theme switch did not invalidate the transcript cache")
	}
	if m.blocks[0].renderedOK {
		t.Fatal("theme switch kept the block render cache")
	}
	if m.renderW != 79 { // layout rebuilt the renderer for width 80
		t.Fatalf("renderer not rebuilt on theme switch: renderW = %d", m.renderW)
	}
	// The re-rendered transcript now carries light-style markdown.
	m.syncViewport(true)
	if !strings.Contains(stripANSI(m.transcript), "Title") {
		t.Fatalf("re-rendered transcript lost content: %q", m.transcript)
	}
}

// TestContextBarFollowsTheme checks the gauge picks up theme colors (its
// ANSI output changes with the active palette).
func TestContextBarFollowsTheme(t *testing.T) {
	restoreDefaultTheme(t)
	applyTheme(ThemeDefault)
	before := contextBar(0.8, 10)
	applyTheme("gruvbox-dark")
	after := contextBar(0.8, 10)
	if before == "" || after == "" {
		t.Fatalf("empty bar render: %q / %q", before, after)
	}
	if before == after {
		t.Fatal("context bar ignored the active theme")
	}
}
