// niffler-tui is an interactive terminal chat client for Niffler sessions.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
	sdk "niffler.dev/sdk"
)

const (
	componentName    = "tui"
	componentVersion = "0.1.0"
	turnTimeout      = 31 * time.Minute
	reconnectDelay   = 2 * time.Second
	maxToolText      = 4000
	maxInputHeight   = 8
	maxHistory       = 200
	defaultNATSURL   = "nats://127.0.0.1:4222"

	// markdownSettleDelay is how long assistant output must be quiet before
	// it is re-rendered as markdown. Glamour can take hundreds of
	// milliseconds on large blocks, so re-rendering per token would stall
	// the UI; instead blocks stream as plain text and upgrade to styled
	// markdown once the model pauses (or the round ends).
	markdownSettleDelay = 300 * time.Millisecond
)

var invalidSessionID = regexp.MustCompile(`[^A-Za-z0-9_-]`)

type usageStats struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type sessionEvent struct {
	SessionID      string          `json:"sessionId"`
	Content        string          `json:"content"`
	Reasoning      string          `json:"reasoning"`
	Tool           string          `json:"tool"`
	Args           json.RawMessage `json:"args"`
	Result         json.RawMessage `json:"result"`
	Error          string          `json:"error"`
	Reply          string          `json:"reply"`
	Provider       string          `json:"provider"`
	ProviderSource string          `json:"providerSource"`
	Model          string          `json:"model"`
	Catalog        string          `json:"catalog"`
	Context        int             `json:"context"`
	ContextSource  string          `json:"contextSource"`
	PromptTokens   int             `json:"promptTokens"`
	UsedTokens     int             `json:"usedTokens"`
	Warning        json.RawMessage `json:"warning"`
	Trimmed        int             `json:"trimmed"`
	Usage          usageStats      `json:"usage"`
}

type connectedMsg struct{}

type connectStoppedMsg struct{}

type sessionEventMsg struct {
	kind  string
	event sessionEvent
}

type turnDoneMsg struct {
	reply string
	err   error
}

// renderSettleMsg fires after markdownSettleDelay of quiet streaming output;
// on arrival the dirty assistant block is rendered as markdown.
type renderSettleMsg struct{}

type model struct {
	ctx      context.Context
	comp     *sdk.Component
	session  string
	natsURL  string
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model
	blocks   []transcriptBlock

	width        int
	height       int
	connected    bool
	busy         bool
	hadAssistant bool
	assistantIdx int
	thinkingIdx  int
	focusCmd     tea.Cmd

	// Sent-message history. histIdx is -1 while editing a fresh draft;
	// otherwise it indexes history (the entry currently in the input).
	// historyFile is the per-session persistent store (best effort; empty
	// when unavailable).
	history     []string
	histIdx     int
	draft       string
	historyFile string

	// Reverse history search (ctrl+r). While searchActive, keyboard input is
	// consumed by the search instead of the textarea.
	searchActive bool
	searchQuery  string
	searchIdx    int

	// Provider/model control plane. Provider selection is the registry's
	// global default; modelOverride is conversation-scoped and sent with
	// every turn. Runtime/context values are authoritative llm/session data.
	mode                  uiMode
	selector              selectorState
	providerForm          providerForm
	providerConfirmDelete string // nickname armed for two-stage delete in /provider
	providerDeleteErr     string
	providers             []providerSummary
	providerStatus        providerStatusResponse
	catalogProviders      []catalogProvider
	models                []modelSummary
	modelsCatalog         string
	runtime               runtimeResolution
	modelOverride         string
	promptTokens          int
	contextUsed           int
	contextNote           string
	controlPending        bool

	// Sessions (conversation switcher). sessionsLoading shows a spinner in
	// the /session selector while the store list is fetched.
	sessionsLoading bool
	// transcript is the cached joined rendering of all blocks (see
	// renderTranscript); viewportContent is the last string pushed into the
	// viewport, so syncViewport can skip SetContent when nothing changed.
	transcript      string
	transcriptDirty bool
	viewportContent string

	// Markdown renderer for assistant output. Rebuilt when the viewport
	// width changes; block render caches are invalidated on rebuild.
	renderer *glamour.TermRenderer
	renderW  int

	// Human approval gate: queued x-harness.approval requests (front of the
	// queue is the modal on screen) and per-session auto-approve memory.
	approvals    []approvalRequest
	autoApproved map[string][]string // sessionId -> tool names

	// mouse toggles terminal mouse tracking (default on). When on, the wheel
	// scrolls the transcript and clicking a tool-run card expands it; copy
	// then uses Shift+drag (standard for SGR mouse). /mouse off disables
	// tracking entirely: native plain-drag copy works, but the wheel is
	// handled by the terminal itself (the app sees no wheel events), so the
	// transcript scrolls with PgUp/PgDn/Ctrl+Up instead. See /mouse.
	mouse bool

	// streaming marks output that should remain plain until it settles.
	// renderTimerActive ensures only one settle timer spans token bursts,
	// including across tool rounds. lastTokenAt drives the quiet check.
	streaming         bool
	renderTimerActive bool
	lastTokenAt       time.Time

	// roundClosed marks the current LLM round as finalized: the assistant
	// event carried the complete content, or the turn ended (done). Late
	// token frames can arrive after that (NATS ordering across subjects
	// isn't guaranteed); they are ignored so they don't reopen the round or
	// spawn a duplicate block. Reset when a new round or turn starts.
	roundClosed bool

	// Two-stage stop (Opencode/Pi-style): while busy, the first ESC arms a
	// "Stop?" prompt in the status bar and the second ESC force-cancels the
	// running LLM stream via llm.cancel.<sessionId>, ending the turn.
	stopArmed bool
	stopping  bool
}

