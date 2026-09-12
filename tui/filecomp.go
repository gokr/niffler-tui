// filecomp.go — @file references in the chat input.
//
// Typing "@" and pressing Tab completes a workspace-relative file path
// (fuzzy, ignore-rule aware). The inserted reference is text only —
// `@path`, delimited by a trailing space (backtick-quoted when the path
// contains whitespace) — and the agent is expected to open it with the
// read tool. Content attachment is deliberately out of scope: selecting a
// path and attaching content are different features.
package main

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"
)

// ---- state -----------------------------------------------------------------

// fileCompState is the live @file completion. Mirrors slashCompleteState:
// the first Tab press lists candidates (async on a cache miss), further
// presses cycle, a single candidate fills without a list.
type fileCompState struct {
	active     bool
	loading    bool
	prefix     string   // text up to and including the "@"
	token      string   // partial path typed after "@"
	cwd        string   // workspace the listing was requested for
	candidates []string // workspace-relative paths, best first
	index      int
}

// fileListEntry is a cached per-workspace listing.
type fileListEntry struct {
	files []string
	at    time.Time
}

// fileListMsg carries an async workspace listing to the model.
type fileListMsg struct {
	cwd   string
	files []string
}

const (
	fileListTTL       = 10 * time.Second
	maxFileCandidates = 100
	maxListedFiles    = 20000
	fileListTimeout   = 5 * time.Second
)

// skipDirs keeps the fallback walker out of dependencies, build output and
// other generated trees. rg handles the gitignored case on its own; this
// list covers repositories without ignore rules.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "vendor": true, "__pycache__": true,
	"var": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "coverage": true,
	".idea": true, ".vscode": true, ".cache": true, ".next": true,
}

// ---- token extraction ------------------------------------------------------

func isSpaceRune(r rune) bool { return unicode.IsSpace(r) }

// cursorOffset returns the cursor's rune offset within the input value.
func (m model) cursorOffset() (int, bool) {
	lines := strings.Split(m.input.Value(), "\n")
	line, col := m.input.Line(), m.input.Column()
	if line < 0 || line >= len(lines) || col < 0 {
		return 0, false
	}
	runes := []rune(lines[line])
	if col > len(runes) {
		col = len(runes)
	}
	offset := col
	for i := 0; i < line; i++ {
		offset += len([]rune(lines[i])) + 1
	}
	return offset, true
}

// fileTokenAtCursor inspects the word ending at the cursor: it must start
// with "@" (the @ itself preceded by value start or whitespace, so emails
// like user@host never trigger) and the token must run to the cursor. It
// returns the text up to and including the "@", the partial path after it,
// and the rune offset of the "@".
func fileTokenAtCursor(runes []rune, offset int) (prefix, token string, at int, ok bool) {
	if offset <= 0 || offset > len(runes) {
		return "", "", 0, false
	}
	start := offset
	for start > 0 && !isSpaceRune(runes[start-1]) {
		start--
	}
	word := runes[start:offset]
	if len(word) == 0 || word[0] != '@' {
		return "", "", 0, false
	}
	return string(runes[:start+1]), string(word[1:]), start, true
}

// ---- candidate filtering ---------------------------------------------------

// filterFileCandidates ranks workspace files for the typed token: basename
// prefix matches first, then path prefix matches, then fuzzy subsequence
// matches (case-insensitive). An empty token lists everything, capped.
func filterFileCandidates(files []string, token string) []string {
	if len(files) == 0 {
		return nil
	}
	if token == "" {
		out := append([]string(nil), files...)
		sort.Strings(out)
		if len(out) > maxFileCandidates {
			out = out[:maxFileCandidates]
		}
		return out
	}
	token = strings.ToLower(token)
	var tier1, tier2 []string
	inTier := make(map[string]bool, len(files))
	for _, f := range files {
		switch {
		case strings.HasPrefix(strings.ToLower(path.Base(f)), token):
			tier1 = append(tier1, f)
			inTier[f] = true
		case strings.HasPrefix(strings.ToLower(f), token):
			tier2 = append(tier2, f)
			inTier[f] = true
		}
	}
	var rest []string
	if len(tier1)+len(tier2) < maxFileCandidates {
		var remainder []string
		for _, f := range files {
			if !inTier[f] {
				remainder = append(remainder, f)
			}
		}
		for _, m := range fuzzy.Find(token, remainder) {
			rest = append(rest, m.Str)
		}
	}
	out := append(append(tier1, tier2...), rest...)
	if len(out) > maxFileCandidates {
		out = out[:maxFileCandidates]
	}
	return out
}

