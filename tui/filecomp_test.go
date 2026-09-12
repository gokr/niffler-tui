// filecomp tests — @file reference completion: token extraction, fuzzy
// candidate ranking, ignore-rule-aware listing, delimited insertion and
// send-time reference resolution.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileTokenAtCursor(t *testing.T) {
	runes := func(s string) []rune { return []rune(s) }
	// "look at @src/ma" — cursor at end: token extracted with prefix.
	prefix, token, _, ok := fileTokenAtCursor(runes("look at @src/ma"), 15)
	if !ok || prefix != "look at @" || token != "src/ma" {
		t.Fatalf("token: ok=%v prefix=%q token=%q", ok, prefix, token)
	}
	// Email-shaped words never trigger.
	if _, _, _, ok := fileTokenAtCursor(runes("mail user@host"), 14); ok {
		t.Fatal("email word must not trigger file completion")
	}
	// Lone "@" right after a space is a valid (empty) token.
	prefix, token, _, ok = fileTokenAtCursor(runes("see @"), 5)
	if !ok || prefix != "see @" || token != "" {
		t.Fatalf("bare @: ok=%v prefix=%q token=%q", ok, prefix, token)
	}
	// Word not starting with @ does not trigger.
	if _, _, _, ok := fileTokenAtCursor(runes("plain"), 5); ok {
		t.Fatal("plain word must not trigger")
	}
	// The @ must be preceded by start or whitespace.
	if _, _, _, ok := fileTokenAtCursor(runes("a@b"), 3); ok {
		t.Fatal("mid-word @ must not trigger")
	}
}

func TestFilterFileCandidates(t *testing.T) {
	files := []string{
		"cmd/root.go", "readme.md", "tui/readme.md", "tui/renderer.go",
		"docs/research/notes.md", "sdk/go/session.go",
	}
	// Basename prefix beats path prefix beats fuzzy subsequence.
	got := filterFileCandidates(files, "readme")
	if len(got) < 2 || got[0] != "readme.md" || got[1] != "tui/readme.md" {
		t.Fatalf("basename prefix must rank first: %v", got)
	}
	// Path prefix matches rank before scattered subsequence matches.
	got = filterFileCandidates(files, "tui")
	if len(got) < 2 || got[0] != "tui/readme.md" && got[0] != "tui/renderer.go" {
		t.Fatalf("path prefix must rank first: %v", got)
	}
	if !strings.HasPrefix(got[0], "tui/") || !strings.HasPrefix(got[1], "tui/") {
		t.Fatalf("both tui files first: %v", got)
	}
	// Case-insensitive.
	got = filterFileCandidates(files, "SDK")
	if len(got) == 0 || got[0] != "sdk/go/session.go" {
		t.Fatalf("case-insensitive match: %v", got)
	}
	// Empty token lists alphabetically.
	got = filterFileCandidates(files, "")
	if len(got) != len(files) || got[0] != "cmd/root.go" {
		t.Fatalf("empty token lists sorted: %v", got)
	}
}

func TestWalkFilesSkipsGeneratedTrees(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"a/a.txt", "var/state.log", "node_modules/pkg/index.js",
		".git/objects/x", ".hidden", "b.md", "build/out.bin",
	} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := walkFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := "a/a.txt b.md"
	if strings.Join(got, " ") != want {
		t.Fatalf("walker picked up ignored trees: got %v", got)
	}
}