var (
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	thinkingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	metaStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

// configureKeymaps sets up chat-style editing bindings. The textarea owns
// all printable input and cursor movement (Enter sends, Alt+Enter/Ctrl+J
// inserts a newline, PageUp/PageDown are released for transcript
// scrolling); the viewport only scrolls with keys the textarea does not
// claim (pgup/pgdn, ctrl+up/ctrl+down), see isViewportKey.
func configureKeymaps(input *textarea.Model, view *viewport.Model) {
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "insert newline"),
	)
	input.KeyMap.PageUp = key.NewBinding()
	input.KeyMap.PageDown = key.NewBinding()

	view.KeyMap.Up = key.NewBinding(key.WithKeys("ctrl+up"))
	view.KeyMap.Down = key.NewBinding(key.WithKeys("ctrl+down"))
	view.KeyMap.PageUp = key.NewBinding(key.WithKeys("pgup"))
	view.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown"))
	view.KeyMap.HalfPageUp = key.NewBinding()
	view.KeyMap.HalfPageDown = key.NewBinding()
	view.KeyMap.Left = key.NewBinding()
	view.KeyMap.Right = key.NewBinding()
}

func newModel(ctx context.Context, comp *sdk.Component, session, natsURL string) model {
	input := textarea.New()
	input.Prompt = "> "
	input.Placeholder = "message (alt+enter: newline)"
	input.CharLimit = 0
	input.MaxHeight = maxInputHeight
	input.DynamicHeight = true
	input.MinHeight = 1
	input.ShowLineNumbers = false
	focusCmd := input.Focus()

	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = true
	view.FillHeight = true
	configureKeymaps(&input, &view)

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = metaStyle

	historyFile := historyFilePath(session)
	history := loadHistory(historyFile)

	m := model{
		ctx:          ctx,
		comp:         comp,
		session:      session,
		natsURL:      natsURL,
		viewport:     view,
		input:        input,
		spinner:      spin,
		assistantIdx: -1,
		thinkingIdx:  -1,
		histIdx:      -1,
		searchIdx:    -1,
		focusCmd:     focusCmd,
		history:      history,
		historyFile:  historyFile,
		// Mouse tracking on by default: the wheel scrolls the transcript and
		// click expands tool cards; copy uses Shift+drag. See /mouse off for
		// a pure-native mode (plain-drag copy, no app wheel).
		mouse: true,
	}
	m.layout()
	m.syncViewport(true)
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.focusCmd, m.connectCmd(), m.spinner.Tick)
}

func (m model) connectCmd() tea.Cmd {
	return func() tea.Msg {
		for {
			if err := m.comp.Connect(); err == nil {
				return connectedMsg{}
			}
			m.comp.Close()
			select {
			case <-m.ctx.Done():
				return connectStoppedMsg{}
			case <-time.After(reconnectDelay):
			}
		}
	}
}

func (m model) sendTurn(content string) tea.Cmd {
	return func() tea.Msg {
		args := map[string]any{
			"sessionId": m.session,
			"content":   content,
			// Always include the key: empty explicitly clears a previously
			// persisted conversation override after a provider/default change.
			"model": m.modelOverride,
		}
		result, err := m.comp.Request("core", "session", args, turnTimeout)
		if err != nil {
			return turnDoneMsg{err: fmt.Errorf("session turn: %w", err)}
		}
		var response struct {
			Reply string `json:"reply"`
		}
		if err := json.Unmarshal(result, &response); err != nil {
			return turnDoneMsg{err: fmt.Errorf("decode session result: %w", err)}
		}
		return turnDoneMsg{reply: response.Reply}
	}
}

// cancelTurnCmd force-cancels the running turn by publishing
// llm.cancel.<sessionId>, which aborts the in-flight streaming chat call
// in the llm component. The session runner sees the cancelled chat error,
// ends the turn with "done", and the pending core/session request returns,
// so finishTurn runs and busy clears. Fire-and-forget (errors surface as
// nothing here; the request will time out on its own if the cancel fails).
func (m model) cancelTurnCmd() tea.Cmd {
	m.stopping = true
	m.stopArmed = false
	return func() tea.Msg {
		subject := "llm.cancel." + sanitizeSessionID(m.session)
		_ = m.comp.Emit(subject, map[string]any{"sessionId": m.session})
		return nil
	}
}

// sendSteer publishes a mid-turn steering message to the session runner's
// svc.session.<id>.steer channel (fire-and-forget). It does NOT change busy
// state: the running turn will fold the message in and may keep working.
// The subject mirrors Niffler's core/conversation.nim steerSubject().
func (m model) sendSteer(content string) tea.Cmd {
	return func() tea.Msg {
		subject := "svc.session." + sanitizeSessionID(m.session) + ".steer"
		payload := map[string]any{"sessionId": m.session, "content": content}
		if err := m.comp.Emit(subject, payload); err != nil {
			return steerDoneMsg{err: fmt.Errorf("steer publish: %w", err)}
		}
		return steerDoneMsg{}
	}
}

