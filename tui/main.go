// niffler-tui is an interactive terminal chat client for Niffler sessions.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
	sdk "niffler.dev/sdk"
)

const (
	componentName    = "tui"
	componentVersion = "0.3.0"
	turnTimeout      = 31 * time.Minute
	reconnectDelay   = 2 * time.Second
	maxToolText      = 4000
	maxInputHeight   = 8
	defaultNATSURL   = "nats://127.0.0.1:4222"

	// markdownSettleDelay is how long assistant output must be quiet before
	// it is re-rendered as markdown. Glamour can take hundreds of
	// milliseconds on large blocks, so re-rendering per token would stall
	// the UI; instead blocks stream as plain text and upgrade to styled
	// markdown once the model pauses (or the round ends).
	markdownSettleDelay = 300 * time.Millisecond

	// viewportFlushInterval is the streaming repaint interval (~30fps).
	// Token frames arrive much faster than the terminal can usefully redraw,
	// and every viewport sync scans the whole scroll window, so updates are
	// coalesced: the first token schedules a flush and all tokens until it
	// fires land in the same frame.
	viewportFlushInterval = 33 * time.Millisecond

	// restartExitCode is what /restart exits with. The niffler-tui wrapper
	// script installed on PATH watches for exactly this code and re-runs the
	// binary, so a restart picks up a rebuilt install (a plugin update
	// replaces var/bin/tui while the running client keeps the old build).
	// Run without the wrapper, /restart simply exits with this code.
	restartExitCode = 85
)

// usageTotals is one conversation's cumulative usage counters, snapshotted
// when switching sessions so returning to a conversation keeps its stats.
type usageTotals struct {
	input, output              int
	cacheHits, cachePrompt     int
	lastPrompt, lastCompletion int
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type usageStats struct {
	PromptTokens     int                  `json:"prompt_tokens"`
	CompletionTokens int                  `json:"completion_tokens"`
	TotalTokens      int                  `json:"total_tokens"`
	Details          *promptTokensDetails `json:"prompt_tokens_details"`
}

// cacheHitPct reports the share of prompt tokens served from the provider's
// prompt cache this round, or -1 when the provider gave no cached-input
// breakdown (the field is omitted, never zero, so "0%" stays meaningful).
func (u usageStats) cacheHitPct() float64 {
	if u.Details == nil || u.Details.CachedTokens <= 0 || u.PromptTokens <= 0 {
		return -1
	}
	return float64(u.Details.CachedTokens) / float64(u.PromptTokens) * 100
}

type sessionEvent struct {
	SessionID      string          `json:"sessionId"`
	Content        string          `json:"content"`
	Reasoning      string          `json:"reasoning"`
	Tool           string          `json:"tool"`
	CallID         string          `json:"callId"`
	Phase          string          `json:"phase"`
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
	DurationMs     int             `json:"durationMs"`
	Attempt        int             `json:"attempt"`
	MaxRetries     int             `json:"maxRetries"`
	DelayMs        int             `json:"delayMs"`
	RetryAfterMs   int             `json:"retryAfterMs"`
	Budget         string          `json:"budget"`
	Reason         string          `json:"reason"`
}

type connectedMsg struct{}

type connectStoppedMsg struct{}

type sessionEventMsg struct {
	kind  string
	event sessionEvent
}

// turnDoneMsg completes a session turn. session is the conversation the
// turn was sent for; after a session switch the stale completion is dropped
// so the old conversation's reply cannot leak into the new transcript.
type turnDoneMsg struct {
	session string
	reply   string
	err     error
}

// renderSettleMsg fires after markdownSettleDelay of quiet streaming output;
// on arrival the dirty assistant block is rendered as markdown.
type renderSettleMsg struct{}

// viewportFlushMsg fires viewportFlushInterval after the first token of a
// burst; the pending viewport sync then runs. See scheduleViewportFlush.
type viewportFlushMsg struct{}

// streamKind says what the latest token frame carried, so the busy label can
// name the phase: reasoning only (thinking), any content (responding), or
// nothing yet. Reset at round boundaries; a streamKind that outlives its
// tokens is ignored via the lastTokenAt freshness check in busyLabel.
type streamKind uint8

const (
	streamNone       streamKind = iota
	streamThinking              // reasoning deltas, no content yet
	streamResponding            // content is on the page
)

type model struct {
	profileForm profileForm
	// Profile selection is client state for new conversations; bootstrap
	// loads only the selected conversation's persisted controls.
	toolProfile string
	// UI identity + lease (core's ui registry): the component name is
	// "tui-<hex>" so approval routing is private to this terminal, and the
	// registry number is the "Niffler 1" header label. launchDir keys the
	// resume-state file and pins new conversations' workspaces.
	uiID               string
	uiName             string
	uiNumber           int
	launchDir          string
	sessionNeedsCreate bool // no conversation header yet: first turn pins cwd
	ctx                context.Context
	comp               *sdk.Component
	session            string
	natsURL            string
	loc                Locale
	theme              string // active UI color theme (themeRegistry key)
	viewport           viewport.Model
	input              textarea.Model
	spinner            spinner.Model
	blocks             []transcriptBlock

	width     int
	height    int
	connected bool
	busy      bool
	// busyLabel inputs: when the turn started (drives the label's elapsed
	// suffix), what the latest token frame carried (thinking vs responding),
	// and how many mid-turn steers the turn accepted.
	busySince    time.Time
	streamKind   streamKind
	steered      int
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
	profileConfirmDelete  string // name armed for two-stage delete in /profile
	providers             []providerSummary
	providerStatus        providerStatusResponse
	catalogProviders      []catalogProvider
	harnessIdentity       harnessIdentity
	selfIdentity          selfIdentity
	// MCP server control plane (/mcp): configured servers, the two-stage
	// delete arm, and the add/edit form.
	mcpServers       []mcpServerSummary
	mcpConfirmDelete string
	mcpForm          mcpForm
	// LSP registry control plane (/lsp): configured language servers, the
	// two-stage delete arm, and the add/edit form.
	lspServers       []lspServerSummary
	lspConfirmDelete string
	lspForm          lspForm
	// Background-processes control plane (/processes + the status-line
	// badge): the registry snapshot, the two-stage kill arm, the peek view,
	// and the badge's refresh-tick guard (one live timer per purpose — the
	// armSpinner lesson).
	processes            []processSummary
	processesConfirmKill string
	processesPeek        processSummary
	processesPeekText    string
	processesPeekErr     string
	processesPeekLoading bool
	bgTicking            bool
	// Subagent roster (agent_list's read-only UI affordance) + the ctrl+o
	// output cycle: running processes and working subagents, one live view
	// per press.
	agents       []agentSummary
	bgPeekRoster []bgTarget
	// childAct is the live activity of this conversation's subagents, fed by
	// the ev.session.* frames of every session on the bus (agents.go).
	childAct      map[string]childActivity
	bgPeekIdx     int
	bgPeekText    string
	bgPeekErr     string
	bgPeekLoading bool
	models        []modelSummary
	modelsCatalog string
	runtime       runtimeResolution
	modelOverride string
	promptTokens  int
	contextUsed   int
	// inputTokens/outputTokens accumulate the session's billed tokens (the
	// header's ↑/↓ chip); cacheHits/cachePrompt accumulate the prompt-cache
	// economics (header and /status).
	// lastUsagePrompt/lastUsageCompletion identify the round whose usage was
	// last folded in, so a round reported on both a status and an assistant
	// event is only counted once.
	lastUsagePrompt     int
	lastUsageCompletion int
	inputTokens         int
	outputTokens        int
	cacheHits           int
	cachePrompt         int
	// usageCache snapshots the counters per conversation for the duration of
	// one TUI run, so switching sessions and back keeps the stats stable; the
	// persisted cache totals seed it on a conversation's first visit.
	usageCache     map[string]usageTotals
	contextNote    string
	controlPending bool

	// cwd is the conversation workspace shown in the bottom row (loaded from
	// the persisted conversation header).
	cwd string

	// transcript is the cached joined rendering of the scroll window (see
	// renderTranscript); viewportContent is the last string pushed into the
	// viewport, so syncViewport can skip SetContent when nothing changed.
	transcript      string
	transcriptDirty bool
	viewportContent string

	// scrollback caps the rendered transcript rows kept in the viewport (0 =
	// unlimited); renderFrom is the first block included in the current
	// window. pieceEpoch invalidates per-block render caches on wholesale
	// display changes (width, theme, thinking/tool level, streaming mode).
	scrollback int
	renderFrom int
	pieceEpoch int

	// flushPending/flushTimerActive coalesce viewport repaints while tokens
	// stream: a sync scans the whole window, so it runs at most once per
	// viewportFlushInterval instead of once per token.
	flushPending     bool
	flushTimerActive bool

	// spinnerTicking guards the busy-indicator tick chain (see armSpinner):
	// the chain re-arms itself while busy, so any other arming site must be
	// idempotent or the frame rate grows with every message sent.
	spinnerTicking bool

	// historyGen stamps stored-transcript replays (startup and /session
	// switching); a reply whose stamp is stale is dropped, so switching away
	// while a load is in flight cannot paste the old conversation into the
	// new one (see conversationHistoryMsg).
	historyGen int

	// Markdown renderer for assistant output. Rebuilt when the viewport
	// width changes; block render caches are invalidated on rebuild.
	renderer *glamour.TermRenderer
	renderW  int

	// thinkingRenderer renders reasoning blocks as markdown too, with prose
	// recoloured to the thinking accent — fenced code inside thinking keeps
	// syntax highlighting instead of rendering as flat dim text.
	thinkingRenderer *glamour.TermRenderer

	// Human approval gate: queued x-harness.approval requests (front of the
	// queue is the modal on screen) and per-session auto-approve memory.
	approvals    []approvalRequest
	autoApproved map[string][]string // sessionId -> tool names

	// mouse toggles terminal mouse tracking (default on). When on, the wheel
	// scrolls the transcript, clicks expand tool cards, and plain left-button
	// drags use the application-owned selection below. /mouse off remains a
	// fallback for terminal-native selection, but the app then receives no
	// wheel events. See /mouse.
	mouse     bool
	selection mouseSelection

	// streaming marks output that should remain plain until it settles.
	// renderTimerActive ensures only one settle timer spans token bursts,
	// including across tool rounds. lastTokenAt drives the quiet check.
	streaming         bool
	renderTimerActive bool
	lastTokenAt       time.Time
	// lastThinkRender rate-limits the streaming thinking block's markdown
	// re-render (see model.piece / thinkRenderInterval).
	lastThinkRender time.Time

	// thinkLevel controls how reasoning blocks render: full (default),
	// brief (one collapsed line per block), or off (hidden). ctrl+t cycles.
	// Display-side only; the transcript always receives the full reasoning
	// from the backend — this only changes how much of it is shown.
	thinkLevel thinkingLevel

	// toolLevel controls how tool-run cards render: brief (one summary line),
	// medium (per-tool previews: bash tail, edit diff, read head — default),
	// full (everything expanded), or off (hidden). ctrl+e cycles.
	// Display-side only, like thinkLevel.
	toolLevel toolLevel

	// toolCards backs tool runs with the theme's card background so adjacent
	// runs and surrounding output stop blending together. Toggled by /cards;
	// themes without a card background ignore it.
	toolCards bool

	// thinkingEffort is the per-conversation LLM thinking-effort selection
	// ("" = provider default | low | medium | high), persisted via
	// core.session {thinking} like the model override. ctrl+g cycles it.
	thinkingEffort string

	// oauthLogin is the active subscription login (ChatGPT / Claude): a nil
	// pointer means no flow is running. modeOAuth renders its panel and the
	// poll chain continues until the credential is stored or the flow fails.
	oauthLogin *oauthLoginState

	// slash is the merged slash-command registry (built-ins + component
	// registrations); slashComp is the live Tab-completion state. aliases
	// holds the user-defined prompt shortcuts from /alias (alias.go).
	slash     slashRegistry
	slashComp slashCompleteState
	aliases   map[string]string

	// fileComp is the live @file-reference completion state; fileList
	// caches per-workspace listings for it (filecomp.go).
	fileComp fileCompState
	fileList map[string]fileListEntry

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

	// restart marks a /restart request: the client quits with
	// restartExitCode so a wrapper script can re-run it. Plain tea.Quit
	// (ctrl+c, window close) exits 0 and stays exited.
	restart bool
}

var (
	// The styles below are the compiled-in default theme (see theme.go).
	// They are the single source of truth for the "default" palette — every
	// other theme overwrites them in applyTheme, and they are re-derived from
	// the theme registry there, so edits to colors must happen in both places
	// (or better: only here, then mirrored).
	headerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	inputBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	userStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	thinkingStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	thinkLevelStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	toolLevelStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	effortStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	linkStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Underline(true)
	codeStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	activeSlashStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("7"))
	toolStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	toolCardStyle    = lipgloss.NewStyle()
	metaStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	noticeStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	errorStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	badgeBgStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	badgeAgentStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

// dotWithoutPadding is spinner.Dot with each frame's trailing space removed.
// Dot's frames are "⣾ ", and activityLabel joins the spinner to the status
// word with its own space, so the built-in padding rendered a double space
// ("⣾  working") in the divider.
var dotWithoutPadding = func() spinner.Spinner {
	frames := make([]string, len(spinner.Dot.Frames))
	for i, f := range spinner.Dot.Frames {
		frames[i] = strings.TrimRight(f, " ")
	}
	return spinner.Spinner{Frames: frames, FPS: spinner.Dot.FPS}
}()

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
	// Apply the persisted theme before any component copies a style value
	// (the spinner keeps its own copy of metaStyle).
	theme := detectTheme()
	applyTheme(theme)

	input := textarea.New()
	input.Prompt = ""
	loc := detectLocale()
	input.CharLimit = 0
	input.MaxHeight = maxInputHeight
	input.DynamicHeight = true
	input.MinHeight = 1
	input.ShowLineNumbers = false
	// The textarea palette depends on the theme's surface (light vs dark);
	// applyTheme above chose the theme, so style the widget to match. The
	// widget otherwise bakes in the dark palette and would keep its active
	// line black even under a light theme.
	input.SetStyles(inputTextareaStyles(currentTheme))
	focusCmd := input.Focus()

	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	// SoftWrap must stay off: the transcript pipeline pre-wraps every block
	// to the viewport width (clampLines), so the viewport's soft wrap never
	// engages on real content — but with it on, bubbles computes scroll
	// geometry (maxYOffset/AtBottom/SetYOffset/GotoBottom, called several
	// times per frame) by ANSI-measuring every line of the whole scroll
	// window. At the 3000-row cap that put ~100ms behind each streaming
	// frame and ~70ms behind every wheel tick — the choppy scrolling felt
	// while tokens stream. Off, the same geometry is O(1) and rendering is
	// unchanged (TestSoftWrapRedundantForClampedContent pins this).
	view.SoftWrap = false
	view.FillHeight = true
	configureKeymaps(&input, &view)

	spin := spinner.New()
	spin.Spinner = dotWithoutPadding
	spin.Style = metaStyle

	historyFile := historyFilePath(session)
	history := loadHistory(historyFile)

	m := model{
		ctx:          ctx,
		comp:         comp,
		session:      session,
		natsURL:      natsURL,
		loc:          loc,
		theme:        theme,
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
		slash:        newSlashRegistry(),
		aliases:      loadAliases(),
		scrollback:   scrollbackLines(),
		pieceEpoch:   1, // 0 is "never rendered" for blocks
		usageCache:   map[string]usageTotals{},
		fileList:     map[string]fileListEntry{},
		cwd:          initialCwd(),
		toolLevel:    toolMedium,
		toolCards:    true,
		// Mouse tracking on by default: wheel scrolling, tool-card clicks, and
		// application-owned plain-drag selection all work simultaneously.
		// /mouse off remains a terminal-native fallback.
		mouse: true,
	}
	m.layout()
	m.syncViewport(true)
	return m
}

