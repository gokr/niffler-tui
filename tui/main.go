// niffler-tui is an interactive terminal chat client for Niffler sessions.
package main

import (
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

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
	sdk "niffler.dev/sdk"
)

const (
	componentName    = "tui"
	componentVersion = "0.1.0"
	defaultNATSURL   = "nats://127.0.0.1:4222"
	turnTimeout      = 31 * time.Minute
	reconnectDelay   = 2 * time.Second
	maxToolText      = 4000
)

var invalidSessionID = regexp.MustCompile(`[^A-Za-z0-9_-]`)

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockThinking
	blockTool
	blockMeta
	blockError
)

type transcriptBlock struct {
	kind blockKind
	text string
}

type sessionEvent struct {
	SessionID string          `json:"sessionId"`
	Content   string          `json:"content"`
	Reasoning string          `json:"reasoning"`
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	Result    json.RawMessage `json:"result"`
	Error     string          `json:"error"`
	Reply     string          `json:"reply"`
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

type model struct {
	ctx      context.Context
	comp     *sdk.Component
	session  string
	natsURL  string
	viewport viewport.Model
	input    textinput.Model
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
}

var (
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	thinkingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	metaStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

func newModel(ctx context.Context, comp *sdk.Component, session, natsURL string) model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "message"
	input.CharLimit = 0
	focusCmd := input.Focus()

	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = true
	view.FillHeight = true

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = metaStyle

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
		focusCmd:     focusCmd,
	}
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
		result, err := m.comp.Request("core", "session", map[string]any{
			"sessionId": m.session,
			"content":   content,
		}, turnTimeout)
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case connectedMsg:
		m.connected = true
		m.addBlock(blockMeta, "connected to "+m.natsURL+"; session "+m.session)
		m.syncViewport(true)

	case connectStoppedMsg:
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.syncViewport(false)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			content := strings.TrimSpace(m.input.Value())
			if content == "" || !m.connected || m.busy {
				return m, nil
			}
			m.input.SetValue("")
			m.busy = true
			m.hadAssistant = false
			m.assistantIdx = -1
			m.thinkingIdx = -1
			m.addBlock(blockUser, content)
			m.syncViewport(true)
			return m, tea.Batch(m.sendTurn(content), m.spinner.Tick)
		}

	case spinner.TickMsg:
		if !m.connected || m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case sessionEventMsg:
		if msg.event.SessionID == m.session {
			m.applySessionEvent(msg)
		}

	case turnDoneMsg:
		if msg.err != nil {
			m.finishTurn("", msg.err.Error())
		} else {
			m.finishTurn(msg.reply, "")
		}
		m.syncViewport(true)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *model) applySessionEvent(msg sessionEventMsg) {
	event := msg.event
	switch msg.kind {
	case "token":
		if event.Reasoning != "" {
			if m.thinkingIdx < 0 {
				m.blocks = append(m.blocks, transcriptBlock{kind: blockThinking})
				m.thinkingIdx = len(m.blocks) - 1
			}
			m.blocks[m.thinkingIdx].text += event.Reasoning
		}
		if event.Content != "" {
			if m.assistantIdx < 0 {
				m.blocks = append(m.blocks, transcriptBlock{kind: blockAssistant})
				m.assistantIdx = len(m.blocks) - 1
			}
			m.blocks[m.assistantIdx].text += event.Content
			m.hadAssistant = true
		}

	case "assistant":
		if event.Content != "" {
			if m.assistantIdx < 0 {
				m.blocks = append(m.blocks, transcriptBlock{kind: blockAssistant})
				m.assistantIdx = len(m.blocks) - 1
			}
			m.blocks[m.assistantIdx].text = event.Content
			m.hadAssistant = true
		}

	case "toolcall":
		text := event.Tool
		if args := compactJSON(event.Args); args != "" {
			text += " " + args
		}
		if result := compactJSON(event.Result); result != "" && result != "null" {
			text += "\n" + truncate(result, maxToolText)
		}
		if event.Error != "" {
			text += "\nerror: " + event.Error
		}
		m.addBlock(blockTool, text)
		m.assistantIdx = -1
		m.thinkingIdx = -1

	case "done":
		m.finishTurn(event.Reply, event.Error)
	}
	m.syncViewport(false)
}

func (m *model) finishTurn(reply, errorText string) {
	m.busy = false
	if errorText != "" {
		m.addUniqueBlock(blockError, errorText)
	} else if reply != "" && !m.hadAssistant {
		m.addBlock(blockAssistant, reply)
		m.hadAssistant = true
	}
	m.assistantIdx = -1
	m.thinkingIdx = -1
}

func (m *model) addBlock(kind blockKind, text string) {
	m.blocks = append(m.blocks, transcriptBlock{kind: kind, text: text})
}

func (m *model) addUniqueBlock(kind blockKind, text string) {
	if len(m.blocks) > 0 {
		last := m.blocks[len(m.blocks)-1]
		if last.kind == kind && last.text == text {
			return
		}
	}
	m.addBlock(kind, text)
}

func (m *model) layout() {
	width := max(20, m.width)
	height := max(6, m.height)
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(height - 3)
	m.input.SetWidth(max(1, width-2))
}

func (m *model) syncViewport(forceBottom bool) {
	follow := forceBottom || m.viewport.AtBottom()
	m.viewport.SetContent(m.renderTranscript())
	if follow {
		m.viewport.GotoBottom()
	}
}

func (m model) renderTranscript() string {
	var out strings.Builder
	for i, block := range m.blocks {
		if i > 0 {
			out.WriteString("\n\n")
		}
		switch block.kind {
		case blockUser:
			out.WriteString(userStyle.Render("you>"))
			out.WriteString(" ")
			out.WriteString(block.text)
		case blockAssistant:
			out.WriteString(assistantStyle.Render("niffler>"))
			out.WriteString(" ")
			out.WriteString(block.text)
		case blockThinking:
			out.WriteString(thinkingStyle.Render("thinking> " + block.text))
		case blockTool:
			out.WriteString(toolStyle.Render("tool> " + block.text))
		case blockMeta:
			out.WriteString(metaStyle.Render(block.text))
		case blockError:
			out.WriteString(errorStyle.Render("error> " + block.text))
		}
	}
	return out.String()
}

func (m model) View() tea.View {
	header := headerStyle.Render("Niffler") + metaStyle.Render(" / "+m.session)
	status := "ready"
	if !m.connected {
		status = m.spinner.View() + " connecting to " + m.natsURL
	} else if m.busy {
		status = m.spinner.View() + " working"
	}
	status += " | pgup/pgdn or wheel: scroll | ctrl+c: quit"

	content := strings.Join([]string{
		header,
		m.viewport.View(),
		m.input.View(),
		metaStyle.Render(status),
	}, "\n")
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "Niffler TUI - " + m.session
	return view
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
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "... (truncated)"
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
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr,
			"niffler-tui: interactive terminal required; run this binary from a terminal")
		return
	}

	defaultSession := strings.TrimSpace(os.Getenv("NIF_SESSION"))
	if defaultSession == "" {
		defaultSession = "console"
	}
	session := flag.String("session", defaultSession, "Niffler session id")
	flag.Parse()
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
	var program *tea.Program
	comp.On("ev.session.>", func(_ *sdk.Component, subject string, payload json.RawMessage) {
		var event sessionEvent
		if err := json.Unmarshal(payload, &event); err != nil || program == nil {
			return
		}
		kind := strings.TrimPrefix(subject, "ev.session.")
		program.Send(sessionEventMsg{kind: kind, event: event})
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