// steerDoneMsg reports a fire-and-forget steer publish outcome (errors only).
type steerDoneMsg struct{ err error }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// A pending approval is a modal gate: Enter/Esc/"a" answer it no matter
	// which mode or control would otherwise consume them.
	if kp, ok := msg.(tea.KeyPressMsg); ok && m.approvalKey(kp) {
		return m, nil
	}

	switch msg := msg.(type) {
	case approvalEventMsg:
		m.applyApprovalEvent(msg)
		return m, nil

	case approvalResolvedMsg:
		m.applyApprovalResolved(msg.id)
		return m, nil

	case connectedMsg:
		m.connected = true
		m.addBlock(blockMeta, "connected to "+m.natsURL+"; session "+m.session)
		m.syncViewport(true)
		cmds = append(cmds, bootstrapBackendCmd(m.comp, m.session))

	case connectStoppedMsg:
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.syncViewport(false)

	case tea.KeyPressMsg:
		if m.mode != modeChat {
			return m.handleControlKey(msg)
		}
		if m.searchActive {
			return m.handleSearchKey(msg)
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			content := strings.TrimSpace(m.input.Value())
			if content == "" {
				return m, nil
			}
			if strings.HasPrefix(content, "/") {
				m.input.SetValue("")
				return m.executeLocalCommand(content)
			}
			if !m.connected {
				return m, nil
			}
			if m.busy {
				// Steer the running turn (Pi-style): the runner folds this into the
				// live conversation and may keep working; we render it locally so it
				// is visible in the flow even though busy stays true until "done".
				m.input.SetValue("")
				m.histIdx = -1
				m.addBlock(blockUser, "Steer: "+content)
				m.layout()
				m.syncViewport(true)
				return m, m.sendSteer(content)
			}
			if m.controlPending {
				return m, nil
			}
			if m.addHistory(content) {
				appendHistoryEntry(m.historyFile, content)
			}
			m.histIdx = -1
			m.draft = ""
			m.input.SetValue("")
			m.busy = true
			m.hadAssistant = false
			m.assistantIdx = -1
			m.thinkingIdx = -1
			m.setStreaming(false)
			m.roundClosed = false
			m.addBlock(blockUser, content)
			m.layout()
			m.syncViewport(true)
			return m, tea.Batch(m.sendTurn(content), m.spinner.Tick)
		case "up":
			// History previous only at the visual top of the textarea;
			// otherwise Up moves within logical or soft-wrapped lines.
			if m.inputAtTop() && len(m.history) > 0 {
				m.histPrev()
				return m, nil
			}
		case "down":
			if m.inputAtBottom() && len(m.history) > 0 {
				m.histNext()
				return m, nil
			}
		case "ctrl+r":
			if len(m.history) > 0 {
				m.searchStart()
				m.layout()
				return m, nil
			}
		case "ctrl+t":
			m.toggleAllToolRuns()
			m.syncViewport(false)
			return m, nil
		case "esc":
			// Two-stage stop: first ESC arms the Stop? prompt, second ESC
			// force-cancels the running turn. Outside a busy turn ESC is left
			// alone (falls through to the textarea below the switch).
			if !m.busy {
				m.stopArmed = false
				break
			}
			if !m.stopArmed {
				m.stopArmed = true
				return m, nil
			}
			return m, m.cancelTurnCmd()
		}

	case tea.MouseClickMsg:
		// A left click toggles the tool-run card under the cursor (only
		// meaningful with mouse tracking on, see /mouse).
		m.handleMouseClick(msg)
		return m, nil

	case tea.PasteMsg:
		// Bracketed-paste arrives as PasteMsg, not KeyPressMsg, so without
		// interception it bypasses the control-mode guard and lands in the
		// chat textarea. Route it to the active control instead.
		if m.mode == modeConnectForm {
			var cmd tea.Cmd
			m.providerForm, cmd = m.providerForm.update(msg)
			return m, cmd
		}
		if m.mode != modeChat {
			return m, nil
		}
		// chat mode: fall through to normal input handling below

	case bootstrapMsg:
		m.providers = msg.Providers.Providers
		m.providerStatus = msg.ProviderStatus
		m.catalogProviders = msg.CatalogProviders
		m.modelOverride = msg.Conversation.ModelOverride
		m.runtime = msg.Runtime
		// Conversation/provider metadata keeps the header useful during a
		// partial or rolling backend upgrade where llm_resolve is unavailable.
		if m.runtime.Provider == "" {
			m.runtime.Provider = msg.ProviderStatus.Provider.Nickname
			if m.runtime.Provider == "" {
				m.runtime.Provider = msg.Conversation.Provider
			}
		}
		if m.runtime.ProviderSource == "" {
			m.runtime.ProviderSource = msg.ProviderStatus.Source
		}
		if m.runtime.Model == "" {
			m.runtime.Model = msg.Conversation.Model
		}
		if msg.Conversation.Context > 0 && m.runtime.Context == 0 {
			m.runtime.Context = msg.Conversation.Context
		}
		if m.runtime.Provider != "" || m.runtime.Model != "" {
			m.runtime.OK = true
		}
		m.contextUsed = msg.Conversation.ContextUsed
		m.promptTokens = msg.Conversation.PromptTokens
		if m.runtime.Catalog != "" {
			cmds = append(cmds, loadModelsCmd(m.comp, m.runtime.Catalog))
		}
		if len(msg.Warnings) > 0 && !m.runtime.OK {
			m.contextNote = "provider/model controls unavailable: " + msg.Warnings[0]
		}

	case runtimeRefreshedMsg:
		if msg.ListErr == nil {
			m.providers = msg.Providers.Providers
			if m.mode == modeProviders {
				m.openProviderSelector()
			}
		}
		if msg.StatusErr == nil {
			m.providerStatus = msg.Status
		}
		if msg.ResolveErr == nil {
			m.runtime = msg.Runtime
			if m.runtime.Catalog != "" && m.runtime.Catalog != m.modelsCatalog {
				cmds = append(cmds, loadModelsCmd(m.comp, m.runtime.Catalog))
			}
		}
		m.contextNote = ""
		for _, refreshErr := range []error{msg.ResolveErr, msg.StatusErr, msg.ListErr} {
			if refreshErr != nil {
				m.contextNote = refreshErr.Error()
				break
			}
		}

	case catalogProvidersMsg:
		if msg.Err == nil {
			m.catalogProviders = msg.Providers
			if m.mode == modeCatalogProviders {
				m.openCatalogProviderSelector()
			}
		}

	case modelsLoadedMsg:
		if m.mode == modeModels {
			m.selector.list.StopSpinner()
		}
		if msg.Err != nil {
			m.contextNote = msg.Err.Error()
		} else {
			m.models = msg.Models
			m.modelsCatalog = msg.Catalog
			if m.mode == modeModels {
				m.openModelSelector()
			}
		}

	case providerActionMsg:
		m.controlPending = false
		m.providerForm.saving = false
		label := "provider: " + msg.Nickname
		switch msg.Action {
		case "add":
			label = "provider added: " + msg.Nickname
		case "update":
			label = "provider updated: " + msg.Nickname
		case "remove":
			label = "provider removed: " + msg.Nickname
		case "switch":
			label = "provider selected: " + msg.Nickname
		}
		if msg.Err != nil {
			if msg.Action == "add" || msg.Action == "update" {
				m.providerForm.err = msg.Err.Error()
				m.mode = modeConnectForm
			} else {
				m.addBlock(blockError, msg.Err.Error())
				m.mode = modeChat
			}
			m.syncViewport(true)
			break
		}
		m.providerForm.clearSecret()
		m.mode = modeChat
		m.providerConfirmDelete = ""
		m.providerDeleteErr = ""
		m.addBlock(blockMeta, label)
		m.syncViewport(true)
		cmds = append(cmds, refreshRuntimeCmd(m.comp, m.modelOverride))

	case modelActionMsg:
		m.controlPending = false
		if msg.Err != nil {
			m.modelOverride = msg.Previous
			m.contextNote = msg.Err.Error()
			m.addBlock(blockError, msg.Err.Error())
			m.syncViewport(true)
			cmds = append(cmds, refreshRuntimeCmd(m.comp, m.modelOverride))
			break
		}
		m.modelOverride = msg.Selected
		if msg.Runtime.Provider != "" {
			m.runtime.Provider = msg.Runtime.Provider
		}
		if msg.Runtime.ProviderSource != "" {
			m.runtime.ProviderSource = msg.Runtime.ProviderSource
		}
		if msg.Runtime.Model != "" {
			m.runtime.Model = msg.Runtime.Model
		}
		if msg.Runtime.Catalog != "" {
			m.runtime.Catalog = msg.Runtime.Catalog
			if m.runtime.Catalog != m.modelsCatalog {
				cmds = append(cmds, loadModelsCmd(m.comp, m.runtime.Catalog))
			}
		}
		if msg.Runtime.Context > 0 {
			m.runtime.Context = msg.Runtime.Context
		}
		if msg.Runtime.ContextSource != "" {
			m.runtime.ContextSource = msg.Runtime.ContextSource
		}
		m.runtime.OK = msg.Runtime.OK
		m.contextNote = msg.Warning
		label := msg.Selected
		if label == "" {
			label = "provider default"
		}
		m.addBlock(blockMeta, "conversation model: "+label)
		m.syncViewport(true)

	case providerBusEventMsg:
		cmds = append(cmds, refreshRuntimeCmd(m.comp, m.modelOverride))

	case modelsCatalogUpdatedMsg:
		cmds = append(cmds, loadCatalogProvidersCmd(m.comp))
		if m.runtime.Catalog != "" {
			cmds = append(cmds, loadModelsCmd(m.comp, m.runtime.Catalog))
		}

	case spinner.TickMsg:
		if !m.connected || m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case sessionEventMsg:
		if msg.event.SessionID == m.session {
			cmds = append(cmds, m.applySessionEvent(msg))
		}

	case turnDoneMsg:
		if msg.err != nil {
			m.finishTurn("", msg.err.Error())
		} else {
			m.finishTurn(msg.reply, "")
		}
		m.syncViewport(true)

	case steerDoneMsg:
		if msg.err != nil {
			m.addBlock(blockMeta, "steer failed: "+msg.err.Error())
			m.syncViewport(true)
		}
	case renderSettleMsg:
		m.renderTimerActive = false
		if !m.streaming {
			break
		}
		if time.Since(m.lastTokenAt) >= markdownSettleDelay {
			m.setStreaming(false)
			m.syncViewport(false)
		} else {
			// Tokens are still flowing; re-arm the settle tick.
			cmds = append(cmds, m.scheduleRender())
		}
	}

	var cmd tea.Cmd
	// Mouse wheel always belongs to the transcript. Sending it to both
	// viewport models would also scroll an overflowing multiline editor.
	if _, isWheel := msg.(tea.MouseWheelMsg); !isWheel {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	// Keys that scroll the transcript are forwarded to the viewport; the
	// textarea gets everything else (plus non-key messages other than wheel).
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		if m.isViewportKey(kp) {
			m.viewport, cmd = m.viewport.Update(kp)
			cmds = append(cmds, cmd)
		}
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.layout()
	return m, tea.Batch(cmds...)
}

