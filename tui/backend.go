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
// a success flag.
type okResponse struct {
	OK bool `json:"ok"`
}

type providerSummary struct {
	Nickname    string `json:"nickname"`
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
}

type modelsResponse struct {
	Models []modelSummary `json:"models"`
}

type conversationState struct {
	ModelOverride string
	Provider      string
	Model         string
	Context       int
	ContextUsed   int
	PromptTokens  int
}

type bootstrapMsg struct {
	Providers        providerListResponse
	ProviderStatus   providerStatusResponse
	CatalogProviders []catalogProvider
	Conversation     conversationState
	Runtime          runtimeResolution
	Warnings         []string
}

type runtimeRefreshedMsg struct {
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
			ModelOverride string `json:"modelOverride"`
			Provider      string `json:"provider"`
			Model         string `json:"model"`
			Context       int    `json:"context"`
			ContextUsed   int    `json:"contextUsed"`
			PromptTokens  int    `json:"promptTokens"`
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
		ModelOverride: response.Value.ModelOverride,
		Provider:      response.Value.Provider, Model: response.Value.Model,
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
// The three independent lookups run concurrently; the conversation state and
// the runtime resolution are sequential because the resolution takes the
// persisted conversation model override as its argument.
func bootstrapBackendCmd(comp *sdk.Component, session string) tea.Cmd {
	return func() tea.Msg {
		var msg bootstrapMsg
		var (
			providers  providerListResponse
			status     providerStatusResponse
			catalog    []catalogProvider
			listErr    error
			statusErr  error
			catalogErr error
		)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); providers, listErr = loadProviderList(comp) }()
		go func() { defer wg.Done(); status, statusErr = loadProviderStatus(comp) }()
		go func() { defer wg.Done(); catalog, catalogErr = loadCatalogProviders(comp) }()
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
		return msg
	}
}

func refreshRuntimeCmd(comp *sdk.Component, modelOverride string) tea.Cmd {
	return func() tea.Msg {
		providers, listErr := loadProviderList(comp)
		status, statusErr := loadProviderStatus(comp)
		resolved, resolveErr := resolveRuntime(comp, modelOverride)
		return runtimeRefreshedMsg{
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
