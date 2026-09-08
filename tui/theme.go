// Color themes for the TUI chrome. The transcript, header, input frame,
// selectors, forms, approval gate, and the context gauge all read their
// colors from the active theme, so the UI is legible on both dark and light
// terminals (the compiled-in ANSI default assumes a dark background and
// washes out on, e.g., macOS Terminal's white default).
//
// A theme names one accent per UI role and a markdown (glamour) style from
// glamour's built-in gallery ("light", "dark", "dracula", "tokyo-night"); the
// default theme passes "" so GLAMOUR_STYLE keeps controlling markdown exactly
// as before. /theme applies and persists the choice (same state dir as
// /locale); NIF_TUI_THEME overrides the compiled-in default at startup.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

const ThemeDefault = "default"

// theme is one named color scheme. Colors are lipgloss color strings: ANSI
// indices ("6") or hex ("#0E7490") — lipgloss degrades truecolor to the
// terminal's color profile automatically. Attributes (bold, italic) are
// fixed per role and shared by every theme; only colors vary.
type theme struct {
	// glamour is the markdown style name; "" honors GLAMOUR_STYLE (the
	// pre-theme behavior, dark by default).
	glamour string

	header      string
	inputBorder string
	user        string
	assistant   string
	thinking    string
	thinkLevel  string
	toolLevel   string
	effort      string
	link        string
	code        string
	slashFg     string
	slashBg     string
	tool        string
	meta        string
	error       string
	approval    string // approval gate border + title accent
	formBorder  string // provider/MCP/OAuth form panel borders
	barOK       string // context gauge
	barWarn     string
	barCrit     string
	barEmpty    string
}

// themeNames is the /theme picker order: lights first (the terminal-default
// cases), then darks.
var themeNames = []string{
	"default",
	"light",
	"sepia",
	"solarized-light",
	"solarized-dark",
	"gruvbox-dark",
	"nord",
	"dracula",
	"tokyo-night",
	"catppuccin-mocha",
}

