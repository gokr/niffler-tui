package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

const controlTimeout = 10 * time.Second

// compactTimeout is longer than controlTimeout: the compactor may make
// several LLM calls inside its own 90s budget, so /compact waits for the
// whole rung instead of the control-plane norm.
const compactTimeout = 150 * time.Second

// okResponse is the minimal envelope of backend RPC results that carry only
// a success flag. Warning carries a partial-failure notice (e.g. "stored but
// bridge did not start") that the UI must still surface.
type okResponse struct {
	OK      bool   `json:"ok"`
	Warning string `json:"warning"`
}

type providerSummary struct {
	Nickname    string `json:"nickname"`
	AuthType    string `json:"authType"`
	BaseURL     string `json:"baseUrl"`
	Model       string `json:"model"`
	Catalog     string `json:"catalog"`
	Context     int    `json:"context"`
	Active      bool   `json:"active"`
	StripPrefix bool   `json:"stripPrefix"`
}

type providerListResponse struct {
	Providers []providerSummary `json:"providers"`
}

type providerStatusResponse struct {
	OK       bool            `json:"ok"`
	Source   string          `json:"source"`
	Provider providerSummary `json:"provider"`
}

type runtimeResolution struct {
	OK             bool   `json:"ok"`
	Provider       string `json:"provider"`
	ProviderSource string `json:"providerSource"`
	Model          string `json:"model"`
	Catalog        string `json:"catalog"`
	Context        int    `json:"context"`
	ContextSource  string `json:"contextSource"`
	Output         int    `json:"output"`
	OutputSource   string `json:"outputSource"`
}

type catalogProvider struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	API        string `json:"api"`
	NPM        string `json:"npm"`
	Configured bool   `json:"configured"`
	ModelCount int    `json:"modelCount"`
}

type catalogProvidersResponse struct {
	Providers []catalogProvider `json:"providers"`
}

// harnessIdentity is the owning harness's identity from the catalog — the
// clone root plus its git revision — so /status shows which instance the
// tui is talking to (two clones must never be confused).
type harnessIdentity struct {
	Root    string `json:"root"`
	GitHash string `json:"gitHash"`
}

// selfIdentity describes this tui's own binary, for /status. The builder
// compiles the tui in an isolated generated module with the sources copied
// in, so Go's VCS stamping records no revision and the binary cannot report
// its own commit; provenance comes from the plugins component instead,
// which knows the clone, the pinned ref and the commit it was built from.
type selfIdentity struct {
	Version string // the component version registered on the bus
	Binary  string // resolved path to this executable
	// Provenance, filled in from the plugins component when the package is
	// a known install; empty when plugins is absent or the binary is not a
	// managed install (e.g. `make build` in a checkout).
	Package string
	Ref     string
	Commit  string
}

// installedPackage mirrors the entries plugins' plugin_installed returns.
type installedPackage struct {
	Name       string  `json:"name"`
	Repo       string  `json:"repo"`
	Ref        string  `json:"ref"`
	Dir        string  `json:"dir"`
	Version    string  `json:"version"`
	Commit     string  `json:"commit"`
	AddedAt    float64 `json:"addedAt"`
	Components []struct {
		Name        string `json:"name"`
		Binary      string `json:"binary"`
		Interactive bool   `json:"interactive"`
		Spawned     bool   `json:"spawned"`
	} `json:"components"`
}

// loadSelfIdentity resolves this binary's path and version, then tries to
// attribute it to an installed plugin package by matching the component
// binary path. The plugin lookup is best effort: the tui must stay usable
// against a harness with no plugins component registered at all.
func loadSelfIdentity(comp *sdk.Component) selfIdentity {
	id := selfIdentity{Version: componentVersion}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		id.Binary = exe
	}
	if id.Binary == "" {
		return id
	}

	// Best effort, and tolerant by construction: a missing plugins
	// component, a request error, or a non-JSON reply all just leave the
	// provenance fields empty.
	var listed struct {
		Packages []installedPackage `json:"packages"`
	}
	if err := requestInto(comp, "plugins", "plugin_installed", map[string]any{}, &listed); err != nil {
		return id
	}
	for _, pkg := range listed.Packages {
		for _, c := range pkg.Components {
			if c.Binary == "" {
				continue
			}
			bin := c.Binary
			if resolved, err := filepath.EvalSymlinks(bin); err == nil {
				bin = resolved
			}
			if bin == id.Binary {
				id.Package = pkg.Name
				id.Ref = pkg.Ref
				id.Commit = pkg.Commit
				return id
			}
		}
	}
	return id
}

type modelLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type modelSummary struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Reasoning bool       `json:"reasoning"`
	ToolCall  bool       `json:"tool_call"`
	Limit     modelLimit `json:"limit"`
	// ReleaseDate is the catalog's release_date (YYYY-MM-DD, may be empty);
	// used to auto-pick the newest model when connecting a provider.
	ReleaseDate string `json:"release_date"`
}

type modelsResponse struct {
	Models []modelSummary `json:"models"`
}

type conversationState struct {
	ProviderOverride string
	ModelOverride    string
	ThinkingEffort   string
	// Approvals is the conversation's gate mode from its header ("auto", or
	// "" for ask). Read at attach so this TUI's answering matches core's mode
	// even when another client changed it since the last visit.
	Approvals    string
	Provider     string
	Model        string
	Context      int
	ContextUsed  int
	PromptTokens int
	Cwd          string
	CachePrompt  int
	CacheRead    int
}

type bootstrapMsg struct {
	// Session is the conversation this snapshot was loaded for; a snapshot
	// arriving after a further session switch is dropped.
	Session          string
	Providers        providerListResponse
	ProviderStatus   providerStatusResponse
	CatalogProviders []catalogProvider
	Identity         harnessIdentity
	Self             selfIdentity
	Conversation     conversationState
	// ConversationExists reports whether the conversation header is already
	// in the store; false means the first turn will create it (and may pin
	// the workspace — see model.sessionNeedsCreate).
	ConversationExists bool
	Runtime            runtimeResolution
	Warnings           []string
	// Slash is the merged slash-command registry (store checkpoint first,
	// catalog snapshot fallback); SlashErr reports a load failure. The
	// registry is global (not per-session), so these survive a stale
	// session drop and are applied regardless.
	SlashCommands []slashCommand
	SlashErr      error
}

