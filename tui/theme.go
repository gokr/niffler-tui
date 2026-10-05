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

	"charm.land/bubbles/v2/textarea"
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
	toolBg      string // card background behind a tool run's lines
	toolBgSet   bool   // true once toolBg is meaningful for this theme
	meta        string
	error       string
	approval    string // approval gate border + title accent
	formBorder  string // provider/MCP/OAuth form panel borders
	barOK       string // context gauge
	barWarn     string
	barCrit     string
	barEmpty    string
	badgeBg     string // background-processes badge (bg N)
	badgeAgent  string // subagent badge (agent N)
}

// themeNames is the /theme picker order: lights first (the terminal-default
// cases), then darks.
var themeNames = []string{
	"default",
	"light",
	"sepia",
	"solarized-light",
	"github-light",
	"rose-pine-dawn",
	"solarized-dark",
	"gruvbox-dark",
	"nord",
	"dracula",
	"tokyo-night",
	"catppuccin-mocha",
	"monokai",
	"one-dark",
	"github-dark",
	"rose-pine",
	"kanagawa",
	"everforest",
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
		toolBg:      "236",
		toolBgSet:   true,
		meta:        "8",
		error:       "9",
		approval:    "3",
		formBorder:  "8",
		barOK:       "6",
		barWarn:     "3",
		barCrit:     "9",
		barEmpty:    "8",
		badgeBg:     "3",
		badgeAgent:  "6",
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
		toolBg:      "#F1F3F5",
		toolBgSet:   true,
		meta:        "#6B7280",
		error:       "#B91C1C",
		approval:    "#B45309",
		formBorder:  "#9CA3AF",
		barOK:       "#0E7490",
		barWarn:     "#B45309",
		barCrit:     "#B91C1C",
		barEmpty:    "#D1D5DB",
		badgeBg:     "#B45309",
		badgeAgent:  "#0E7490",
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
		toolBg:      "#F2EADC",
		toolBgSet:   true,
		meta:        "#8C8273",
		error:       "#B3261E",
		approval:    "#A16207",
		formBorder:  "#C0B4A0",
		barOK:       "#4D7C0F",
		barWarn:     "#A16207",
		barCrit:     "#B3261E",
		barEmpty:    "#E0D8C8",
		badgeBg:     "#A16207",
		badgeAgent:  "#0F766E",
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
		toolBg:      "#EEE8D5",
		toolBgSet:   true,
		meta:        "#657B83",
		error:       "#DC322F",
		approval:    "#B58900",
		formBorder:  "#93A1A1",
		barOK:       "#859900",
		barWarn:     "#B58900",
		barCrit:     "#DC322F",
		barEmpty:    "#EEE8D5",
		badgeBg:     "#B58900",
		badgeAgent:  "#268BD2",
	},
	// github-light: GitHub's Primer palette on its light base.
	"github-light": {
		glamour:     "light",
		header:      "#0550AE",
		inputBorder: "#0550AE",
		user:        "#0550AE",
		assistant:   "#1A7F37",
		thinking:    "#57606A",
		thinkLevel:  "#8250DF",
		toolLevel:   "#9A6700",
		effort:      "#BC4C00",
		link:        "#0969DA",
		code:        "#1A7F37",
		slashFg:     "#FFFFFF",
		slashBg:     "#24292F",
		tool:        "#9A6700",
		toolBg:      "#F6F8FA",
		toolBgSet:   true,
		meta:        "#57606A",
		error:       "#CF222E",
		approval:    "#9A6700",
		formBorder:  "#D0D7DE",
		barOK:       "#1A7F37",
		barWarn:     "#9A6700",
		barCrit:     "#CF222E",
		barEmpty:    "#D0D7DE",
		badgeBg:     "#9A6700",
		badgeAgent:  "#0550AE",
	},
	// rose-pine-dawn: Rosé Pine Dawn, the light Rosé Pine variant.
	"rose-pine-dawn": {
		glamour:     "light",
		header:      "#56949F",
		inputBorder: "#907AA9",
		user:        "#907AA9",
		assistant:   "#56949F",
		thinking:    "#9893A5",
		thinkLevel:  "#D7827E",
		toolLevel:   "#EA9D34",
		effort:      "#286983",
		link:        "#56949F",
		code:        "#56949F",
		slashFg:     "#FAF4ED",
		slashBg:     "#575279",
		tool:        "#EA9D34",
		toolBg:      "#F2E9E1",
		toolBgSet:   true,
		meta:        "#9893A5",
		error:       "#B4637A",
		approval:    "#EA9D34",
		formBorder:  "#9893A5",
		barOK:       "#56949F",
		barWarn:     "#EA9D34",
		barCrit:     "#B4637A",
		barEmpty:    "#F2E9E1",
		badgeBg:     "#EA9D34",
		badgeAgent:  "#56949F",
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
		toolBg:      "#073642",
		toolBgSet:   true,
		meta:        "#586E75",
		error:       "#DC322F",
		approval:    "#B58900",
		formBorder:  "#586E75",
		barOK:       "#859900",
		barWarn:     "#B58900",
		barCrit:     "#DC322F",
		barEmpty:    "#073642",
		badgeBg:     "#B58900",
		badgeAgent:  "#2AA198",
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
		toolBg:      "#32302F",
		toolBgSet:   true,
		meta:        "#928374",
		error:       "#FB4934",
		approval:    "#FABD2F",
		formBorder:  "#928374",
		barOK:       "#B8BB26",
		barWarn:     "#FABD2F",
		barCrit:     "#FB4934",
		barEmpty:    "#3C3836",
		badgeBg:     "#FABD2F",
		badgeAgent:  "#83A598",
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
		toolBg:      "#3B4252",
		toolBgSet:   true,
		meta:        "#4C566A",
		error:       "#BF616A",
		approval:    "#EBCB8B",
		formBorder:  "#4C566A",
		barOK:       "#A3BE8C",
		barWarn:     "#EBCB8B",
		barCrit:     "#BF616A",
		barEmpty:    "#3B4252",
		badgeBg:     "#EBCB8B",
		badgeAgent:  "#88C0D0",
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
		toolBg:      "#343746",
		toolBgSet:   true,
		meta:        "#6272A4",
		error:       "#FF5555",
		approval:    "#F1FA8C",
		formBorder:  "#6272A4",
		barOK:       "#50FA7B",
		barWarn:     "#F1FA8C",
		barCrit:     "#FF5555",
		barEmpty:    "#44475A",
		badgeBg:     "#F1FA8C",
		badgeAgent:  "#8BE9FD",
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
		toolBg:      "#24283B",
		toolBgSet:   true,
		meta:        "#565F89",
		error:       "#F7768E",
		approval:    "#E0AF68",
		formBorder:  "#565F89",
		barOK:       "#9ECE6A",
		barWarn:     "#E0AF68",
		barCrit:     "#F7768E",
		barEmpty:    "#292E42",
		badgeBg:     "#E0AF68",
		badgeAgent:  "#7DCFFF",
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
		toolBg:      "#313244",
		toolBgSet:   true,
		meta:        "#6C7086",
		error:       "#F38BA8",
		approval:    "#F9E2AF",
		formBorder:  "#6C7086",
		barOK:       "#A6E3A1",
		barWarn:     "#F9E2AF",
		barCrit:     "#F38BA8",
		barEmpty:    "#313244",
		badgeBg:     "#F9E2AF",
		badgeAgent:  "#89DCEB",
	},
	// monokai: the classic Monokai palette (Sublime Text's default).
	"monokai": {
		glamour:     "dark",
		header:      "#66D9EF",
		inputBorder: "#AE81FF",
		user:        "#66D9EF",
		assistant:   "#A6E22E",
		thinking:    "#75715E",
		thinkLevel:  "#AE81FF",
		toolLevel:   "#E6DB74",
		effort:      "#FD971F",
		link:        "#66D9EF",
		code:        "#A6E22E",
		slashFg:     "#272822",
		slashBg:     "#F8F8F2",
		tool:        "#E6DB74",
		toolBg:      "#3E3D32",
		toolBgSet:   true,
		meta:        "#75715E",
		error:       "#F92672",
		approval:    "#E6DB74",
		formBorder:  "#75715E",
		barOK:       "#A6E22E",
		barWarn:     "#E6DB74",
		barCrit:     "#F92672",
		barEmpty:    "#3E3D32",
		badgeBg:     "#E6DB74",
		badgeAgent:  "#66D9EF",
	},
	// one-dark: Atom's One Dark palette.
	"one-dark": {
		glamour:     "dark",
		header:      "#61AFEF",
		inputBorder: "#61AFEF",
		user:        "#61AFEF",
		assistant:   "#98C379",
		thinking:    "#5C6370",
		thinkLevel:  "#C678DD",
		toolLevel:   "#E5C07B",
		effort:      "#D19A66",
		link:        "#61AFEF",
		code:        "#56B6C2",
		slashFg:     "#282C34",
		slashBg:     "#ABB2BF",
		tool:        "#E5C07B",
		toolBg:      "#2C313A",
		toolBgSet:   true,
		meta:        "#5C6370",
		error:       "#E06C75",
		approval:    "#E5C07B",
		formBorder:  "#5C6370",
		barOK:       "#98C379",
		barWarn:     "#E5C07B",
		barCrit:     "#E06C75",
		barEmpty:    "#3E4451",
		badgeBg:     "#E5C07B",
		badgeAgent:  "#56B6C2",
	},
	// github-dark: GitHub's Primer palette on its dark base.
	"github-dark": {
		glamour:     "dark",
		header:      "#58A6FF",
		inputBorder: "#58A6FF",
		user:        "#58A6FF",
		assistant:   "#3FB950",
		thinking:    "#8B949E",
		thinkLevel:  "#BC8CFF",
		toolLevel:   "#E3B341",
		effort:      "#D29922",
		link:        "#58A6FF",
		code:        "#7EE787",
		slashFg:     "#0D1117",
		slashBg:     "#E6EDF3",
		tool:        "#E3B341",
		toolBg:      "#161B22",
		toolBgSet:   true,
		meta:        "#8B949E",
		error:       "#F85149",
		approval:    "#E3B341",
		formBorder:  "#30363D",
		barOK:       "#3FB950",
		barWarn:     "#E3B341",
		barCrit:     "#F85149",
		barEmpty:    "#21262D",
		badgeBg:     "#E3B341",
		badgeAgent:  "#58A6FF",
	},
	// rose-pine: the Rosé Pine palette (soft, all-natural pastels).
	"rose-pine": {
		glamour:     "dark",
		header:      "#9CCFD8",
		inputBorder: "#C4A7E7",
		user:        "#C4A7E7",
		assistant:   "#9CCFD8",
		thinking:    "#6E6A86",
		thinkLevel:  "#EBBCBA",
		toolLevel:   "#F6C177",
		effort:      "#31748F",
		link:        "#9CCFD8",
		code:        "#9CCFD8",
		slashFg:     "#191724",
		slashBg:     "#E0DEF4",
		tool:        "#F6C177",
		toolBg:      "#26233A",
		toolBgSet:   true,
		meta:        "#6E6A86",
		error:       "#EB6F92",
		approval:    "#F6C177",
		formBorder:  "#6E6A86",
		barOK:       "#9CCFD8",
		barWarn:     "#F6C177",
		barCrit:     "#EB6F92",
		barEmpty:    "#26233A",
		badgeBg:     "#F6C177",
		badgeAgent:  "#9CCFD8",
	},
	// kanagawa: the Kanagawa wave palette.
	"kanagawa": {
		glamour:     "dark",
		header:      "#7E9CD8",
		inputBorder: "#7E9CD8",
		user:        "#7E9CD8",
		assistant:   "#98BB6C",
		thinking:    "#727169",
		thinkLevel:  "#D27E99",
		toolLevel:   "#E6C384",
		effort:      "#FFA066",
		link:        "#7FB4CA",
		code:        "#7AA89F",
		slashFg:     "#1F1F28",
		slashBg:     "#C8C093",
		tool:        "#E6C384",
		toolBg:      "#2A2A37",
		toolBgSet:   true,
		meta:        "#727169",
		error:       "#E46876",
		approval:    "#E6C384",
		formBorder:  "#54546D",
		barOK:       "#98BB6C",
		barWarn:     "#E6C384",
		barCrit:     "#E46876",
		barEmpty:    "#2A2A37",
		badgeBg:     "#E6C384",
		badgeAgent:  "#7FB4CA",
	},
	// everforest: the Everforest palette on its dark base.
	"everforest": {
		glamour:     "dark",
		header:      "#7FBBB3",
		inputBorder: "#7FBBB3",
		user:        "#7FBBB3",
		assistant:   "#A7C080",
		thinking:    "#859289",
		thinkLevel:  "#D699B6",
		toolLevel:   "#DBBC7F",
		effort:      "#E69875",
		link:        "#7FBBB3",
		code:        "#83C092",
		slashFg:     "#2D353B",
		slashBg:     "#D3C6AA",
		tool:        "#DBBC7F",
		toolBg:      "#343F44",
		toolBgSet:   true,
		meta:        "#859289",
		error:       "#E67E80",
		approval:    "#DBBC7F",
		formBorder:  "#859289",
		barOK:       "#A7C080",
		barWarn:     "#DBBC7F",
		barCrit:     "#E67E80",
		barEmpty:    "#3D484D",
		badgeBg:     "#DBBC7F",
		badgeAgent:  "#7FBBB3",
	},
}