func TestListWorkspaceFilesRespectsGitignore(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	root := t.TempDir()
	for _, f := range []string{"kept.txt", "var/generated.log", "sub/also.txt"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("var/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := listWorkspaceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "generated.log") {
		t.Fatalf("gitignored var/ must be skipped: %v", got)
	}
	if !strings.Contains(joined, "kept.txt") {
		t.Fatalf("tracked files must be listed: %v", got)
	}
}

func TestInsertFileRefDelimits(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("look at @")
	m.input.CursorEnd()
	m.fileComp = fileCompState{prefix: "look at @"}
	m.insertFileRef("tui/main.go")
	if got := m.input.Value(); got != "look at @tui/main.go " {
		t.Fatalf("plain ref: %q", got)
	}
	m.input.SetValue("open @")
	m.input.CursorEnd()
	m.fileComp = fileCompState{prefix: "open @"}
	m.insertFileRef("my notes/file.md")
	if got := m.input.Value(); got != "open @`my notes/file.md` " {
		t.Fatalf("spaced path must be backtick-quoted: %q", got)
	}
}

func TestHandleFileTabCompletesFromCache(t *testing.T) {
	tmp := t.TempDir()
	m := newTestModel()
	m.cwd = tmp
	m.fileList = map[string]fileListEntry{tmp: {files: []string{"a.md", "b.md", "sub/c.md"}, at: time.Now()}}
	m.input.SetValue("see @a")
	m.input.CursorEnd()
	m2, _ := m.handleFileTab(false)
	if got := m2.input.Value(); got != "see @a.md " {
		t.Fatalf("single candidate fills: %q", got)
	}
	m.input.SetValue("see @")
	m.input.CursorEnd()
	m2, cmd := m.handleFileTab(false)
	if !m2.fileComp.active || len(m2.fileComp.candidates) != 3 {
		t.Fatalf("multi candidates open the list: active=%v n=%d",
			m2.fileComp.active, len(m2.fileComp.candidates))
	}
	if cmd != nil {
		t.Fatal("cached listing must complete synchronously")
	}
	// Tab on a non-@token falls through untouched.
	m.input.SetValue("plain text")
	m.input.CursorEnd()
	m3, cmd := m.handleFileTab(false)
	if cmd != nil || m3.fileComp.active || m3.input.Value() != "plain text" {
		t.Fatal("non-@token must fall through")
	}
}

func TestHandleFileTabFetchesAsync(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "only.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	m.cwd = tmp
	m.input.SetValue("@on")
	m.input.CursorEnd()
	m2, cmd := m.handleFileTab(false)
	if cmd == nil || !m2.fileComp.loading {
		t.Fatal("cache miss must fetch async in a loading state")
	}
	// The fetched listing fills the waiting completion.
	m2.applyFileList(fileListMsg{cwd: tmp, files: []string{"only.md"}})
	if got := m2.input.Value(); got != "@only.md " {
		t.Fatalf("async fill: %q", got)
	}
	if m2.fileComp.active {
		t.Fatal("single candidate must close the list")
	}
}

func TestResolveFileRefs(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "existing.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "my notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "my notes", "idea.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	m.cwd = tmp

	got := m.resolveFileRefs("check @existing.md and @missing.md please")
	footer := got[strings.LastIndex(got, "\n\n")+2:]
	if !strings.Contains(footer, "@existing.md") || strings.Contains(footer, "@missing.md") {
		t.Fatalf("footer must list only existing refs: %q", footer)
	}
	if !strings.Contains(footer, "read tool") {
		t.Fatalf("footer must point at the read tool: %q", footer)
	}
	if !strings.HasPrefix(got, "check @existing.md and @missing.md please") {
		t.Fatalf("message text must stay untouched: %q", got)
	}
	// Trailing punctuation still resolves; the message text stays intact.
	got = m.resolveFileRefs("see @existing.md, then act")
	if !strings.Contains(got, "@existing.md") || !strings.HasPrefix(got, "see @existing.md, then act") {
		t.Fatalf("punctuated ref: %q", got)
	}
	// Backtick-quoted references (paths with spaces) resolve.
	got = m.resolveFileRefs("plan in @`my notes/idea.md`")
	if !strings.Contains(got, "@my notes/idea.md") {
		t.Fatalf("backtick ref: %q", got)
	}
	// No references: message untouched.
	if got := m.resolveFileRefs("plain message"); got != "plain message" {
		t.Fatalf("no refs must not append a footer: %q", got)
	}
	// Traversal out of the workspace never resolves.
	if got := m.resolveFileRefs("@../secrets.txt"); strings.Contains(got, "file references") {
		t.Fatalf("traversal must not resolve: %q", got)
	}
}
