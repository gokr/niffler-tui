package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
)

type uiMode int

const (
	modeChat uiMode = iota
	modeProviders
	modeCatalogProviders
	modeModels
	modeSessions
	modeConnectForm
	modeOAuth
	modeMcp
	modeMcpSearch
	modeMcpForm
	modeThemes
	modeProfiles
)

type selectorItemKind int

const (
	selectorProvider selectorItemKind = iota
	selectorEnvironment
	selectorConnect
	selectorCatalogProvider
	selectorCustomProvider
	selectorOAuthOpenAIBrowser
	selectorOAuthOpenAIDevice
	selectorOAuthAnthropic
	selectorModel
	selectorProviderDefaultModel
	selectorSession
	selectorNewSession
	selectorMcpServer
	selectorMcpAdd
	selectorMcpEntry
	selectorTheme
	selectorProfile
	selectorProfileDefault
)

type selectorItem struct {
	kind        selectorItemKind
	id          string
	title       string
	description string
	payload     any
}

func (i selectorItem) Title() string       { return i.title }
func (i selectorItem) Description() string { return i.description }
func (i selectorItem) FilterValue() string { return i.title + " " + i.id + " " + i.description }

type selectorState struct {
	list list.Model
}

func newSelector(title string, items []list.Item, width, height int) selectorState {
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	model := list.New(items, delegate, max(20, width), max(6, height))
	model.Title = title
	model.SetShowHelp(false)
	model.DisableQuitKeybindings()
	model.SetShowStatusBar(true)
	model.SetShowPagination(true)
	model.InfiniteScrolling = true
	return selectorState{list: model}
}

func (s *selectorState) setSize(width, height int) {
	s.list.SetSize(max(20, width), max(6, height))
}

func (s selectorState) selected() (selectorItem, bool) {
	item, ok := s.list.SelectedItem().(selectorItem)
	return item, ok
}

// mcpSelectorItems builds the /mcp list: configured servers plus the
// add-server entry. Live state comes from the catalog snapshot the manager
// embeds (registered bridge tools, session status).
func mcpSelectorItems(loc Locale, servers []mcpServerSummary, confirmDelete string) []list.Item {
	items := make([]list.Item, 0, len(servers)+1)
	for _, server := range servers {
		title := server.Name
		description := server.Type
		switch {
		case !server.Enabled:
			title = "× " + title
			description += " · " + t(loc, "selector.mcpDisabled")
		case server.Live:
			title = "● " + title
			if server.Bridge != nil && server.Bridge.Connected {
				description += " · " + t(loc, "selector.mcpSession")
			}
		default:
			title = "○ " + title
			description += " · " + t(loc, "selector.mcpStopped")
		}
		description += " · " + t(loc, "selector.mcpTools", strconv.Itoa(server.ToolCount))
		if server.Type == "stdio" && server.Command != "" {
			description += " · " + endpointCommand(server.Command)
		} else if host := endpointHost(server.URL); host != "" {
			description += " · " + host
		}
		if server.Approval == "always" {
			description += " · " + t(loc, "selector.mcpApproval")
		}
		if server.Error != "" {
			description += " · " + server.Error
		} else if server.Bridge != nil && server.Bridge.LastError != "" {
			description += " · " + server.Bridge.LastError
		}
		items = append(items, selectorItem{
			kind: selectorMcpServer,
			id:   server.Name, title: title, description: strings.Trim(description, " ·"),
			payload: server,
		})
	}
	items = append(items, selectorItem{
		kind: selectorMcpAdd,
		id:   "__mcp_add__", title: t(loc, "selector.mcpAdd"),
		description: t(loc, "selector.mcpAddDesc"),
	})
	return items
}

// mcpSearchSelectorItems renders /mcp search results: installable entries
// first-class (enter opens a prefilled add form), non-installable ones with
// their reason so the distinction is visible before selection.
func mcpSearchSelectorItems(loc Locale, entries []mcpRegistryEntry) []list.Item {
	items := make([]list.Item, 0, len(entries))
	for i, entry := range entries {
		title := entry.Title
		if title == "" {
			title = entry.Name
		}
		description := entry.Name
		if entry.Version != "" {
			description += " v" + entry.Version
		}
		if entry.Transport != "" {
			description += " · " + entry.Transport
		}
		if entry.Installable {
			title = "+ " + title
			description += " · " + t(loc, "selector.mcpInstallable")
		} else {
			title = "− " + title
			reason := entry.NotInstallable
			if len(entry.Requirements) > 0 {
				reason = strings.Join(entry.Requirements, "; ")
			}
			if reason == "" {
				reason = t(loc, "selector.mcpNotInstallable")
			}
			description += " · " + reason
		}
		if entry.Description != "" {
			description += "\n" + entry.Description
		}
		items = append(items, selectorItem{
			kind: selectorMcpEntry,
			id:   strconv.Itoa(i), title: title, description: description,
			payload: entry,
		})
	}
	return items
}

