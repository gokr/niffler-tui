package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// switchSession resets the model for a different conversation (session) id:
// clears the transcript, per-session state (history, model override, runtime,
// approvals) and input, then reloads the new session's history. The caller
// must re-bootstrap (bootstrapBackendCmd) to repopulate provider/model/runtime.
func (m model) switchSession(id string) model {
	m.session = id
	m.blocks = nil
	m.markTranscriptDirty()
	m.assistantIdx = -1
	m.thinkingIdx = -1
	m.hadAssistant = false
	m.busy = false
	m.stopArmed = false
	m.stopping = false
	m.setStreaming(false)
	m.roundClosed = false
	m.renderTimerActive = false
	m.contextNote = ""
	m.modelOverride = ""
	m.runtime = runtimeResolution{}
	m.promptTokens = 0
	m.contextUsed = 0
	// controlPending guards the control-plane UI; a completion for the old
	// session (e.g. a conversation model save) is dropped by its session
	// guard, so the flag must not survive the switch.
	m.controlPending = false
	m.models = nil
	m.modelsCatalog = ""
	m.approvals = nil
	m.searchActive = false
	m.searchQuery = ""
	m.mode = modeChat
	m.providerConfirmDelete = ""
	m.providerDeleteErr = ""
	m.histIdx = -1
	m.draft = ""
	m.input.SetValue("")
	m.historyFile = historyFilePath(id)
	m.history = loadHistory(m.historyFile)
	return m
}

// newSessionID generates a fresh conversation id for /new and the session
// browser's "+ New session" entry.
func newSessionID() string {
	return "conv-" + strconv.FormatInt(time.Now().Unix(), 10)
}

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
			// /provider strip [on|off] — toggle the active provider's model
			// prefix stripping for gateways that route on the canonical id.
			if argument == "strip" || strings.HasPrefix(argument, "strip ") {
				on := true
				if parts := strings.Fields(argument); len(parts) > 1 {
					on = parts[1] != "off"
				}
				nickname := m.providerStatus.Provider.Nickname
				if nickname == "" {
					nickname = "default"
				}
				return m, setProviderStripCmd(m.comp, nickname, on)
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

	case "new", "newsession":
		id := strings.TrimSpace(argument)
		if id == "" {
			id = newSessionID()
		}
		return m.switchSession(id), bootstrapBackendCmd(m.comp, id)

	case "session", "sessions":
		if argument != "" {
			id := strings.TrimSpace(argument)
			return m.switchSession(id), bootstrapBackendCmd(m.comp, id)
		}
		// Open the conversation browser; the store list is fetched in the
		// background and the selector rebuilds when it arrives.
		m.sessionListSelecting()
		return m, sessionListCmd(m.comp)

	case "mouse":
		if argument == "" {
			m.mouse = !m.mouse
		} else {
			m.mouse = argument == "on"
		}
		if m.mouse {
			m.addBlock(blockMeta, "mouse on — wheel scrolls the transcript; click a tool card to expand it "+
				"(ctrl+t toggles all); copy with shift+drag")
		} else {
			m.addBlock(blockMeta, "mouse off — native plain-drag copy; wheel is terminal-scroll (transcript: "+
				"pgup/pgdn/ctrl+up)")
		}
		m.syncViewport(true)
		return m, nil

	case "help", "?":
		m.addBlock(blockMeta, strings.Join([]string{
			"local commands:",
			"  /provider [nickname|environment]  choose the global provider (e: edit, d: remove selected)",
			"  /model [id|default]   choose this conversation's model",
			"  /connect              store a provider connection",
			"  /status               show provider/model/context details",
			"  /mouse [on|off]       tool-card click expansion (off = native copy)",
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

// sessionListSelecting opens the conversation browser with a loading
// placeholder; the selector is rebuilt with the loaded list in the
// sessionListMsg handler.
func (m *model) sessionListSelecting() {
	m.selector = newSelector("Sessions — loading…", nil, m.width, m.height-3)
	m.mode = modeSessions
	m.layout()
}

// openSessionSelector rebuilds the /session list with the fetched sessions.
func (m *model) openSessionSelector(sessions []sessionSummary) {
	m.selector = newSelector("Sessions",
		sessionSelectorItems(m.session, sessions), m.width, m.height-3)
	m.mode = modeSessions
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
	// Provider management shortcuts (modeProviders only): e = edit the selected
	// provider, d/x = delete it. Two-stage delete: first press arms it, the second
	// (or enter) confirms; navigating or esc disarms.
	if m.mode == modeProviders {
		key := msg.String()
		if key == "esc" {
			m.providerConfirmDelete = ""
			m.providerDeleteErr = ""
			m.mode = modeChat
			m.layout()
			return m, nil
		}
		selected, ok := m.selector.selected()
		armed := m.providerConfirmDelete
		if armed != "" && selected.id != armed {
			// Selection moved: disarm the pending delete.
			m.providerConfirmDelete = ""
		}
		if ok && selected.kind == selectorProvider && !m.busy {
			switch key {
			case "e":
				if p, ok := selected.payload.(providerSummary); ok {
					m.providerForm = newEditProviderForm(p, m.width)
					m.mode = modeConnectForm
					m.providerConfirmDelete = ""
					m.layout()
					return m, nil
				}
			case "d", "x", "enter":
				if armed != "" {
					// Confirmed: delete it.
					m.providerConfirmDelete = ""
					m.providerDeleteErr = ""
					m.controlPending = true
					return m, removeProviderCmd(m.comp, selected.id)
				}
				if armed == "" && key != "enter" {
					// First press: arm the delete confirmation.
					m.providerConfirmDelete = selected.id
					return m, nil
				}
			}
		}
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
	case modeSessions:
		switch selected.kind {
		case selectorNewSession:
			id := newSessionID()
			return m.switchSession(id), bootstrapBackendCmd(m.comp, id)
		case selectorSession:
			if selected.id == m.session {
				// Re-selecting the current session just dismisses the
				// browser; switching would wipe the transcript.
				m.mode = modeChat
				m.layout()
				return m, nil
			}
			return m.switchSession(selected.id), bootstrapBackendCmd(m.comp, selected.id)
		}
		return m, nil
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
	if m.providerForm.edit {
		return m, updateProviderCmd(m.comp, values)
	}
	return m, addProviderCmd(m.comp, values)
}

func (m model) detailedRuntimeStatus() string {
	lines := []string{
		"provider: " + valueOr(m.runtime.Provider, "unknown") + " (" + valueOr(m.runtime.ProviderSource, "unknown source") + ")",
		"model: " + valueOr(m.runtime.Model, "unknown"),
		"catalog: " + valueOr(m.runtime.Catalog, "none"),
		"context: " + formatTokens(m.runtime.Context) + " (" + valueOr(m.runtime.ContextSource, "unknown source") + ")",
		"output: " + formatTokens(m.runtime.Output) + " (" + valueOr(m.runtime.OutputSource, "unknown source") + ")",
		"used: " + formatTokens(m.contextUsed) + fmt.Sprintf(" (%.1f%%)", contextPercent(m.contextUsed, m.runtime.Context)*100),
	}
	if m.providerStatus.Provider.StripPrefix {
		lines = append(lines, "strip model prefix: on")
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