type runtimeRefreshedMsg struct {
	// Session identifies the conversation whose model override fed the
	// runtime resolution; on mismatch only the global parts (providers,
	// status) are applied.
	Session    string
	Providers  providerListResponse
	Status     providerStatusResponse
	Runtime    runtimeResolution
	ListErr    error
	StatusErr  error
	ResolveErr error
}

type catalogProvidersMsg struct {
	Providers []catalogProvider
	Err       error
}

type modelsLoadedMsg struct {
	Catalog string
	Models  []modelSummary
	Err     error
}

// modelCandidate is one model belonging to one configured provider. The
// provider tag is what lets the /model picker group, label and filter a
// cross-provider list — a bare modelSummary is scoped to a single catalog.
type modelCandidate struct {
	modelSummary
	Provider string // stored nickname, or the environment fallback's name
	Served   bool   // came from the provider's live /models probe (no catalog)
}

// allModelsMsg is the cross-provider model list for the /model picker. Key
// identifies the provider set it was built from, so a reply that lands after
// a provider add/remove is dropped instead of mislabeled.
type allModelsMsg struct {
	Key   string
	Items []modelCandidate
	Errs  []string
}

type providerActionMsg struct {
	Action   string
	Nickname string
	// Detail carries action-specific context for the status label (e.g. the
	// new strip-prefix state for the "strip" action).
	Detail string
	Err    error
}

type modelActionMsg struct {
	// Session is the conversation the override was saved for; a completion
	// arriving after a session switch is dropped (its runtime snapshot and
	// rollback belong to the old conversation).
	Session  string
	Selected string
	Previous string
	// A cross-provider pick saves provider AND model in one session call;
	// ProviderChanged marks that the call carried a provider key, so an error
	// rolls the old pin back instead of clearing it, and a model-only save
	// leaves the provider pin alone.
	SelectedProvider string
	PreviousProvider string
	ProviderChanged  bool
	Runtime          runtimeResolution
	Warning          string
	Err              error
}

// providerBusEventMsg signals a change on ev.provider.>; the client refreshes
// provider and runtime state in response. Changed marks the events that prove
// the active backend moved (ev.provider.switch with a different nickname): the
// conversation's model pin belongs to the provider it was chosen under, so it
// must be dropped then. Other provider events only refresh the views.
type providerBusEventMsg struct{ Changed bool }

type modelsCatalogUpdatedMsg struct{}

// compactResultMsg reports one manual /compact control call. Session guards
// against a stale result after a conversation switch; Err is a transport or
// decode failure, not a declined compaction (that is Compacted=false + Reason).
type compactResultMsg struct {
	Session    string
	Compacted  bool
	Reason     string
	Before     int
	After      int
	Generation int
	Err        error
}

func requestInto(comp *sdk.Component, component, tool string, args any, out any) error {
	raw, err := comp.Request(component, tool, args, controlTimeout)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", component, tool, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s.%s: %w", component, tool, err)
	}
	return nil
}

func loadProviderList(comp *sdk.Component) (providerListResponse, error) {
	var response providerListResponse
	err := requestInto(comp, "provider", "provider_list", map[string]any{}, &response)
	return response, err
}

func loadProviderStatus(comp *sdk.Component) (providerStatusResponse, error) {
	var response providerStatusResponse
	err := requestInto(comp, "provider", "provider_status", map[string]any{}, &response)
	return response, err
}

// servedModelsMsg reports the live model ids a provider's own /models
// endpoint serves (via the provider component's provider_models tool). The
// connect/edit form uses it to auto-pick or repair the model field; the
// catalog prefill remains the fallback when the probe fails.
type servedModelsMsg struct {
	Models  []string
	Err     error
	editFor string // nickname when probed for the edit form; "" for connect
}