// themeRegistry maps theme name → definition. The default theme carries the
// exact pre-theme ANSI palette, so an untouched install renders identically.
var themeRegistry = map[string]theme{
	"default": {
		glamour:     "",
		header:      "6",
		inputBorder: "6",
		user:        "12",
		assistant:   "10",
		thinking:    "8",
		thinkLevel:  "5",
		toolLevel:   "3",
		effort:      "4",
		link:        "12",
		code:        "2",
		slashFg:     "0",
		slashBg:     "7",
		tool:        "3",
		meta:        "8",
		error:       "9",
		approval:    "3",
		formBorder:  "8",
		barOK:       "6",
		barWarn:     "3",
		barCrit:     "9",
		barEmpty:    "8",
	},
	// light: high-contrast dark-on-white for terminals like macOS
	// Terminal's default profile.
	"light": {
		glamour:     "light",
		header:      "#0E7490",
		inputBorder: "#0E7490",
		user:        "#1D4ED8",
		assistant:   "#15803D",
		thinking:    "#6B7280",
		thinkLevel:  "#7C3AED",
		toolLevel:   "#B45309",
		effort:      "#0369A1",
		link:        "#1D4ED8",
		code:        "#047857",
		slashFg:     "#FFFFFF",
		slashBg:     "#1E293B",
		tool:        "#B45309",
		meta:        "#6B7280",
		error:       "#B91C1C",
		approval:    "#B45309",
		formBorder:  "#9CA3AF",
		barOK:       "#0E7490",
		barWarn:     "#B45309",
		barCrit:     "#B91C1C",
		barEmpty:    "#D1D5DB",
	},
	// sepia: warm paper tones for a light terminal.
	"sepia": {
		glamour:     "light",
		header:      "#8B5E34",
		inputBorder: "#B08D57",
		user:        "#9A3412",
		assistant:   "#4D7C0F",
		thinking:    "#8C8273",
		thinkLevel:  "#6D28D9",
		toolLevel:   "#A16207",
		effort:      "#0F766E",
		link:        "#9A3412",
		code:        "#4D7C0F",
		slashFg:     "#FDF6E3",
		slashBg:     "#7C6F64",
		tool:        "#A16207",
		meta:        "#8C8273",
		error:       "#B3261E",
		approval:    "#A16207",
		formBorder:  "#C0B4A0",
		barOK:       "#4D7C0F",
		barWarn:     "#A16207",
		barCrit:     "#B3261E",
		barEmpty:    "#E0D8C8",
	},
	// solarized-light: the Solarized palette on its light base.
	"solarized-light": {
		glamour:     "light",
		header:      "#2AA198",
		inputBorder: "#586E75",
		user:        "#268BD2",
		assistant:   "#859900",
		thinking:    "#93A1A1",
		thinkLevel:  "#D33682",
		toolLevel:   "#B58900",
		effort:      "#6C71C4",
		link:        "#268BD2",
		code:        "#2AA198",
		slashFg:     "#FDF6E3",
		slashBg:     "#586E75",
		tool:        "#B58900",
		meta:        "#657B83",
		error:       "#DC322F",
		approval:    "#B58900",
		formBorder:  "#93A1A1",
		barOK:       "#859900",
		barWarn:     "#B58900",
		barCrit:     "#DC322F",
		barEmpty:    "#EEE8D5",
	},
	// solarized-dark: the Solarized palette on its dark base.
	"solarized-dark": {
		glamour:     "dark",
		header:      "#2AA198",
		inputBorder: "#586E75",
		user:        "#268BD2",
		assistant:   "#859900",
		thinking:    "#586E75",
		thinkLevel:  "#D33682",
		toolLevel:   "#B58900",
		effort:      "#6C71C4",
		link:        "#268BD2",
		code:        "#2AA198",
		slashFg:     "#002B36",
		slashBg:     "#93A1A1",
		tool:        "#B58900",
		meta:        "#586E75",
		error:       "#DC322F",
		approval:    "#B58900",
		formBorder:  "#586E75",
		barOK:       "#859900",
		barWarn:     "#B58900",
		barCrit:     "#DC322F",
		barEmpty:    "#073642",
	},
	// gruvbox-dark: the retro-groove palette on its dark base.
	"gruvbox-dark": {
		glamour:     "dark",
		header:      "#8EC07C",
		inputBorder: "#8EC07C",
		user:        "#83A598",
		assistant:   "#B8BB26",
		thinking:    "#928374",
		thinkLevel:  "#D3869B",
		toolLevel:   "#FABD2F",
		effort:      "#FE8019",
		link:        "#83A598",
		code:        "#8EC07C",
		slashFg:     "#282828",
		slashBg:     "#EBDBB2",
		tool:        "#FABD2F",
		meta:        "#928374",
		error:       "#FB4934",
		approval:    "#FABD2F",
		formBorder:  "#928374",
		barOK:       "#B8BB26",
		barWarn:     "#FABD2F",
		barCrit:     "#FB4934",
		barEmpty:    "#3C3836",
	},
	// nord: the Nord palette on polar night.
	"nord": {
		glamour:     "dark",
		header:      "#88C0D0",
		inputBorder: "#88C0D0",
		user:        "#81A1C1",
		assistant:   "#A3BE8C",
		thinking:    "#4C566A",
		thinkLevel:  "#B48EAD",
		toolLevel:   "#EBCB8B",
		effort:      "#5E81AC",
		link:        "#81A1C1",
		code:        "#8FBCBB",
		slashFg:     "#2E3440",
		slashBg:     "#D8DEE9",
		tool:        "#EBCB8B",
		meta:        "#4C566A",
		error:       "#BF616A",
		approval:    "#EBCB8B",
		formBorder:  "#4C566A",
		barOK:       "#A3BE8C",
		barWarn:     "#EBCB8B",
		barCrit:     "#BF616A",
		barEmpty:    "#3B4252",
	},
	// dracula: glamour's built-in dracula markdown style plus a matching
	// chrome palette.
	"dracula": {
		glamour:     "dracula",
		header:      "#8BE9FD",
		inputBorder: "#BD93F9",
		user:        "#BD93F9",
		assistant:   "#50FA7B",
		thinking:    "#6272A4",
		thinkLevel:  "#FF79C6",
		toolLevel:   "#F1FA8C",
		effort:      "#FFB86C",
		link:        "#8BE9FD",
		code:        "#50FA7B",
		slashFg:     "#282A36",
		slashBg:     "#F8F8F2",
		tool:        "#F1FA8C",
		meta:        "#6272A4",
		error:       "#FF5555",
		approval:    "#F1FA8C",
		formBorder:  "#6272A4",
		barOK:       "#50FA7B",
		barWarn:     "#F1FA8C",
		barCrit:     "#FF5555",
		barEmpty:    "#44475A",
	},
	// tokyo-night: glamour's built-in tokyo-night markdown style plus a
	// matching chrome palette.
	"tokyo-night": {
		glamour:     "tokyo-night",
		header:      "#7DCFFF",
		inputBorder: "#7AA2F7",
		user:        "#7AA2F7",
		assistant:   "#9ECE6A",
		thinking:    "#565F89",
		thinkLevel:  "#BB9AF7",
		toolLevel:   "#E0AF68",
		effort:      "#FF9E64",
		link:        "#7DCFFF",
		code:        "#9ECE6A",
		slashFg:     "#1A1B26",
		slashBg:     "#C0CAF5",
		tool:        "#E0AF68",
		meta:        "#565F89",
		error:       "#F7768E",
		approval:    "#E0AF68",
		formBorder:  "#565F89",
		barOK:       "#9ECE6A",
		barWarn:     "#E0AF68",
		barCrit:     "#F7768E",
		barEmpty:    "#292E42",
	},
	// catppuccin-mocha: the Catppuccin Mocha palette.
	"catppuccin-mocha": {
		glamour:     "dark",
		header:      "#94E2D5",
		inputBorder: "#94E2D5",
		user:        "#89B4FA",
		assistant:   "#A6E3A1",
		thinking:    "#6C7086",
		thinkLevel:  "#CBA6F7",
		toolLevel:   "#F9E2AF",
		effort:      "#FAB387",
		link:        "#89DCFE",
		code:        "#A6E3A1",
		slashFg:     "#1E1E2E",
		slashBg:     "#CDD6F4",
		tool:        "#F9E2AF",
		meta:        "#6C7086",
		error:       "#F38BA8",
		approval:    "#F9E2AF",
		formBorder:  "#6C7086",
		barOK:       "#A6E3A1",
		barWarn:     "#F9E2AF",
		barCrit:     "#F38BA8",
		barEmpty:    "#313244",
	},
}