// endpointCommand shortens a stdio command for the selector description.
func endpointCommand(command string) string {
	if i := strings.LastIndexByte(command, '/'); i >= 0 {
		command = command[i+1:]
	}
	return command
}

func providerSelectorItems(loc Locale, providers []providerSummary, status providerStatusResponse) []list.Item {
	items := make([]list.Item, 0, len(providers)+2)
	envTitle := t(loc, "selector.envDefault")
	envDescription := t(loc, "selector.fallback")
	if status.Source == "environment" {
		envTitle = "● " + envTitle
		if status.Provider.Model != "" {
			envDescription = status.Provider.Model + " · " + endpointHost(status.Provider.BaseURL)
		}
	}
	items = append(items, selectorItem{
		kind: selectorEnvironment,
		id:   "__environment__", title: envTitle, description: envDescription,
	})
	for _, provider := range providers {
		title := provider.Nickname
		if provider.Active {
			title = "● " + title
		}
		description := provider.Model
		if provider.AuthType == "oauth" {
			description += " · OAuth"
		}
		if host := endpointHost(provider.BaseURL); host != "" {
			description += " · " + host
		}
		if provider.Catalog != "" && provider.Catalog != provider.Nickname {
			description += " · " + t(loc, "selector.catalog", provider.Catalog)
		}
		items = append(items, selectorItem{
			kind: selectorProvider,
			id:   provider.Nickname, title: title, description: strings.Trim(description, " ·"),
			payload: provider,
		})
	}
	items = append(items, selectorItem{
		kind: selectorConnect,
		id:   "__connect__", title: t(loc, "selector.connectProvider"),
		description: t(loc, "selector.connectProviderDesc"),
	})
	items = append(items, selectorItem{
		kind: selectorOAuthOpenAIBrowser,
		id:   "__oauth_openai_browser__", title: t(loc, "oauth.option.openai.browser"),
		description: t(loc, "oauth.option.openai.desc"),
	})
	items = append(items, selectorItem{
		kind: selectorOAuthOpenAIDevice,
		id:   "__oauth_openai_device__", title: t(loc, "oauth.option.openai.device"),
		description: t(loc, "oauth.option.openai.deviceDesc"),
	})
	items = append(items, selectorItem{
		kind: selectorOAuthAnthropic,
		id:   "__oauth_anthropic__", title: t(loc, "oauth.option.anthropic"),
		description: t(loc, "oauth.option.anthropic.desc"),
	})
	return items
}

func catalogProviderItems(loc Locale, providers []catalogProvider) []list.Item {
	items := make([]list.Item, 0, len(providers)+1)
	items = append(items, selectorItem{
		kind: selectorCustomProvider,
		id:   "__custom__", title: t(loc, "selector.customProvider"),
		description: t(loc, "selector.customProviderDesc"),
	})
	for _, provider := range providers {
		if !openAICompatibleProvider(provider) {
			continue
		}
		name := provider.Name
		if name == "" {
			name = provider.ID
		}
		if provider.Configured {
			name = "● " + name
		}
		description := provider.ID
		if host := endpointHost(provider.API); host != "" {
			description += " · " + host
		}
		description += " · " + t(loc, "selector.modelsCount", strconv.Itoa(provider.ModelCount))
		if provider.Configured {
			description += " · " + t(loc, "selector.connected")
		}
		items = append(items, selectorItem{
			kind: selectorCatalogProvider,
			id:   provider.ID, title: name, description: description, payload: provider,
		})
	}
	return items
}

