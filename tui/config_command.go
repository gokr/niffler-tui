package main

// The /config command: a browser over every declared configuration domain,
// then a field-by-field editor for one of them.
//
// Two levels, because a domain can have many properties and a flat list of
// everything would be unusable:
//
//	/config                  → domains (config_list)
//	/config <domain>         → fields of that domain (config_get)
//
// Editing is deliberate rather than inline: a field is chosen, a new value is
// typed, and only then is a patch sent with expectRev. That is the same
// "confirm before mutating" discipline as the rest of the client, and it keeps
// `esc` meaning "abandon" at every point.

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// configDomainsMsg carries config_list.
type configDomainsMsg struct {
	Domains []configDomain
	Err     error
}

// configDetailMsg carries config_get for one domain.
type configDetailMsg struct {
	Domain string
	Detail configDetail
	Fields []configField
	Err    error
}

// configAppliedMsg reports a completed patch/reset.
type configAppliedMsg struct {
	Domain string
	Rev    int
	Label  string
	Err    error
}

func configDomainsCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		domains, err := configDomains(comp)
		return configDomainsMsg{Domains: domains, Err: err}
	}
}

func configDetailCmd(comp *sdk.Component, domain string) tea.Cmd {
	return func() tea.Msg {
		detail, err := configDetailFor(comp, domain)
		if err != nil {
			return configDetailMsg{Domain: domain, Detail: detail, Err: err}
		}
		return configDetailMsg{Domain: domain, Detail: detail,
			Fields: configFields(detail)}
	}
}

func configApplyCmd(comp *sdk.Component, domain string, patch map[string]any,
	expectRev int, label string) tea.Cmd {
	return func() tea.Msg {
		result, err := configApply(comp, domain, patch, expectRev)
		if err != nil {
			return configAppliedMsg{Domain: domain, Err: err}
		}
		return configAppliedMsg{Domain: domain, Rev: result.Rev, Label: label}
	}
}

func configResetCmd(comp *sdk.Component, domain string, properties []string,
	expectRev int, label string) tea.Cmd {
	return func() tea.Msg {
		result, err := configReset(comp, domain, properties, expectRev)
		if err != nil {
			return configAppliedMsg{Domain: domain, Err: err}
		}
		return configAppliedMsg{Domain: domain, Rev: result.Rev, Label: label}
	}
}

// localConfig is the /config builtin.
func localConfig(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	if domain := strings.TrimSpace(argument); domain != "" {
		m.configEditing = domain
		m.configFields = nil
		m.configNote = t(m.loc, "config.loading")
		m.mode = modeConfig
		m.layout()
		return m, configDetailCmd(m.comp, domain)
	}
	m.configDomains = nil
	m.configNote = t(m.loc, "config.loading")
	m.openConfigDomainSelector()
	return m, configDomainsCmd(m.comp)
}

// openConfigDomainSelector shows the domain list, including domains whose
// owner is not running: a durable declaration stays listable, and the human
// deserves to see it with the reason it cannot be edited.
func (m *model) openConfigDomainSelector() {
	m.selector = newSelector(t(m.loc, "selector.configDomains"),
		configDomainItems(m.loc, m.configDomains), m.width, m.height-3)
	m.mode = modeConfigDomains
	m.layout()
}

func configDomainItems(loc Locale, domains []configDomain) []list.Item {
	items := make([]list.Item, 0, len(domains))
	for _, d := range domains {
		title := d.Title
		if title == "" {
			title = d.Domain
		}
		state := ""
		if !d.OwnerAvailable && d.OwnerTool != "" {
			state = t(loc, "config.ownerUnavailable")
		}
		description := fmt.Sprintf("%s — %s · %d %s · %s",
			d.Domain, d.Owner, d.Properties, t(loc, "config.props"), d.Application)
		if state != "" {
			description += " · " + state
		}
		items = append(items, configDomainItem{domain: d, title: title,
			description: description})
	}
	return items
}

// configDomainItem carries the domain through the selector.
type configDomainItem struct {
	domain      configDomain
	title       string
	description string
}

