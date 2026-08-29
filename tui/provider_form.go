package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	providerFieldNickname = iota
	providerFieldBaseURL
	providerFieldAPIKey
	providerFieldCatalog
	providerFieldModel
	providerFieldContext
	providerFieldCount
)

type providerForm struct {
	inputs   [providerFieldCount]textinput.Model
	focus    int
	template string
	edit     bool // editing an existing provider (nickname locked, key optional)
	err      string
	saving   bool
	loc      Locale
}

func newProviderForm(template *catalogProvider, runtime runtimeResolution, width int, loc Locale) providerForm {
	form := newProviderFormFields(width, loc)
	if template != nil {
		form.template = template.Name
		if form.template == "" {
			form.template = template.ID
		}
		form.inputs[providerFieldNickname].SetValue(template.ID)
		form.inputs[providerFieldBaseURL].SetValue(template.API)
		form.inputs[providerFieldCatalog].SetValue(template.ID)
		if runtime.Catalog == template.ID {
			form.inputs[providerFieldModel].SetValue(runtime.Model)
		}
	} else {
		form.template = t(loc, "selector.customProvider")
	}
	form.focusField(0)
	return form
}

// newEditProviderForm builds a form pre-filled from an existing provider so the
// user can edit its non-secret settings (and optionally rotate the key) without
// re-typing a credential. The nickname is immutable (the backend updates by it).
func newEditProviderForm(p providerSummary, width int, loc Locale) providerForm {
	form := newProviderFormFields(width, loc)
	form.edit = true
	form.template = p.Nickname
	form.inputs[providerFieldNickname].SetValue(p.Nickname)
	form.inputs[providerFieldBaseURL].SetValue(p.BaseURL)
	form.inputs[providerFieldCatalog].SetValue(p.Catalog)
	form.inputs[providerFieldModel].SetValue(p.Model)
	if p.Context > 0 {
		form.inputs[providerFieldContext].SetValue(strconv.Itoa(p.Context))
	}
	form.inputs[providerFieldAPIKey].Placeholder = t(loc, "form.leaveBlankToKeep")
	form.inputs[providerFieldNickname].Prompt = t(loc, "form.prompt.nickname") + " "
	form.focusField(providerFieldBaseURL)
	return form
}

// newProviderFormFields allocates the raw text inputs shared by the add and
// edit forms (masked key field, width, char limits).
func newProviderFormFields(width int, loc Locale) providerForm {
	prompts := []string{
		t(loc, "form.prompt.nickname"),
		t(loc, "form.prompt.baseUrl"),
		t(loc, "form.prompt.apiKey"),
		t(loc, "form.prompt.catalog"),
		t(loc, "form.prompt.model"),
		t(loc, "form.prompt.context"),
	}
	placeholders := []string{
		"work-openrouter",
		"https://openrouter.ai/api/v1",
		t(loc, "form.placeholder.apiKey"),
		t(loc, "form.placeholder.catalog"),
		t(loc, "form.placeholder.model"),
		t(loc, "form.placeholder.context"),
	}
	var form providerForm
	form.loc = loc
	for i := range form.inputs {
		input := textinput.New()
		input.Prompt = prompts[i]
		input.Placeholder = placeholders[i]
		input.CharLimit = 4096
		input.SetWidth(providerInputWidth(width))
		form.inputs[i] = input
	}
	form.inputs[providerFieldAPIKey].EchoMode = textinput.EchoPassword
	form.inputs[providerFieldAPIKey].EchoCharacter = '•'
	form.inputs[providerFieldContext].CharLimit = 12
	return form
}

func (f *providerForm) focusField(index int) tea.Cmd {
	if index < 0 {
		index = providerFieldCount - 1
	}
	if index >= providerFieldCount {
		index = 0
	}
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.focus = index
	return f.inputs[index].Focus()
}

func (f *providerForm) nextField(delta int) tea.Cmd {
	return f.focusField((f.focus + delta + providerFieldCount) % providerFieldCount)
}

func providerInputWidth(width int) int {
	return max(12, min(80, width-12))
}

func (f *providerForm) setWidth(width int) {
	for i := range f.inputs {
		f.inputs[i].SetWidth(providerInputWidth(width))
	}
}

func (f providerForm) update(msg tea.Msg) (providerForm, tea.Cmd) {
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return f, cmd
}

func (f providerForm) values() (providerFormValues, error) {
	values := providerFormValues{
		Nickname: strings.TrimSpace(f.inputs[providerFieldNickname].Value()),
		BaseURL:  strings.TrimSpace(f.inputs[providerFieldBaseURL].Value()),
		APIKey:   strings.TrimSpace(f.inputs[providerFieldAPIKey].Value()),
		Catalog:  strings.TrimSpace(f.inputs[providerFieldCatalog].Value()),
		Model:    strings.TrimSpace(f.inputs[providerFieldModel].Value()),
	}
	if values.Nickname == "" && !f.edit {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.nicknameRequired"))
	}
	if values.APIKey == "" && !f.edit {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.apiKeyRequired"))
	}
	if values.BaseURL == "" {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.baseUrlRequired"))
	}
	parsed, err := url.Parse(values.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.baseUrlInvalid"))
	}
	if values.Model == "" {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.modelRequired"))
	}
	contextText := strings.TrimSpace(f.inputs[providerFieldContext].Value())
	if contextText != "" && contextText != "0" {
		contextSize, err := strconv.Atoi(contextText)
		if err != nil || contextSize < 0 {
			return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.contextInvalid"))
		}
		values.Context = contextSize
	}
	return values, nil
}

func (f *providerForm) clearSecret() {
	f.inputs[providerFieldAPIKey].SetValue("")
}

func (f providerForm) view(width int) string {
	var out strings.Builder
	title := t(f.loc, "form.connectTitle", f.template)
	if f.edit {
		title = t(f.loc, "form.editTitle", f.template)
	}
	out.WriteString(headerStyle.Render(title))
	out.WriteString("\n\n")
	meta := t(f.loc, "form.connectMeta")
	if f.edit {
		meta = t(f.loc, "form.editMeta", f.template)
	}
	out.WriteString(metaStyle.Render(meta))
	out.WriteString("\n\n")
	for i := range f.inputs {
		out.WriteString(f.inputs[i].View())
		out.WriteByte('\n')
	}
	if f.err != "" {
		out.WriteByte('\n')
		out.WriteString(errorStyle.Render(f.err))
		out.WriteByte('\n')
	}
	if f.saving {
		out.WriteString(metaStyle.Render(t(f.loc, "form.saving")))
	} else {
		out.WriteString(metaStyle.Render(t(f.loc, "form.keys")))
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1, 2).
		Width(max(24, min(width-6, 88))).
		Render(out.String())
	return panel
}
