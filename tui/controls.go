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
// clears the transcript, per-session state (sent-message history, model
// override, runtime, approvals) and input, then reloads the new session's
// sent-message history. The caller must re-bootstrap (bootstrapBackendCmd)
// and replay the stored transcript (switchSessionWithHistory) to repopulate
// provider/model/runtime and show the previous messages.
func (m model) switchSession(id string) model {
	// Snapshot the outgoing conversation's usage counters and restore the
	// incoming one's (zero on first visit), so the header stats stay stable
	// across switches.
	if m.usageCache == nil {
		m.usageCache = map[string]usageTotals{}
	}
	m.usageCache[m.session] = m.usageSnapshot()
	m.session = id
	m.restoreUsage(m.usageCache[id])
	m.cwd = initialCwd()
	// Remember the active conversation so a restart resumes it (explicit
	// -session/NIF_SESSION still win at startup).
	persistSession(m.natsURL, id)
	m.blocks = nil
	m.markTranscriptDirty()
	m.renderFrom = 0
	m.flushPending = false
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
	m.mcpConfirmDelete = ""
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

// executeLocalCommand runs a UI-local command. Dispatch reads the built-in
// registry (builtinCommand): the entry Tab completes and /help lists is the
// entry that carries the handler, so a local command is declared exactly
// once. Registered plugin commands are the fallback.
func (m model) executeLocalCommand(command string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return m, nil
	}
	name := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	argument := strings.Join(parts[1:], " ")
	if cmd, ok := builtinCommand(name); ok && cmd.run != nil {
		return cmd.run(m, cmd, argument)
	}
	if cmd, ok := m.slash.lookup(name); ok && !cmd.builtin {
		return m.executeSlashCommand(cmd, argument)
	}
	m.addBlock(blockError, t(m.loc, "chat.unknownCommand", name)+suggestSlash(m.slash, name))
	m.syncViewport(true)
	return m, nil
}

// splitCommand splits an argument string into its first field and the
// trimmed remainder, for subcommand dispatch.
func splitCommand(argument string) (first, rest string) {
	fields := strings.Fields(argument)
	if len(fields) == 0 {
		return "", ""
	}
	return fields[0], strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(argument), fields[0]))
}

// ---- local command handlers ------------------------------------------------
//
// One handler per registry entry (tui/slash.go). Subcommand handlers take the
// text after the subcommand name.

func localComponents(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return m, m.toolVisibilityCmd(cmd.Name, argument)
}

func localDiscover(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if m.busy {
		m.addBlock(blockError, "Wait for the turn to finish before explicit discovery.")
		m.syncViewport(true)
		return m, nil
	}
	return m, m.toolVisibilityCmd(cmd.Name, argument)
}

func localProfile(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	// A bare /profile opens the picker (the help text and README promise a
	// listing with the current choice marked); an argument still applies
	// directly, which also validates the name against the store.
	if strings.TrimSpace(argument) == "" {
		m.openProfileSelector()
		return m, profilesCmd(m.comp)
	}
	return m, m.toolVisibilityCmd(cmd.Name, argument)
}

func localModel(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
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
}

func localStatus(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	m.addBlock(blockMeta, m.detailedRuntimeStatus())
	m.syncViewport(true)
	return m, nil
}

func localNew(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	id := strings.TrimSpace(argument)
	if id == "" {
		id = newSessionID()
	}
	m, historyCmd := m.switchSessionWithHistory(id)
	return m, tea.Batch(historyCmd, bootstrapBackendCmd(m.comp, id))
}

func localSession(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if id := strings.TrimSpace(argument); id != "" {
		m, historyCmd := m.switchSessionWithHistory(id)
		return m, tea.Batch(historyCmd, bootstrapBackendCmd(m.comp, id))
	}
	// Open the conversation browser; the store list is fetched in the
	// background and the selector rebuilds when it arrives.
	m.sessionListSelecting()
	return m, sessionListCmd(m.comp)
}

func localMouse(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	argument = strings.TrimSpace(argument)
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
}