// loadServedModelsCmd probes the endpoint. For the connect form the
// baseUrl/apiKey are explicit (the key is not saved yet); for the edit form
// the stored credential is used by nickname.
func loadServedModelsCmd(comp *sdk.Component, nickname, baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		args := map[string]any{"refresh": true}
		if nickname != "" {
			args["nickname"] = nickname
		} else {
			args["baseUrl"] = baseURL
			if apiKey != "" {
				args["apiKey"] = apiKey
			}
		}
		var response struct {
			OK     bool `json:"ok"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		err := requestInto(comp, "provider", "provider_models", args, &response)
		if err != nil {
			return servedModelsMsg{Err: err, editFor: nickname}
		}
		ids := make([]string, 0, len(response.Models))
		for _, model := range response.Models {
			if model.ID != "" {
				ids = append(ids, model.ID)
			}
		}
		return servedModelsMsg{Models: ids, editFor: nickname}
	}
}

func resolveRuntime(comp *sdk.Component, providerOverride, modelOverride string) (runtimeResolution, error) {
	args := map[string]any{}
	if strings.TrimSpace(providerOverride) != "" {
		args["provider"] = strings.TrimSpace(providerOverride)
	}
	if strings.TrimSpace(modelOverride) != "" {
		args["model"] = strings.TrimSpace(modelOverride)
	}
	var response runtimeResolution
	err := requestInto(comp, "llm", "llm_resolve", args, &response)
	return response, err
}

func loadCatalogProviders(comp *sdk.Component) ([]catalogProvider, error) {
	var response catalogProvidersResponse
	if err := requestInto(comp, "models", "models_providers", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return response.Providers, nil
}

func loadConversationState(comp *sdk.Component, session string) (conversationState, bool, error) {
	var response struct {
		OK    bool `json:"ok"`
		Value struct {
			ProviderOverride string `json:"providerOverride"`
			ModelOverride    string `json:"modelOverride"`
			ThinkingEffort   string `json:"thinkingEffort"`
			Approvals        string `json:"approvals"`
			Provider         string `json:"provider"`
			Model            string `json:"model"`
			Context          int    `json:"context"`
			ContextUsed      int    `json:"contextUsed"`
			PromptTokens     int    `json:"promptTokens"`
			Cwd              string `json:"cwd"`
			CachePrompt      int    `json:"cachePrompt"`
			CacheRead        int    `json:"cacheRead"`
		} `json:"value"`
		Code string `json:"code"`
	}
	if err := requestInto(comp, "store", "get", map[string]any{
		"kind": "conversation", "id": session,
	}, &response); err != nil {
		return conversationState{}, false, err
	}
	if !response.OK {
		return conversationState{}, false, nil
	}
	return conversationState{
		ProviderOverride: response.Value.ProviderOverride,
		ModelOverride:    response.Value.ModelOverride,
		ThinkingEffort:   response.Value.ThinkingEffort,
		Approvals:        response.Value.Approvals,
		Provider:         response.Value.Provider, Model: response.Value.Model,
		Context: response.Value.Context, ContextUsed: response.Value.ContextUsed,
		PromptTokens: response.Value.PromptTokens,
		Cwd:          response.Value.Cwd,
		CachePrompt:  response.Value.CachePrompt,
		CacheRead:    response.Value.CacheRead,
	}, true, nil
}

// sessionSummary is one conversation listed from the store for /session.
//
// Parent is the subagent lineage the agent component records (store kind
// sessionmeta): a non-empty parent means the conversation exists as a
// delegated child's own session. The browser hides those by default — a
// conversation that delegates accumulates dozens of them, and they are not
// what a human switches between. Closed marks a retired child (its records
// survive; only further continuation refuses).
type sessionSummary struct {
	ID        string
	Title     string
	CreatedAt float64
	Parent    string
	Closed    bool
}

// subagent reports whether the conversation is a delegated child session.
func (s sessionSummary) subagent() bool { return s.Parent != "" }

// sessionListMsg carries the store's conversations for the /session selector.
type sessionListMsg struct {
	Sessions []sessionSummary
	Err      error
}

// storePageLimit is the store's per-call cap (its `limit` is clamped to this).
const storePageLimit = 1000

// storeMaxPages bounds a paged read so a huge kind cannot make the UI chatty:
// ten pages is 10k documents, far beyond what a session list needs.
const storeMaxPages = 10

// pageStoreList reads every document of one kind by following the store's
// nextAfter cursor, up to storeMaxPages. A single `list` answers with at most
// 100 documents in id order, so the browser used to see an arbitrary slice of
// the keyspace — with subagent sessions in it, the human's own conversations
// were often not in the window at all. Items stay raw so one helper serves
// every kind the browser reads.
func pageStoreList(comp *sdk.Component, kind string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	after := ""
	for page := 0; page < storeMaxPages; page++ {
		var response struct {
			Items     []json.RawMessage `json:"items"`
			HasMore   bool              `json:"hasMore"`
			NextAfter string            `json:"nextAfter"`
		}
		args := map[string]any{"kind": kind, "limit": storePageLimit}
		if after != "" {
			args["after"] = after
		}
		if err := requestInto(comp, "store", "list", args, &response); err != nil {
			return nil, err
		}
		all = append(all, response.Items...)
		if !response.HasMore || response.NextAfter == "" {
			break
		}
		after = response.NextAfter
	}
	return all, nil
}

// conversationHeader is the stored conversation fields the browser reads.
type conversationHeader struct {
	Title     string  `json:"title"`
	CreatedAt float64 `json:"createdAt"`
}

// sessionRow is one stored conversation header.
type sessionRow struct {
	ID    string             `json:"id"`
	Value conversationHeader `json:"value"`
}

// sessionMeta is the agent component's lineage record for a child session.
type sessionMeta struct {
	Parent string `json:"parent"`
	Closed bool   `json:"closed"`
}

// metaRow is one subagent-lineage record: the child session id plus the
// parent that spawned it.
type metaRow struct {
	ID    string      `json:"id"`
	Value sessionMeta `json:"value"`
}

// loadSessionList lists the store's conversations, newest first, each marked
// with its subagent lineage. The lineage read is best-effort: when it fails
// (an older core, a store hiccup) the conversations still list, only without
// the filter — degrading to the previous behavior instead of an empty browser.
func loadSessionList(comp *sdk.Component) ([]sessionSummary, error) {
	rows, err := pageStoreList(comp, "conversation")
	if err != nil {
		return nil, err
	}
	conversations := make([]sessionRow, 0, len(rows))
	for _, raw := range rows {
		var row sessionRow
		if json.Unmarshal(raw, &row) == nil {
			conversations = append(conversations, row)
		}
	}
	var metas []metaRow
	if raws, err := pageStoreList(comp, "sessionmeta"); err == nil {
		for _, raw := range raws {
			var meta metaRow
			if json.Unmarshal(raw, &meta) == nil {
				metas = append(metas, meta)
			}
		}
	}
	return buildSessionSummaries(conversations, metas), nil
}

// buildSessionSummaries joins conversation headers with their lineage records
// and orders the result newest first. Pure, so the join (which decides what the
// browser hides) is testable without a bus.
func buildSessionSummaries(conversations []sessionRow, metas []metaRow) []sessionSummary {
	lineage := make(map[string]metaRow, len(metas))
	for _, meta := range metas {
		lineage[meta.ID] = meta
	}
	sessions := make([]sessionSummary, 0, len(conversations))
	for _, row := range conversations {
		sessions = append(sessions, sessionSummary{
			ID: row.ID, Title: row.Value.Title, CreatedAt: row.Value.CreatedAt,
			Parent: lineage[row.ID].Value.Parent, Closed: lineage[row.ID].Value.Closed,
		})
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt > sessions[j].CreatedAt
	})
	return sessions
}

func sessionListCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		sessions, err := loadSessionList(comp)
		return sessionListMsg{Sessions: sessions, Err: err}
	}
}

// bootstrapBackendCmd loads the backend state the header and controls need.
// The independent lookups run concurrently; the conversation state and the
// runtime resolution are sequential because the resolution takes the
// persisted conversation model override as its argument.
// loadHarnessIdentity reads the owning harness's root + git revision from
// the catalog list response (core publishes them for exactly this purpose:
// showing users which clone a UI is attached to).
func loadHarnessIdentity(comp *sdk.Component) (harnessIdentity, error) {
	var identity harnessIdentity
	err := requestInto(comp, "core", "catalog", map[string]any{"op": "list"}, &identity)
	return identity, err
}

func bootstrapBackendCmd(comp *sdk.Component, session string) tea.Cmd {
	return func() tea.Msg {
		var msg bootstrapMsg
		var (
			providers  providerListResponse
			status     providerStatusResponse
			catalog    []catalogProvider
			slashCmds  []slashCommand
			identity   harnessIdentity
			self       selfIdentity
			listErr    error
			statusErr  error
			catalogErr error
			slashErr   error
			idErr      error
		)
		var wg sync.WaitGroup
		wg.Add(6)
		go func() { defer wg.Done(); providers, listErr = loadProviderList(comp) }()
		go func() { defer wg.Done(); status, statusErr = loadProviderStatus(comp) }()
		go func() { defer wg.Done(); catalog, catalogErr = loadCatalogProviders(comp) }()
		go func() { defer wg.Done(); slashCmds, slashErr = loadSlashTable(comp) }()
		go func() { defer wg.Done(); identity, idErr = loadHarnessIdentity(comp) }()
		// Best effort and never fatal: the plugin lookup inside degrades to
		// version + binary path when plugins is not registered.
		go func() { defer wg.Done(); self = loadSelfIdentity(comp) }()
		wg.Wait()
		if listErr != nil {
			msg.Warnings = append(msg.Warnings, listErr.Error())
		} else {
			msg.Providers = providers
		}
		if statusErr != nil {
			msg.Warnings = append(msg.Warnings, statusErr.Error())
		} else {
			msg.ProviderStatus = status
		}
		if catalogErr != nil {
			msg.Warnings = append(msg.Warnings, catalogErr.Error())
		} else {
			msg.CatalogProviders = catalog
		}
		if idErr == nil {
			msg.Identity = identity
		}
		msg.Self = self
		msg.SlashCommands = slashCmds
		msg.SlashErr = slashErr
		conversation, exists, err := loadConversationState(comp, session)
		if err != nil {
			msg.Warnings = append(msg.Warnings, err.Error())
		} else {
			msg.Conversation = conversation
			msg.ConversationExists = exists
		}
		resolved, err := resolveRuntime(comp, conversation.ProviderOverride, conversation.ModelOverride)
		if err != nil {
			msg.Warnings = append(msg.Warnings, err.Error())
		} else {
			msg.Runtime = resolved
		}
		msg.Session = session
		return msg
	}
}

func refreshRuntimeCmd(comp *sdk.Component, session, providerOverride, modelOverride string) tea.Cmd {
	return func() tea.Msg {
		providers, listErr := loadProviderList(comp)
		status, statusErr := loadProviderStatus(comp)
		resolved, resolveErr := resolveRuntime(comp, providerOverride, modelOverride)
		return runtimeRefreshedMsg{
			Session:   session,
			Providers: providers, Status: status, Runtime: resolved,
			ListErr: listErr, StatusErr: statusErr, ResolveErr: resolveErr,
		}
	}
}

func loadCatalogProvidersCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		providers, err := loadCatalogProviders(comp)
		return catalogProvidersMsg{Providers: providers, Err: err}
	}
}

func loadModelsCmd(comp *sdk.Component, catalog string) tea.Cmd {
	return func() tea.Msg {
		if catalog == "" {
			return modelsLoadedMsg{Catalog: catalog, Err: fmt.Errorf("active provider has no catalog id")}
		}
		var response modelsResponse
		err := requestInto(comp, "models", "models_list", map[string]any{
			"provider": catalog, "status": "active", "input": "text",
			"toolCall": true, "limit": 500,
		}, &response)
		return modelsLoadedMsg{Catalog: catalog, Models: response.Models, Err: err}
	}
}

// allModelsKey identifies the provider set a cross-provider model list was
// built from: every configured nickname with its catalog id, plus the
// environment fallback when active. A reply whose key no longer matches is
// stale (a provider was added/removed while it was in flight).
func allModelsKey(providers []providerSummary, status providerStatusResponse) string {
	var b strings.Builder
	seen := map[string]bool{}
	add := func(nick, catalog string) {
		if nick == "" || seen[nick] {
			return
		}
		seen[nick] = true
		b.WriteString(nick)
		b.WriteByte(0)
		b.WriteString(catalog)
		b.WriteByte(1)
	}
	for _, p := range providers {
		add(p.Nickname, p.Catalog)
	}
	if status.Source == "environment" {
		add(status.Provider.Nickname, status.Provider.Catalog)
	}
	return b.String()
}

// loadAllModelsCmd fetches every configured provider's models in ONE command
// so the /model picker can list them together: catalog-backed providers
// through models_list (capabilities and limits included), and providers with
// no catalog entry through their own /models endpoint (ids only). Sequential
// on purpose — providers are few and each round trip is short, and one msg
// keeps the picker's spinner and ordering honest.
func loadAllModelsCmd(comp *sdk.Component, providers []providerSummary, status providerStatusResponse) tea.Cmd {
	type target struct{ nick, catalog string }
	var targets []target
	seen := map[string]bool{}
	for _, p := range providers {
		if p.Nickname == "" || seen[p.Nickname] {
			continue
		}
		seen[p.Nickname] = true
		targets = append(targets, target{p.Nickname, p.Catalog})
	}
	if status.Source == "environment" && status.Provider.Nickname != "" &&
		!seen[status.Provider.Nickname] {
		targets = append(targets, target{status.Provider.Nickname, status.Provider.Catalog})
	}
	key := allModelsKey(providers, status)
	return func() tea.Msg {
		var items []modelCandidate
		var errs []string
		added := map[string]bool{}
		appendOne := func(provider string, summary modelSummary, served bool) {
			if summary.ID == "" {
				return
			}
			k := provider + "\x00" + summary.ID
			if added[k] {
				return
			}
			added[k] = true
			items = append(items, modelCandidate{
				modelSummary: summary, Provider: provider, Served: served,
			})
		}
		for _, t := range targets {
			if t.catalog != "" {
				var response modelsResponse
				if err := requestInto(comp, "models", "models_list", map[string]any{
					"provider": t.catalog, "status": "active", "input": "text",
					"toolCall": true, "limit": 500,
				}, &response); err != nil {
					errs = append(errs, t.nick+": "+err.Error())
					continue
				}
				for _, summary := range response.Models {
					appendOne(t.nick, summary, false)
				}
				continue
			}
			// No catalog id (a custom endpoint such as a self-hosted model):
			// ask the provider itself which ids it serves.
			var response struct {
				Models []struct {
					ID string `json:"id"`
				} `json:"models"`
			}
			if err := requestInto(comp, "provider", "provider_models", map[string]any{
				"nickname": t.nick, "refresh": true,
			}, &response); err != nil {
				errs = append(errs, t.nick+": "+err.Error())
				continue
			}
			for _, served := range response.Models {
				appendOne(t.nick, modelSummary{ID: served.ID}, true)
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Provider != items[j].Provider {
				return items[i].Provider < items[j].Provider
			}
			return items[i].ID < items[j].ID
		})
		return allModelsMsg{Key: key, Items: items, Errs: errs}
	}
}

// setConversationProviderModelCmd pins provider AND model in one session
// call. Core treats the presence of each key as set/clear, so a cross-provider
// model pick has to land atomically — two calls would leave a window where the
// new model pairs with the old provider (and a crash between them strands a
// half-changed conversation).
func setConversationProviderModelCmd(comp *sdk.Component, session, provider, selected,
	prevProvider, prevModel string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK             bool   `json:"ok"`
			Provider       string `json:"provider"`
			ProviderSource string `json:"providerSource"`
			Model          string `json:"model"`
			Catalog        string `json:"catalog"`
			Context        int    `json:"context"`
			ContextSource  string `json:"contextSource"`
			Warning        string `json:"warning"`
		}
		err := requestInto(comp, "core", "session", map[string]any{
			"sessionId": session, "provider": provider, "model": selected,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider/model selection failed")
		}
		return modelActionMsg{
			Session: session, Selected: selected, Previous: prevModel,
			SelectedProvider: provider, PreviousProvider: prevProvider,
			ProviderChanged: true,
			Runtime: runtimeResolution{
				OK: response.OK, Provider: response.Provider,
				ProviderSource: response.ProviderSource, Model: response.Model,
				Catalog: response.Catalog, Context: response.Context,
				ContextSource: response.ContextSource,
			},
			Warning: response.Warning,
			Err:     err,
		}
	}
}

// thinkingEffortMsg reports the result of persisting a per-conversation
// thinking-effort selection; like modelActionMsg, a completion for the old
// session is dropped.
type thinkingEffortMsg struct {
	Session string
	Effort  string
	Err     error
}

// setConversationThinkingCmd persists the conversation's thinking-effort
// selection through the session runner (model-only call: no inference).
func setConversationThinkingCmd(comp *sdk.Component, session, effort string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK             bool   `json:"ok"`
			ThinkingEffort string `json:"thinkingEffort"`
		}
		err := requestInto(comp, "core", "session", map[string]any{
			"sessionId": session, "thinking": effort,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("thinking effort selection failed")
		}
		return thinkingEffortMsg{Session: session, Effort: effort, Err: err}
	}
}

// setConversationApprovalsCmd persists this conversation's approval gate mode
// through the session runner (core's /approvals control: "auto" grants every
// x-harness.approval tool without asking any client, "ask" restores the gate,
// and an empty mode also clears to ask). Control call, no inference.
func setConversationApprovalsCmd(comp *sdk.Component, session, mode string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK        bool   `json:"ok"`
			Approvals string `json:"approvals"`
		}
		err := requestInto(comp, "core", "session", map[string]any{
			"sessionId": session, "approvals": mode,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("approval mode change failed")
		}
		if err != nil {
			return approvalsModeMsg{Session: session, Mode: mode, Err: err}
		}
		// Report what core stored, not what was asked for — "ask" persists as
		// "" (the same faithfulness the web UI's handler keeps).
		return approvalsModeMsg{Session: session, Mode: response.Approvals}
	}
}

func setConversationModelCmd(comp *sdk.Component, session, selected, previous string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK             bool   `json:"ok"`
			Provider       string `json:"provider"`
			ProviderSource string `json:"providerSource"`
			Model          string `json:"model"`
			Catalog        string `json:"catalog"`
			Context        int    `json:"context"`
			ContextSource  string `json:"contextSource"`
			Warning        string `json:"warning"`
		}
		err := requestInto(comp, "core", "session", map[string]any{
			"sessionId": session,
			"model":     selected,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("model selection failed")
		}
		return modelActionMsg{
			Session:  session,
			Selected: selected, Previous: previous,
			Runtime: runtimeResolution{
				OK: response.OK, Provider: response.Provider,
				ProviderSource: response.ProviderSource, Model: response.Model,
				Catalog: response.Catalog, Context: response.Context,
				ContextSource: response.ContextSource,
			},
			Warning: response.Warning,
			Err:     err,
		}
	}
}

// compactConversationCmd runs the manual compaction control (/compact): core
// executes the replaceable-compactor rung on demand, with no LLM turn and no
// user message. It is a control call like the model save above, so the reply
// is decoded here rather than through requestInto (longer timeout).
func compactConversationCmd(comp *sdk.Component, session string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK         bool   `json:"ok"`
			Compacted  bool   `json:"compacted"`
			Reason     string `json:"reason"`
			Before     int    `json:"beforeTokens"`
			After      int    `json:"afterTokens"`
			Generation int    `json:"generation"`
		}
		raw, err := comp.Request("core", "session", map[string]any{
			"sessionId": session, "compact": true,
		}, compactTimeout)
		if err == nil {
			if uerr := json.Unmarshal(raw, &response); uerr != nil {
				err = fmt.Errorf("decode core.session: %w", uerr)
			} else if !response.OK {
				err = fmt.Errorf("compaction failed")
			}
		}
		return compactResultMsg{
			Session: session, Compacted: response.Compacted,
			Reason: response.Reason, Before: response.Before,
			After: response.After, Generation: response.Generation, Err: err,
		}
	}
}

func setConversationProviderCmd(comp *sdk.Component, session, provider string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK bool `json:"ok"`
		}
		err := requestInto(comp, "core", "session", map[string]any{
			"sessionId": session, "provider": provider,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider selection failed")
		}
		return providerActionMsg{Action: "switch", Nickname: provider, Err: err}
	}
}

// setProviderStripCmd toggles the active provider's stripModelPrefix option
// (gateways that route on the canonical id, e.g. devpass).
func setProviderStripCmd(comp *sdk.Component, nickname string, strip bool) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "provider", "provider_update", map[string]any{
			"nickname": nickname, "stripPrefix": strip,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider update failed")
		}
		detail := "off"
		if strip {
			detail = "on"
		}
		return providerActionMsg{Action: "strip", Nickname: nickname, Detail: detail, Err: err}
	}
}

func useEnvironmentProviderCmd(comp *sdk.Component, session string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK bool `json:"ok"`
		}
		err := requestInto(comp, "core", "session", map[string]any{
			"sessionId": session, "provider": "",
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("environment provider selection failed")
		}
		return providerActionMsg{Action: "environment", Nickname: "default", Err: err}
	}
}

type providerFormValues struct {
	Nickname string
	APIKey   string
	BaseURL  string
	Catalog  string
	Model    string
	Context  int
}

func addProviderCmd(comp *sdk.Component, values providerFormValues) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "provider", "provider_add", map[string]any{
			"nickname": values.Nickname, "apiKey": values.APIKey,
			"baseUrl": values.BaseURL, "catalog": values.Catalog,
			"model": values.Model, "context": values.Context, "active": true,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider add failed")
		}
		return providerActionMsg{Action: "add", Nickname: values.Nickname, Err: err}
	}
}

// probeThenAddProviderCmd probes the endpoint's live model list with the
// not-yet-saved credentials and repairs the model id against it, then adds
// the provider. Probe failure (no /models route, bad gateway) falls through
// to the add unchanged.
func probeThenAddProviderCmd(comp *sdk.Component, values providerFormValues) tea.Cmd {
	return func() tea.Msg {
		var probe struct {
			OK     bool `json:"ok"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		err := requestInto(comp, "provider", "provider_models", map[string]any{
			"baseUrl": values.BaseURL, "apiKey": values.APIKey, "refresh": true,
		}, &probe)
		if err == nil && probe.OK && len(probe.Models) > 0 {
			ids := make([]string, 0, len(probe.Models))
			for _, model := range probe.Models {
				if model.ID != "" {
					ids = append(ids, model.ID)
				}
			}
			if match := matchServedModel(ids, values.Model); match != "" {
				values.Model = match
			} else if pick := pickDefaultServedModel(ids); pick != "" && values.Model == "" {
				values.Model = pick
			}
		}
		return addProviderCmd(comp, values)()
	}
}

