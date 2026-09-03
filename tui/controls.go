package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
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
	m.thinkingEffort = ""
	m.runtime = runtimeResolution{}
	m.promptTokens = 0
	m.contextUsed = 0
	m.cacheHits = 0
	m.cachePrompt = 0
	m.lastCachePrompt = 0
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
	// Completion state is per-input, not per-session: the cleared input
	// invalidates any active candidate list.
	m.slashComp = slashCompleteState{}
	m.historyFile = historyFilePath(id)
	m.history = loadHistory(m.historyFile)
	// The viewport only repaints through syncViewport; without this the
	// old conversation stays on screen after /new (or any session switch)
	// until the next event that happens to re-sync.
	m.syncViewport(true)
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
	case "locale":
		arg := strings.TrimSpace(argument)
		if arg == "" {
			arg = string(m.loc)
		}
		loc, ok := validLocale(arg)
		if !ok {
			m.addBlock(blockError, t(m.loc, "locale.invalid", arg))
			m.syncViewport(true)
			return m, nil
		}
		m.loc = loc
		m.input.Placeholder = t(loc, "input.placeholder")
		persistLocale(loc)
		m.addBlock(blockMeta, t(m.loc, "locale.switched", arg))
		m.syncViewport(true)
		return m, nil

	case "provider", "providers":
		if !m.connected {
			m.contextNote = t(m.loc, "note.notConnected")
			return m, nil
		}
		if argument != "" {
			if m.busy {
				m.contextNote = t(m.loc, "note.betweenTurnsProvider")
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
			m.contextNote = t(m.loc, "note.notConnected")
			return m, nil
		}
		if m.busy {
			m.contextNote = t(m.loc, "note.betweenTurnsProvider")
			return m, nil
		}
		m.openCatalogProviderSelector()
		return m, nil

	case "model", "models":
		if !m.connected {
			m.contextNote = t(m.loc, "note.notConnected")
			return m, nil
		}
		if m.busy {
			m.contextNote = t(m.loc, "note.betweenTurnsModel")
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
			m.contextNote = t(m.loc, "note.savingModel")
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
		m.clearMouseSelection()
		if m.mouse {
			m.addBlock(blockMeta, t(m.loc, "chat.mouseOn"))
		} else {
			m.addBlock(blockMeta, t(m.loc, "chat.mouseOff"))
		}
		m.syncViewport(true)
		return m, nil

	case "help", "?":
		lines := []string{
			t(m.loc, "help.title"),
			t(m.loc, "help.new"),
			t(m.loc, "help.session"),
			t(m.loc, "help.provider"),
			t(m.loc, "help.model"),
			t(m.loc, "help.connect"),
			t(m.loc, "help.status"),
			t(m.loc, "help.mouse"),
			t(m.loc, "help.locale"),
			t(m.loc, "help.help"),
			"",
			t(m.loc, "help.keys"),
		}
		if plugins := m.slash.pluginCommands(); len(plugins) > 0 {
			lines = append(lines, "", t(m.loc, "help.pluginTitle"))
			for _, cmd := range plugins {
				line := "  /" + cmd.Name
				if cmd.Description != "" {
					line += " — " + cmd.Description
				}
				lines = append(lines, line+" ("+cmd.Component+")")
			}
		}
		m.addBlock(blockMeta, strings.Join(lines, "\n"))
		m.syncViewport(true)
		return m, nil

	default:
		// Registered commands (docs/WIRE.md): parse against the declared
		// params and issue the target tool call. Built-ins shadow any
		// same-named registration, so only non-builtins reach this.
		if cmd, ok := m.slash.lookup(name); ok && !cmd.builtin {
			return m.executeSlashCommand(cmd, argument)
		}
		m.addBlock(blockError, t(m.loc, "chat.unknownCommand", name)+suggestSlash(m.slash, name))
		m.syncViewport(true)
		return m, nil
	}
}

func (m *model) openProviderSelector() {
	m.selector = newSelector(t(m.loc, "selector.providers"),
		providerSelectorItems(m.loc, m.providers, m.providerStatus), m.width, m.height-3)
	m.mode = modeProviders
	m.layout()
}

