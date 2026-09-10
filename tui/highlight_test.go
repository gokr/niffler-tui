package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHighlightedLines(t *testing.T) {
	if got := highlightedLines("", "code"); got != nil {
		t.Fatalf("empty path highlighted: %q", got)
	}
	if got := highlightedLines("file.zzzunknown", "code"); got != nil {
		t.Fatalf("unknown extension highlighted: %q", got)
	}
	code := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	lines := highlightedLines("main.go", code)
	if len(lines) != 5 {
		t.Fatalf("highlighted %d lines, want 5: %q", len(lines), lines)
	}
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "func main()") || !strings.Contains(plain, "println") {
		t.Fatalf("highlighting lost the code: %q", plain)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "\x1b[") {
		t.Fatalf("highlighted lines carry no colours: %q", lines)
	}
}

func TestChromaStyleFollowsTheme(t *testing.T) {
	// Leave the compiled-in default theme applied for the rest of the
	// package's tests regardless of what ran before.
	defer func() { _ = applyTheme(ThemeDefault) }()

	for theme, want := range map[string]string{
		"default":     "monokai",
		"light":       "github",
		"dracula":     "dracula",
		"tokyo-night": "tokyonight-night",
	} {
		if !applyTheme(theme) {
			t.Fatalf("unknown theme %q", theme)
		}
		if got := chromaStyleName(); got != want {
			t.Fatalf("theme %s chroma style = %q, want %q", theme, got, want)
		}
	}
}