// themeDescriptions render as selector descriptions.
var themeDescriptions = map[string]string{
	"default":          "compiled-in ANSI palette (dark terminals), markdown from GLAMOUR_STYLE",
	"light":            "high-contrast dark-on-white (macOS Terminal default), light markdown",
	"sepia":            "warm paper tones for a light terminal, light markdown",
	"solarized-light":  "Solarized on light base, light markdown",
	"github-light":     "GitHub Primer on light base, light markdown",
	"rose-pine-dawn":   "Rosé Pine Dawn palette, light markdown",
	"solarized-dark":   "Solarized on dark base, dark markdown",
	"gruvbox-dark":     "Gruvbox on dark base, dark markdown",
	"nord":             "Nord polar-night palette, dark markdown",
	"dracula":          "Dracula palette, dracula markdown",
	"tokyo-night":      "Tokyo Night palette, tokyo-night markdown",
	"catppuccin-mocha": "Catppuccin Mocha palette, dark markdown",
	"monokai":          "Monokai palette, dark markdown",
	"one-dark":         "Atom One Dark palette, dark markdown",
	"github-dark":      "GitHub Primer on dark base, dark markdown",
	"rose-pine":        "Rosé Pine palette, dark markdown",
	"kanagawa":         "Kanagawa wave palette, dark markdown",
	"everforest":       "Everforest on dark base, dark markdown",
}

