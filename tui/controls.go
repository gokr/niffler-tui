package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m model) executeLocalCommand(command string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(strings.TrimSpace(command))
	name := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	argument := ""
	if len(parts) > 1 {
		argument = strings.Join(parts[1:], " ")
	}

	switch name {
	case "provider", "providers":
		if !m.connected {
			m.contextNote = "not connected"
			return m, nil
		}
		if argument != "" {
			if m.busy {
				m.contextNote = "provider changes apply between turns"
				return m, nil
			}
			m.controlPending = true
			if argument == "environment" || argument == "env" {
				return m, useEnvironmentProviderCmd(m.comp)
			}
			return m, switchProviderCmd(m.comp, argument)
		}
		m.openProviderSelector()
		return m, nil

	case "connect":
		if !m.connected {
			m.contextNote = "not connected"
			return m, nil
		}
		if m.busy {
			m.contextNote = "provider changes apply between turns"
			return m, nil
		}
		m.openCatalogProviderSelector()
		return m, nil

	case "model", "models":
		if !m.connected {
			m.contextNote = "not connected"
			return m, nil
		}
		if m.busy {
			m.contextNote = "model changes apply between turns"
			return m, nil
		}
		if argument != "" {
			previous := m.modelOverride
			if argument == "default" {
				m.modelOverride = ""
			} else {
				m.modelOverride = argument
				m.runtime.Model = argument
			}
			m.controlPending = true
			m.contextNote = "saving conversation model…"
			return m, setConversationModelCmd(m.comp, m.session, m.modelOverride, previous)
		}
		m.openModelSelector()
		if m.runtime.Catalog != "" && (m.modelsCatalog != m.runtime.Catalog || len(m.models) == 0) {
			spinCmd := m.selector.list.StartSpinner()
			return m, tea.Batch(spinCmd, loadModelsCmd(m.comp, m.runtime.Catalog))
		}
		return m, nil

	case "status":
		m.addBlock(blockMeta, m.detailedRuntimeStatus())
		m.syncViewport(true)
		return m, nil

	case "help", "?":
		m.addBlock(blockMeta, strings.Join([]string{
			"local commands:",
			"  /provider [nickname|environment]  choose the global provider",
			"  /model [id|default]   choose this conversation's model",
			"  /connect              store a provider connection",
			"  /status               show provider/model/context details",
			"  /help                 show this help",
		}, "\n"))
		m.syncViewport(true)
		return m, nil

	default:
		m.addBlock(blockError, "unknown local command /"+name+" (try /help)")
		m.syncViewport(true)
		return m, nil
	}
}

func (m *model) openProviderSelector() {
	m.selector = newSelector("Providers — global default",
		providerSelectorItems(m.providers, m.providerStatus), m.width, m.height-3)
	m.mode = modeProviders
	m.layout()
}

func (m *model) openCatalogProviderSelector() {
	m.selector = newSelector("Connect provider — choose a catalog template",
		catalogProviderItems(m.configuredCatalogProviders()), m.width, m.height-3)
	m.mode = modeCatalogProviders
	m.layout()
}

func (m model) configuredCatalogProviders() []catalogProvider {
	configured := make(map[string]bool, len(m.providers)+1)
	for _, provider := range m.providers {
		catalog := provider.Catalog
		if catalog == "" {
			catalog = provider.Nickname
		}
		configured[catalog] = true
	}
	if m.providerStatus.Provider.Catalog != "" {
		configured[m.providerStatus.Provider.Catalog] = true
	}
	result := make([]catalogProvider, len(m.catalogProviders))
	copy(result, m.catalogProviders)
	for i := range result {
		result[i].Configured = result[i].Configured || configured[result[i].ID]
	}
	return result
}

func (m *model) openModelSelector() {
	title := "Models"
	if m.runtime.Provider != "" {
		title += " — " + m.runtime.Provider
	}
	if m.runtime.Catalog != "" {
		title += " / " + m.runtime.Catalog
	}
	models := m.models
	if m.modelsCatalog != m.runtime.Catalog {
		models = nil // never offer stale candidates from the previous provider
	}
	if len(models) > 0 {
		title += fmt.Sprintf(" (%d)", len(models))
	}
	m.selector = newSelector(title,
		modelSelectorItems(models, m.runtime, m.modelOverride, m.providerDefaultModel()), m.width, m.height-3)
	m.mode = modeModels
	m.layout()
}