func (m *model) openCatalogProviderSelector() {
	m.selector = newSelector(t(m.loc, "selector.connectCatalog"),
		catalogProviderItems(m.loc, m.configuredCatalogProviders()), m.width, m.height-3)
	m.mode = modeCatalogProviders
	m.layout()
}

// sessionListSelecting opens the conversation browser with a loading
// placeholder; the selector is rebuilt with the loaded list in the
// sessionListMsg handler.
func (m *model) sessionListSelecting() {
	m.selector = newSelector(t(m.loc, "selector.sessionsLoading"), nil, m.width, m.height-3)
	m.mode = modeSessions
	m.layout()
}

// openSessionSelector rebuilds the /session list with the fetched sessions.
func (m *model) openSessionSelector(sessions []sessionSummary) {
	m.selector = newSelector(t(m.loc, "selector.sessions"),
		sessionSelectorItems(m.loc, m.session, sessions), m.width, m.height-3)
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
	title := t(m.loc, "selector.models")
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
		modelSelectorItems(m.loc, models, m.runtime, m.modelOverride, m.providerDefaultModel()), m.width, m.height-3)
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

	if m.mode == modeOAuth {
		if m.oauthLogin == nil {
			m.mode = modeChat
			m.layout()
			return m, nil
		}
		switch msg.String() {
		case "esc":
			flowID := m.oauthLogin.flowID
			m.oauthLogin = nil
			m.mode = modeChat
			m.layout()
			return m, cancelOAuthCmd(m.comp, flowID)
		case "enter":
			if m.oauthLogin.submitManual() {
				state := *m.oauthLogin
				state.seq++ // invalidate results from the sleeping chain
				m.oauthLogin = &state
				return m, pollOAuthCmd(m.comp, state)
			}
			return m, nil
		}
		var cmd tea.Cmd
		var input textinput.Model
		input, cmd = m.oauthLogin.input.Update(msg)
		m.oauthLogin.input = input
		return m, cmd
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
					m.providerForm = newEditProviderForm(p, m.width, m.loc)
					m.mode = modeConnectForm
					m.providerConfirmDelete = ""
					m.layout()
					// Catalog models normalize/prefill the model field; the live
					// endpoint probe (stored credential) refines it when it lands.
					if m.providerForm.catalogID != "" {
						m.providerForm.loading = true
						return m, tea.Batch(
							loadModelsCmd(m.comp, m.providerForm.catalogID),
							loadServedModelsCmd(m.comp, p.Nickname, "", ""))
					}
					return m, loadServedModelsCmd(m.comp, p.Nickname, "", "")
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
			m.contextNote = t(m.loc, "note.betweenTurnsProvider")
			m.mode = modeChat
			return m, nil
		}
		switch selected.kind {
		case selectorConnect:
			m.openCatalogProviderSelector()
			return m, nil
		case selectorOAuthOpenAIBrowser:
			m.controlPending = true
			return m, startOAuthCmd(m.comp, m.loc, oauthProtocolCodex, oauthMethodBrowser)
		case selectorOAuthOpenAIDevice:
			m.controlPending = true
			return m, startOAuthCmd(m.comp, m.loc, oauthProtocolCodex, oauthMethodDevice)
		case selectorOAuthAnthropic:
			m.controlPending = true
			return m, startOAuthCmd(m.comp, m.loc, oauthProtocolAnthropic, oauthMethodBrowser)
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
		m.providerForm = newProviderForm(template, m.runtime, m.width, m.loc)
		m.mode = modeConnectForm
		m.layout()
		// Fetch the provider's catalog models so the form can prefill a
		// model id (or normalize what the runtime already resolved).
		if m.providerForm.catalogID != "" {
			m.providerForm.loading = true
			return m, loadModelsCmd(m.comp, m.providerForm.catalogID)
		}
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
		m.contextNote = t(m.loc, "note.savingModel")
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
	// Connect form: probe the endpoint with the typed credentials first so
	// the saved model id is spelled exactly as the provider serves it (the
	// probe also validates the key/URL before the provider is stored).
	return m, probeThenAddProviderCmd(m.comp, values)
}

