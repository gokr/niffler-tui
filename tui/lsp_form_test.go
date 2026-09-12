// The /lsp form: registry entries are data (docs/OCTOFRIEND-STEAL.md), so
// the form is the whole "add a language" flow — name, launch command, and a
// comma-separated extension list whose language id is the extension minus
// its dot. Validation errors are the contract the component would otherwise
// surface as [E_BAD_SHAPE]/[E_LSP_REGISTRY]; catching them in the form keeps
// the approval-gated save from being spent on a refused write.
package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

func lspFormValuesFor(t *testing.T, name, command, extensions string) (lspFormValues, error) {
	t.Helper()
	form := newLspForm(100, LocaleEN)
	form.inputs[lspFieldName].SetValue(name)
	form.inputs[lspFieldCommand].SetValue(command)
	form.inputs[lspFieldExtensions].SetValue(extensions)
	return form.values()
}

func TestLspFormValuesHappyPath(t *testing.T) {
	values, err := lspFormValuesFor(t, "gopls", "gopls", ".go")
	if err != nil {
		t.Fatalf("valid form rejected: %v", err)
	}
	if values.Name != "gopls" || values.Command != "gopls" {
		t.Fatalf("name/command lost: %+v", values)
	}
	if values.Extensions[".go"] != "go" {
		t.Fatalf("language id must be the extension sans dot: %+v", values.Extensions)
	}
}

func TestLspFormValuesMultipleExtensions(t *testing.T) {
	values, err := lspFormValuesFor(t, "ts", "typescript-language-server --stdio",
		".ts, .tsx;.mts  ,  .js")
	if err != nil {
		t.Fatalf("valid multi-extension form rejected: %v", err)
	}
	// whitespace/semicolon separators accepted; keys lowercased; ids derived
	for _, ext := range []string{".ts", ".tsx", ".mts", ".js"} {
		want := strings.TrimPrefix(ext, ".")
		if values.Extensions[ext] != want {
			t.Fatalf("extension %s missing or wrong: %+v", ext, values.Extensions)
		}
	}
}

func TestLspFormValuesRejectsBadName(t *testing.T) {
	for _, name := range []string{"", "Gopls", "has space", "under_score"} {
		if _, err := lspFormValuesFor(t, name, "gopls", ".go"); err == nil {
			t.Fatalf("name %q must be rejected (lowercase letters, digits, hyphens)", name)
		}
	}
}

func TestLspFormValuesRejectsMissingPieces(t *testing.T) {
	if _, err := lspFormValuesFor(t, "gopls", "", ".go"); err == nil {
		t.Fatal("empty command must be rejected")
	}
	if _, err := lspFormValuesFor(t, "gopls", "gopls", ""); err == nil {
		t.Fatal("empty extensions must be rejected")
	}
	if _, err := lspFormValuesFor(t, "gopls", "gopls", "go"); err == nil {
		t.Fatal("extension without the leading dot must be rejected")
	}
}

// The edit form locks the name (the registry is keyed by name) and skips it
// in tab order; saving a built-in is allowed — it writes a user override.
func TestLspEditFormLocksName(t *testing.T) {
	server := lspServerSummary{
		Name:       "gopls",
		Command:    []string{"gopls"},
		Extensions: map[string]string{".go": "go"},
		Source:     "builtin",
	}
	form := newEditLspForm(server, 100, LocaleEN)
	if got := form.inputs[lspFieldName].Value(); got != "gopls" {
		t.Fatalf("edit form must prefill the name, got %q", got)
	}
	if got := form.inputs[lspFieldCommand].Value(); got != "gopls" {
		t.Fatalf("edit form must prefill the command, got %q", got)
	}
	if got := form.inputs[lspFieldExtensions].Value(); got != ".go" {
		t.Fatalf("edit form must prefill the extensions, got %q", got)
	}
	if form.focus == lspFieldName {
		t.Fatal("edit form must start focus past the locked name")
	}
	// nextField from the last field wraps around the locked name
	form.focus = lspFieldExtensions
	_ = form.nextField(1)
	if form.focus == lspFieldName {
		t.Fatal("tab order must skip the locked name field")
	}
}

// Regression: update() fanned every keystroke out to ALL inputs, and
// bubbles textinput applies key messages regardless of focus — typing into
// the command field also typed into the name and extensions fields. Keys
// must reach only the focused input.
func TestLspFormUpdateFeedsOnlyFocusedField(t *testing.T) {
	form := newLspForm(100, LocaleEN)
	form.focusField(lspFieldCommand) // focus the command field
	form.inputs[lspFieldCommand], _ = form.inputs[lspFieldCommand].Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	form, _ = form.update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if got := form.inputs[lspFieldCommand].Value(); got != "xy" {
		t.Fatalf("focused field must receive keystrokes, got %q", got)
	}
	if got := form.inputs[lspFieldName].Value(); got != "" {
		t.Fatalf("unfocused name field must stay empty, got %q", got)
	}
	if got := form.inputs[lspFieldExtensions].Value(); got != "" {
		t.Fatalf("unfocused extensions field must stay empty, got %q", got)
	}
}

// The /lsp selector must be a rendered mode: it joins the selector view
// switch and the resize refit (a missing case rendered a bare empty screen).
func TestLspModeRendersSelector(t *testing.T) {
	// Regression: /lsp switched modes and handled keys but was never wired
	// into View() — the screen showed only the header (the same gap
	// TestMcpModesRender pinned for /mcp). The selector, its entries and the
	// footer hint must all render.
	m := newTestModel()
	m.loc = LocaleEN
	m.width, m.height = 100, 30
	m.lspServers = []lspServerSummary{{
		Name:       "gopls",
		Command:    []string{"gopls"},
		Extensions: map[string]string{".go": "go"},
		Source:     "builtin",
	}}
	m.openLspServerSelector()
	if m.mode != modeLsp {
		t.Fatalf("openLspServerSelector must set modeLsp, got %v", m.mode)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "gopls") {
		t.Fatalf("modeLsp view missing selector content:\n%s", view)
	}
	if !strings.Contains(view, "Language servers") {
		t.Fatalf("modeLsp view missing the selector title:\n%s", view)
	}
	if !strings.Contains(view, "d: remove") { // footer.lsp
		t.Fatalf("modeLsp view missing the lsp footer:\n%s", view)
	}
}