// currentTheme backs the non-style-var consumers (context gauge, form and
// approval borders). Initialized to the compiled-in default; applyTheme
// keeps it in sync.
var currentTheme = themeRegistry[ThemeDefault]

// inputDark reports whether the active theme renders on a dark surface. The
// bubbles textarea ships one style palette per terminal surface (see
// textarea.DefaultDarkStyles / DefaultLightStyles); a theme whose markdown
// (glamour) style is built on a light background gets the light palette so
// the input field does not keep the dark palette's hard black active-line
// background when a light theme is active. Every non-light theme -- the
// compiled-in default included -- keeps the dark palette, matching its
// annotated terminal assumption.
func inputDark(th theme) bool {
	return th.glamour != "light"
}

// inputTextareaStyles is the theme-driven palette for the textarea widget.
// The widget's two built-in palettes are tuned for the terminal surface the
// theme picks; choosing by the active theme keeps the field legible when
// switching between light and dark themes instead of leaving a black input
// line behind.
func inputTextareaStyles(th theme) textarea.Styles {
	isDark := inputDark(th)
	styles := textarea.DefaultStyles(isDark)
	// The default textarea palette backgrounds only CursorLine. That makes
	// multiline input visibly lose its dark (or light) field background when
	// Alt+Enter moves the cursor to the next line: the previous line falls
	// back to the terminal background. Put the surface on Base so every line
	// shares the same field background; CursorLine can still override it
	// when the palette wants a distinct active line.
	if isDark {
		styles.Focused.Base = lipgloss.NewStyle().Background(lipgloss.Color("0"))
		styles.Blurred.Base = lipgloss.NewStyle().Background(lipgloss.Color("0"))
	} else {
		styles.Focused.Base = lipgloss.NewStyle().Background(lipgloss.Color("255"))
		styles.Blurred.Base = lipgloss.NewStyle().Background(lipgloss.Color("255"))
	}
	return styles
}

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
	// toolCardStyle backs a tool run's lines so cards read as one block
	// instead of blending into the surrounding transcript. Themes without a
	// card background (toolBgSet false) leave the terminal default alone.
	if th.toolBgSet {
		toolCardStyle = lipgloss.NewStyle().Background(lipgloss.Color(th.toolBg))
	} else {
		toolCardStyle = lipgloss.NewStyle()
	}
	metaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.meta))
	noticeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.meta)).Italic(true)
	errorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.error))
	approvalBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.approval)).
		Padding(1, 2)
	approvalTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.approval))
	badgeBgStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.badgeBg))
	badgeAgentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.badgeAgent))
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
