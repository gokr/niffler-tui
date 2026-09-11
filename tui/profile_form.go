package main

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Creation is client-side control state only: it never alters an existing
// conversation's frozen prefix. Saving selects the profile for future sessions.
type profileForm struct {
	inputs [3]textinput.Model
	focus  int
	loc    Locale
	err    string
	saving bool
}

type profileDraft struct {
	name  string
	tools []string
	note  string
}

type profileSavedMsg struct {
	name string
	err  error
}

func newProfileForm(width int, loc Locale) profileForm {
	f := profileForm{loc: loc}
	for i, key := range []string{"name", "selectors", "note"} {
		f.inputs[i] = textinput.New()
		f.inputs[i].Prompt = t(loc, "profileForm."+key) + ": "
		f.inputs[i].CharLimit = 4096
	}
	f.inputs[1].Placeholder = "git, grep, edit.read, -bash"
	f.setWidth(width)
	return f
}

func (f *profileForm) setWidth(width int) {
	for i := range f.inputs {
		f.inputs[i].SetWidth(max(8, width-20))
	}
}

func (f *profileForm) focusField(delta int) tea.Cmd {
	f.inputs[f.focus].Blur()
	f.focus = (f.focus + delta + len(f.inputs)) % len(f.inputs)
	return f.inputs[f.focus].Focus()
}

func (f profileForm) values() (profileDraft, error) {
	d := profileDraft{
		name:  strings.TrimSpace(f.inputs[0].Value()),
		tools: strings.FieldsFunc(f.inputs[1].Value(), func(r rune) bool { return r == ',' || unicode.IsSpace(r) }),
		note:  strings.TrimSpace(f.inputs[2].Value()),
	}
	if d.name == "" || d.name == "default" || strings.ContainsAny(d.name, " \t\r\n") {
		return d, fmt.Errorf("%s", t(f.loc, "profileForm.badName"))
	}
	if d.tools == nil {
		d.tools = []string{} // Empty means the base direct set, not JSON null.
	}
	return d, nil
}

func (f profileForm) view(width int) string {
	lines := []string{t(f.loc, "profileForm.new"), t(f.loc, "profileForm.hint"), ""}
	for _, input := range f.inputs {
		lines = append(lines, input.View())
	}
	lines = append(lines, "", t(f.loc, "profileForm.keys"))
	if f.saving {
		lines = append(lines, t(f.loc, "profileForm.saving"))
	}
	if f.err != "" {
		lines = append(lines, errorStyle.Render(f.err))
	}
	return lipgloss.NewStyle().Width(max(20, width-1)).Render(strings.Join(lines, "\n"))
}

func (m model) updateProfileForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.profileForm.saving {
		return m, nil // Do not change the draft while its write is in flight.
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.profileForm = profileForm{}
			m.openProfileSelector()
			return m, profilesCmd(m.comp)
		case "tab", "down":
			return m, m.profileForm.focusField(1)
		case "shift+tab", "up":
			return m, m.profileForm.focusField(-1)
		case "enter":
			if m.profileForm.focus != 2 {
				return m, m.profileForm.focusField(1)
			}
			return m.submitProfileForm()
		case "ctrl+s":
			return m.submitProfileForm()
		}
	}
	var cmd tea.Cmd
	m.profileForm.inputs[m.profileForm.focus], cmd = m.profileForm.inputs[m.profileForm.focus].Update(msg)
	return m, cmd
}

func (m model) submitProfileForm() (tea.Model, tea.Cmd) {
	if m.profileForm.saving {
		return m, nil
	}
	draft, err := m.profileForm.values()
	if err != nil {
		m.profileForm.err = err.Error()
		return m, nil
	}
	if !m.connected {
		m.profileForm.err = t(m.loc, "note.notConnected")
		return m, nil
	}
	m.profileForm.err = ""
	m.profileForm.saving = true
	comp, loc := m.comp, m.loc
	return m, func() tea.Msg {
		err := createProfile(draft, loc, func(tool string, args map[string]any, out any) error {
			return requestInto(comp, "core", tool, args, out)
		})
		return profileSavedMsg{name: draft.name, err: err}
	}
}

// createProfile validates without temporary store writes. The list-before-save
// duplicate check is best effort: core's save is an upsert, not create-if-absent.
func createProfile(d profileDraft, loc Locale, request func(string, map[string]any, any) error) error {
	var existing profilesResponse
	if err := request("profile", map[string]any{"op": "list"}, &existing); err != nil {
		return err
	}
	for _, p := range existing.Profiles {
		if p.Name == d.name {
			return fmt.Errorf("%s", t(loc, "profileForm.exists", d.name))
		}
	}
	var status struct {
		Components []visibilityComponent `json:"components"`
	}
	if err := request("status", map[string]any{}, &status); err != nil {
		return err
	}
	if missing := missingProfileSelectors(d.tools, status.Components); len(missing) > 0 {
		return fmt.Errorf("%s", t(loc, "profileForm.missing", strings.Join(missing, ", ")))
	}
	var saved okResponse
	if err := request("profile", map[string]any{"op": "save", "name": d.name, "tools": d.tools, "note": d.note}, &saved); err != nil {
		return err
	}
	if !saved.OK {
		return fmt.Errorf("%s", t(loc, "profileForm.failed"))
	}
	return nil
}

// Positive selectors name a component or component.tool; exclusions name a
// bare tool. Validate both against non-hidden live tools, catching typos before
// core's permissive resolver silently skips them. Registered-but-stopped
// components stay nameable — a profile resolves at a future conversation's
// first turn, when the component may well be back. This is typo-catching,
// not a sandbox.
func missingProfileSelectors(selectors []string, components []visibilityComponent) []string {
	valid := map[string]bool{}
	for _, c := range components {
		valid[c.Name] = true
		for _, tool := range c.Tools {
			if tool.Schema.Harness.Hidden {
				continue
			}
			valid[c.Name+"."+tool.Name] = true
			valid["-"+tool.Name] = true
		}
	}
	var missing []string
	for _, sel := range selectors {
		if !valid[sel] {
			missing = append(missing, sel)
		}
	}
	return missing
}

func (m model) applyProfileSaved(msg profileSavedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeProfileForm || !m.profileForm.saving {
		return m, nil
	}
	m.profileForm.saving = false
	if msg.err != nil {
		m.profileForm.err = msg.err.Error()
		return m, nil
	}
	m.toolProfile = msg.name
	m.profileForm = profileForm{}
	m.contextNote = t(m.loc, "profile.selected", msg.name)
	m.openProfileSelector()
	return m, profilesCmd(m.comp)
}
