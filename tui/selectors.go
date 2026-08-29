package main

import (
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
)

type selectorItemKind int

const (
	selectorProvider selectorItemKind = iota
	selectorEnvironment
	selectorConnect
	selectorCatalogProvider
	selectorCustomProvider
	selectorModel
	selectorProviderDefaultModel
	selectorSession
	selectorNewSession
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