func (m *model) applySessionEvent(msg sessionEventMsg) tea.Cmd {
	event := msg.event
	var renderCmd tea.Cmd
	switch msg.kind {
	case "token":
		// Once the assistant event delivered the complete content (or the
		// turn ended), late token frames from the same round are redundant
		// and must not reopen the block or push a duplicate tail.
		if m.roundClosed {
			return nil
		}
		m.setStreaming(true)
		m.lastTokenAt = time.Now()
		if event.Reasoning != "" {
			m.appendStreamingText(blockThinking, &m.thinkingIdx, event.Reasoning)
		}
		if event.Content != "" {
			m.appendStreamingText(blockAssistant, &m.assistantIdx, event.Content)
			m.hadAssistant = true
		}
		renderCmd = m.scheduleRender()

	case "assistant":
		m.setStreaming(false)
		m.updateRuntimeFromEvent(event)
		if event.Content != "" {
			// Finalize the round's streamed block: replace its partial text
			// with the complete content. Coalesce into the trailing
			// unfinalized block even if a late tool-call event reset
			// assistantIdx (NATS orders per subject, not across them), so a
			// fresh duplicate block is not opened.
			idx := m.streamingBlock(blockAssistant, m.assistantIdx)
			if idx < 0 {
				m.blocks = append(m.blocks, transcriptBlock{kind: blockAssistant})
				idx = len(m.blocks) - 1
			}
			m.blocks[idx].text = event.Content
			m.blocks[idx].finalized = true
			m.markTranscriptDirty()
			m.assistantIdx = idx
			m.hadAssistant = true
		}
		m.roundClosed = true

	case "toolcall":
		m.assistantIdx = -1
		m.thinkingIdx = -1
		m.roundClosed = false
		// The assistant round ended; render its markdown immediately.
		m.setStreaming(false)
		m.appendToolCall(toolCall{
			name:   event.Tool,
			args:   event.Args,
			result: event.Result,
			err:    event.Error,
		})

	case "status":
		m.updateRuntimeFromEvent(event)
		if warning := rawJSONString(event.Warning); warning != "" {
			m.contextNote = warning
		}

	case "context":
		m.updateRuntimeFromEvent(event)
		if event.Trimmed > 0 {
			m.contextNote = fmt.Sprintf("context trimmed — dropped %d earlier messages", event.Trimmed)
		} else if rawJSONBool(event.Warning) {
			pct := contextPercent(m.contextUsed, m.runtime.Context) * 100
			m.contextNote = fmt.Sprintf("context at %.0f%% — will trim soon", pct)
		}

	case "done":
		m.finishTurn(event.Reply, event.Error)
	}
	m.syncViewport(false)
	return renderCmd
}

