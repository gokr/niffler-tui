package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// lspForm mirrors the /mcp form for language-server registry entries.
// Three fields: name, launch command (split on whitespace — LSP server
// commands are simple argv), and the extension map as a comma-separated
// list (".go, .nim" — the LSP language id is derived by stripping the
// dot, which matches every default server; exotic language ids can be set
// by editing the registry JSON directly). Saving always writes a user
// registry entry: adding a new server, or overriding a built-in of the
// same name (docs/OCTOFRIEND-STEAL.md — language support is data).
const (
	lspFieldName = iota
	lspFieldCommand
	lspFieldExtensions
	lspFieldCount
)

type lspForm struct {
	inputs [lspFieldCount]textinput.Model
	focus  int
	name   string // locked when editing an existing entry
	edit   bool
	isUser bool // editing a user entry (remove applies); false = built-in override
	err    string
	saving bool
	loc    Locale
}

func newLspForm(width int, loc Locale) lspForm {
	form := newLspFormFields(width, loc)
	form.focusField(0)
	return form
}

// newEditLspForm prefills from a configured server. Name is locked — the
// registry is keyed by name.
func newEditLspForm(s lspServerSummary, width int, loc Locale) lspForm {
	form := newLspFormFields(width, loc)
	form.inputs[lspFieldName].SetValue(s.Name)
	form.inputs[lspFieldCommand].SetValue(strings.Join(s.Command, " "))
	exts := make([]string, 0, len(s.Extensions))
	for ext := range s.Extensions {
		exts = append(exts, ext)
	}
	sortStrings(exts)
	form.inputs[lspFieldExtensions].SetValue(strings.Join(exts, ", "))
	form.name = s.Name
	form.edit = true
	form.isUser = s.Source == "user"
	form.focusField(1) // name is locked; start on the command
	return form
}

func newLspFormFields(width int, loc Locale) lspForm {
	w := lspInputWidth(width)
	form := lspForm{loc: loc}
	placeholders := [lspFieldCount]string{
		t(loc, "lsp.form.namePlaceholder"),
		t(loc, "lsp.form.commandPlaceholder"),
		t(loc, "lsp.form.extensionsPlaceholder"),
	}
	for i := 0; i < lspFieldCount; i++ {
		input := textinput.New()
		input.Placeholder = placeholders[i]
		input.SetWidth(w)
		input.CharLimit = 512
		form.inputs[i] = input
	}
	return form
}

func lspInputWidth(width int) int {
	w := width - 16
	if w < 24 {
		w = 24
	}
	if w > 72 {
		w = 72
	}
	return w
}

func (f *lspForm) focusField(index int) tea.Cmd {
	if f.edit && index == lspFieldName {
		index = lspFieldCommand
	}
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.focus = index
	return f.inputs[index].Focus()
}

func (f *lspForm) nextField(delta int) tea.Cmd {
	for {
		f.focus += delta
		if f.focus < 0 {
			f.focus = lspFieldCount - 1
		}
		if f.focus >= lspFieldCount {
			f.focus = 0
		}
		if !(f.edit && f.focus == lspFieldName) {
			break
		}
	}
	return f.inputs[f.focus].Focus()
}

func (f *lspForm) update(msg tea.Msg) (lspForm, tea.Cmd) {
	var cmds []tea.Cmd
	for i := range f.inputs {
		var cmd tea.Cmd
		f.inputs[i], cmd = f.inputs[i].Update(msg)
		cmds = append(cmds, cmd)
	}
	return *f, tea.Batch(cmds...)
}

// values validates the form. Extensions accept ".go, .nims" — whitespace
// and semicolons too; the LSP language id is the extension minus its dot.
func (f lspForm) values() (lspFormValues, error) {
	name := strings.TrimSpace(f.inputs[lspFieldName].Value())
	if name == "" {
		return lspFormValues{}, fmt.Errorf("%s", t(f.loc, "lsp.err.nameRequired"))
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return lspFormValues{}, fmt.Errorf("%s", t(f.loc, "lsp.err.nameChars"))
		}
	}
	command := strings.TrimSpace(f.inputs[lspFieldCommand].Value())
	if command == "" {
		return lspFormValues{}, fmt.Errorf("%s", t(f.loc, "lsp.err.commandRequired"))
	}
	exts := map[string]string{}
	for _, raw := range strings.FieldsFunc(f.inputs[lspFieldExtensions].Value(),
		func(r rune) bool { return r == ',' || r == ';' }) {
		ext := strings.TrimSpace(raw)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") || len(ext) < 2 {
			return lspFormValues{}, fmt.Errorf("%s", t(f.loc, "lsp.err.extensionDot", ext))
		}
		ext = strings.ToLower(ext)
		exts[ext] = strings.TrimPrefix(ext, ".")
	}
	if len(exts) == 0 {
		return lspFormValues{}, fmt.Errorf("%s", t(f.loc, "lsp.err.extensionsRequired"))
	}
	return lspFormValues{Name: name, Command: command, Extensions: exts}, nil
}

// view renders the form: locked names show as read-only text.
func (f lspForm) view(width int) string {
	var b strings.Builder
	title := t(f.loc, "lsp.form.titleAdd")
	if f.edit {
		title = t(f.loc, "lsp.form.titleEdit")
		if !f.isUser {
			title += " " + t(f.loc, "lsp.form.overrideNote")
		}
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
	b.WriteString("\n\n")
	labels := [lspFieldCount]string{
		t(f.loc, "lsp.form.name"),
		t(f.loc, "lsp.form.command"),
		t(f.loc, "lsp.form.extensions"),
	}
	for i := 0; i < lspFieldCount; i++ {
		if f.edit && i == lspFieldName {
			b.WriteString(lipgloss.NewStyle().Faint(true).Render(
				labels[i] + ": " + f.inputs[i].Value() + " " + t(f.loc, "lsp.form.locked")))
			b.WriteString("\n")
			continue
		}
		b.WriteString(labels[i] + ": " + f.inputs[i].View())
		b.WriteString("\n")
	}
	b.WriteString("\n" + t(f.loc, "lsp.form.help"))
	if f.err != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(f.err))
	}
	if f.saving {
		b.WriteString("\n" + t(f.loc, "lsp.form.saving"))
	}
	return b.String()
}

// sortStrings keeps this file free of a sort import decision elsewhere.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