// updateProviderCmd edits an existing provider's non-secret settings, preserving
// its stored API key unless a replacement is supplied. Editing never changes
// which provider is currently active.
func updateProviderCmd(comp *sdk.Component, values providerFormValues) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		args := map[string]any{
			"nickname": values.Nickname,
			"baseUrl":  values.BaseURL,
			"catalog":  values.Catalog,
			"model":    values.Model, "context": values.Context,
		}
		if values.APIKey != "" {
			args["apiKey"] = values.APIKey
		}
		err := requestInto(comp, "provider", "provider_update", args, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider update failed")
		}
		return providerActionMsg{Action: "update", Nickname: values.Nickname, Err: err}
	}
}

// removeProviderCmd deletes a configured provider from the store. If it was the
// active one, the backend falls back to another provider (or the environment).
func removeProviderCmd(comp *sdk.Component, nickname string) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "provider", "provider_remove", map[string]any{"nickname": nickname}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider remove failed")
		}
		return providerActionMsg{Action: "remove", Nickname: nickname, Err: err}
	}
}

// ---- MCP servers (components/mcp + one mcp-bridge per server) --------------

// mcpBridgeStatus is the live state a bridge reports through its hidden
// status tool (surfaced by mcp_servers as "bridge").
type mcpBridgeStatus struct {
	Connected bool    `json:"connected"`
	Tools     int     `json:"tools"`
	Drifted   bool    `json:"drifted"`
	LastError string  `json:"lastError"`
	StartedAt float64 `json:"startedAt"`
	LastUsed  float64 `json:"lastUsed"`
}