// streamingBlock returns the index of the assistant/thinking block that a
// token frame or the assistant event should target. It prefers the trailing
// unfinalized block of that kind — the one currently being streamed — even
// when idx was reset by an out-of-order tool-call event, so streaming and
// the final content always land in a single block for the round. Creates and
// returns a fresh block when there is none to reuse.
func (m *model) streamingBlock(kind blockKind, idx int) int {
	if idx >= 0 && idx < len(m.blocks) && m.blocks[idx].kind == kind && !m.blocks[idx].finalized {
		return idx
	}
	// Scan from the end: reuse the last unfinalized block of this kind from
	// the current round, but do not cross into an earlier completed round.
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind != kind {
			continue
		}
		if m.blocks[i].finalized {
			return -1 // the most recent block of this kind is complete
		}
		return i
	}
	return -1
}

// appendStreamingText appends text to the active streaming block of kind,
// creating one if necessary, and records its index in *idx.
func (m *model) appendStreamingText(kind blockKind, idx *int, text string) {
	target := m.streamingBlock(kind, *idx)
	if target < 0 {
		m.blocks = append(m.blocks, transcriptBlock{kind: kind})
		target = len(m.blocks) - 1
	}
	m.blocks[target].text += text
	m.markTranscriptDirty()
	*idx = target
}

func rawJSONBool(raw json.RawMessage) bool {
	var value bool
	return json.Unmarshal(raw, &value) == nil && value
}

func rawJSONString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func (m *model) updateRuntimeFromEvent(event sessionEvent) {
	if event.Provider != "" {
		m.runtime.Provider = event.Provider
		m.runtime.OK = true
	}
	if event.ProviderSource != "" {
		m.runtime.ProviderSource = event.ProviderSource
	}
	if event.Model != "" {
		m.runtime.Model = event.Model
		m.runtime.OK = true
	}
	if event.Catalog != "" {
		m.runtime.Catalog = event.Catalog
	}
	if event.Context > 0 {
		m.runtime.Context = event.Context
	}
	if event.ContextSource != "" {
		m.runtime.ContextSource = event.ContextSource
	}
	if event.PromptTokens > 0 {
		m.promptTokens = event.PromptTokens
	}
	if event.Usage.PromptTokens > 0 {
		m.promptTokens = event.Usage.PromptTokens
	}
	if event.UsedTokens > 0 {
		m.contextUsed = event.UsedTokens
	} else if event.Usage.TotalTokens > 0 {
		m.contextUsed = event.Usage.TotalTokens
	} else if event.Usage.PromptTokens > 0 {
		m.contextUsed = event.Usage.PromptTokens + event.Usage.CompletionTokens
	}
}

