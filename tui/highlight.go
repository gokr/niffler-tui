// Syntax highlighting for tool output (read previews) via chroma, using a
// style from the same family as the active theme's markdown code blocks.
// Card renderings are cached as transcript pieces, so a file is tokenised
// once per (card, detail, theme) — not per frame.
package main

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// highlightedLines returns code split into terminal-coloured lines for the
// given file path, or nil when no lexer matches the path or the content
// cannot be tokenised (the caller then shows the plain text).
func highlightedLines(path, code string) []string {
	if code == "" || path == "" {
		return nil
	}
	lexer := lexers.Match(filepath.Base(path))
	formatter := formatters.Get("terminal256")
	style := styles.Get(chromaStyleName())
	if lexer == nil || formatter == nil || style == nil {
		return nil
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return nil
	}
	// The indexed TTY formatter clears the style's base background, so the
	// card background (when the theme has one) stays intact. Trailing lines
	// that are empty once styles are stripped (the formatter's final reset)
	// are dropped so the preview row count matches the code.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// chromaStyleName maps the active theme's markdown style to a chroma style
// for tool output. Glamour's own chroma definitions are not reusable as
// chroma styles, so the closest built-in palette is chosen per family.
func chromaStyleName() string {
	switch currentTheme.glamour {
	case "light":
		return "github"
	case "dracula":
		return "dracula"
	case "tokyo-night":
		return "tokyonight-night"
	}
	return "monokai" // default/dark, and custom GLAMOUR_STYLE files
}