// mcpServerSummary mirrors one mcp_servers entry. Secrets are redacted
// server-side; only key names (envKeys/headerKeys) reach the UI.
type mcpServerSummary struct {
	Name            string           `json:"name"`
	Type            string           `json:"type"`
	Enabled         bool             `json:"enabled"`
	ToolCount       int              `json:"toolCount"`
	PromptCount     int              `json:"promptCount"`
	TimeoutMs       int              `json:"timeoutMs"`
	IdleMs          int              `json:"idleMs"`
	Effect          string           `json:"effect"`
	Concurrency     string           `json:"concurrency"`
	Approval        string           `json:"approval"`
	Expose          string           `json:"expose"`
	Command         string           `json:"command"`
	Args            []string         `json:"args"`
	URL             string           `json:"url"`
	EnvKeys         []string         `json:"envKeys"`
	HeaderKeys      []string         `json:"headerKeys"`
	Component       string           `json:"component"`
	Live            bool             `json:"live"`
	RegisteredTools []string         `json:"registeredTools"`
	Bridge          *mcpBridgeStatus `json:"bridge"`
	Error           string           `json:"error"`
}

type mcpServersResponse struct {
	Servers []mcpServerSummary `json:"servers"`
}

func loadMcpServers(comp *sdk.Component) (mcpServersResponse, error) {
	var response mcpServersResponse
	err := requestInto(comp, "mcp", "mcp_servers", map[string]any{}, &response)
	return response, err
}

