package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	sdk "niffler.dev/sdk"
)

// OAuth login controller: drives the provider component's
// provider_oauth_start/complete/cancel tools from the TUI. ChatGPT
// (openai-codex) and Claude (anthropic) subscription logins follow the same
// PKCE flows the web UI offers: the TUI starts a flow, shows the
// authorization URL / device code, polls until the backend reports the
// exchanged credential, and offers a manual code/redirect-URL fallback for
// machines where the fixed localhost callback port cannot receive the
// browser redirect.

const (
	oauthProtocolCodex     = "openai-codex"
	oauthProtocolAnthropic = "anthropic"
	oauthMethodBrowser     = "browser"
	oauthMethodDevice      = "device"
)

// oauthLoginState is the live login view state. A nil m.oauthLogin means no
// flow is active. seq identifies the active poll chain: manual submits bump
// it so in-flight poll results from an older chain are dropped instead of
// forking a second parallel chain.
type oauthLoginState struct {
	loc               Locale
	flowID            string
	seq               int
	provider          string // display name from the backend, e.g. "OpenAI Codex (ChatGPT Plus/Pro)"
	protocol          string
	method            string
	url               string
	userCode          string
	callbackAvailable bool
	retryAfterMs      int
	manualPending     string
	input             textinput.Model
	status            string
	err               string
}

func newOAuthLoginState(loc Locale) oauthLoginState {
	input := textinput.New()
	input.CharLimit = 4096
	input.Prompt = t(loc, "oauth.manualPrompt") + " "
	input.Placeholder = t(loc, "oauth.manualPlaceholder")
	input.Focus() // the manual code field owns Enter on the login panel
	return oauthLoginState{loc: loc, retryAfterMs: 1000, input: input}
}

// oauthStartMsg reports the provider_oauth_start outcome.
type oauthStartMsg struct {
	state oauthLoginState
	err   error
}

// oauthPollMsg reports one provider_oauth_complete iteration. done marks a
// finished flow (credential stored or terminal error); provider is the
// redacted stored provider on success.
type oauthPollMsg struct {
	state    oauthLoginState
	done     bool
	provider providerSummary
	err      error
}

// startOAuthCmd begins a login flow. It also opens the authorization URL in
// the system browser best-effort (matching the desktop UI; silently ignored
// on headless hosts).
func startOAuthCmd(comp *sdk.Component, loc Locale, protocol, method string) tea.Cmd {
	return func() tea.Msg {
		var response struct {
			OK                bool   `json:"ok"`
			FlowID            string `json:"flowId"`
			Provider          string `json:"provider"`
			Method            string `json:"method"`
			URL               string `json:"url"`
			UserCode          string `json:"userCode"`
			CallbackAvailable bool   `json:"callbackAvailable"`
		}
		err := requestInto(comp, "provider", "provider_oauth_start", map[string]any{
			"protocol": protocol, "method": method,
		}, &response)
		if err == nil && !response.OK {
			err = fmt.Errorf("OAuth start failed")
		}
		if err != nil {
			return oauthStartMsg{err: fmt.Errorf("oauth start: %w", err)}
		}
		state := newOAuthLoginState(loc)
		state.flowID = response.FlowID
		state.seq = 1
		state.provider = response.Provider
		state.protocol = protocol
		state.method = response.Method
		if state.method == "" {
			state.method = method
		}
		state.url = response.URL
		state.userCode = response.UserCode
		state.callbackAvailable = response.CallbackAvailable
		state.status = t(loc, "oauth.waiting")
		_ = openBrowserBestEffort(response.URL)
		return oauthStartMsg{state: state}
	}
}

// pollOAuthCmd performs one provider_oauth_complete round after sleeping the
// backend-suggested interval. A pending manual code is attached exactly once.
func pollOAuthCmd(comp *sdk.Component, state oauthLoginState) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(time.Duration(max(250, state.retryAfterMs)) * time.Millisecond)
		args := map[string]any{"flowId": state.flowID}
		if state.manualPending != "" {
			args["code"] = state.manualPending
		}
		var response struct {
			OK           bool            `json:"ok"`
			Pending      bool            `json:"pending"`
			RetryAfterMs int             `json:"retryAfterMs"`
			Provider     providerSummary `json:"provider"`
		}
		err := requestInto(comp, "provider", "provider_oauth_complete", args, &response)
		if err != nil {
			return oauthPollMsg{state: state, done: true, err: err}
		}
		next := state
		next.manualPending = ""
		if response.Pending {
			if response.RetryAfterMs > 0 {
				next.retryAfterMs = response.RetryAfterMs
			} else {
				next.retryAfterMs = 1000
			}
			return oauthPollMsg{state: next}
		}
		if !response.OK {
			return oauthPollMsg{state: next, done: true, err: fmt.Errorf("OAuth completion failed")}
		}
		return oauthPollMsg{state: next, done: true, provider: response.Provider}
	}
}

// cancelOAuthCmd tears down a pending flow (closes the backend's callback
// listener). Fire-and-forget outcome.
func cancelOAuthCmd(comp *sdk.Component, flowID string) tea.Cmd {
	return func() tea.Msg {
		var response okResponse
		_ = requestInto(comp, "provider", "provider_oauth_cancel", map[string]any{"flowId": flowID}, &response)
		return nil
	}
}

// openBrowserBestEffort launches the system browser; errors are ignored —
// the URL is always rendered in the login panel for manual copying.
func openBrowserBestEffort(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	return cmd.Start()
}

func (s *oauthLoginState) setWidth(width int) {
	s.input.SetWidth(providerInputWidth(width))
}

func (s *oauthLoginState) submitManual() bool {
	value := strings.TrimSpace(s.input.Value())
	if value == "" {
		return false
	}
	s.manualPending = value
	s.input.SetValue("")
	s.retryAfterMs = 0 // poll immediately
	s.err = ""
	s.status = t(s.loc, "oauth.completing")
	return true
}

func (s oauthLoginState) view(width int) string {
	var out strings.Builder
	out.WriteString(headerStyle.Render(t(s.loc, "oauth.title", s.provider)))
	out.WriteString("\n\n")
	out.WriteString(metaStyle.Render(t(s.loc, "oauth.meta")))
	out.WriteString("\n\n")
	out.WriteString(metaStyle.Render(t(s.loc, "oauth.openHint")))
	out.WriteByte('\n')
	out.WriteString(linkStyle.Render(s.url))
	out.WriteByte('\n')
	if s.userCode != "" {
		out.WriteByte('\n')
		out.WriteString(metaStyle.Render(t(s.loc, "oauth.deviceCode")))
		out.WriteByte('\n')
		out.WriteString(codeStyle.Render(s.userCode))
		out.WriteByte('\n')
	}
	if !s.callbackAvailable && s.method == oauthMethodBrowser {
		out.WriteByte('\n')
		out.WriteString(errorStyle.Render(t(s.loc, "oauth.callbackUnavailable")))
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	out.WriteString(s.input.View())
	out.WriteByte('\n')
	out.WriteString(metaStyle.Render(t(s.loc, "oauth.manualHint")))
	out.WriteByte('\n')
	if s.err != "" {
		out.WriteByte('\n')
		out.WriteString(errorStyle.Render(s.err))
		out.WriteByte('\n')
	} else if s.status != "" {
		out.WriteByte('\n')
		out.WriteString(metaStyle.Render(s.status))
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	out.WriteString(metaStyle.Render(t(s.loc, "oauth.keys")))
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1, 2).
		Width(max(24, min(width-6, 88))).
		Render(out.String())
	return panel
}