// localCards toggles the card background behind tool runs. Themes without a
// card background are unaffected, so this is a display-only preference.
func localCards(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	argument = strings.TrimSpace(argument)
	if argument == "" {
		m.toolCards = !m.toolCards
	} else {
		m.toolCards = argument == "on"
	}
	// Card rendering is cached per block; the whole transcript must rebuild.
	m.invalidatePieces()
	m.markTranscriptDirty()
	if m.toolCards {
		m.addBlock(blockMeta, t(m.loc, "chat.cardsOn"))
	} else {
		m.addBlock(blockMeta, t(m.loc, "chat.cardsOff"))
	}
	m.syncViewport(true)
	return m, nil
}

func localTheme(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	arg := strings.TrimSpace(argument)
	if arg == "" {
		m.openThemeSelector()
		return m, nil
	}
	if !m.setTheme(arg) {
		m.addBlock(blockError, t(m.loc, "theme.invalid", arg))
		m.syncViewport(true)
		return m, nil
	}
	persistTheme(arg)
	m.addBlock(blockMeta, t(m.loc, "theme.switched", arg))
	m.syncViewport(true)
	return m, nil
}

func localLocale(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
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
	persistLocale(loc)
	m.addBlock(blockMeta, t(m.loc, "locale.switched", arg))
	m.syncViewport(true)
	return m, nil
}

// localHelp renders the command summary: the registry listing (one line per
// canonical built-in) plus the registered plugin commands.
func localHelp(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	lines := []string{t(m.loc, "help.title")}
	for _, builtin := range m.slash.helpCommands() {
		lines = append(lines, m.helpLine(builtin))
	}
	lines = append(lines, "", t(m.loc, "help.keys"))
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
}

func localProvider(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		m.openProviderSelector()
		return m, nil
	}
	if m.busy {
		m.contextNote = t(m.loc, "note.betweenTurnsProvider")
		return m, nil
	}
	if first, rest := splitCommand(argument); first != "" {
		if sub, ok := cmd.localSubcommand(first); ok {
			return sub.run(m, cmd, rest)
		}
	}
	m.controlPending = true
	return m, switchProviderCmd(m.comp, argument)
}

// providerEnvironment selects the environment (NIF_OPENAI_*) provider.
func providerEnvironment(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	m.controlPending = true
	return m, useEnvironmentProviderCmd(m.comp)
}

// providerStrip toggles model-id prefix stripping for the active provider
// (gateways that route on the canonical id).
func providerStrip(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	on := true
	if first, _ := splitCommand(argument); first != "" {
		on = first != "off"
	}
	nickname := m.providerStatus.Provider.Nickname
	if nickname == "" {
		nickname = "default"
	}
	// The provider_action completion clears the flag (main.go).
	m.controlPending = true
	return m, setProviderStripCmd(m.comp, nickname, on)
}

func localConnect(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
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
}

func localMcp(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	if m.busy {
		m.contextNote = t(m.loc, "note.betweenTurnsProvider")
		return m, nil
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		m.openMcpSelector()
		return m, mcpServersCmd(m.comp)
	}
	first, rest := splitCommand(argument)
	sub, ok := cmd.localSubcommand(first)
	if !ok {
		m.addBlock(blockError, t(m.loc, "mcp.unknownSubcommand", first))
		m.syncViewport(true)
		return m, nil
	}
	return sub.run(m, cmd, rest)
}

func mcpAdd(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	m.mcpForm = newMcpForm(m.width, m.loc)
	m.mode = modeMcpForm
	m.layout()
	return m, nil
}

func mcpEdit(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	name, _ := splitCommand(argument)
	if name == "" {
		m.addBlock(blockError, t(m.loc, "mcp.nameRequired"))
		m.syncViewport(true)
		return m, nil
	}
	m.controlPending = true
	return m, loadMcpEditCmd(m.comp, name)
}

func mcpOn(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return mcpToggle(m, argument, true)
}

func mcpOff(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	return mcpToggle(m, argument, false)
}

func mcpToggle(m model, argument string, on bool) (tea.Model, tea.Cmd) {
	name, _ := splitCommand(argument)
	if name == "" {
		m.addBlock(blockError, t(m.loc, "mcp.nameRequired"))
		m.syncViewport(true)
		return m, nil
	}
	m.controlPending = true
	return m, mcpToggleCmd(m.comp, name, on)
}