// ---- workspace listing -----------------------------------------------------

// listWorkspaceFiles returns workspace-relative file paths, best-effort
// respecting ignore rules: ripgrep (--files, .gitignore honored like the
// grep tool) when installed, else a bounded walker that skips hidden
// entries and the generated/dependency trees in skipDirs.
func listWorkspaceFiles(cwd string) ([]string, error) {
	if files, err := ripgrepFiles(cwd); err == nil {
		return files, nil
	}
	return walkFiles(cwd)
}

func ripgrepFiles(cwd string) ([]string, error) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), fileListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, rg, "--files", "--no-require-git")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, filepath.ToSlash(filepath.Clean(f)))
			if len(files) >= maxListedFiles {
				break
			}
		}
	}
	return files, nil
}

func walkFiles(cwd string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable subtree: skip it, never abort the walk.
			return fs.SkipDir
		}
		rel, rerr := filepath.Rel(cwd, p)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(name, ".") {
			return nil
		}
		if len(files) >= maxListedFiles {
			return fs.SkipAll
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, err
}

func fetchFilesCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		files, err := listWorkspaceFiles(cwd)
		if err != nil {
			return fileListMsg{cwd: cwd}
		}
		return fileListMsg{cwd: cwd, files: files}
	}
}

// fileListFor returns the cached listing when it is still fresh.
func (m model) fileListFor(cwd string) ([]string, bool) {
	if cwd == "" {
		return nil, false
	}
	if e, ok := m.fileList[cwd]; ok && time.Since(e.at) < fileListTTL {
		return e.files, true
	}
	return nil, false
}

func (m model) compCwd() string {
	if m.cwd != "" {
		return m.cwd
	}
	return initialCwd()
}

// ---- Tab handling ----------------------------------------------------------

// handleFileTab processes Tab in chat mode while the cursor ends an
// @-reference token. The first press computes and shows candidates
// (fetching the workspace listing async on a cache miss), further presses
// cycle, a single candidate fills directly. A key with no @-token to
// complete falls through untouched (nil cmd), like the slash handler.
func (m model) handleFileTab(backward bool) (model, tea.Cmd) {
	runes := []rune(m.input.Value())
	offset, ok := m.cursorOffset()
	if !ok || offset != len(runes) {
		return m, nil
	}
	prefix, token, _, ok := fileTokenAtCursor(runes, offset)
	if !ok {
		return m, nil
	}
	if m.fileComp.active {
		if len(m.fileComp.candidates) == 0 {
			return m, nil
		}
		if backward {
			m.fileComp.index = (m.fileComp.index - 1 + len(m.fileComp.candidates)) % len(m.fileComp.candidates)
		} else {
			m.fileComp.index = (m.fileComp.index + 1) % len(m.fileComp.candidates)
		}
		m.applyFileCandidate()
		return m, nil
	}
	cwd := m.compCwd()
	if files, fresh := m.fileListFor(cwd); fresh {
		filtered := filterFileCandidates(files, token)
		if len(filtered) == 0 {
			return m, nil
		}
		if len(filtered) == 1 {
			m.fileComp = fileCompState{prefix: prefix}
			m.insertFileRef(filtered[0])
			m.layout()
			return m, nil
		}
		m.fileComp = fileCompState{active: true, prefix: prefix, token: token, cwd: cwd, candidates: filtered}
		m.applyFileCandidate()
		m.layout()
		return m, nil
	}
	m.fileComp = fileCompState{active: true, loading: true, prefix: prefix, token: token, cwd: cwd}
	m.layout()
	return m, fetchFilesCmd(cwd)
}

// applyFileCandidate fills the input with the currently selected path.
func (m *model) applyFileCandidate() {
	if m.fileComp.index < 0 || m.fileComp.index >= len(m.fileComp.candidates) {
		return
	}
	m.insertFileRef(m.fileComp.candidates[m.fileComp.index])
}