// probeThenAddProviderCmd chains a provider_models probe (explicit
// baseUrl/apiKey) into the provider_add call. A failed probe still saves —
// the endpoint may simply lack a /models route — but the model id is then
// whatever normalization produced from the catalog.

// cycleThinkingLevel advances the reasoning-display level (full → brief →
// off → full). Persists across session switches: it is a display preference,
// not per-conversation state.
func (m *model) cycleThinkingLevel() {
	m.thinkLevel = (m.thinkLevel + 1) % 3
	m.markTranscriptDirty()
}

// effortCycle is the LLM thinking-effort rotation for ctrl+g: empty means
// the provider default (no reasoning_effort sent).
var effortCycle = []string{"", "low", "medium", "high"}

// nextThinkingEffort returns the effort level following the current
// per-conversation selection.
func (m model) nextThinkingEffort() string {
	idx := -1
	for i, level := range effortCycle {
		if level == m.thinkingEffort {
			idx = i
			break
		}
	}
	return effortCycle[(idx+1)%len(effortCycle)]
}

// effortLabel renders the current effort for the header chip; the empty
// selection (provider default) reads as "auto".
func (m model) effortLabel() string {
	if m.thinkingEffort == "" {
		return "auto"
	}
	return m.thinkingEffort
}

// applyThinkingEffort folds a persisted thinking-effort save into the model.
// Like modelActionMsg, a completion for the old session is dropped.
func (m *model) applyThinkingEffort(msg thinkingEffortMsg) {
	if msg.Session != m.session {
		return
	}
	m.controlPending = false
	if msg.Err != nil {
		m.contextNote = msg.Err.Error()
		m.addBlock(blockError, msg.Err.Error())
		m.syncViewport(true)
		return
	}
	m.thinkingEffort = msg.Effort
	m.contextNote = ""
	m.addBlock(blockMeta, t(m.loc, "chat.thinkingEffort", t(m.loc, "level."+m.effortLabel())))
	m.syncViewport(true)
}

func (m model) detailedRuntimeStatus() string {
	lines := []string{
		t(m.loc, "status.detailProvider", valueOr(m.runtime.Provider, t(m.loc, "status.unknown")), valueOr(m.runtime.ProviderSource, t(m.loc, "status.unknownSource"))),
		t(m.loc, "status.detailModel", valueOr(m.runtime.Model, t(m.loc, "status.unknown"))),
		t(m.loc, "status.detailCatalog", valueOr(m.runtime.Catalog, t(m.loc, "status.none"))),
		t(m.loc, "status.detailContext", formatTokens(m.runtime.Context), valueOr(m.runtime.ContextSource, t(m.loc, "status.unknownSource"))),
		t(m.loc, "status.detailOutput", formatTokens(m.runtime.Output), valueOr(m.runtime.OutputSource, t(m.loc, "status.unknownSource"))),
		t(m.loc, "status.detailUsed", formatTokens(m.contextUsed), fmt.Sprintf("%.1f%%", contextPercent(m.contextUsed, m.runtime.Context)*100)),
	}
	// Cache-hit economics: only shown once the provider has reported a
	// cached-input breakdown (the ratio is undefined before that).
	if m.cachePrompt > 0 {
		hit := float64(m.cacheHits) / float64(m.cachePrompt) * 100
		lines = append(lines, t(m.loc, "status.detailCache",
			formatTokens(m.cacheHits), formatTokens(m.cachePrompt), fmt.Sprintf("%.1f%%", hit)))
	}
	if m.providerStatus.Provider.StripPrefix {
		lines = append(lines, t(m.loc, "status.detailStrip"))
	}
	if m.modelOverride != "" {
		lines = append(lines, t(m.loc, "status.detailOverride", m.modelOverride))
	}
	return strings.Join(lines, "\n")
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