// mcpServersMsg carries the configured MCP servers for the /mcp selector.
type mcpServersMsg struct {
	Servers []mcpServerSummary
	Err     error
}

func mcpServersCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		response, err := loadMcpServers(comp)
		return mcpServersMsg{Servers: response.Servers, Err: err}
	}
}

// lspServerSummary is one configured language server as the lsp
// component's lsp_servers tool reports it. Source is "user" for entries in
// the editable registry file, "builtin" for shipped defaults (overridable
// by adding the same name).
type lspServerSummary struct {
	Name       string            `json:"name"`
	Command    []string          `json:"command"`
	Extensions map[string]string `json:"extensions"`
	Source     string            `json:"source"`
}

type lspServersResponse struct {
	Servers []lspServerSummary `json:"servers"`
	Path    string             `json:"path"`
}

func loadLspServers(comp *sdk.Component) (lspServersResponse, error) {
	var response lspServersResponse
	err := requestInto(comp, "lsp", "lsp_servers", map[string]any{}, &response)
	return response, err
}

// lspServersMsg carries the configured language servers for the /lsp
// selector.
type lspServersMsg struct {
	Servers []lspServerSummary
	Err     error
}

func lspServersCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		response, err := loadLspServers(comp)
		return lspServersMsg{Servers: response.Servers, Err: err}
	}
}