// themeDescriptions render as selector descriptions.
var themeDescriptions = map[string]string{
	"default":          "compiled-in ANSI palette (dark terminals), markdown from GLAMOUR_STYLE",
	"light":            "high-contrast dark-on-white (macOS Terminal default), light markdown",
	"sepia":            "warm paper tones for a light terminal, light markdown",
	"solarized-light":  "Solarized on light base, light markdown",
	"solarized-dark":   "Solarized on dark base, dark markdown",
	"gruvbox-dark":     "Gruvbox on dark base, dark markdown",
	"nord":             "Nord polar-night palette, dark markdown",
	"dracula":          "Dracula palette, dracula markdown",
	"tokyo-night":      "Tokyo Night palette, tokyo-night markdown",
	"catppuccin-mocha": "Catppuccin Mocha palette, dark markdown",
}

// currentTheme backs the non-style-var consumers (context gauge, form and
// approval borders). Initialized to the compiled-in default; applyTheme
// keeps it in sync.
var currentTheme = themeRegistry[ThemeDefault]

// themeDescription returns the picker blurb for name, or "" when unknown.
func themeDescription(name string) string {
	return themeDescriptions[name]
}

// applyTheme switches the global styles to the named theme. Returns false
// for an unknown name (the previous theme stays active).
func applyTheme(name string) bool {
	th, ok := themeRegistry[name]
	if !ok {
		return false
	}
	currentTheme = th
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.header))
	inputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.inputBorder))
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.user))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.assistant))
	thinkingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.thinking)).Italic(true)
	thinkLevelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.thinkLevel))
	toolLevelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.toolLevel))
	effortStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.effort))
	linkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.link)).Underline(true)
	codeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.code))
	activeSlashStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.slashFg)).Background(lipgloss.Color(th.slashBg))
	toolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.tool))
	metaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.meta))
	errorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.error))
	approvalBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.approval)).
		Padding(1, 2)
	approvalTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.approval))
	return true
}

// ---- persistence -----------------------------------------------------------

// themeFilePath is the single-line persistence file for the /theme choice
// (same state dir as the locale choice; empty when unavailable).
func themeFilePath() string {
	dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "niffler-tui", "theme")
}

// detectTheme resolves the startup theme: an explicit /theme choice, then
// NIF_TUI_THEME, then the compiled-in default. Unknown names fall through.
func detectTheme() string {
	if path := themeFilePath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if name := strings.TrimSpace(string(data)); validTheme(name) {
				return name
			}
		}
	}
	if env := strings.TrimSpace(os.Getenv("NIF_TUI_THEME")); env != "" && validTheme(env) {
		return env
	}
	return ThemeDefault
}

// persistTheme stores the /theme choice (best effort; the in-memory value
// still applies for this run when the file is unavailable).
func persistTheme(name string) {
	if !validTheme(name) {
		return
	}
	if path := themeFilePath(); path != "" {
		if dir := filepath.Dir(path); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		_ = os.WriteFile(path, []byte(name+"\n"), 0o644)
	}
}

func validTheme(name string) bool {
	_, ok := themeRegistry[name]
	return ok
}