func (i configDomainItem) Title() string       { return i.title }
func (i configDomainItem) Description() string { return i.description }
func (i configDomainItem) FilterValue() string {
	return i.title + " " + i.domain.Domain + " " + i.domain.Owner
}

// openConfigFieldSelector lists one domain's fields with their current values.
func (m *model) openConfigFieldSelector() {
	m.selector = newSelector(t(m.loc, "selector.configFields")+" — "+m.configEditing,
		configFieldItems(m.loc, m.configFields), m.width, m.height-3)
	m.mode = modeConfig
	m.layout()
}

func configFieldItems(loc Locale, fields []configField) []list.Item {
	items := make([]list.Item, 0, len(fields))
	for _, f := range fields {
		description := f.rendered()
		if f.ReadOnly {
			description += " · " + t(loc, "config.readOnly")
		}
		if f.Application != "" {
			description += " · " + f.Application
		}
		items = append(items, configFieldItem{field: f, title: f.Title,
			description: description})
	}
	return items
}

type configFieldItem struct {
	field       configField
	title       string
	description string
}

func (i configFieldItem) Title() string       { return i.title }
func (i configFieldItem) Description() string { return i.description }
func (i configFieldItem) FilterValue() string {
	return i.title + " " + i.field.Name
}

// openConfigEditForm starts editing one field. Secrets start empty: the client
// never holds a secret, so there is nothing to prefill.
func (m *model) openConfigEditForm(f configField) {
	m.configEditField = f
	// One reusable input: the editor holds a single field at a time.
	if m.configInput.Placeholder == "" {
		m.configInput = textinput.New()
		m.configInput.CharLimit = 4096
	}
	m.configInput.Placeholder = f.rendered()
	m.configInput.SetWidth(max(20, m.width-16))
	m.configInput.SetValue("")
	m.configInput.Focus()
	m.configErr = ""
	m.mode = modeConfigForm
	m.layout()
}

// submitConfigEdit parses the typed value and sends the patch.
func (m model) submitConfigEdit() (tea.Model, tea.Cmd) {
	f := m.configEditField
	if !f.editable() {
		m.configErr = t(m.loc, "config.notEditable")
		return m, nil
	}
	raw := strings.TrimSpace(m.configInput.Value())
	// An empty secret input clears the value only when the human says so
	// explicitly; empty must not silently unset a working credential.
	if f.Secret && raw == "" && f.Set {
		m.configErr = t(m.loc, "config.secretUnchanged")
		return m, nil
	}
	value, err := configParseValue(f, raw)
	if err != nil {
		m.configErr = err.Error()
		return m, nil
	}
	m.configErr = ""
	m.controlPending = true
	m.configNote = t(m.loc, "config.saving")
	return m, configApplyCmd(m.comp, m.configEditing,
		map[string]any{f.Name: value}, m.configRev,
		f.Title+" = "+raw)
}

// configFormView renders the one-field editor. It shows the field's help, its
// current/default value and the declared application timing, because "when
// does this take effect" is part of what the human is agreeing to.
func (m model) configFormView() string {
	f := m.configEditField
	var b strings.Builder
	title := f.Title
	if f.Application != "" {
		title += " · " + f.Application
	}
	b.WriteString(metaStyle.Render(title))
	b.WriteString("\n")
	if f.Description != "" {
		b.WriteString(truncate(f.Description, max(20, m.width-2)))
		b.WriteString("\n")
	}
	b.WriteString(metaStyle.Render(t(m.loc, "config.current")+": "+f.rendered()))
	b.WriteString("\n\n")
	b.WriteString(m.configInput.View())
	b.WriteString("\n")
	if m.configErr != "" {
		b.WriteString(errorStyle.Render(m.configErr))
	} else {
		b.WriteString(metaStyle.Render(t(m.loc, "config.editHelp")))
	}
	return b.String()
}

// updateConfigForm handles the one-field editor: enter submits, esc abandons
// without a write, and every other key goes to the input.
func (m model) updateConfigForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.configErr = ""
		m.mode = modeConfig
		m.openConfigFieldSelector()
		return m, nil
	case "enter":
		return m.submitConfigEdit()
	}
	var cmd tea.Cmd
	m.configInput, cmd = m.configInput.Update(msg)
	return m, cmd
}
