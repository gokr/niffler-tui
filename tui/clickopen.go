// Ctrl+click opens what the pointer is on: a URL in the browser, a file
// path with the platform's default handler. The transcript is rendered
// text, so the token under the pointer is recovered from the screen itself
// (selection.go's grapheme helpers), classified, and resolved against the
// bases transcript paths are plausibly relative to. The gesture rides the
// application-owned selection: the press modifier is remembered so a
// ctrl+click stays reserved from the tool-card toggle, and the open
// replaces the toggle instead of joining it.
//
// Modifiers reach the app in the mouse report itself (SGR button bits), so
// this needs an xterm-compatible terminal — the same ones that report
// drag events for the selection. NIF_TUI_OPEN replaces the platform
// opener wholesale for users who want a specific handler (e.g. an editor
// that understands file:line).
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// screenTokenAt extracts the contiguous non-space token of the rendered
// screen under a cell (ANSI styling stripped). Grapheme-aware expansion
// keeps a double-width glyph from being split; an empty result means the
// pointer is on a blank cell.
func screenTokenAt(content string, row, col int) string {
	lines := strings.Split(content, "\n")
	if row < 0 || row >= len(lines) {
		return ""
	}
	plain := ansi.Strip(lines[row])
	width := ansi.StringWidth(plain)
	if width == 0 || col < 0 || col >= width {
		return ""
	}
	start := graphemeStart(plain, col)
	end := graphemeEnd(plain, col)
	if isSpaceCells(plain, start, end) {
		return ""
	}
	for start > 0 {
		prev := graphemeStart(plain, start-1)
		if isSpaceCells(plain, prev, start) {
			break
		}
		start = prev
	}
	for end < width {
		next := graphemeEnd(plain, end)
		if isSpaceCells(plain, end, next) {
			break
		}
		end = next
	}
	return ansi.Cut(plain, start, end)
}

// isSpaceCells reports whether cells [start,end) of the line hold only
// whitespace.
func isSpaceCells(line string, start, end int) bool {
	return strings.TrimSpace(ansi.Cut(line, start, end)) == ""
}

// pathLineSuffixRe matches a grep/compiler-style path:line[:col] suffix.
// The path part is lazy so both trailing number groups are consumed.
var pathLineSuffixRe = regexp.MustCompile(`^(.+?):([0-9]{1,5})(?::([0-9]{1,5}))?$`)

// tokenTarget classifies a screen token as something ctrl+click can open:
// a URL (http/https, a bare www. host, or file://), or a filesystem path,
// optionally carrying a :line[:col] suffix (which is dropped). ok is false
// when the token is neither — a bare word, a version number, an elapsed
// time — and the click does nothing but say so.
func tokenTarget(token string) (target string, isURL bool, ok bool) {
	token = trimWrapPairs(strings.TrimSpace(token))
	token = trimTokenTail(token)
	if token == "" || len(token) > 512 {
		return "", false, false
	}
	lower := strings.ToLower(token)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return token, true, true
	case strings.HasPrefix(lower, "www.") && len(token) > 4:
		return "https://" + token, true, true
	case strings.HasPrefix(lower, "file://"):
		path := token[len("file://"):]
		// Only an empty host names a local path; file://host/path does not.
		if strings.HasPrefix(path, "/") {
			return path, false, true
		}
		return "", false, false
	}
	// grep-style path:line:col. Only strip when the remainder still reads
	// as a path (a directory separator), so host:port and clock times
	// survive untouched.
	if m := pathLineSuffixRe.FindStringSubmatch(token); m != nil && strings.Contains(m[1], "/") {
		token = m[1]
	}
	if isPathToken(token) {
		return token, false, true
	}
	return "", false, false
}

// isPathToken reports whether a token plausibly names a filesystem path:
// no whitespace or glue characters, no scheme, and either a directory
// separator or a file extension that is not just digits (so "main.go"
// opens but "3.14", "v0.4.1" and bare words do not).
func isPathToken(p string) bool {
	if p == "" || len(p) > 512 || strings.Contains(p, "://") {
		return false
	}
	if strings.ContainsAny(p, " \t\"'`@*?<>|") {
		return false
	}
	if strings.Contains(p, "/") {
		return true
	}
	switch p[0] {
	case '.', '~':
		return true
	}
	ext := filepath.Ext(p)
	if len(ext) < 2 || len(ext) > 6 {
		return false
	}
	digitsOnly := true
	for _, r := range ext[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
		if !unicode.IsDigit(r) {
			digitsOnly = false
		}
	}
	return !digitsOnly
}