// lspActionMsg reports a completed /lsp control action (add/remove); the
// transcript shows the label and the /lsp selector reloads.
type lspActionMsg struct {
	Action string
	Name   string
	Err    error
}

// lspFormValues is what the /lsp form submits. The command is split on
// whitespace (LSP server commands are simple argv); the language id for
// each extension is the extension minus its dot.
type lspFormValues struct {
	Name       string
	Command    string
	Extensions map[string]string
}

func lspSaveCmd(comp *sdk.Component, values lspFormValues) tea.Cmd {
	return func() tea.Msg {
		var cmdArgs []string
		for _, tok := range strings.Fields(values.Command) {
			cmdArgs = append(cmdArgs, tok)
		}
		_, err := comp.Request("lsp", "lsp_registry", map[string]any{
			"action":     "add",
			"name":       values.Name,
			"command":    cmdArgs,
			"extensions": values.Extensions,
		}, controlTimeout)
		if err != nil {
			err = fmt.Errorf("lsp.lsp_registry: %w", err)
		}
		return lspActionMsg{Action: "add", Name: values.Name, Err: err}
	}
}

func lspRemoveCmd(comp *sdk.Component, name string) tea.Cmd {
	return func() tea.Msg {
		_, err := comp.Request("lsp", "lsp_registry", map[string]any{
			"action": "remove", "name": name,
		}, controlTimeout)
		if err != nil {
			err = fmt.Errorf("lsp.lsp_registry: %w", err)
		}
		return lspActionMsg{Action: "remove", Name: name, Err: err}
	}
}

// toolProfileSummary is one stored tool profile as core's `profile` tool
// reports it for pickers: the selector list plus the resolved cost of the
// profile against the live catalog.
type toolProfileSummary struct {
	Name      string   `json:"name"`
	Tools     []string `json:"tools"`
	Note      string   `json:"note"`
	ToolCount int      `json:"toolCount"`
	EstTokens int      `json:"estTokens"`
	Missing   []string `json:"missing"`
}

type profilesResponse struct {
	Profiles []toolProfileSummary `json:"profiles"`
}

type profileDeletedMsg struct {
	name string
	err  error
}

// profilesMsg carries the stored tool profiles for the /profile picker.
type profilesMsg struct {
	Profiles []toolProfileSummary
	Err      error
}

func profilesCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		var response profilesResponse
		err := requestInto(comp, "core", "profile", map[string]any{"op": "list"}, &response)
		return profilesMsg{Profiles: response.Profiles, Err: err}
	}
}

func deleteProfileCmd(comp *sdk.Component, name string) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "core", "profile", map[string]any{
			"op": "delete", "name": name,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("profile delete failed")
		}
		return profileDeletedMsg{name: name, err: err}
	}
}

// mcpActionMsg reports a completed MCP control action (add/edit/remove/
// refresh/toggle); the transcript shows the label and the /mcp selector
// reloads.
type mcpActionMsg struct {
	Action  string
	Name    string
	Warning string // partial-failure notice from add/edit (store saved, bridge issue)
	Err     error
}

// mcpControlTimeout covers add/edit/refresh: validation connects to the
// server for real, and first runs of npx/uvx servers download packages.
const mcpControlTimeout = 120 * time.Second

// mcpFormValues is what the /mcp form submits. Empty strings mean "not
// provided"; on edit the manager merges provided fields and keeps the rest.
// Effect/concurrency/idleMs are always sent (prefilled on edit) so the form
// fully describes the server, matching the web UI's mcpForm.
type mcpFormValues struct {
	Name        string
	Type        string
	Command     string
	Args        []string
	URL         string
	EnvJSON     string // raw JSON object text; empty = keep on edit
	HeadersJSON string // raw JSON object text; empty = keep on edit
	Approval    string
	Expose      string
	Effect      string
	Concurrency string
	TimeoutMs   int
	IdleMs      int
}

