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
	err      string
	saving   bool
}

func newProviderForm(template *catalogProvider, runtime runtimeResolution, width int) providerForm {
	prompts := []string{
		"Nickname   ",
		"Base URL   ",
		"API key    ",
		"Catalog ID ",
		"Model       ",
		"Context     ",
	}
	placeholders := []string{
		"work-openrouter",
		"https://openrouter.ai/api/v1",
		"required",
		"models.dev provider id (optional)",
		"provider-specific model id",
		"0 = auto",
	}
	var form providerForm
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
		form.template = "Custom OpenAI-compatible"
	}
	form.focusField(0)
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
	if values.Nickname == "" {
		return providerFormValues{}, fmt.Errorf("nickname is required")
	}
	if values.APIKey == "" {
		return providerFormValues{}, fmt.Errorf("API key is required (use a placeholder for a keyless local endpoint)")
	}
	if values.BaseURL == "" {
		return providerFormValues{}, fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(values.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return providerFormValues{}, fmt.Errorf("base URL must be an http(s) URL")
	}
	if values.Model == "" {
		return providerFormValues{}, fmt.Errorf("model is required")
	}
	contextText := strings.TrimSpace(f.inputs[providerFieldContext].Value())
	if contextText != "" && contextText != "0" {
		contextSize, err := strconv.Atoi(contextText)
		if err != nil || contextSize < 0 {
			return providerFormValues{}, fmt.Errorf("context must be a non-negative integer")
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
	title := "Connect provider — " + f.template
	out.WriteString(headerStyle.Render(title))
	out.WriteString("\n\n")
	out.WriteString(metaStyle.Render("OpenAI-compatible endpoint; credentials are stored by Niffler and never added to chat history."))
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
		out.WriteString(metaStyle.Render("saving provider…"))
	} else {
		out.WriteString(metaStyle.Render("tab/shift+tab: field  •  enter: next/save  •  ctrl+s: save  •  esc: cancel"))
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1, 2).
		Width(max(24, min(width-6, 88))).
		Render(out.String())
	return panel
}