func (m *model) finishTurn(reply, errorText string) {
	m.stopArmed = false
	m.stopping = false
	m.busy = false
	m.setStreaming(false)
	m.roundClosed = true
	if errorText != "" {
		m.addUniqueBlock(blockError, errorText)
	} else if reply != "" && !m.hadAssistant {
		m.addBlock(blockAssistant, reply)
		m.hadAssistant = true
	}
	m.assistantIdx = -1
	m.thinkingIdx = -1
}

// setStreaming flips the streaming flag, invalidating the transcript cache
// on transition: streaming changes how the active assistant block renders
// (plain text while tokens flow, markdown once output settles).
func (m *model) setStreaming(streaming bool) {
	if m.streaming == streaming {
		return
	}
	m.streaming = streaming
	m.markTranscriptDirty()
}

// scheduleRender arms the markdown settle tick. The tick re-renders the
// streaming assistant block once output has been quiet for
// markdownSettleDelay, avoiding a glamour render for every token in a
// fast-growing block. Returns nil when a tick is already pending.
func (m *model) scheduleRender() tea.Cmd {
	if m.renderTimerActive {
		return nil
	}
	m.renderTimerActive = true
	return tea.Tick(markdownSettleDelay, func(time.Time) tea.Msg {
		return renderSettleMsg{}
	})
}

// isViewportKey reports whether the key press is a transcript-scroll key
// owned by the viewport. The textarea consumes everything else, so keys
// matching both (printable characters, arrows, ctrl+u/d, ...) always go to
// the input.
func (m model) isViewportKey(msg tea.KeyPressMsg) bool {
	km := m.viewport.KeyMap
	return key.Matches(msg, km.Up) || key.Matches(msg, km.Down) ||
		key.Matches(msg, km.PageUp) || key.Matches(msg, km.PageDown) ||
		key.Matches(msg, km.HalfPageUp) || key.Matches(msg, km.HalfPageDown)
}

// inputAtTop/inputAtBottom include soft-wrapped rows. Checking only Line()
// would treat every visual row of a long wrapped first/last logical line as
// an edge and steal Up/Down from normal cursor movement.
func (m model) inputAtTop() bool {
	return m.input.Line() == 0 && m.input.LineInfo().RowOffset == 0
}

func (m model) inputAtBottom() bool {
	info := m.input.LineInfo()
	return m.input.Line() == m.input.LineCount()-1 && info.RowOffset >= info.Height-1
}

// addHistory records a sent message, skipping empty entries and consecutive
// duplicates, and caps the stored history. It reports whether an entry was
// added so persistent history stays in sync with the in-memory list.
func (m *model) addHistory(content string) bool {
	if content == "" {
		return false
	}
	if len(m.history) > 0 && m.history[len(m.history)-1] == content {
		return false
	}
	m.history = append(m.history, content)
	if len(m.history) > maxHistory {
		m.history = m.history[len(m.history)-maxHistory:]
	}
	return true
}

// histPrev walks back through sent messages, snapshotting the in-progress
// draft the first time so histNext can restore it.
func (m *model) histPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.histIdx == -1 {
		m.draft = m.input.Value()
		m.histIdx = len(m.history)
	}
	if m.histIdx > 0 {
		m.histIdx--
		m.input.SetValue(m.history[m.histIdx])
	}
}

// histNext walks forward through sent messages, returning to the draft after
// the newest entry.
func (m *model) histNext() {
	if m.histIdx == -1 {
		return
	}
	if m.histIdx < len(m.history)-1 {
		m.histIdx++
		m.input.SetValue(m.history[m.histIdx])
	} else {
		m.histIdx = -1
		m.input.SetValue(m.draft)
	}
}

// searchStart enters reverse history search. Subsequent key presses are
// handled by handleSearchKey until the search is accepted or cancelled.
func (m *model) searchStart() {
	m.searchActive = true
	m.searchQuery = ""
	m.searchIdx = len(m.history)
	m.draft = m.input.Value()
	m.searchFindPrev()
}

// searchFindPrev moves to the most recent history entry (older than the
// current match, or from the end when the query just changed) containing the
// query. Returns false when there is no (further) match.
func (m *model) searchFindPrev() bool {
	query := strings.ToLower(m.searchQuery)
	if query == "" {
		m.searchIdx = -1
		m.input.SetValue(m.draft)
		return false
	}
	for i := m.searchIdx - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(m.history[i]), query) {
			m.searchIdx = i
			m.input.SetValue(m.history[i])
			return true
		}
	}
	m.searchIdx = -1
	m.input.SetValue(m.draft)
	return false
}

func (m *model) searchAccept() {
	m.searchActive = false
	m.searchQuery = ""
	if m.searchIdx >= 0 {
		m.histIdx = m.searchIdx
	} else {
		m.histIdx = -1
	}
	m.searchIdx = -1
}

func (m *model) searchCancel() {
	m.searchActive = false
	m.searchQuery = ""
	m.searchIdx = -1
	m.histIdx = -1
	m.input.SetValue(m.draft)
}

func (m *model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		m.searchAccept()
	case "esc":
		m.searchCancel()
	case "ctrl+r":
		m.searchFindPrev()
	case "backspace":
		runes := []rune(m.searchQuery)
		if len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
			m.searchIdx = len(m.history)
			m.searchFindPrev()
		}
	default:
		if msg.Text != "" {
			m.searchQuery += msg.Text
			m.searchIdx = len(m.history)
			m.searchFindPrev()
		}
	}
	m.layout()
	return m, nil
}