// trimWrapPairs strips symmetric wrapping quotes and bracket pairs (a
// quoted URL, a parenthesised path, an autolink's angle brackets).
func trimWrapPairs(s string) string {
	for len(s) >= 2 {
		var close byte
		switch s[0] {
		case '"':
			close = '"'
		case '\'':
			close = '\''
		case '`':
			close = '`'
		case '(':
			close = ')'
		case '[':
			close = ']'
		case '{':
			close = '}'
		case '<':
			close = '>'
		default:
			return s
		}
		if s[len(s)-1] != close {
			return s
		}
		s = s[1 : len(s)-1]
	}
	return s
}

// trimTokenTail strips trailing sentence punctuation and unbalanced closing
// brackets, which prose glues onto URLs and paths ("see a/b.go.)" and the
// like).
func trimTokenTail(s string) string {
	for len(s) > 1 {
		switch c := s[len(s)-1]; c {
		case '.', ',', ';', ':', '!', '?', '\'', '"', '`':
			s = s[:len(s)-1]
			continue
		case ')':
			if strings.Count(s, "(") < strings.Count(s, ")") {
				s = s[:len(s)-1]
				continue
			}
		case ']':
			if strings.Count(s, "[") < strings.Count(s, "]") {
				s = s[:len(s)-1]
				continue
			}
		case '}':
			if strings.Count(s, "{") < strings.Count(s, "}") {
				s = s[:len(s)-1]
				continue
			}
		}
		break
	}
	return s
}

// resolveOpenPath turns a transcript path into an absolute one. Relative
// paths are ambiguous: they may belong to the conversation's workspace
// (this client's launch dir pins it for new conversations, but a
// conversation can come from anywhere), to the harness clone, or to the
// directory the tui itself runs in. The first candidate that exists wins;
// an unresolvable path is passed through so the opener's failure is the
// visible outcome.
func (m model) resolveOpenPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	bases := []string{m.cwd, m.launchDir, m.harnessIdentity.Root}
	candidates := make([]string, 0, len(bases)+1)
	for _, base := range bases {
		if base != "" {
			candidates = append(candidates, filepath.Join(base, p))
		}
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, p))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return p
}

// openUnderPointer resolves a ctrl+click: open the URL or file path under
// the pointer. A ctrl+click is always consumed — the tool-card toggle must
// not also fire — and when nothing openable sits under the pointer a note
// says so instead of silently doing nothing.
func (m *model) openUnderPointer(x, y int) tea.Cmd {
	token := screenTokenAt(m.View().Content, y, x)
	target, isURL, ok := tokenTarget(token)
	if !ok {
		m.contextNote = t(m.loc, "note.openNothing")
		return nil
	}
	if !isURL {
		target = m.resolveOpenPath(target)
	}
	return openTargetCmd(target)
}

// openTargetCmd opens a target with the platform's default handler. The
// command reports a failure to start (a missing xdg-open); a later non-zero
// exit of the opener itself is invisible — fire-and-forget, like a file
// manager opening a link.
func openTargetCmd(target string) tea.Cmd {
	return func() tea.Msg {
		if err := startOpener(target); err != nil {
			return openResultMsg{target: target, err: err}
		}
		return nil
	}
}

// startOpener launches the platform default handler for a URL or path.
// NIF_TUI_OPEN overrides it wholesale: its space-separated words become the
// command, with `{}` replaced by the target when present and the target
// appended otherwise — e.g. NIF_TUI_OPEN='kitty nvim' or a wrapper script
// that understands file:line.
func startOpener(target string) error {
	if words := strings.Fields(os.Getenv("NIF_TUI_OPEN")); len(words) > 0 {
		args := make([]string, 0, len(words))
		placeholder := false
		for _, w := range words {
			if w == "{}" {
				w, placeholder = target, true
			}
			args = append(args, w)
		}
		if !placeholder {
			args = append(args, target)
		}
		return exec.Command(args[0], args[1:]...).Start()
	}
	var bin string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		bin, args = "open", []string{target}
	case "windows":
		bin, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		bin, args = "xdg-open", []string{target}
	}
	return exec.Command(bin, args...).Start()
}

// openResultMsg reports a failed open attempt back to the UI.
type openResultMsg struct {
	target string
	err    error
}