func mcpRefresh(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	name, _ := splitCommand(argument)
	if name == "" {
		m.addBlock(blockError, t(m.loc, "mcp.nameRequired"))
		m.syncViewport(true)
		return m, nil
	}
	m.controlPending = true
	return m, mcpRefreshCmd(m.comp, name)
}

func mcpSearch(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	query := strings.TrimSpace(argument)
	if query == "" {
		m.addBlock(blockError, t(m.loc, "mcp.searchQueryRequired"))
		m.syncViewport(true)
		return m, nil
	}
	m.openMcpSearchSelector(query)
	return m, mcpSearchCmd(m.comp, query)
}

// helpLine renders one built-in command's /help entry: the localized
// catalog line ("help.<name>") when the locale carries it, else a line
// derived from the registry entry — a command stays visible in /help even
// if its translation is missing. Plugin commands are listed separately.
func (m model) helpLine(cmd slashCommand) string {
	if line := t(m.loc, "help."+cmd.Name); line != "" {
		return line
	}
	line := "  /" + cmd.Name + cmd.usage()
	if cmd.Description != "" {
		line += "  — " + cmd.Description
	}
	return line
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

// openMcpSelector opens the /mcp browser with a loading placeholder; the
// mcpServersMsg handler rebuilds it with the loaded list.
func (m *model) openMcpSelector() {
	m.selector = newSelector(t(m.loc, "selector.mcpLoading"), nil, m.width, m.height-3)
	m.mode = modeMcp
	m.mcpConfirmDelete = ""
	m.layout()
}

// openMcpSearchSelector opens the /mcp search browser with a loading
// placeholder; the mcpSearchMsg handler rebuilds it with the results.
func (m *model) openMcpSearchSelector(query string) {
	m.selector = newSelector(t(m.loc, "mcp.searchLoading", query), nil, m.width, m.height-3)
	m.mode = modeMcpSearch
	m.mcpConfirmDelete = ""
	m.layout()
}

// openMcpSearchResults rebuilds the /mcp search list from the loaded
// entries; an empty result set keeps the selector open with a note item.
func (m *model) openMcpSearchResults(query string, entries []mcpRegistryEntry) {
	items := mcpSearchSelectorItems(m.loc, entries)
	if len(items) == 0 {
		m.contextNote = t(m.loc, "mcp.searchNone", query)
		m.mode = modeChat
		m.layout()
		return
	}
	m.selector = newSelector(t(m.loc, "mcp.searchTitle", query), items, m.width, m.height-3)
	m.mode = modeMcpSearch
	m.layout()
}

// openMcpServerSelector rebuilds the /mcp list from the current snapshot.
func (m *model) openMcpServerSelector() {
	m.selector = newSelector(t(m.loc, "selector.mcpServers"),
		mcpSelectorItems(m.loc, m.mcpServers, m.mcpConfirmDelete), m.width, m.height-3)
	m.mode = modeMcp
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

// openThemeSelector opens the /theme picker with every registered theme;
// the current choice is marked, and moving the selection previews each
// palette live (the whole UI re-renders from the global styles).
func (m *model) openThemeSelector() {
	m.selector = newSelector(t(m.loc, "selector.themes"), themeSelectorItems(m.theme), m.width, m.height-3)
	m.mode = modeThemes
	m.layout()
}

// openProfileSelector opens the /profile picker with a loading placeholder;
// the profilesMsg handler rebuilds it with the loaded list. The stored
// profiles come from core, so the picker needs a round trip first.
func (m *model) openProfileSelector() {
	m.openProfileSelectorWith(nil)
}

// openProfileSelectorWith builds the /profile picker from the loaded list.
func (m *model) openProfileSelectorWith(profiles []toolProfileSummary) {
	title := t(m.loc, "selector.profiles")
	if profiles == nil {
		title = t(m.loc, "selector.profilesLoading")
	}
	m.selector = newSelector(title,
		profileSelectorItems(m.loc, m.toolProfile, profiles), m.width, m.height-3)
	m.mode = modeProfiles
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

	if m.mode == modeProfileForm {
		return m.updateProfileForm(msg)
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

	if m.mode == modeMcpForm {
		switch msg.String() {
		case "esc":
			m.mode = modeChat
			m.layout()
			return m, nil
		case "tab", "down":
			return m, m.mcpForm.nextField(1)
		case "shift+tab", "up":
			return m, m.mcpForm.nextField(-1)
		case "left":
			m.mcpForm.cycleEnum(-1)
			return m, nil
		case "right":
			m.mcpForm.cycleEnum(1)
			return m, nil
		case "ctrl+s":
			return m.submitMcpForm()
		case "enter":
			if m.mcpForm.focus == mcpFieldCount-1 {
				return m.submitMcpForm()
			}
			return m, m.mcpForm.nextField(1)
		}
		var cmd tea.Cmd
		m.mcpForm, cmd = m.mcpForm.update(msg)
		return m, cmd
	}

	// MCP registry search (modeMcpSearch): enter/a on an installable entry
	// opens the add form prefilled from the entry; esc returns to chat.
	if m.mode == modeMcpSearch {
		key := msg.String()
		if key == "esc" {
			m.mode = modeChat
			m.layout()
			return m, nil
		}
		if selected, ok := m.selector.selected(); ok && selected.kind == selectorMcpEntry &&
			(key == "enter" || key == "a") && !m.busy {
			if entry, ok := selected.payload.(mcpRegistryEntry); ok {
				if !entry.Installable {
					reason := entry.NotInstallable
					if len(entry.Requirements) > 0 {
						reason = strings.Join(entry.Requirements, "; ")
					}
					m.contextNote = t(m.loc, "mcp.entryNotInstallable", reason)
					return m, nil
				}
				m.mcpForm = newRegistryMcpForm(entry, m.width, m.loc)
				m.mode = modeMcpForm
				m.layout()
				return m, nil
			}
		}
	}

	// MCP server management (modeMcp): a = add, e = edit, r = refresh,
	// t/space = enable/disable, d/x = remove (two-stage like providers).
	if m.mode == modeMcp {
		key := msg.String()
		if key == "esc" {
			m.mcpConfirmDelete = ""
			m.mode = modeChat
			m.layout()
			return m, nil
		}
		selected, ok := m.selector.selected()
		armed := m.mcpConfirmDelete
		if armed != "" && (!ok || selected.id != armed) {
			// Selection moved: disarm the pending delete.
			m.mcpConfirmDelete = ""
		}
		if ok && selected.kind == selectorMcpAdd && (key == "a" || key == "enter") {
			m.mcpForm = newMcpForm(m.width, m.loc)
			m.mode = modeMcpForm
			m.layout()
			return m, nil
		}
		if ok && selected.kind == selectorMcpServer && !m.busy {
			switch key {
			case "e":
				if server, ok := selected.payload.(mcpServerSummary); ok {
					m.mcpForm = newEditMcpForm(server, m.width, m.loc)
					m.mode = modeMcpForm
					m.mcpConfirmDelete = ""
					m.layout()
					return m, nil
				}
			case "r":
				m.mcpConfirmDelete = ""
				m.controlPending = true
				return m, mcpRefreshCmd(m.comp, selected.id)
			case "t", " ":
				m.mcpConfirmDelete = ""
				m.controlPending = true
				if server, ok := selected.payload.(mcpServerSummary); ok {
					return m, mcpToggleCmd(m.comp, selected.id, !server.Enabled)
				}
				return m, nil
			case "d", "x", "enter":
				if armed != "" && key != "enter" || armed != "" && key == "enter" {
					// Confirmed: remove it.
					m.mcpConfirmDelete = ""
					m.controlPending = true
					return m, mcpRemoveCmd(m.comp, selected.id)
				}
				if armed == "" && key != "enter" {
					// First press: arm the remove confirmation.
					m.mcpConfirmDelete = selected.id
					return m, nil
				}
			}
		}
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
	case modeThemes:
		name := selected.id
		if !m.setTheme(name) {
			return m, nil
		}
		persistTheme(name)
		m.mode = modeChat
		m.layout()
		m.addBlock(blockMeta, t(m.loc, "theme.switched", name))
		m.syncViewport(true)
		return m, nil
	case modeProfiles:
		// The profile applies to conversations created from here on, so it
		// is client state rather than a per-conversation override (see
		// /profile NAME, which shares the same field).
		switch selected.kind {
		case selectorProfileNew:
			m.profileForm = newProfileForm(m.width, m.loc)
			m.mode = modeProfileForm
			m.layout()
			return m, m.profileForm.focusField(0)
		case selectorProfileDefault:
			m.toolProfile = ""
		case selectorProfile:
			m.toolProfile = selected.id
		default:
			return m, nil
		}
		m.mode = modeChat
		m.layout()
		if m.toolProfile == "" {
			m.addBlock(blockMeta, t(m.loc, "profile.cleared"))
		} else {
			m.addBlock(blockMeta, t(m.loc, "profile.selected", m.toolProfile))
		}
		m.syncViewport(true)
		return m, nil
	case modeSessions:
		switch selected.kind {
		case selectorNewSession:
			id := newSessionID()
			m, historyCmd := m.switchSessionWithHistory(id)
			return m, tea.Batch(historyCmd, bootstrapBackendCmd(m.comp, id))
		case selectorSession:
			if selected.id == m.session {
				// Re-selecting the current session just dismisses the
				// browser; switching would wipe the transcript.
				m.mode = modeChat
				m.layout()
				return m, nil
			}
			m, historyCmd := m.switchSessionWithHistory(selected.id)
			return m, tea.Batch(historyCmd, bootstrapBackendCmd(m.comp, selected.id))
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

// submitMcpForm saves the /mcp form. The manager validates the server with
// one real connect before storing, so saving may take a while on first runs
// (npx/uvx downloads) — the form stays up with a saving hint until the
// action completes.
func (m model) submitMcpForm() (tea.Model, tea.Cmd) {
	if m.controlPending || m.mcpForm.saving {
		return m, nil
	}
	values, err := m.mcpForm.values()
	if err != nil {
		m.mcpForm.err = err.Error()
		return m, nil
	}
	m.mcpForm.err = ""
	m.mcpForm.saving = true
	m.controlPending = true
	if m.mcpForm.edit {
		return m, mcpEditCmd(m.comp, values)
	}
	return m, mcpAddCmd(m.comp, values)
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
	m.invalidatePieces()
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
	}
	// Which harness (clone) this tui is attached to — root + git revision,
	// exactly what core prints at startup. Empty when the harness predates
	// identity publication.
	if m.harnessIdentity.Root != "" {
		hash := valueOr(m.harnessIdentity.GitHash, t(m.loc, "status.unknown"))
		lines = append(lines, t(m.loc, "status.detailHarness", m.harnessIdentity.Root, hash))
	}
	// This tui's own build, next to the harness line so a stale binary is
	// obvious. The builder copies sources into an isolated module, so the
	// binary carries no VCS revision of its own; provenance comes from the
	// plugins component when this binary is a managed install.
	lines = append(lines, m.selfStatusLine())
	lines = append(lines,
		t(m.loc, "status.detailContext", formatTokens(m.runtime.Context), valueOr(m.runtime.ContextSource, t(m.loc, "status.unknownSource"))),
		t(m.loc, "status.detailOutput", formatTokens(m.runtime.Output), valueOr(m.runtime.OutputSource, t(m.loc, "status.unknownSource"))),
		t(m.loc, "status.detailUsed", formatTokens(m.contextUsed), fmt.Sprintf("%.1f%%", contextPercent(m.contextUsed, m.runtime.Context)*100)),
	)
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

// selfStatusLine renders this tui's own identity for /status: version and
// binary path always, plus the plugin package's pinned ref and commit when
// the binary is a managed install. The ref@commit is what separates two
// builds that share the same component version.
func (m model) selfStatusLine() string {
	self := m.selfIdentity
	version := valueOr(self.Version, t(m.loc, "status.unknown"))
	if self.Binary == "" {
		return t(m.loc, "status.detailTui", version)
	}
	location := self.Binary
	if self.Package != "" {
		ref := valueOr(self.Ref, t(m.loc, "status.unknown"))
		commit := self.Commit
		if len(commit) > 7 {
			commit = commit[:7]
		}
		if commit != "" {
			ref += " " + commit
		}
		location = fmt.Sprintf("%s (%s @ %s)", location, self.Package, ref)
	}
	return t(m.loc, "status.detailTui", version) + " " + location
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