// insertFileRef replaces the @-token being completed with a clearly
// delimited reference: path, backtick-quoted when it contains whitespace,
// followed by a trailing space so it stays delimited from whatever the
// user types next. The prefix already carries the "@".
func (m *model) insertFileRef(path string) {
	ref := path
	if strings.ContainsAny(path, " \t") {
		ref = "`" + path + "`"
	}
	m.input.SetValue(m.fileComp.prefix + ref + " ")
	m.input.CursorEnd()
}

func (m *model) dismissFileComp() {
	m.fileComp = fileCompState{}
	m.layout()
}

// applyFileList stores an async listing and, when a completion is waiting
// for it, fills the candidates. Mirrors applySlashSource.
func (m *model) applyFileList(msg fileListMsg) {
	if m.fileList == nil {
		m.fileList = map[string]fileListEntry{}
	}
	m.fileList[msg.cwd] = fileListEntry{files: msg.files, at: time.Now()}
	if !m.fileComp.active || !m.fileComp.loading || m.fileComp.cwd != msg.cwd {
		return
	}
	filtered := filterFileCandidates(msg.files, m.fileComp.token)
	if len(filtered) == 0 {
		m.dismissFileComp()
		return
	}
	if len(filtered) == 1 {
		m.insertFileRef(filtered[0])
		m.dismissFileComp()
		return
	}
	m.fileComp.loading = false
	m.fileComp.candidates = filtered
	m.fileComp.index = 0
	m.applyFileCandidate()
	m.layout()
}

// ---- view ------------------------------------------------------------------

// fileCompletionView renders the active @file completion row (the same
// single-line pattern as slash completion).
func (m model) fileCompletionView() string {
	state := m.fileComp
	if state.loading {
		return metaStyle.Render(truncate(t(m.loc, "file.completing", state.token), max(1, m.width-1)))
	}
	if !state.active || len(state.candidates) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range state.candidates {
		if i > 0 {
			b.WriteString("  ")
		}
		if i == state.index {
			b.WriteString(activeSlashStyle.Render(c))
		} else {
			b.WriteString(metaStyle.Render(c))
		}
	}
	return metaStyle.Render("▸ ") + truncate(b.String(), max(1, m.width-3))
}

// ---- send-time resolution --------------------------------------------------

// resolveFileRefs appends a footer naming the @-references that resolve to
// existing workspace files, so the agent knows they are workspace-relative
// paths to open with the read tool. The message text itself is untouched:
// a reference stays a pointer, never attached content.
func (m model) resolveFileRefs(content string) string {
	refs := fileRefsIn(content)
	if len(refs) == 0 {
		return content
	}
	seen := make(map[string]bool, len(refs))
	var resolved []string
	for _, r := range refs {
		if seen[r] {
			continue
		}
		seen[r] = true
		if fileRefExists(m.compCwd(), r) {
			resolved = append(resolved, "@"+r)
		}
	}
	if len(resolved) == 0 {
		return content
	}
	return content + "\n\n" + t(m.loc, "file.refs.note", strings.Join(resolved, ", "))
}

// fileRefsIn extracts @-referenced paths: plain tokens (@src/main.go) and
// backtick-quoted ones (@`path with spaces`), with trailing punctuation
// stripped so "@readme.md," still resolves.
func fileRefsIn(content string) []string {
	var out []string
	quoted := "@`([^`\\n]+)`"
	for _, m := range regexp.MustCompile(quoted).FindAllStringSubmatch(content, -1) {
		out = append(out, m[1])
	}
	rest := regexp.MustCompile(quoted).ReplaceAllString(content, " ")
	for _, m := range regexp.MustCompile("@([^\\s@`]+)").FindAllStringSubmatch(rest, -1) {
		out = append(out, m[1])
	}
	for i, r := range out {
		out[i] = strings.TrimRight(r, ".,;:!?)\"'`*")
	}
	return out
}

// fileRefExists reports whether ref resolves to a regular file inside the
// workspace (path traversal out of it is rejected).
func fileRefExists(cwd, ref string) bool {
	if cwd == "" || ref == "" {
		return false
	}
	rel := filepath.FromSlash(ref)
	if filepath.IsAbs(rel) {
		return false
	}
	p := filepath.Join(cwd, rel)
	if r, err := filepath.Rel(cwd, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