// setTheme switches the color theme live: the global styles flip, the
// glamour renderer is marked for a rebuild (its style is theme-dependent),
// and every block's cached rendering is invalidated so the transcript
// repaints in the new palette. Returns false for an unknown name.
func (m *model) setTheme(name string) bool {
	if !applyTheme(name) {
		return false
	}
	m.theme = name
	m.spinner.Style = metaStyle
	// The textarea palette is surface-dependent (light vs dark), like the
	// startup styling in newModel; switching themes live must re-style the
	// widget too or a light theme leaves the dark palette's black active
	// line behind.
	m.input.SetStyles(inputTextareaStyles(currentTheme))
	m.renderW = -1 // force the glamour renderer rebuild on next layout
	for i := range m.blocks {
		m.blocks[i].renderedOK = false
	}
	m.invalidatePieces()
	m.layout()
	return true
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.focusCmd, m.connectCmd(), m.armSpinner(), leaseTickCmd())
}

// armSpinner starts the busy-indicator tick chain, at most once. The chain
// re-arms itself on every spinner.TickMsg while the UI is busy (see the
// spinner.TickMsg case), so every other arming site must go through here:
// arming a second copy starts a second, equally self-perpetuating chain, and
// the frame rate then grows with the conversation's history (measured: an
// instance that had sent a dozen messages pinned a core at ~96% CPU while
// busy, and stayed hot between turns). One live timer per purpose — the same
// shape as flushTimerActive for viewport flushes.
func (m *model) armSpinner() tea.Cmd {
	if m.spinnerTicking {
		return nil
	}
	m.spinnerTicking = true
	return m.spinner.Tick
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

// applyRuntimeRefresh applies a runtime resolution to the model and reports
// whether it was applied (a failed resolve keeps the current runtime). The
// caller has already checked that the refresh belongs to the current session.
func (m *model) applyRuntimeRefresh(msg runtimeRefreshedMsg) bool {
	if msg.ResolveErr != nil {
		return false
	}
	m.runtime = msg.Runtime
	return true
}

// --- UI lease (core's ui registry) -----------------------------------------
//
// Every TUI announces a client UUID (component name "tui-<hex>") and
// re-registers every few seconds. Core assigns a display number ("Niffler
// 1", ...) and brokers conversation ownership: a second TUI resuming the
// same conversation is told who holds it instead of silently joining the
// stream. Coordination only — a registry hiccup never blocks chatting.

const uiLeaseRenewInterval = 5 * time.Second

type uiAttachMsg struct {
	session     string
	registered  bool
	number      int
	claimed     bool
	ownerNumber int
	err         error
}

type uiRenewMsg struct {
	session     string
	number      int
	claimed     bool
	ownerNumber int
	err         error
}

type leaseTickMsg struct{}

func leaseTickCmd() tea.Cmd {
	return tea.Tick(uiLeaseRenewInterval, func(time.Time) tea.Msg { return leaseTickMsg{} })
}

// uiRegisterRequest calls the ui registry. A warm lease keeps the number; a
// lapsed one returns a fresh number, which is the expiry signal.
func uiRegisterRequest(comp *sdk.Component, uiID string) (int, error) {
	var reg struct {
		OK     bool `json:"ok"`
		Number int  `json:"number"`
	}
	if err := requestInto(comp, "core", "ui", map[string]any{"op": "register", "ui": uiID}, &reg); err != nil {
		return 0, err
	}
	if !reg.OK {
		return 0, fmt.Errorf("ui register refused")
	}
	return reg.Number, nil
}

func uiClaimRequest(comp *sdk.Component, uiID, session string) (claimed bool, ownerNumber int, err error) {
	var claim struct {
		OK     bool   `json:"ok"`
		Owner  string `json:"owner"`
		Number int    `json:"number"`
	}
	err = requestInto(comp, "core", "ui", map[string]any{
		"op": "claim", "ui": uiID, "session": session}, &claim)
	if err != nil {
		return false, 0, err
	}
	return claim.OK, claim.Number, nil
}

// uiAttachCmd registers this UI and claims the startup conversation.
func uiAttachCmd(comp *sdk.Component, uiID, session string) tea.Cmd {
	return func() tea.Msg {
		msg := uiAttachMsg{session: session}
		number, err := uiRegisterRequest(comp, uiID)
		if err != nil {
			msg.err = err // uncoordinated: chatting still works
			return msg
		}
		msg.registered, msg.number = true, number
		msg.claimed, msg.ownerNumber, msg.err = uiClaimRequest(comp, uiID, session)
		return msg
	}
}

// uiRenewCmd re-registers and re-claims the displayed conversation
// (idempotent for the owner; tells us when someone else took it).
func uiRenewCmd(comp *sdk.Component, uiID, session string) tea.Cmd {
	return func() tea.Msg {
		msg := uiRenewMsg{session: session}
		number, err := uiRegisterRequest(comp, uiID)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.number = number
		msg.claimed, msg.ownerNumber, msg.err = uiClaimRequest(comp, uiID, session)
		return msg
	}
}

// uiStartupDecision is the pure ownership rule for a refused attach/renew:
// keep the conversation when we own it (or coordination is unavailable);
// otherwise start a fresh conversation and say why. Pure so the ownership
// UX is unit-testable without a bus.
func uiStartupDecision(msg uiAttachMsg, newID string) (switchTo, note string) {
	if msg.err != nil || msg.claimed {
		return "", ""
	}
	if msg.ownerNumber > 0 {
		return newID, fmt.Sprintf(
			"This conversation is open in Niffler %d — started a new conversation.",
			msg.ownerNumber)
	}
	return newID, "This conversation is open in another UI — started a new conversation."
}

func (m model) sendTurn(content string) tea.Cmd {
	return func() tea.Msg {
		args := map[string]any{
			"sessionId": m.session,
			"profile":   m.toolProfile,
			"content":   content,
			// Always include the key: empty explicitly clears a previously
			// persisted conversation override after a provider/default change.
			"model": m.modelOverride,
		}
		// A conversation with no header yet gets this TUI's launch directory
		// pinned as its (immutable) workspace: starting niffler-tui inside a
		// project means the conversation works on that project. The retry
		// below covers the small race where a model-only call created the
		// header between bootstrap and this turn (its workspace is already
		// fixed, so cwd would be refused as immutable).
		if m.sessionNeedsCreate {
			args["cwd"] = m.cwd
		}
		result, err := m.comp.Request("core", "session", args, turnTimeout)
		if err != nil && m.sessionNeedsCreate {
			delete(args, "cwd")
			result, err = m.comp.Request("core", "session", args, turnTimeout)
		}
		if err != nil {
			return turnDoneMsg{session: m.session, err: fmt.Errorf("session turn: %w", err)}
		}
		var response struct {
			Reply string `json:"reply"`
		}
		if err := json.Unmarshal(result, &response); err != nil {
			return turnDoneMsg{session: m.session, err: fmt.Errorf("decode session result: %w", err)}
		}
		return turnDoneMsg{session: m.session, reply: response.Reply}
	}
}

// cancelTurnCmd force-cancels the running turn by publishing
// llm.cancel.<sessionId>, which aborts the in-flight streaming chat call
// in the llm component. The session runner sees the cancelled chat error,
// ends the turn with "done", and the pending core/session request returns,
// so finishTurn runs and busy clears. Fire-and-forget (errors surface as
// nothing here; the request will time out on its own if the cancel fails).
// Pointer receiver: arming the stopping state must survive on the model.
func (m *model) cancelTurnCmd() tea.Cmd {
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
	// which mode or control would otherwise consume them. consumed reports
	// the key was taken; cmd carries any follow-up (persisting a fresh
	// auto-approve) that must run off the update loop.
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		if cmd, consumed := m.approvalKey(kp); consumed {
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case approvalEventMsg:
		return m, m.applyApprovalEvent(msg)

	case approvalResolvedMsg:
		m.applyApprovalResolved(msg.id)
		return m, nil

	case connectedMsg:
		m.connected = true
		m.addBlock(blockMeta, t(m.loc, "note.connected", m.natsURL, m.session))
		m.syncViewport(true)
		// Attach to the ui registry first: the attach decides whether we
		// keep the resumed conversation or start a fresh one (someone else
		// may hold it), and the bootstrap below must target the session we
		// actually end up on.
		cmds = append(cmds, uiAttachCmd(m.comp, m.uiID, m.session))

	case connectStoppedMsg:
		return m, nil

	case uiAttachMsg:
		if msg.registered {
			m.uiNumber = msg.number
		}
		switchTo, note := uiStartupDecision(msg, newSessionID())
		if switchTo != "" {
			var histCmd tea.Cmd
			m, histCmd = m.switchSessionWithHistory(switchTo)
			// The switch cleared the transcript; the explanation must land
			// after that reset to stay visible.
			m.addBlock(blockMeta, note)
			m.syncViewport(true)
			cmds = append(cmds, histCmd)
		}
		// Bootstrap + transcript replay for whichever session we ended on.
		cmds = append(cmds, bootstrapBackendCmd(m.comp, m.session))
		cmds = append(cmds, m.startHistoryLoad())
		return m, tea.Batch(cmds...)

	case uiRenewMsg:
		if msg.number > 0 {
			m.uiNumber = msg.number
		}
		if msg.session == m.session && !msg.claimed && msg.err == nil && msg.ownerNumber > 0 {
			// The lease lapsed while we were frozen (fresh number on
			// re-register) and another UI claimed the conversation meanwhile:
			// move to a fresh one instead of silently co-owning.
			switchTo, note := uiStartupDecision(uiAttachMsg{
				session: msg.session, registered: true,
				claimed: false, ownerNumber: msg.ownerNumber,
			}, newSessionID())
			if switchTo != "" {
				var histCmd tea.Cmd
				m, histCmd = m.switchSessionWithHistory(switchTo)
				m.addBlock(blockMeta, note)
				m.syncViewport(true)
				cmds = append(cmds, histCmd, bootstrapBackendCmd(m.comp, m.session),
					m.startHistoryLoad())
				return m, tea.Batch(cmds...)
			}
		}
		return m, tea.Batch(cmds...)

	case leaseTickMsg:
		cmds = append(cmds, leaseTickCmd())
		if m.connected && m.uiID != "" {
			cmds = append(cmds, uiRenewCmd(m.comp, m.uiID, m.session))
		}
		return m, tea.Batch(cmds...)

	case sessionListMsg:
		// The user may have left the browser while the store list was
		// loading; drop the result instead of yanking them back into it.
		if m.mode != modeSessions {
			return m, nil
		}
		if msg.Err != nil {
			m.contextNote = msg.Err.Error()
			m.mode = modeChat
			return m, nil
		}
		m.openSessionSelector(msg.Sessions)
		return m, nil

	case conversationHistoryMsg:
		// A replay belongs to the session and load it was fetched for; a
		// switch (or A→B→A) that happened while the store read was in
		// flight must not paste the old conversation into the new one.
		if msg.Session != m.session || msg.Gen != m.historyGen {
			break
		}
		if msg.Err != nil {
			m.contextNote = t(m.loc, "note.historyFailed", msg.Err.Error())
			break
		}
		if len(msg.Blocks) == 0 {
			break
		}
		// Insert at the recorded anchor, not the top: blocks that existed
		// when the load started (the startup "connected" banner) stay above
		// the history, and a message sent while the load was in flight stays
		// below it.
		anchor := min(max(msg.Anchor, 0), len(m.blocks))
		merged := make([]transcriptBlock, 0, len(m.blocks)+len(msg.Blocks))
		merged = append(merged, m.blocks[:anchor]...)
		merged = append(merged, msg.Blocks...)
		merged = append(merged, m.blocks[anchor:]...)
		m.blocks = merged
		m.renderFrom = 0 // recomputed by the next render
		m.markTranscriptDirty()
		// Pin to the newest message: startup and switches should show the
		// tail of the conversation, with the rest scrollable above it.
		m.syncViewport(true)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clearMouseSelection()
		m.layout()
		m.syncViewport(false)

	case tea.KeyPressMsg:
		if m.mode != modeChat {
			return m.handleControlKey(msg)
		}
		if m.searchActive {
			return m.handleSearchKey(msg)
		}
		keyName := msg.String()
		// An active @file completion behaves like the slash one: Tab cycles,
		// Enter accepts the filled reference and falls through to sending,
		// Esc dismisses, any other key dismisses and then edits.
		if m.fileComp.active {
			switch keyName {
			case "tab":
				var cmd tea.Cmd
				m, cmd = m.handleFileTab(false)
				return m, cmd
			case "shift+tab":
				var cmd tea.Cmd
				m, cmd = m.handleFileTab(true)
				return m, cmd
			case "enter":
				m.applyFileCandidate()
				m.dismissFileComp()
				// Fall through: the reference is filled in; the message
				// sends with it.
			case "esc", "ctrl+c":
				if keyName == "ctrl+c" {
					return m, tea.Quit
				}
				m.dismissFileComp()
				return m, nil
			default:
				m.dismissFileComp()
			}
		}
		// An active slash completion owns Tab (cycling) and is dismissed by
		// any other key, which then falls through to normal handling.
		if m.slashComp.active {
			switch keyName {
			case "tab":
				var cmd tea.Cmd
				m, cmd = m.handleSlashTab(false)
				return m, cmd
			case "shift+tab":
				var cmd tea.Cmd
				m, cmd = m.handleSlashTab(true)
				return m, cmd
			case "enter":
				m.dismissSlashComplete()
				// Fall through: the command line is already filled in.
			case "esc", "ctrl+c":
				if keyName == "ctrl+c" {
					return m, tea.Quit
				}
				m.dismissSlashComplete()
				return m, nil
			default:
				m.dismissSlashComplete()
			}
		}
		switch keyName {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			var cmd tea.Cmd
			if strings.HasPrefix(strings.TrimSpace(m.input.Value()), "/") {
				m, cmd = m.handleSlashTab(false)
			} else {
				m, cmd = m.handleFileTab(false)
			}
			return m, cmd
		case "enter":
			content := strings.TrimSpace(m.input.Value())
			if content == "" {
				return m, nil
			}
			if strings.HasPrefix(content, "/") {
				m.input.SetValue("")
				// Slash commands are kept in the input history (Pi-style):
				// up-arrow and ctrl+r recall the last commands as well as
				// prompts. Recorded before dispatch, so a /session or /new
				// switch still stores it in the session it was typed in.
				if m.addHistory(content) {
					appendHistoryEntry(m.historyFile, content)
				}
				m.histIdx = -1
				return m.executeLocalCommand(content)
			}
			// @file references resolve to a workspace-relative footer naming
			// the existing targets (filecomp.go) — the agent is told to open
			// them with read; no content is ever attached here.
			content = m.resolveFileRefs(content)
			if !m.connected {
				return m, nil
			}
			if m.busy {
				// Steer the running turn (Pi-style): the runner folds this into the
				// live conversation and may keep working; we render it locally so it
				// is visible in the flow even though busy stays true until "done".
				// Busy-Enter is not a fresh send, so it stays out of the input
				// history (see TestSteerWhileBusy).
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
			m.busySince = time.Now()
			m.lastTokenAt = m.busySince
			m.streamKind = streamNone
			m.steered = 0
			m.hadAssistant = false
			m.assistantIdx = -1
			m.thinkingIdx = -1
			m.setStreaming(false)
			m.roundClosed = false
			m.addBlock(blockUser, content)
			m.layout()
			m.syncViewport(true)
			return m, tea.Batch(m.sendTurn(content), m.armSpinner())
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
		case "ctrl+e":
			// Tool-card visibility cycle (brief → full → off).
			m.cycleToolVisibility()
			m.syncViewport(false)
			return m, nil
		case "ctrl+t":
			m.cycleThinkingLevel()
			m.syncViewport(false)
			return m, nil
		case "shift+tab":
			// Rotate the conversation's LLM thinking effort (auto → low →
			// medium → high → max); persisted like the model override and
			// applied by the session runner on the next turn.
			if !m.connected {
				m.contextNote = t(m.loc, "note.notConnected")
				return m, nil
			}
			if m.busy {
				m.contextNote = t(m.loc, "note.betweenTurnsEffort")
				return m, nil
			}
			if m.controlPending {
				return m, nil
			}
			next := m.nextThinkingEffort()
			m.controlPending = true
			m.contextNote = t(m.loc, "note.savingEffort")
			return m, setConversationThinkingCmd(m.comp, m.session, next)
		case "ctrl+o":
			// The worker cycle: running background processes and working
			// subagents, one live view per press. Nothing working -> a
			// transient note, not an empty mode.
			roster := bgRoster(m.processes, m.agents, m.childAct, epochNow())
			if len(roster) == 0 {
				m.contextNote = t(m.loc, "note.nothingRunning")
				m.layout()
				return m, nil
			}
			m.bgPeekRoster = roster
			m.bgPeekIdx = 0
			m.mode = modeBgPeek
			m.layout()
			return m, tea.Batch(m.loadBgPeekBody(), processesListCmd(m.comp),
				agentsListCmd(m.comp, m.session))
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
		// Mouse tracking supplies both viewport wheel events and the press,
		// motion, and release sequence used for application-owned selection.
		// Delay tool-card activation until release so a drag never toggles it.
		m.beginMouseSelection(msg)
		return m, nil

	case tea.MouseMotionMsg:
		m.extendMouseSelection(msg)
		return m, nil

	case tea.MouseReleaseMsg:
		if !m.selection.pressed {
			return m, nil
		}
		if cmd, dragged := m.finishMouseSelection(msg); dragged {
			return m, cmd
		}
		m.handleMouseClick(tea.MouseClickMsg{
			X: msg.X, Y: msg.Y, Button: tea.MouseLeft, Mod: msg.Mod,
		})
		return m, nil

	case tea.MouseWheelMsg:
		// A screen-coordinate selection cannot stay attached to content that
		// moves underneath it. Clear it, then let the viewport consume the
		// wheel below.
		m.clearMouseSelection()

	case tea.PasteMsg:
		if m.mode == modeProfileForm {
			return m.updateProfileForm(msg)
		}
		// Bracketed-paste arrives as PasteMsg, not KeyPressMsg, so without
		// interception it bypasses the control-mode guard and lands in the
		// chat textarea. Route it to the active control instead.
		if m.mode == modeConnectForm {
			var cmd tea.Cmd
			m.providerForm, cmd = m.providerForm.update(msg)
			return m, cmd
		}
		if m.mode == modeMcpForm {
			var cmd tea.Cmd
			m.mcpForm, cmd = m.mcpForm.update(msg)
			return m, cmd
		}
		if m.mode == modeLspForm {
			var cmd tea.Cmd
			m.lspForm, cmd = m.lspForm.update(msg)
			return m, cmd
		}
		if m.mode == modeOAuth && m.oauthLogin != nil {
			var cmd tea.Cmd
			var input textinput.Model
			input, cmd = m.oauthLogin.input.Update(msg)
			m.oauthLogin.input = input
			return m, cmd
		}
		if m.mode != modeChat {
			return m, nil
		}
		// chat mode: fall through to normal input handling below

	case bootstrapMsg:
		// A snapshot belongs to the session it was loaded for; after a
		// further /session or /new switch it is stale and must not clobber
		// the new conversation's state.
		if msg.Session != m.session {
			break
		}
		m.providers = msg.Providers.Providers
		m.providerStatus = msg.ProviderStatus
		m.catalogProviders = msg.CatalogProviders
		m.harnessIdentity = msg.Identity
		m.selfIdentity = msg.Self
		m.modelOverride = msg.Conversation.ModelOverride
		m.thinkingEffort = msg.Conversation.ThinkingEffort
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
		if msg.Conversation.Cwd != "" {
			m.cwd = msg.Conversation.Cwd
		}
		// No header yet: the first turn pins the launch directory as the
		// conversation workspace (see sendTurn). An existing header means
		// the workspace is already fixed — never re-pin it.
		m.sessionNeedsCreate = !msg.ConversationExists
		// First visit to this conversation in this run: seed the persisted
		// prompt-cache economics from its header. The in/out token totals are
		// per-run (core does not persist them), so they start at zero.
		if m.usageCache == nil {
			m.usageCache = map[string]usageTotals{}
		}
		if _, seen := m.usageCache[m.session]; !seen {
			m.cacheHits = msg.Conversation.CacheRead
			m.cachePrompt = msg.Conversation.CachePrompt
			m.lastUsagePrompt, m.lastUsageCompletion = 0, 0
			m.usageCache[m.session] = m.usageSnapshot()
		}
		// The slash registry is global; apply it even though the rest of the
		// snapshot is session-scoped.
		if msg.SlashErr == nil {
			m.mergeSlashRegistry(msg.SlashCommands)
		} else if len(m.slash.order) == 0 {
			m.contextNote = "slash registry unavailable: " + msg.SlashErr.Error()
		}
		if m.runtime.Catalog != "" {
			cmds = append(cmds, loadModelsCmd(m.comp, m.runtime.Catalog))
		}
		if len(msg.Warnings) > 0 && !m.runtime.OK {
			m.contextNote = "provider/model controls unavailable: " + msg.Warnings[0]
		}
		// The subagent badge loads with the conversation snapshot (the
		// session id scopes the roster); the tick chain keeps it fresh while
		// anything works.
		cmds = append(cmds, agentsListCmd(m.comp, m.session))

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
		// The runtime resolution reflects the model override of the session
		// the refresh was fired for; on mismatch keep the current runtime.
		if msg.Session == m.session && m.applyRuntimeRefresh(msg) {
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

	case catalogUpdatedMsg:
		// Components registered or departed: the slash registry changed.
		// Reload store-first (core checkpoints before announcing).
		return m, loadSlashTableCmd(m.comp)

	case slashTableMsg:
		if msg.Err == nil {
			m.mergeSlashRegistry(msg.Commands)
		} else if len(m.slash.order) == 0 {
			m.contextNote = "slash registry unavailable: " + msg.Err.Error()
		}

	case slashSourceMsg:
		m.applySlashSource(msg)
	case fileListMsg:
		m.applyFileList(msg)

	case slashResultMsg:
		cmds = append(cmds, m.applySlashResult(msg))

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
		} else if m.mode == modeConnectForm {
			// The connect/edit form consumes catalog models directly (prefill
			// or normalize its model field) without touching the model list
			// cached for the active provider's selector.
			m.providerForm.setCatalogModels(msg.Catalog, msg.Models)
		} else {
			m.models = msg.Models
			m.modelsCatalog = msg.Catalog
			if m.mode == modeModels {
				m.openModelSelector()
			}
		}

	case servedModelsMsg:
		// Edit form: live ids from the stored provider's endpoint.
		if m.mode == modeConnectForm && m.providerForm.edit && msg.editFor == m.providerForm.template {
			if msg.Err == nil {
				m.providerForm.setServedModels(msg.Models)
			}
		}

	case oauthStartMsg:
		m.controlPending = false
		if msg.err != nil {
			m.oauthLogin = nil
			m.mode = modeChat
			m.addBlock(blockError, msg.err.Error())
			m.syncViewport(true)
			break
		}
		state := msg.state
		state.setWidth(m.width)
		m.oauthLogin = &state
		m.mode = modeOAuth
		m.layout()
		cmds = append(cmds, pollOAuthCmd(m.comp, state))

	case oauthPollMsg:
		// Drop stale polls: cancelled flows (m.oauthLogin nil or flowID
		// mismatch) and results from a superseded poll chain (seq mismatch —
		// e.g. a manual submit started a fresh chain while this one slept).
		if m.oauthLogin == nil || m.oauthLogin.flowID != msg.state.flowID || m.oauthLogin.seq != msg.state.seq {
			break
		}
		state := msg.state
		if msg.err != nil {
			state.err = msg.err.Error()
			state.status = ""
			m.oauthLogin = &state
			break // panel stays for the manual retry; poll chain ends
		}
		if msg.done {
			m.oauthLogin = nil
			m.mode = modeChat
			m.layout()
			m.addBlock(blockMeta, t(m.loc, "oauth.signedIn", msg.provider.Nickname))
			m.syncViewport(true)
			cmds = append(cmds, refreshRuntimeCmd(m.comp, m.session, m.modelOverride))
			break
		}
		if state.manualPending != "" || state.status != m.oauthLogin.status {
			state.err = ""
		}
		m.oauthLogin = &state
		cmds = append(cmds, pollOAuthCmd(m.comp, state))

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
		case "strip":
			label = "strip model prefix: " + msg.Detail + " — " + msg.Nickname
		case "environment":
			label = t(m.loc, "chat.usingEnvironment")
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
		// A provider action that moved the active backend invalidates the
		// conversation's model pin: the pinned id was chosen for the provider
		// it was set under, and carrying it over is how a stale model — one
		// the new provider may not even serve — stayed selected. Drop the pin
		// so the newly active provider's configured default applies; the
		// model-only session call persists the clear and its response reports
		// the resolved runtime. Actions that keep the same backend (or only
		// update settings) leave the pin alone.
		if m.providerActionChangedProvider(msg) && m.modelOverride != "" {
			previous := m.modelOverride
			m.modelOverride = ""
			cmds = append(cmds, setConversationModelCmd(m.comp, m.session, "", previous))
		}
		cmds = append(cmds, refreshRuntimeCmd(m.comp, m.session, m.modelOverride))

	case mcpServersMsg:
		if msg.Err == nil {
			m.mcpServers = msg.Servers
			if m.mode == modeMcp {
				m.openMcpServerSelector()
			}
		} else {
			m.contextNote = msg.Err.Error()
			m.mode = modeChat
			m.layout()
		}

	case profileSavedMsg:
		return m.applyProfileSaved(msg)

	case profileDeletedMsg:
		return m.applyProfileDeleted(msg)

	case profilesMsg:
		if m.mode != modeProfiles {
			return m, nil
		}
		if msg.Err == nil {
			m.openProfileSelectorWith(msg.Profiles)
		} else {
			m.contextNote = msg.Err.Error()
			m.mode = modeChat
			m.layout()
		}

	case mcpSearchMsg:
		if msg.Err != nil {
			m.contextNote = msg.Err.Error()
			if m.mode == modeMcpSearch {
				m.mode = modeChat
				m.layout()
			}
			break
		}
		if m.mode == modeMcpSearch {
			m.openMcpSearchResults(msg.Query, msg.Entries)
		}

	case mcpEditReadyMsg:
		m.controlPending = false
		if msg.Err != nil {
			m.addBlock(blockError, msg.Err.Error())
			m.syncViewport(true)
			return m, nil
		}
		m.mcpForm = newEditMcpForm(*msg.Server, m.width, m.loc)
		m.mode = modeMcpForm
		m.layout()

	case mcpActionMsg:
		m.controlPending = false
		m.mcpForm.saving = false
		label := "mcp: " + msg.Name
		switch msg.Action {
		case "add":
			label = t(m.loc, "mcp.added", msg.Name)
		case "edit":
			label = t(m.loc, "mcp.updated", msg.Name)
		case "remove":
			label = t(m.loc, "mcp.removed", msg.Name)
		case "refresh":
			label = t(m.loc, "mcp.refreshed", msg.Name)
		case "on":
			label = t(m.loc, "mcp.enabled", msg.Name)
		case "off":
			label = t(m.loc, "mcp.disabled", msg.Name)
		}
		if msg.Err != nil {
			if msg.Action == "add" || msg.Action == "edit" {
				m.mcpForm.err = msg.Err.Error()
				m.mode = modeMcpForm
			} else {
				m.addBlock(blockError, msg.Err.Error())
				m.mode = modeChat
			}
			m.syncViewport(true)
			break
		}
		m.mode = modeChat
		m.mcpConfirmDelete = ""
		m.addBlock(blockMeta, label)
		if msg.Warning != "" {
			// Partial failure: stored/configured, but something (bridge start,
			// old bridge shutdown) needs attention.
			m.addBlock(blockError, t(m.loc, "mcp.warning", msg.Warning))
		}
		m.syncViewport(true)

	case lspServersMsg:
		m.controlPending = false
		if msg.Err == nil {
			m.lspServers = msg.Servers
			if m.mode == modeLsp {
				m.openLspServerSelector()
			}
		} else {
			m.contextNote = msg.Err.Error()
			m.mode = modeChat
			m.layout()
		}

	case lspActionMsg:
		m.controlPending = false
		m.lspForm.saving = false
		label := "lsp: " + msg.Name
		switch msg.Action {
		case "add":
			label = t(m.loc, "lsp.saved", msg.Name)
		case "remove":
			label = t(m.loc, "lsp.removed", msg.Name)
		}
		if msg.Err != nil {
			if msg.Action == "add" {
				m.lspForm.err = msg.Err.Error()
				m.mode = modeLspForm
			} else {
				m.addBlock(blockError, msg.Err.Error())
				m.mode = modeChat
			}
			m.syncViewport(true)
			break
		}
		m.mode = modeChat
		m.lspConfirmDelete = ""
		m.addBlock(blockMeta, label)
		m.syncViewport(true)
		// the registry changed: /lsp's next open re-fetches; keep the local
		// snapshot warm for the picker title
		m.lspServers = nil

	case processesListMsg:
		m.controlPending = false
		if msg.Err == nil {
			m.processes = msg.Processes
			if m.mode == modeProcesses {
				// background refreshes (badge tick, tool events) must not
				// rebuild the list under a browsing user: update items in
				// place so the cursor and any filter survive
				m.selector.list.SetItems(processSelectorItems(m.loc, m.processes, m.processesConfirmKill))
				m.layout()
			}
		} else if m.mode == modeProcesses {
			// panel open but the component is not running (required: false):
			// back to chat with a note instead of a stuck loading list
			m.contextNote = msg.Err.Error()
			m.mode = modeChat
			m.layout()
		}
		// badge: rearm the slow refresh while anything runs; drop the note
		// quietly when the component is absent and nothing was started
		if msg.Err == nil {
			m.layout()
		}
		if cmd := m.armBgTick(); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case processesPeekMsg:
		m.processesPeekLoading = false
		m.processesPeekText = msg.Text
		if msg.Err != nil {
			m.processesPeekErr = msg.Err.Error()
		}
		if m.mode == modeProcessesPeek {
			m.layout()
		}
		// The ctrl+o cycle shares the peek fetch; land the tail on the
		// cycled target only (the panel's peek has its own fields).
		if m.mode == modeBgPeek {
			if tgt, ok := m.bgPeekTarget(); ok && tgt.kind == "process" && tgt.id == msg.ID {
				m.bgPeekLoading = false
				m.bgPeekText = msg.Text
				if msg.Err != nil {
					m.bgPeekErr = msg.Err.Error()
				}
			}
			m.layout()
		}

	case agentEventMsg:
		if msg.Parent == "" || msg.Parent == m.session {
			if msg.Status == "failed" {
				m.contextNote = "subagent failed"
				if msg.Error != "" {
					m.contextNote += ": " + msg.Error
				}
			}
			cmds = append(cmds, agentsListCmd(m.comp, m.session))
		}

	case agentsListMsg:
		// An error means the agent component is absent or too old for the
		// read-only UI listing: keep the empty roster quietly (the badge
		// disappears; nothing else depends on it).
		if msg.Err == nil {
			m.agents = msg.Agents
		} else {
			m.agents = nil
		}
		if m.mode == modeBgPeek {
			m.refreshBgPeekRoster()
		}
		if cmd := m.armBgTick(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.layout()

	case processesActionMsg:
		m.controlPending = false
		if msg.Err != nil {
			m.addBlock(blockError, msg.Err.Error())
			m.syncViewport(true)
			break
		}
		m.processesConfirmKill = ""
		m.addBlock(blockMeta, t(m.loc, "processes.killed", msg.ID, msg.Status))
		m.syncViewport(true)
		cmds = append(cmds, processesListCmd(m.comp))

	case bgTickMsg:
		// the chain re-arms itself only while something is running
		if cmd := m.armBgTick(); cmd != nil {
			cmds = append(cmds, cmd, processesListCmd(m.comp), agentsListCmd(m.comp, m.session))
		}

	case thinkingEffortMsg:
		m.applyThinkingEffort(msg)

	case modelActionMsg:
		// A conversation-scoped save belongs to the session it was fired for;
		// after a switch neither its runtime snapshot nor the rollback apply.
		if msg.Session != m.session {
			break
		}
		m.controlPending = false
		if msg.Err != nil {
			m.modelOverride = msg.Previous
			m.contextNote = msg.Err.Error()
			m.addBlock(blockError, msg.Err.Error())
			m.syncViewport(true)
			cmds = append(cmds, refreshRuntimeCmd(m.comp, m.session, m.modelOverride))
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
			label = t(m.loc, "selector.providerDefault")
		}
		m.addBlock(blockMeta, t(m.loc, "chat.conversationModel", label))
		m.syncViewport(true)

	case providerBusEventMsg:
		// A switch from another client (or a plugin) also moves the backend
		// out from under the conversation's model pin, so reconcile it here
		// too. Events that only changed provider metadata just refresh.
		if msg.Changed && m.modelOverride != "" {
			previous := m.modelOverride
			m.modelOverride = ""
			cmds = append(cmds, setConversationModelCmd(m.comp, m.session, "", previous))
		}
		cmds = append(cmds, refreshRuntimeCmd(m.comp, m.session, m.modelOverride))

	case modelsCatalogUpdatedMsg:
		cmds = append(cmds, loadCatalogProvidersCmd(m.comp))
		if m.runtime.Catalog != "" {
			cmds = append(cmds, loadModelsCmd(m.comp, m.runtime.Catalog))
		}

	case spinner.TickMsg:
		m.spinnerTicking = false
		if !m.connected || m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			m.spinnerTicking = true
			cmds = append(cmds, cmd)
		}

	case sessionEventMsg:
		if msg.event.SessionID == m.session {
			cmds = append(cmds, m.applySessionEvent(msg))
		} else {
			// Another session's frames are this conversation's subagents (and
			// other clients' children): fold the known ones into the activity
			// map the worker badge and cycle read.
			m.noteChildActivity(msg.kind, msg.event)
		}

	case turnDoneMsg:
		// A turn completion belongs to the session it was sent for; after a
		// /session or /new switch the old turn's reply must not leak into
		// the new conversation's transcript or clear its busy flag.
		if msg.session != m.session {
			break
		}
		// The conversation header exists now (this or a prior model-only
		// call created it); later turns must not re-pin the workspace.
		m.sessionNeedsCreate = false
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
		} else if m.busy {
			// Accepted mid-turn steers surface as +N in the busy label.
			m.steered++
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

	case viewportFlushMsg:
		m.flushTimerActive = false
		if !m.flushPending {
			break
		}
		m.flushPending = false
		// A sync is cheap when nothing changed (renderTranscript is cached,
		// SetContent is skipped on equal content), so this also absorbs a
		// flush racing an immediate sync from assistant/tool/done events.
		m.syncViewport(false)
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
	var renderCmd, flushCmd tea.Cmd
	var bgCmd tea.Cmd
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
		// The label follows the stream's phase: content wins over reasoning,
		// and a responding label is never downgraded by a later reasoning
		// frame within the same round.
		if event.Content != "" {
			m.streamKind = streamResponding
		} else if event.Reasoning != "" && m.streamKind != streamResponding {
			m.streamKind = streamThinking
		}
		if event.Reasoning != "" {
			m.appendStreamingText(blockThinking, &m.thinkingIdx, event.Reasoning)
		}
		if event.Content != "" {
			m.appendStreamingText(blockAssistant, &m.assistantIdx, event.Content)
			m.hadAssistant = true
		}
		renderCmd = m.scheduleRender()
		// Tokens arrive far faster than the viewport can be repainted; the
		// first token schedules a flush and everything until it fires lands in
		// the same frame (see scheduleViewportFlush).
		flushCmd = m.scheduleViewportFlush()

	case "assistant":
		m.setStreaming(false)
		m.streamKind = streamNone
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
		// Round closed: finalize the trailing thinking block too. Only
		// assistant blocks used to be finalized, so the streamingBlock
		// reuse scan kept appending every later round's reasoning into
		// the first round's thinking block, stacking it all above the
		// conversation.
		m.finalizeThinking()
		m.roundClosed = true

	case "toolcall":
		m.assistantIdx = -1
		m.thinkingIdx = -1
		m.roundClosed = false
		// The assistant round ended; render its markdown immediately.
		m.setStreaming(false)
		m.streamKind = streamNone
		if event.Phase == "start" {
			// Phase 1 of core's two-phase protocol: the call is about to
			// run. Append a pending entry so long-running tools are
			// visible immediately; the done event completes it in place.
			m.appendToolCall(toolCall{
				name:    event.Tool,
				args:    event.Args,
				callID:  event.CallID,
				pending: true,
			})
		} else if m.completeToolCall(event.CallID, event.Tool, event.Args, event.Result, event.Error, event.DurationMs) {
			m.markTranscriptDirty()
			bgCmd = m.bgRefreshAfterTool(event.Tool, event.Args)
		} else {
			// Phase 2 with no pending entry to complete (or a legacy
			// single-phase event): append the finished call directly.
			m.appendToolCall(toolCall{
				name:       event.Tool,
				args:       event.Args,
				result:     event.Result,
				err:        event.Error,
				callID:     event.CallID,
				durationMs: event.DurationMs,
			})
		}

	case "retry":
		// Retry events are emitted between provider attempts. Keep them visible
		// in the transcript: a long Retry-After wait otherwise looks like a
		// frozen terminal, and the budget label explains why retries stop.
		m.updateRuntimeFromEvent(event)
		m.addBlock(blockMeta, formatRetryNotice(event))

	case "notice":
		// The runner folded background machinery into this conversation: a
		// settlement notice, a process exit, or the opening of an autonomous
		// wake turn. Render it as its own dim row — the payload carries the
		// text (docs/WIRE.md "Settlement notices"), so no store round-trip.
		if event.Content != "" {
			m.addBlock(blockNotice, event.Content)
		}

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
	if msg.kind == "token" {
		return tea.Batch(renderCmd, flushCmd, bgCmd)
	}
	m.syncViewport(false)
	return tea.Batch(renderCmd, bgCmd)
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

func formatRetryNotice(event sessionEvent) string {
	if event.Reason == "context-overflow" {
		return "recovering context after provider rejected an oversized request"
	}
	label := "retrying LLM"
	if event.Budget != "" {
		label += " (" + event.Budget + ")"
	}
	if event.Attempt > 0 {
		label += fmt.Sprintf(" — attempt %d", event.Attempt)
		if event.MaxRetries < 0 {
			label += "/unbounded"
		} else if event.MaxRetries > 0 {
			label += fmt.Sprintf("/%d", event.MaxRetries)
		}
	}
	if event.DelayMs > 0 {
		if event.DelayMs >= 1000 {
			label += fmt.Sprintf("; waiting %.1fs", float64(event.DelayMs)/1000)
		} else {
			label += fmt.Sprintf("; waiting %dms", event.DelayMs)
		}
	}
	if event.RetryAfterMs > 0 {
		label += fmt.Sprintf(" (server hint %.1fs)", float64(event.RetryAfterMs)/1000)
	}
	return label
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
	// Accumulate session usage once per LLM round: status and assistant events
	// both carry the round's usage object, so the (prompt, completion) pair
	// identifies it and keeps the round from counting twice. The totals feed
	// the header's ↑/↓ and cache chips.
	if u := event.Usage; u.PromptTokens > 0 || u.CompletionTokens > 0 {
		if u.PromptTokens != m.lastUsagePrompt || u.CompletionTokens != m.lastUsageCompletion {
			m.lastUsagePrompt = u.PromptTokens
			m.lastUsageCompletion = u.CompletionTokens
			m.inputTokens += u.PromptTokens
			m.outputTokens += u.CompletionTokens
			if u.Details != nil && u.Details.CachedTokens > 0 {
				m.cacheHits += u.Details.CachedTokens
				m.cachePrompt += u.PromptTokens
			}
		}
	}
}

// usageSnapshot/restoreUsage move the session usage counters in and out of
// usageCache on a conversation switch.
func (m *model) usageSnapshot() usageTotals {
	return usageTotals{
		input: m.inputTokens, output: m.outputTokens,
		cacheHits: m.cacheHits, cachePrompt: m.cachePrompt,
		lastPrompt: m.lastUsagePrompt, lastCompletion: m.lastUsageCompletion,
	}
}

func (m *model) restoreUsage(u usageTotals) {
	m.inputTokens, m.outputTokens = u.input, u.output
	m.cacheHits, m.cachePrompt = u.cacheHits, u.cachePrompt
	m.lastUsagePrompt, m.lastUsageCompletion = u.lastPrompt, u.lastCompletion
}

func (m *model) finishTurn(reply, errorText string) {
	m.stopArmed = false
	m.stopping = false
	m.busy = false
	m.setStreaming(false)
	m.streamKind = streamNone
	m.steered = 0
	m.busySince = time.Time{}
	m.roundClosed = true
	// Finalize any trailing thinking block here as well: a turn can end on
	// "done" without an assistant event (errors, short replies), and an
	// unfinalized thinking block would pull the next turn's reasoning into
	// this one (see the assistant-event finalization in applySessionEvent).
	m.finalizeThinking()
	if errorText != "" {
		m.addUniqueBlock(blockError, errorText)
	} else if reply != "" && !m.hadAssistant {
		m.addBlock(blockAssistant, reply)
		m.hadAssistant = true
	}
	m.assistantIdx = -1
	m.thinkingIdx = -1
}

// finalizeThinking marks the trailing unfinalized thinking block complete.
// Without it, streamingBlock's reuse scan would keep appending later
// rounds' reasoning into the first round's thinking block, stacking all
// thinking above the conversation instead of per-round.
func (m *model) finalizeThinking() {
	if idx := m.streamingBlock(blockThinking, m.thinkingIdx); idx >= 0 {
		m.blocks[idx].finalized = true
		m.markTranscriptDirty()
	}
}

// setStreaming flips the streaming flag. Only the active (unfinalized)
// assistant block renders differently in streaming mode, and its pieceKey
// encodes that state — settled blocks keep their cached renderings, so the
// flip re-renders the tail only instead of restyling the whole transcript
// (the old wholesale invalidation was the "markdown and color flicker").
// The join still re-runs for the tail block, hence dirty, not an epoch bump.
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

// scheduleViewportFlush coalesces streamed viewport repaints. Every sync
// rebuilds the scroll window and hands it to the viewport (which scans all
// of its rows), so this runs at most once per viewportFlushInterval no
// matter how fast tokens arrive; the pending flag records that new content
// arrived since the last repaint. Returns nil while a flush is already
// scheduled.
func (m *model) scheduleViewportFlush() tea.Cmd {
	m.flushPending = true
	if m.flushTimerActive {
		return nil
	}
	m.flushTimerActive = true
	return tea.Tick(viewportFlushInterval, func(time.Time) tea.Msg {
		return viewportFlushMsg{}
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
	// One cell short of the terminal everywhere: the viewport pads every
	// visible line to its exact width, and a line reaching the terminal's
	// last column flips the terminal into pending-wrap — bubbletea's
	// relative cursor moves then land one row off, showing up as phantom
	// blank lines between random lines while streaming or scrolling
	// (notably in the VSCode terminal). Reserving the last column keeps
	// soft-wrap out of the picture.
	m.viewport.SetWidth(width - 1)
	// SetWidth recalculates the textarea height (DynamicHeight, capped at
	// MaxHeight), so the viewport height below sees the settled input size.
	m.input.SetWidth(max(1, width-2))
	extra := 0
	if m.searchActive || m.slashComp.active {
		extra = 1
	}
	// The chat frame is header + viewport + blank spacer + rule + input +
	// rule + status — six fixed rows besides the viewport and input.
	m.viewport.SetHeight(max(1, height-6-m.input.Height()-extra))
	if m.mode == modeProviders || m.mode == modeCatalogProviders || m.mode == modeModels || m.mode == modeSessions || m.mode == modeThemes || m.mode == modeProfiles || m.mode == modeLsp || m.mode == modeProcesses {
		m.selector.setSize(width-1, max(6, height-4))
	}
	if m.mode == modeConnectForm {
		m.providerForm.setWidth(width)
	}
	if m.mode == modeOAuth && m.oauthLogin != nil {
		m.oauthLogin.setWidth(width)
	}
	if m.mode == modeProfileForm {
		m.profileForm.setWidth(width)
	}
	m.ensureRenderer(width - 1)
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
	m.invalidatePieces()

	// The renderer's markdown style comes from the active theme; the
	// default theme passes "" so GLAMOUR_STYLE still controls it (previous
	// behavior), named themes pin a matching built-in style.
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	}
	if currentTheme.glamour == "" {
		opts = append(opts, glamour.WithEnvironmentConfig())
	} else {
		opts = append(opts, glamour.WithStandardStyle(currentTheme.glamour))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		// Fall back to plain text rendering without retrying on every key
		// event. A terminal resize will attempt construction again.
		m.renderer = nil
		m.thinkingRenderer = nil
		return
	}
	m.renderer = r
	m.thinkingRenderer = newThinkingRenderer(width)
}

// newThinkingRenderer builds the markdown renderer used for reasoning blocks:
// the theme's markdown style with prose recoloured to the thinking accent and
// italic, so fenced code inside thinking keeps its syntax highlighting while
// the reasoning stays visibly muted (the same treatment Pi gives thinking).
// A GLAMOUR_STYLE pointing at a custom file falls back to the dark base.
func newThinkingRenderer(width int) *glamour.TermRenderer {
	name := currentTheme.glamour
	if name == "" {
		name = strings.TrimSpace(os.Getenv("GLAMOUR_STYLE"))
	}
	if name == "" {
		name = styles.DarkStyle
	}
	config, ok := styles.DefaultStyles[name]
	if !ok {
		config = styles.DefaultStyles[styles.DarkStyle]
	}
	if config == nil {
		return nil
	}
	style := *config
	// Flush-left reasoning: every built-in style sets document margin 2,
	// which indented settled thinking two columns right of its plain
	// streaming form — the "it also indents a bit" jump at the transition.
	noMargin := uint(0)
	style.Document.Margin = &noMargin
	accent := currentTheme.thinking
	italic, faint := true, true
	style.Text = glamouransi.StylePrimitive{Color: &accent, Italic: &italic, Faint: &faint}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil
	}
	return r
}

func (m model) searchView() string {
	preview := t(m.loc, "search.noMatch")
	if m.searchIdx >= 0 && m.searchIdx < len(m.history) {
		preview = strings.NewReplacer("\r\n", " ↵ ", "\n", " ↵ ", "\r", " ↵ ").Replace(m.history[m.searchIdx])
	}
	line := t(m.loc, "search.prompt", m.searchQuery, preview)
	return metaStyle.Render(truncate(line, max(1, m.width-1)))
}

func (m model) View() tea.View {
	label := "Niffler"
	if m.uiNumber > 0 {
		label = fmt.Sprintf("Niffler %d", m.uiNumber)
	}
	header := headerStyle.Render(label) + metaStyle.Render(" / "+m.session)
	const headerSep = "  │  "
	// Thinking level chip: standalone color so the mode reads at a glance
	// (ctrl+t cycles full → brief → off).
	thinkChip := thinkLevelStyle.Render(t(m.loc, "chip.think", t(m.loc, "level."+m.thinkLevel.String())))
	// Tool-card and thinking-effort chips share the same standalone-color
	// treatment (ctrl+e cycles tool cards; ctrl+g cycles the LLM effort).
	toolChip := toolLevelStyle.Render(t(m.loc, "chip.tool", t(m.loc, "level."+m.toolLevel.String())))
	effortChip := effortStyle.Render(t(m.loc, "chip.effort", t(m.loc, "level."+m.effortLabel())))
	runtimeLine := runtimeStatusLine(m.loc, m.runtime, m.modelOverride,
		max(0, m.width-1-ansi.StringWidth(header)-ansi.StringWidth(thinkChip)-ansi.StringWidth(toolChip)-ansi.StringWidth(effortChip)-3*ansi.StringWidth(headerSep)))
	headerLine := header + headerSep + thinkChip + headerSep + toolChip + headerSep + effortChip + headerSep + runtimeLine
	makeView := func(content string) tea.View {
		view := tea.NewView(m.applyMouseSelection(content))
		view.AltScreen = true
		// Cell-motion tracking provides wheel, press, drag, and release events.
		// selection.go turns drags into highlighted text and copies it on
		// release, so wheel scrolling and plain-drag selection coexist. With
		// /mouse off the terminal owns selection and scrollback instead.
		if m.mouse {
			view.MouseMode = tea.MouseModeCellMotion
		}
		view.WindowTitle = fmt.Sprintf("%s TUI - %s", label, m.session)
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
		case modeProviders, modeCatalogProviders, modeModels, modeSessions,
			modeMcp, modeMcpSearch, modeThemes, modeProfiles, modeLsp, modeProcesses:
			control = m.selector.list.View()
			parts = append(parts, control)
			footer := t(m.loc, "footer.filterChoose")
			if m.mode == modeProfiles {
				if m.profileConfirmDelete != "" {
					footer = errorStyle.Render(t(m.loc, "footer.confirmRemove", m.profileConfirmDelete))
				} else {
					footer = t(m.loc, "footer.filterSwitch")
				}
			}
			if m.mode == modeProviders {
				if m.providerDeleteErr != "" {
					footer = errorStyle.Render(m.providerDeleteErr)
				} else if m.providerConfirmDelete != "" {
					footer = errorStyle.Render(t(m.loc, "footer.confirmRemove", m.providerConfirmDelete))
				} else {
					footer = t(m.loc, "footer.filterSwitch")
				}
			}
			if m.mode == modeMcp {
				footer = t(m.loc, "footer.mcp")
				if m.mcpConfirmDelete != "" {
					footer = errorStyle.Render(t(m.loc, "footer.confirmRemove", m.mcpConfirmDelete))
				}
			}
			if m.mode == modeLsp {
				footer = t(m.loc, "footer.lsp")
				if m.lspConfirmDelete != "" {
					footer = errorStyle.Render(t(m.loc, "footer.confirmRemove", m.lspConfirmDelete))
				}
			}
			if m.mode == modeProcesses {
				footer = t(m.loc, "footer.processes")
				if m.processesConfirmKill != "" {
					footer = errorStyle.Render(t(m.loc, "footer.confirmKill", m.processesConfirmKill))
				}
			}
			if m.controlPending {
				footer = t(m.loc, "status.updating") + "…"
			} else if m.contextNote != "" {
				footer = m.contextNote
			}
			parts = append(parts, metaStyle.Render(truncate(footer, max(1, m.width-1))))
		case modeProfileForm:
			parts = append(parts, m.profileForm.view(m.width))
		case modeMcpForm:
			parts = append(parts, m.mcpForm.view(m.width))
		case modeLspForm:
			parts = append(parts, m.lspForm.view(m.width))
		case modeProcessesPeek:
			parts = append(parts, peekView(m.loc, m.processesPeek, m.processesPeekText,
				m.width, m.height, m.processesPeekLoading))
			if m.processesPeekErr != "" {
				parts = append(parts, errorStyle.Render(truncate(m.processesPeekErr, max(1, m.width-1))))
			}
		case modeBgPeek:
			if tgt, ok := m.bgPeekTarget(); ok {
				parts = append(parts, bgPeekView(m.loc, tgt, m.bgPeekText,
					m.width, m.height, m.bgPeekLoading, m.bgPeekContextOf()))
			}
			if m.bgPeekErr != "" {
				parts = append(parts, errorStyle.Render(truncate(m.bgPeekErr, max(1, m.width-1))))
			}
		case modeConnectForm:
			parts = append(parts, m.providerForm.view(m.width))
		case modeOAuth:
			if m.oauthLogin != nil {
				parts = append(parts, m.oauthLogin.view(m.width))
			}
			footer := t(m.loc, "oauth.esc")
			parts = append(parts, metaStyle.Render(truncate(footer, max(1, m.width-1))))
		}
		return makeView(strings.Join(parts, "\n"))
	}

	// Pi-style input zone: the transient activity state (spinner + working,
	// stopping, connecting, …) is embedded in the divider above the input,
	// which keeps the bottom row free for the workspace and notes.
	parts := []string{
		headerLine, m.viewport.View(), "",
		m.inputRule(m.activityLabel()), m.input.View(), m.inputRule(""),
	}
	if m.searchActive {
		parts = append(parts, m.searchView())
	}
	if m.slashComp.active {
		parts = append(parts, m.slashCompletionView())
	}
	if m.fileComp.active {
		parts = append(parts, m.fileCompletionView())
	}
	parts = append(parts, metaStyle.Render(truncate(m.bottomLine(), max(1, m.width-1))))
	return makeView(strings.Join(parts, "\n"))
}

// activityStallAfter is how long a busy turn may stay silent before the
// label admits the provider is not producing anything. Generous enough that
// a pause between reasoning bursts does not flash Waiting, short enough to
// catch a hung request at roughly the moment a human starts wondering.
const activityStallAfter = 5 * time.Second

// activityLabel is the transient turn state shown embedded in the input's
// top divider (Pi-style: a few characters into the line). Empty while idle,
// when the bottom row only carries the workspace and any note.
func (m model) activityLabel() string {
	switch {
	case !m.connected:
		return m.spinner.View() + " " + t(m.loc, "status.connecting", m.natsURL)
	case m.stopping:
		return m.spinner.View() + " " + t(m.loc, "status.stopping")
	case m.stopArmed:
		return errorStyle.Render(t(m.loc, "status.stopArmed"))
	case m.busy:
		return m.spinner.View() + " " + m.busyLabel()
	case m.controlPending:
		return t(m.loc, "status.updating") + "…"
	}
	return ""
}

// busyLabel names what the turn is doing right now, most specific signal
// first: a running tool names itself ("bash go build ./..."), fresh tokens
// say thinking or responding, a provider silent past activityStallAfter
// says waiting, and working is the fallback. The turn's elapsed time rides
// along on every variant — a frozen label cannot tell a slow build from a
// hung one — and accepted mid-turn steers show as +N. It re-evaluates on
// every frame the spinner chain already paints while busy, so the elapsed
// count and the stall transition need no timer of their own.
func (m model) busyLabel() string {
	reference := m.lastTokenAt
	if reference.IsZero() {
		reference = m.busySince
	}
	streaming := !reference.IsZero() && time.Since(reference) < activityStallAfter

	var label string
	if call := m.pendingCall(); call != nil {
		label = call.name
		if snippet := activitySnippet(call.name, toolArgs(call)); snippet != "" {
			label += " " + snippet
		}
	} else if m.streamKind == streamResponding && streaming {
		label = t(m.loc, "status.responding")
	} else if m.streamKind == streamThinking && streaming {
		label = t(m.loc, "status.thinking")
	} else if !reference.IsZero() && time.Since(reference) >= activityStallAfter {
		label = t(m.loc, "status.waiting")
	} else {
		label = t(m.loc, "status.working")
	}
	if m.steered > 0 {
		label += fmt.Sprintf(" +%d", m.steered)
	}
	if !m.busySince.IsZero() {
		label += " (" + fmtElapsed(time.Since(m.busySince)) + ")"
	}
	return label
}

// pendingCall returns the most recent tool call still awaiting its done
// event, scanning backwards from the newest block. A running call sits at
// the transcript tail, so the scan stops almost immediately; with nothing
// running it walks the transcript once, still trivial next to rendering it.
func (m model) pendingCall() *toolCall {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := &m.blocks[i]
		if b.kind != blockTool || b.run == nil {
			continue
		}
		for j := len(b.run.calls) - 1; j >= 0; j-- {
			if b.run.calls[j].pending {
				return &b.run.calls[j]
			}
		}
	}
	return nil
}

// activitySnippet picks one short identifying argument for the busy label:
// bash shows its command, file tools the path's basename, search and web
// tools their query, agents their description. Tools with nothing compact
// to show — fabric programs, git flags, MCP calls — stay name-only.
func activitySnippet(name string, args map[string]any) string {
	var key string
	switch name {
	case "bash":
		key = "command"
	case "read", "write", "edit":
		key = "path"
	case "grep", "glob", "files":
		key = "pattern"
	case "fetch":
		key = "url"
	case "agent":
		key = "description"
	}
	if key == "" {
		return ""
	}
	s, _ := args[key].(string)
	if s == "" {
		return ""
	}
	if key == "path" {
		if i := strings.LastIndexByte(s, '/'); i >= 0 {
			s = s[i+1:]
		}
	}
	const maxSnippet = 24
	if runes := []rune(s); len(runes) > maxSnippet {
		s = string(runes[:maxSnippet-1]) + "…"
	}
	return s
}

// fmtElapsed renders a compact turn age for the busy label: 42s, then
// 1m05s, then 1h02m03s.
func fmtElapsed(d time.Duration) string {
	const h, m = 3600, 60
	s := int(d.Round(time.Second).Seconds())
	switch {
	case s < m:
		return fmt.Sprintf("%ds", s)
	case s < h:
		return fmt.Sprintf("%dm%02ds", s/m, s%m)
	default:
		return fmt.Sprintf("%dh%02dm%02ds", s/h, (s%h)/m, s%m)
	}
}

// inputRule renders one of the dividers around the input. A non-empty label
// is embedded a few characters into the line and truncated to fit; an
// empty label is a plain rule. The state also remains visible through the
// bottom row's note, so a too-narrow terminal only loses the decoration.
func (m model) inputRule(label string) string {
	width := max(1, m.width-1)
	if label != "" {
		const lead = "── "
		leadW := ansi.StringWidth(lead)
		if room := width - leadW - 1; room >= 1 {
			label = truncate(label, room)
			rest := width - leadW - ansi.StringWidth(label) - 1
			return inputBorderStyle.Render(lead) + label + " " +
				inputBorderStyle.Render(strings.Repeat("─", max(0, rest)))
		}
	}
	return inputBorderStyle.Render(strings.Repeat("─", width))
}

// bottomLine is the single bottom row: the conversation workspace (the
// session's cwd), the context gauge, the session usage chip (↑/↓ tokens
// and cache hit rate) and the transient note — each after a vertical bar.
func (m model) bottomLine() string {
	line := displayPath(m.cwd, max(12, m.width/2))
	if ctx := contextStatusText(m.loc, m.runtime, m.contextUsed); ctx != "" {
		if line != "" {
			line += "  │  "
		}
		line += ctx
	}
	if chip := m.usageChip(); chip != "" {
		if line != "" {
			line += "  │  "
		}
		line += chip
	}
	if badge := processesBadgeText(m.loc, m.processes, epochNow()); badge != "" {
		if line != "" {
			line += "  │  "
		}
		// Distinct accents so "is a build running" and "is a subagent
		// working" read at a glance: amber for processes, cyan for subagents.
		line += badgeBgStyle.Render(badge)
	}
	if badge := agentsBadgeText(m.loc, m.agents, m.childAct, epochNow()); badge != "" {
		if line != "" {
			line += "  │  "
		}
		line += badgeAgentStyle.Render(badge)
	}
	if m.contextNote != "" {
		if line != "" {
			line += "  │  "
		}
		line += m.contextNote
	}
	return line
}

// armBgTick arms the badge's refresh chain at most once; it lives only
// while the last snapshot showed a running process and disarms itself from
// bgTickMsg when the count reaches zero (one live timer per purpose).
func (m *model) armBgTick() tea.Cmd {
	if runningCount(m.processes) == 0 && runningAgents(m.agents, m.childAct, epochNow()) == 0 {
		m.bgTicking = false
		return nil
	}
	if m.bgTicking {
		return nil
	}
	m.bgTicking = true
	return bgTickCmd()
}

// bgRefreshAfterTool schedules a badge refresh when a tool call just
// started or stopped a background process: the processes tools directly,
// or bash carrying run_in_background. Cheap guard: only the done event of
// those tools pays for a process_list round trip.
func (m *model) bgRefreshAfterTool(tool string, args json.RawMessage) tea.Cmd {
	interesting := tool == "process_start" || tool == "process_kill"
	if tool == "bash" {
		var parsed struct {
			RunInBackground bool `json:"run_in_background"`
		}
		if json.Unmarshal(args, &parsed) == nil && parsed.RunInBackground {
			interesting = true
		}
	}
	if !interesting {
		return nil
	}
	return processesListCmd(m.comp)
}

// initialCwd is the workspace fallback before the conversation header loads
// (and for brand-new conversations): the directory the TUI was started in.
func initialCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// displayPath shortens a filesystem path for the bottom row: the home
// directory collapses to ~, and paths wider than limit keep their tail with
// a leading ellipsis (the project name matters more than the prefix).
func displayPath(path string, limit int) string {
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			path = "~"
		} else if strings.HasPrefix(path, home+string(os.PathSeparator)) {
			path = "~" + path[len(home):]
		}
	}
	if limit > 0 {
		if w := ansi.StringWidth(path); w > limit {
			path = ansi.TruncateLeft(path, w-(limit-1), "…")
		}
	}
	return path
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

// newUIID returns a random hex id for this UI process. It becomes the
// component-name suffix ("tui-<id>"), which is what makes approval routing
// genuinely private per terminal.
func newUIID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on Linux/macOS; a time fallback keeps
		// startup going even if it somehow did.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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

// sessionFilePath is the state file remembering the last active conversation
// for one harness AND workspace: two harnesses sharing the per-user state dir
// never resume each other's conversations, and starting niffler-tui in a
// different project does not resume the previous project's conversation.
func sessionFilePath(natsURL, workspace string) string {
	dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	name := "session"
	if natsURL != "" || workspace != "" {
		sum := sha256.Sum256([]byte(natsURL + "\x00" + workspace))
		name = fmt.Sprintf("session-%x", sum[:6])
	}
	return filepath.Join(dir, "niffler-tui", name)
}

// loadLastSession returns the conversation the TUI was last switched to for
// this harness+workspace, or "" when none was recorded.
func loadLastSession(natsURL, workspace string) string {
	path := sessionFilePath(natsURL, workspace)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return sanitizeSessionID(strings.TrimSpace(string(data)))
}

// persistSession records the active conversation so the next launch resumes
// it (best effort; explicit -session/NIF_SESSION still win).
func persistSession(natsURL, workspace, id string) {
	if id == "" {
		return
	}
	path := sessionFilePath(natsURL, workspace)
	if path == "" {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, []byte(id+"\n"), 0o644)
}

func main() {
	// The saved last session is keyed by bus URL + launch directory: two
	// harnesses (each with its own store) never resume each other's
	// conversations, and starting in a different project starts that
	// project's conversation, not the previous one's.
	natsURL := resolveNATSURL()
	launchDir := initialCwd()
	// A /restart predecessor hands its registry identity — and the conversation
	// it was on — to this process (handoff.go), so a restart resumes that
	// conversation even when the predecessor's release never reached core.
	handoffUI, handoffSession := consumeHandoff(natsURL, launchDir, time.Now())
	defaultSession := strings.TrimSpace(os.Getenv("NIF_SESSION"))
	if defaultSession == "" {
		defaultSession = loadLastSession(natsURL, launchDir)
	}
	if defaultSession == "" {
		// The handoff names the conversation too: a missing or unreadable
		// last-session file must not turn a handover into a fresh conversation.
		defaultSession = handoffSession
	}
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

	if err := os.Setenv("NIF_NATS_URL", natsURL); err != nil {
		fmt.Fprintln(os.Stderr, "niffler-tui: set bus URL:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The SDK announces connections through slog; terminal output outside the
	// Bubble Tea renderer would corrupt the alternate screen.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Unique component identity per terminal process: core routes approvals
	// to the caller's own subject, so "tui-<hex>" makes directed approvals
	// private to this terminal (two plain "tui"s shared one subject), and
	// the ui registry numbers each client ("Niffler 1", "Niffler 2", ...).
	//
	// The registry identity is inherited when a /restart predecessor handed it
	// over — that is what keeps the conversation's claim, and the "Niffler N"
	// label, in the same hands across the restart. The component name is always
	// fresh: it is this process's approval subject, and a hard-exited
	// predecessor never announced a departure, so reusing its name would leave
	// a duplicate in the catalog.
	uiID := handoffUI
	if uiID == "" {
		uiID = newUIID()
	}
	uiName := "tui-" + newUIID()

	comp := sdk.New(uiName, componentVersion)
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
	comp.On("ev.provider.>", func(_ *sdk.Component, subject string, payload json.RawMessage) {
		if program == nil {
			return
		}
		changed := false
		if subject == "ev.provider.switch" {
			var event struct {
				Nickname string `json:"nickname"`
				Previous string `json:"previous"`
			}
			if json.Unmarshal(payload, &event) == nil && event.Nickname != event.Previous {
				changed = true
			}
		}
		program.Send(providerBusEventMsg{Changed: changed})
	})
	comp.On("ev.models.updated", func(_ *sdk.Component, _ string, _ json.RawMessage) {
		if program != nil {
			program.Send(modelsCatalogUpdatedMsg{})
		}
	})
	comp.On("ev.agent.started", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if program == nil {
			return
		}
		var event agentEventMsg
		if json.Unmarshal(payload, &event) == nil {
			program.Send(event)
		}
	})
	comp.On("ev.agent.notice", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if program == nil {
			return
		}
		var event agentEventMsg
		if json.Unmarshal(payload, &event) == nil {
			program.Send(event)
		}
	})
	// Catalog changes (components registering/departing) refresh the slash
	// registry: core checkpoints the merged table to the store before this
	// event goes out, so a store-first reload is consistent.
	comp.On("ev.catalog.updated", func(_ *sdk.Component, _ string, _ json.RawMessage) {
		if program != nil {
			program.Send(catalogUpdatedMsg{})
		}
	})
	// Human approval gate: directed requests arrive on this UI's own
	// subject (core derives it from the call envelope's caller — the unique
	// "tui-<hex>" name makes it private to this terminal), broadcast
	// requests are direct calls or fallbacks.
	comp.On("svc.approval."+uiName+".request", func(_ *sdk.Component, _ string, payload json.RawMessage) {
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
	m.uiID = uiID
	m.uiName = uiName
	m.launchDir = launchDir
	program = tea.NewProgram(m)
	final, err := program.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "niffler-tui:", err)
		comp.Close()
		os.Exit(1)
	}
	if fm, ok := final.(model); ok {
		// /restart hands the restart to whoever launched us: the installed
		// wrapper loops on this exit code and re-runs the binary from disk, so
		// the fresh process picks up a rebuilt install. Run() returns the final
		// model, which carries the flag set by the command.
		if fm.restart {
			// The successor inherits this identity through the handoff record
			// localRestart wrote, so the registration must stay warm: releasing
			// it here would make the successor a stranger that core refuses the
			// very conversation the handoff is carrying over.
			os.Exit(restartExitCode)
		}
		releaseUI(fm.comp, fm.uiID)
	}
	comp.Close()
}