func (m *model) layout() {
	width := max(20, m.width)
	height := max(6, m.height)
	m.viewport.SetWidth(width)
	// SetWidth recalculates the textarea height (DynamicHeight, capped at
	// MaxHeight), so the viewport height below sees the settled input size.
	m.input.SetWidth(max(1, width-2))
	extra := 0
	if m.searchActive {
		extra = 1
	}
	m.viewport.SetHeight(max(1, height-3-m.input.Height()-extra))
	if m.mode == modeProviders || m.mode == modeCatalogProviders || m.mode == modeModels || m.mode == modeSessions {
		m.selector.setSize(width, max(6, height-4))
	}
	if m.mode == modeConnectForm {
		m.providerForm.setWidth(width)
	}
	m.ensureRenderer(width)
}

func (m *model) syncViewport(forceBottom bool) {
	follow := forceBottom || m.viewport.AtBottom()
	content := m.renderTranscript()
	if content != m.viewportContent {
		m.viewport.SetContent(content)
		m.viewportContent = content
	}
	if follow {
		m.viewport.GotoBottom()
	}
}

// ensureRenderer builds the markdown renderer for the given width, rebuilding
// (and invalidating block render caches) when the width changes.
func (m *model) ensureRenderer(width int) {
	if m.renderW == width {
		return
	}
	m.renderW = width
	for i := range m.blocks {
		m.blocks[i].renderedOK = false
	}
	m.markTranscriptDirty()

	// WithEnvironmentConfig honors GLAMOUR_STYLE (default: dark).
	r, err := glamour.NewTermRenderer(
		glamour.WithEnvironmentConfig(),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		// Fall back to plain text rendering without retrying on every key
		// event. A terminal resize will attempt construction again.
		m.renderer = nil
		return
	}
	m.renderer = r
}

func (m model) searchView() string {
	preview := "(no match)"
	if m.searchIdx >= 0 && m.searchIdx < len(m.history) {
		preview = strings.NewReplacer("\r\n", " ↵ ", "\n", " ↵ ", "\r", " ↵ ").Replace(m.history[m.searchIdx])
	}
	line := "(reverse-i-search)`" + m.searchQuery + "`: " + preview
	return metaStyle.Render(truncate(line, max(1, m.width-1)))
}

func (m model) View() tea.View {
	header := headerStyle.Render("Niffler") + metaStyle.Render(" / "+m.session)
	const headerSep = "  │  "
	runtimeLine := runtimeStatusLine(m.runtime, m.modelOverride, m.contextUsed, max(0, m.width-ansi.StringWidth(header)-ansi.StringWidth(headerSep)))
	headerLine := header + headerSep + runtimeLine
	makeView := func(content string) tea.View {
		view := tea.NewView(content)
		view.AltScreen = true
		// Mouse tracking is on by default (see /mouse): the wheel scrolls
		// the transcript and clicking a tool-run card expands it; native
		// selection then needs Shift+drag. With /mouse off the terminal's
		// native click-and-drag text selection works untouched. The
		// transcript stays keyboard-scrollable (PgUp/PgDn,
		// Ctrl+Up/Ctrl+Down) either way.
		if m.mouse {
			view.MouseMode = tea.MouseModeCellMotion
		}
		view.WindowTitle = "Niffler TUI - " + m.session
		return view
	}

	if len(m.approvals) > 0 {
		parts := []string{headerLine, m.approvalBox()}
		return makeView(strings.Join(parts, "\n"))
	}

	if m.mode != modeChat {
		var control string
		parts := []string{headerLine}
		switch m.mode {
		case modeProviders, modeCatalogProviders, modeModels, modeSessions:
			control = m.selector.list.View()
			parts = append(parts, control)
			footer := "/: filter  •  enter: choose  •  esc: back"
			if m.mode == modeProviders {
				if m.providerDeleteErr != "" {
					footer = errorStyle.Render(m.providerDeleteErr)
				} else if m.providerConfirmDelete != "" {
					footer = errorStyle.Render("Remove " + m.providerConfirmDelete + "? press d/x again to confirm, esc to cancel")
				} else {
					footer = "/: filter  •  enter: switch  •  e: edit  •  d: remove  •  esc: back"
				}
			}
			if m.controlPending {
				footer = "updating settings…"
			} else if m.contextNote != "" {
				footer = m.contextNote
			}
			parts = append(parts, metaStyle.Render(truncate(footer, max(1, m.width-1))))
		case modeConnectForm:
			parts = append(parts, m.providerForm.view(m.width))
		}
		return makeView(strings.Join(parts, "\n"))
	}

	status := "ready"
	if !m.connected {
		status = m.spinner.View() + " connecting to " + m.natsURL
	} else if m.busy {
		// While busy, the status line carries the two-stage stop prompt:
		// first ESC arms Stop?, second ESC force-cancels the turn.
		switch {
		case m.stopping:
			status = m.spinner.View() + " stopping…"
		case m.stopArmed:
			status = errorStyle.Render("Stop?") + "  (press esc again to cancel)"
		default:
			status = m.spinner.View() + " working"
		}
	} else if m.controlPending {
		status = "updating settings"
	}

	if m.contextNote != "" {
		status += " | " + m.contextNote
	} else {
		status += " | /provider /model /connect /help | alt+enter: newline | ctrl+r: history | ctrl+t: tools | esc: stop | ctrl+c: quit"
	}

	parts := []string{headerLine, m.viewport.View()}
	if m.searchActive {
		parts = append(parts, m.searchView())
	}
	parts = append(parts, m.input.View(), metaStyle.Render(truncate(status, max(1, m.width-1))))
	return makeView(strings.Join(parts, "\n"))
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return string(raw)
	}
	return out.String()
}

