package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

const controlTimeout = 10 * time.Second

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
	ModelOverride  string
	ThinkingEffort string
	Provider       string
	Model          string
	Context        int
	ContextUsed    int
	PromptTokens   int
}

type bootstrapMsg struct {
	// Session is the conversation this snapshot was loaded for; a snapshot
	// arriving after a further session switch is dropped.
	Session          string
	Providers        providerListResponse
	ProviderStatus   providerStatusResponse
	CatalogProviders []catalogProvider
	Identity         harnessIdentity
	Conversation     conversationState
	Runtime          runtimeResolution
	Warnings         []string
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
	Runtime  runtimeResolution
	Warning  string
	Err      error
}

// providerBusEventMsg signals a change on ev.provider.>; the client refreshes
// provider and runtime state in response.
type providerBusEventMsg struct{}

type modelsCatalogUpdatedMsg struct{}

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

func resolveRuntime(comp *sdk.Component, modelOverride string) (runtimeResolution, error) {
	args := map[string]any{}
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

func loadConversationState(comp *sdk.Component, session string) (conversationState, error) {
	var response struct {
		OK    bool `json:"ok"`
		Value struct {
			ModelOverride  string `json:"modelOverride"`
			ThinkingEffort string `json:"thinkingEffort"`
			Provider       string `json:"provider"`
			Model          string `json:"model"`
			Context        int    `json:"context"`
			ContextUsed    int    `json:"contextUsed"`
			PromptTokens   int    `json:"promptTokens"`
		} `json:"value"`
		Code string `json:"code"`
	}
	if err := requestInto(comp, "store", "get", map[string]any{
		"kind": "conversation", "id": session,
	}, &response); err != nil {
		return conversationState{}, err
	}
	if !response.OK {
		return conversationState{}, nil
	}
	return conversationState{
		ModelOverride:  response.Value.ModelOverride,
		ThinkingEffort: response.Value.ThinkingEffort,
		Provider:       response.Value.Provider, Model: response.Value.Model,
		Context: response.Value.Context, ContextUsed: response.Value.ContextUsed,
		PromptTokens: response.Value.PromptTokens,
	}, nil
}

// sessionSummary is one conversation listed from the store for /session.
type sessionSummary struct {
	ID        string
	Title     string
	CreatedAt float64
}

// sessionListMsg carries the store's conversations for the /session selector.
type sessionListMsg struct {
	Sessions []sessionSummary
	Err      error
}

// loadSessionList lists every conversation (session) in the store, newest
// first. Columns include the persisted model override so the selector can
// show per-session configuration.
type sessionRow struct {
	ID    string `json:"id"`
	Value struct {
		Title     string  `json:"title"`
		CreatedAt float64 `json:"createdAt"`
	} `json:"value"`
}

func loadSessionList(comp *sdk.Component) ([]sessionSummary, error) {
	var response struct {
		Items []sessionRow `json:"items"`
	}
	if err := requestInto(comp, "store", "list", map[string]any{
		"kind": "conversation",
	}, &response); err != nil {
		return nil, err
	}
	sessions := make([]sessionSummary, 0, len(response.Items))
	for _, row := range response.Items {
		sessions = append(sessions, sessionSummary{
			ID: row.ID, Title: row.Value.Title, CreatedAt: row.Value.CreatedAt,
		})
	}
	// newest first
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt > sessions[j].CreatedAt
	})
	return sessions, nil
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
			listErr    error
			statusErr  error
			catalogErr error
			slashErr   error
			idErr      error
		)
		var wg sync.WaitGroup
		wg.Add(5)
		go func() { defer wg.Done(); providers, listErr = loadProviderList(comp) }()
		go func() { defer wg.Done(); status, statusErr = loadProviderStatus(comp) }()
		go func() { defer wg.Done(); catalog, catalogErr = loadCatalogProviders(comp) }()
		go func() { defer wg.Done(); slashCmds, slashErr = loadSlashTable(comp) }()
		go func() { defer wg.Done(); identity, idErr = loadHarnessIdentity(comp) }()
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
		msg.SlashCommands = slashCmds
		msg.SlashErr = slashErr
		conversation, err := loadConversationState(comp, session)
		if err != nil {
			msg.Warnings = append(msg.Warnings, err.Error())
		} else {
			msg.Conversation = conversation
		}
		resolved, err := resolveRuntime(comp, conversation.ModelOverride)
		if err != nil {
			msg.Warnings = append(msg.Warnings, err.Error())
		} else {
			msg.Runtime = resolved
		}
		msg.Session = session
		return msg
	}
}

func refreshRuntimeCmd(comp *sdk.Component, session, modelOverride string) tea.Cmd {
	return func() tea.Msg {
		providers, listErr := loadProviderList(comp)
		status, statusErr := loadProviderStatus(comp)
		resolved, resolveErr := resolveRuntime(comp, modelOverride)
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

func switchProviderCmd(comp *sdk.Component, nickname string) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		err := requestInto(comp, "provider", "provider_switch", map[string]any{"nickname": nickname}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("provider switch failed")
		}
		return providerActionMsg{Action: "switch", Nickname: nickname, Err: err}
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

func useEnvironmentProviderCmd(comp *sdk.Component) tea.Cmd {
	return func() tea.Msg {
		var response providerStatusResponse
		err := requestInto(comp, "provider", "provider_use_environment", map[string]any{}, &response)
		// ok=false is valid when the environment has no credential; clearing
		// the stored marker still succeeded.
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