func (m model) providerDefaultModel() string {
	if m.providerStatus.Provider.Model != "" {
		return m.providerStatus.Provider.Model
	}
	for _, provider := range m.providers {
		if provider.Active {
			return provider.Model
		}
	}
	return ""
}

func (m model) handleControlKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.controlPending {
		return m, nil
	}

	if m.mode == modeConnectForm {
		switch msg.String() {
		case "esc":
			m.providerForm.clearSecret()
			m.mode = modeChat
			m.layout()
			return m, nil
		case "tab", "down":
			return m, m.providerForm.nextField(1)
		case "shift+tab", "up":
			return m, m.providerForm.nextField(-1)
		case "ctrl+s":
			return m.submitProviderForm()
		case "enter":
			if m.providerForm.focus == providerFieldCount-1 {
				return m.submitProviderForm()
			}
			return m, m.providerForm.nextField(1)
		}
		var cmd tea.Cmd
		m.providerForm, cmd = m.providerForm.update(msg)
		return m, cmd
	}

	// Let the list own Enter/Esc while editing its filter.
	if m.selector.list.SettingFilter() {
		var cmd tea.Cmd
		m.selector.list, cmd = m.selector.list.Update(msg)
		return m, cmd
	}
	if msg.String() == "esc" {
		m.mode = modeChat
		m.layout()
		return m, nil
	}
	if msg.String() != "enter" {
		var cmd tea.Cmd
		m.selector.list, cmd = m.selector.list.Update(msg)
		return m, cmd
	}

	selected, ok := m.selector.selected()
	if !ok {
		return m, nil
	}
	switch m.mode {
	case modeProviders:
		if m.busy {
			m.contextNote = "provider changes apply between turns"
			m.mode = modeChat
			return m, nil
		}
		switch selected.kind {
		case selectorConnect:
			m.openCatalogProviderSelector()
			return m, nil
		case selectorEnvironment:
			m.controlPending = true
			return m, useEnvironmentProviderCmd(m.comp)
		case selectorProvider:
			m.controlPending = true
			return m, switchProviderCmd(m.comp, selected.id)
		default:
			return m, nil
		}

	case modeCatalogProviders:
		var template *catalogProvider
		if selected.kind == selectorCatalogProvider {
			value := selected.payload.(catalogProvider)
			template = &value
		}
		m.providerForm = newProviderForm(template, m.runtime, m.width)
		m.mode = modeConnectForm
		m.layout()
		return m, nil

	case modeModels:
		previous := m.modelOverride
		if selected.kind == selectorProviderDefaultModel {
			m.modelOverride = ""
		} else if selected.kind == selectorModel {
			m.modelOverride = selected.id
			m.runtime.Model = selected.id
			if candidate, ok := selected.payload.(modelSummary); ok && candidate.Limit.Context > 0 {
				m.runtime.Context = candidate.Limit.Context
				m.runtime.ContextSource = "catalog"
			}
		} else {
			return m, nil
		}
		m.mode = modeChat
		m.controlPending = true
		m.contextNote = "saving conversation model…"
		m.layout()
		return m, setConversationModelCmd(m.comp, m.session, m.modelOverride, previous)
	}
	return m, nil
}

func (m model) submitProviderForm() (tea.Model, tea.Cmd) {
	if m.controlPending || m.providerForm.saving {
		return m, nil
	}
	values, err := m.providerForm.values()
	if err != nil {
		m.providerForm.err = err.Error()
		return m, nil
	}
	m.providerForm.err = ""
	m.providerForm.saving = true
	m.controlPending = true
	return m, addProviderCmd(m.comp, values)
}

func (m model) detailedRuntimeStatus() string {
	lines := []string{
		"provider: " + valueOr(m.runtime.Provider, "unknown") + " (" + valueOr(m.runtime.ProviderSource, "unknown source") + ")",
		"model: " + valueOr(m.runtime.Model, "unknown"),
		"catalog: " + valueOr(m.runtime.Catalog, "none"),
		"context: " + formatTokens(m.runtime.Context) + " (" + valueOr(m.runtime.ContextSource, "unknown source") + ")",
		"used: " + formatTokens(m.contextUsed) + fmt.Sprintf(" (%.1f%%)", contextPercent(m.contextUsed, m.runtime.Context)*100),
	}
	if m.modelOverride != "" {
		lines = append(lines, "session model override: "+m.modelOverride)
	}
	return strings.Join(lines, "\n")
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