func truncate(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	return ansi.Truncate(s, limit, "…")
}

// historyFilePath returns the per-session persistent history file
// (JSONL, one sent message per line). Empty when the user state directory
// is unavailable.
func historyFilePath(session string) string {
	// Go has no os.UserStateDir; resolve $XDG_STATE_HOME ourselves with the
	// conventional fallback.
	dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "niffler-tui", "history-"+sanitizeSessionID(session)+".jsonl")
}

// loadHistory reads the persistent history file, keeping at most maxHistory
// entries. Malformed/empty lines and consecutive duplicates are dropped; a
// changed file is rewritten trimmed. Errors are swallowed: history is best
// effort.
func loadHistory(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []string
	dirty := false
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var entry string
			if err := json.Unmarshal(line, &entry); err != nil || entry == "" {
				dirty = true
			} else if len(entries) > 0 && entries[len(entries)-1] == entry {
				dirty = true
			} else {
				entries = append(entries, entry)
			}
		} else if readErr == nil {
			dirty = true
		}
		if readErr != nil {
			break
		}
	}
	if len(entries) > maxHistory {
		entries = entries[len(entries)-maxHistory:]
		dirty = true
	}
	if dirty {
		writeHistory(path, entries)
	}
	return entries
}

// appendHistoryEntry appends one sent message to the persistent history
// file, creating it if needed. Errors are ignored.
func appendHistoryEntry(path, content string) {
	if path == "" || content == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Chmod(0o600)
	line, err := json.Marshal(content)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}

// writeHistory rewrites the history file with the given entries.
func writeHistory(path string, entries []string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Chmod(0o600)
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		_, _ = f.Write(append(line, '\n'))
	}
}

func sanitizeSessionID(id string) string {
	return invalidSessionID.ReplaceAllString(id, "-")
}

func resolveNATSURL() string {
	if url := strings.TrimSpace(os.Getenv("NIF_NATS_URL")); url != "" {
		return url
	}
	paths := []string{}
	if root := strings.TrimSpace(os.Getenv("NIF_ROOT")); root != "" {
		paths = append(paths, filepath.Join(root, "var", "nats-url"))
	}
	paths = append(paths, filepath.Join("var", "nats-url"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			if url := strings.TrimSpace(string(data)); url != "" {
				return url
			}
		}
	}
	return defaultNATSURL
}

func main() {
	defaultSession := strings.TrimSpace(os.Getenv("NIF_SESSION"))
	if defaultSession == "" {
		defaultSession = "console"
	}
	session := flag.String("session", defaultSession, "Niffler session id")
	flag.Parse()
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr,
			"niffler-tui: interactive terminal required; run this binary from a terminal")
		return
	}
	*session = sanitizeSessionID(strings.TrimSpace(*session))
	if *session == "" {
		*session = "console"
	}

	natsURL := resolveNATSURL()
	if err := os.Setenv("NIF_NATS_URL", natsURL); err != nil {
		fmt.Fprintln(os.Stderr, "niffler-tui: set bus URL:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The SDK announces connections through slog; terminal output outside the
	// Bubble Tea renderer would corrupt the alternate screen.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	comp := sdk.New(componentName, componentVersion)
	comp.Client = true
	var program *tea.Program
	comp.On("ev.session.>", func(_ *sdk.Component, subject string, payload json.RawMessage) {
		var event sessionEvent
		if err := json.Unmarshal(payload, &event); err != nil || program == nil {
			return
		}
		kind := strings.TrimPrefix(subject, "ev.session.")
		program.Send(sessionEventMsg{kind: kind, event: event})
	})
	comp.On("ev.provider.>", func(_ *sdk.Component, subject string, _ json.RawMessage) {
		if program != nil {
			program.Send(providerBusEventMsg{Kind: strings.TrimPrefix(subject, "ev.provider.")})
		}
	})
	comp.On("ev.models.updated", func(_ *sdk.Component, _ string, _ json.RawMessage) {
		if program != nil {
			program.Send(modelsCatalogUpdatedMsg{})
		}
	})
	// Human approval gate: directed requests arrive on this component's own
	// subject (core derives it from the call envelope's caller — no
	// hardcoded names), broadcast requests are direct calls or fallbacks.
	comp.On("svc.approval."+componentName+".request", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if program == nil {
			return
		}
		if req, ok := parseApprovalPayload(payload); ok {
			program.Send(approvalEventMsg{req: req, directed: true})
		}
	})
	comp.On("ev.approval.request", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if program == nil {
			return
		}
		if req, ok := parseApprovalPayload(payload); ok {
			program.Send(approvalEventMsg{req: req, directed: false})
		}
	})
	comp.On("ev.approval.resolved", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if program == nil {
			return
		}
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(payload, &p); err == nil && p.ID != "" {
			program.Send(approvalResolvedMsg{id: p.ID})
		}
	})

	m := newModel(ctx, comp, *session, natsURL)
	program = tea.NewProgram(m)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "niffler-tui:", err)
		comp.Close()
		os.Exit(1)
	}
	comp.Close()
}