func modelSelectorItems(loc Locale, models []modelSummary, runtime runtimeResolution, modelOverride, providerDefault string) []list.Item {
	items := make([]list.Item, 0, len(models)+1)
	defaultModel := providerDefault
	if defaultModel == "" && modelOverride == "" {
		defaultModel = runtime.Model
	}
	if defaultModel == "" {
		defaultModel = t(loc, "selector.providerDefault")
	}
	items = append(items, selectorItem{
		kind: selectorProviderDefaultModel,
		id:   "__default__", title: t(loc, "selector.useProviderDefault"),
		description: defaultModel,
	})
	for _, candidate := range models {
		title := candidate.Name
		if title == "" {
			title = candidate.ID
		}
		if candidate.ID == modelOverride || (modelOverride == "" && candidate.ID == runtime.Model) {
			title = "● " + title
		}
		flags := make([]string, 0, 3)
		if candidate.Reasoning {
			flags = append(flags, t(loc, "selector.reasoning"))
		}
		if candidate.ToolCall {
			flags = append(flags, t(loc, "selector.tools"))
		}
		if candidate.Limit.Context > 0 {
			flags = append(flags, t(loc, "selector.ctx", formatTokens(candidate.Limit.Context)))
		}
		description := candidate.ID
		if len(flags) > 0 {
			description += " · " + strings.Join(flags, " · ")
		}
		items = append(items, selectorItem{
			kind: selectorModel,
			id:   candidate.ID, title: title, description: description, payload: candidate,
		})
	}
	return items
}

func openAICompatibleProvider(provider catalogProvider) bool {
	switch provider.ID {
	case "deepseek", "openai", "openrouter":
		return true
	}
	return strings.Contains(strings.ToLower(provider.NPM), "openai")
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return strings.TrimSpace(raw)
	}
	if parsed.Port() != "" {
		return parsed.Hostname() + ":" + parsed.Port()
	}
	return parsed.Hostname()
}

// themeSelectorItems builds the /theme picker: every registered theme with
// its one-line description, the active one marked. Selection re-renders
// live, so arrowing through the list previews each palette immediately.
func themeSelectorItems(current string) []list.Item {
	items := make([]list.Item, 0, len(themeNames))
	for _, name := range themeNames {
		title := name
		if name == current {
			title = "● " + name
		}
		items = append(items, selectorItem{
			kind: selectorTheme,
			id:   name, title: title,
			description: themeDescription(name),
		})
	}
	return items
}

// profileSelectorItems builds the /profile picker: the "no profile" entry
// first (which clears the selection), then every stored profile marked with
// the resolved tool count and estimated token cost so the budget impact is
// visible while choosing.
func profileSelectorItems(loc Locale, current string, profiles []toolProfileSummary) []list.Item {
	noProfile := t(loc, "selector.profileDefault")
	if current == "" {
		noProfile = "● " + noProfile
	}
	items := make([]list.Item, 0, len(profiles)+1)
	items = append(items, selectorItem{
		kind: selectorProfileDefault,
		id:   "", title: noProfile,
		description: t(loc, "selector.profileDefaultDesc"),
	})
	for _, profile := range profiles {
		title := profile.Name
		if profile.Name == current {
			title = "● " + title
		}
		desc := t(loc, "selector.profileTools", fmt.Sprint(profile.ToolCount),
			formatTokens(profile.EstTokens))
		if profile.Note != "" {
			desc += " — " + profile.Note
		}
		if len(profile.Missing) > 0 {
			desc += "  " + t(loc, "selector.profileMissing", fmt.Sprint(len(profile.Missing)))
		}
		items = append(items, selectorItem{
			kind: selectorProfile, id: profile.Name, title: title,
			description: desc, payload: profile,
		})
	}
	return items
}

// sessionSelectorItems builds the /session list: the current session first,
// then every stored conversation (newest first), plus a "new session" entry.
func sessionSelectorItems(loc Locale, current string, sessions []sessionSummary) []list.Item {
	items := make([]list.Item, 0, len(sessions)+2)
	items = append(items, selectorItem{
		kind: selectorNewSession,
		id:   "__new__", title: t(loc, "selector.newSession"),
		description: t(loc, "selector.newSessionDesc"),
	})
	for _, s := range sessions {
		title := s.ID
		marked := ""
		if s.ID == current {
			marked = "● "
		}
		if s.Title != "" {
			title = s.Title
			if marked != "" {
				title = marked + title
			}
		} else if marked != "" {
			title = marked + s.ID
		}
		description := s.ID
		if stamp := fmtTimeShort(s.CreatedAt); stamp != "" {
			description += " · " + stamp
		}
		items = append(items, selectorItem{
			kind: selectorSession,
			id:   s.ID, title: title, description: description,
			payload: s,
		})
	}
	return items
}

func fmtTimeShort(ts float64) string {
	if ts <= 0 {
		return ""
	}
	t := time.Unix(int64(ts), 0)
	return t.Format("2006-01-02 15:04")
}
