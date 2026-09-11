// Card background: a shaded tool run must keep its background across the
// whole line, including the call line of a grouped (multi-call) run. Each
// fragment of a line (status glyph, "$ cmd" head, body line) is styled on its
// own and ends with a full SGR reset, and lipgloss does not re-emit the
// background after such a reset — so without an explicit repaint the shading
// stopped right after the command text instead of running to the end.
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// shellArgs builds a bash call's argument object for a command.
func shellArgs(t *testing.T, command string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// bgOn reports whether s currently has the default theme's card background
// (colour 236, the 16-colour form toolBg uses) turned on at its end.
const defaultCardBg = "\x1b[48;5;236m"

func cardRun(t *testing.T, calls int) *toolRun {
	t.Helper()
	run := &toolRun{}
	for i := 0; i < calls; i++ {
		cmd := "ls"
		if i > 0 {
			cmd = "pwd"
		}
		run.calls = append(run.calls, toolCall{
			name:   "bash",
			args:   shellArgs(t, cmd),
			result: shellResult(t, "a\nb"),
		})
	}
	return run
}

// shadedCells replays the SGR stream of a single line and returns, per visible
// rune, whether the card background was active. This is what the terminal
// shows, unlike a substring check that a stray reset could satisfy by
// accident. Split the input on newlines first: a line end legitimately drops
// the background, and the next line re-arms it.
func shadedCells(line string) []bool {
	var (
		out   []bool
		shade bool
	)
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			end := strings.IndexByte(line[i:], 'm')
			if end < 0 {
				break
			}
			params := strings.Split(line[i+2:i+end], ";")
			for p := 0; p < len(params); p++ {
				switch params[p] {
				case "", "0", "49":
					shade = false
				case "38", "48", "58":
					if params[p] == "48" {
						shade = true
					}
					// RGB/indexed colour values aren't SGR attributes:
					// a zero channel must not be mistaken for a reset.
					if p+1 < len(params) {
						switch params[p+1] {
						case "5":
							p += 2
						case "2":
							p += 4
						}
					}
				}
			}
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		_ = r
		out = append(out, shade)
		i += size
	}
	return out
}

// TestCardBackgroundSpansWholeLine is the regression test: with card shading
// on, every cell of every rendered card line is backgrounded — for a single
// call and for a grouped run alike.
func TestCardBackgroundSpansWholeLine(t *testing.T) {
	applyTheme(ThemeDefault)
	for _, calls := range []int{1, 2, 3} {
		m := newTestModel()
		m.loc = LocaleEN
		m.toolCards = true
		m.toolLevel = toolMedium

		rendered := m.renderToolRun(cardRun(t, calls), detailPreview)
		for i, line := range strings.Split(rendered, "\n") {
			if ansi.StringWidth(line) == 0 {
				continue
			}
			cells := shadedCells(line)
			for j, shaded := range cells {
				if !shaded {
					t.Fatalf("calls=%d line %d: cell %d is unshaded\n%q",
						calls, i, j, line)
				}
			}
		}
	}
}

// TestCardBackgroundRepaintsAfterReset pins the mechanism: the background
// sequence follows every reset inside a grouped call line.
func TestCardBackgroundRepaintsAfterReset(t *testing.T) {
	applyTheme(ThemeDefault)
	m := newTestModel()
	m.loc = LocaleEN
	m.toolCards = true
	m.toolLevel = toolMedium

	lines := strings.Split(m.renderToolRun(cardRun(t, 2), detailPreview), "\n")
	callLine := ""
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line), "$ ls") {
			callLine = line
			break
		}
	}
	if callLine == "" {
		t.Fatalf("no bash call line in\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(callLine, defaultCardBg) {
		t.Fatalf("call line lost the card background: %q", callLine)
	}
	// The background must be re-armed after the reset that ends the "$ ls"
	// head, otherwise the remainder of the line renders unshaded.
	idx := strings.Index(callLine, "$ ls")
	if idx < 0 {
		t.Fatalf("no command text: %q", callLine)
	}
	tail := callLine[idx:]
	reset := strings.Index(tail, "\x1b[m")
	if reset < 0 {
		t.Fatalf("command head not reset-terminated: %q", tail)
	}
	if !strings.HasPrefix(tail[reset:], "\x1b[m"+defaultCardBg) {
		t.Fatalf("background not re-applied after the head reset: %q", tail[reset:])
	}
}

// TestCardsOffLeavesLinesUnshaded guards /cards off: no background at all.
func TestCardsOffLeavesLinesUnshaded(t *testing.T) {
	applyTheme(ThemeDefault)
	m := newTestModel()
	m.loc = LocaleEN
	m.toolCards = false
	m.toolLevel = toolMedium

	rendered := m.renderToolRun(cardRun(t, 2), detailPreview)
	if strings.Contains(rendered, "48;") {
		t.Fatalf("/cards off must not shade card lines:\n%q", rendered)
	}
}

// TestBriefCardKeepsBackground covers the collapsed one-line form.
func TestBriefCardKeepsBackground(t *testing.T) {
	applyTheme(ThemeDefault)
	m := newTestModel()
	m.loc = LocaleEN
	m.toolCards = true
	m.toolLevel = toolBrief

	rendered := m.renderToolRun(cardRun(t, 2), detailBrief)
	if !strings.Contains(rendered, defaultCardBg) {
		t.Fatalf("brief card lost its background: %q", rendered)
	}
	for i, line := range strings.Split(rendered, "\n") {
		for j, shaded := range shadedCells(line) {
			if !shaded {
				t.Fatalf("brief card line %d cell %d unshaded: %q", i, j, line)
			}
		}
	}
}