func mcpAddCmd(comp *sdk.Component, values mcpFormValues) tea.Cmd {
	return func() tea.Msg {
		args := map[string]any{
			"name": values.Name, "type": values.Type,
			"approval": values.Approval, "expose": values.Expose,
			"effect": values.Effect, "concurrency": values.Concurrency,
			"timeoutMs": values.TimeoutMs, "idleMs": values.IdleMs,
		}
		if values.Type == "stdio" {
			args["command"] = values.Command
			args["args"] = values.Args
		} else {
			args["url"] = values.URL
		}
		if values.EnvJSON != "" {
			var env map[string]any
			if err := json.Unmarshal([]byte(values.EnvJSON), &env); err == nil {
				args["env"] = env
			}
		}
		if values.HeadersJSON != "" {
			var headers map[string]string
			if err := json.Unmarshal([]byte(values.HeadersJSON), &headers); err == nil {
				args["headers"] = headers
			}
		}
		var response okResponse
		err := requestInto(comp, "mcp", "mcp_add", args, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("mcp add failed")
		}
		return mcpActionMsg{Action: "add", Name: values.Name, Warning: response.Warning, Err: err}
	}
}

func mcpEditCmd(comp *sdk.Component, values mcpFormValues) tea.Cmd {
	return func() tea.Msg {
		args := map[string]any{
			"name": values.Name, "type": values.Type,
			"approval": values.Approval, "expose": values.Expose,
			"effect": values.Effect, "concurrency": values.Concurrency,
			"timeoutMs": values.TimeoutMs, "idleMs": values.IdleMs,
		}
		if values.Type == "stdio" {
			args["command"] = values.Command
			args["args"] = values.Args
		} else {
			args["url"] = values.URL
		}
		if values.EnvJSON != "" {
			var env map[string]any
			if err := json.Unmarshal([]byte(values.EnvJSON), &env); err == nil {
				args["env"] = env
			}
		}
		if values.HeadersJSON != "" {
			var headers map[string]string
			if err := json.Unmarshal([]byte(values.HeadersJSON), &headers); err == nil {
				args["headers"] = headers
			}
		}
		var response okResponse
		err := requestInto(comp, "mcp", "mcp_edit", args, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("mcp edit failed")
		}
		return mcpActionMsg{Action: "edit", Name: values.Name, Warning: response.Warning, Err: err}
	}
}

func mcpRemoveCmd(comp *sdk.Component, name string) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "mcp", "mcp_remove", map[string]any{"name": name}, mcpControlTimeout)
		if err == nil && !response.OK {
			err = fmt.Errorf("mcp remove failed")
		}
		return mcpActionMsg{Action: "remove", Name: name, Err: err}
	}
}

func mcpRefreshCmd(comp *sdk.Component, name string) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "mcp", "mcp_refresh", map[string]any{"name": name}, mcpControlTimeout)
		if err == nil && !response.OK {
			err = fmt.Errorf("mcp refresh failed")
		}
		return mcpActionMsg{Action: "refresh", Name: name, Err: err}
	}
}

// mcpToggleCmd enables or disables a server (mcp_edit {enabled}); disabled
// servers keep their config but stop (or never start) their bridge.
func mcpToggleCmd(comp *sdk.Component, name string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "mcp", "mcp_edit", map[string]any{
			"name": name, "enabled": enabled,
		}, mcpControlTimeout)
		if err == nil && !response.OK {
			err = fmt.Errorf("mcp edit failed")
		}
		action := "off"
		if enabled {
			action = "on"
		}
		return mcpActionMsg{Action: action, Name: name, Err: err}
	}
}

// ---- registry browse (/mcp search) -----------------------------------------

// mcpRegistryEntry mirrors one mcp_search result: the official registry
// narrowed to what mcp_add can consume. Browsing is read-only — installing
// still goes through the add form and its validation + approval gate.
type mcpRegistryEntry struct {
	Name           string   `json:"name"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Version        string   `json:"version"`
	Transport      string   `json:"transport"`
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	URL            string   `json:"url"`
	Requirements   []string `json:"requirements"`
	Installable    bool     `json:"installable"`
	NotInstallable string   `json:"notInstallableReason"`
}

type mcpSearchMsg struct {
	Query   string
	Entries []mcpRegistryEntry
	Err     error
}

func mcpSearchCmd(comp *sdk.Component, query string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			Entries []mcpRegistryEntry `json:"entries"`
		}
		err := requestInto(comp, "mcp", "mcp_search", map[string]any{"query": query}, &response)
		return mcpSearchMsg{Query: query, Entries: response.Entries, Err: err}
	}
}

// suggestedServerName derives an mcp_add name from a registry entry id
// ("io.github.user/server-name" → "server-name"), sanitized to the
// manager's alphabet (lowercase, digits, hyphen; ≤32 chars). Empty when
// nothing usable survives — the user then types a name themselves.
func suggestedServerName(entry mcpRegistryEntry) string {
	candidate := entry.Name
	if i := strings.LastIndexByte(candidate, '/'); i >= 0 {
		candidate = candidate[i+1:]
	}
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	var b strings.Builder
	for _, r := range candidate {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ' ':
			// separators collapse to a single hyphen
			if !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 32 {
		out = strings.Trim(out[:32], "-")
	}
	// must start with a letter per the manager's name rule
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		return ""
	}
	return out
}

// mcpEditReadyMsg carries the stored server snapshot for /mcp edit <name>;
// the handler opens the pre-filled form.
type mcpEditReadyMsg struct {
	Server *mcpServerSummary
	Err    error
}

func loadMcpEditCmd(comp *sdk.Component, name string) tea.Cmd {
	return func() tea.Msg {
		response, err := loadMcpServers(comp)
		if err != nil {
			return mcpEditReadyMsg{Err: err}
		}
		for i := range response.Servers {
			if response.Servers[i].Name == name {
				return mcpEditReadyMsg{Server: &response.Servers[i]}
			}
		}
		return mcpEditReadyMsg{Err: fmt.Errorf("mcp server %q is not configured", name)}
	}
}
