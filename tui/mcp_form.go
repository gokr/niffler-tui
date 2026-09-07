package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// mcpForm mirrors the provider connect/edit form for MCP servers. Type,
// approval and expose are enum cycles (left/right) instead of free text —
// they map 1:1 onto the manager's mcp_add/mcp_edit schema.
const (
	mcpFieldName = iota
	mcpFieldType
	mcpFieldCommand
	mcpFieldArgs
	mcpFieldURL
	mcpFieldEnv
	mcpFieldApproval
	mcpFieldExpose
	mcpFieldTimeout
	mcpFieldCount
)

var mcpTypeCycle = []string{"stdio", "http", "sse"}
var mcpApprovalCycle = []string{"", "always"}
var mcpExposeCycle = []string{"ondemand", "direct"}

type mcpForm struct {
	inputs [mcpFieldCount]textinput.Model
	focus  int
	name   string
	edit   bool // editing an existing server (name locked, env blank = keep)
	err    string
	saving bool
	loc    Locale
}

// newMcpForm builds the add form.
func newMcpForm(width int, loc Locale) mcpForm {
	form := newMcpFormFields(width, loc)
	form.focusField(0)
	return form
}

// newEditMcpForm prefills from a stored server. Env/header values are
// redacted server-side, so the env field starts blank: leaving it keeps the
// stored env, typing a JSON object replaces it.
func newEditMcpForm(s mcpServerSummary, width int, loc Locale) mcpForm {
	form := newMcpFormFields(width, loc)
	form.edit = true
	form.name = s.Name
	form.inputs[mcpFieldName].SetValue(s.Name)
	form.inputs[mcpFieldName].Blur()
	form.setType(indexOf(mcpTypeCycle, s.Type))
	form.inputs[mcpFieldCommand].SetValue(s.Command)
	form.inputs[mcpFieldArgs].SetValue(joinArgs(s.Args))
	form.inputs[mcpFieldURL].SetValue(s.URL)
	if s.TimeoutMs > 0 {
		form.inputs[mcpFieldTimeout].SetValue(strconv.Itoa(s.TimeoutMs / 1000))
	}
	form.inputs[mcpFieldApproval].SetValue(approvalLabel(s.Approval))
	form.inputs[mcpFieldExpose].SetValue(indexDefault(mcpExposeCycle, s.Expose, "ondemand"))
	form.inputs[mcpFieldName].Prompt = t(loc, "mcpform.prompt.nameLocked") + " "
	form.focusField(mcpFieldCommand)
	return form
}

func newMcpFormFields(width int, loc Locale) mcpForm {
	prompts := []string{
		t(loc, "mcpform.prompt.name"),
		t(loc, "mcpform.prompt.type"),
		t(loc, "mcpform.prompt.command"),
		t(loc, "mcpform.prompt.args"),
		t(loc, "mcpform.prompt.url"),
		t(loc, "mcpform.prompt.env"),
		t(loc, "mcpform.prompt.approval"),
		t(loc, "mcpform.prompt.expose"),
		t(loc, "mcpform.prompt.timeout"),
	}
	placeholders := []string{
		"filesystem",
		"stdio (left/right)",
		"npx",
		"-y @modelcontextprotocol/server-filesystem /tmp",
		"https://example.com/mcp",
		t(loc, "mcpform.placeholder.env"),
		"none (left/right)",
		"ondemand (left/right)",
		"0 = default",
	}
	var form mcpForm
	form.loc = loc
	for i := range form.inputs {
		input := textinput.New()
		input.Prompt = prompts[i]
		input.Placeholder = placeholders[i]
		input.CharLimit = 8192
		input.SetWidth(mcpInputWidth(width))
		form.inputs[i] = input
	}
	// Enum fields carry their current value (left/right cycles); empty
	// values would break the cycle arithmetic.
	form.inputs[mcpFieldType].SetValue("stdio")
	form.inputs[mcpFieldExpose].SetValue("ondemand")
	form.inputs[mcpFieldType].Blur() // enum display, not editable text
	form.inputs[mcpFieldApproval].Blur()
	form.inputs[mcpFieldExpose].Blur()
	return form
}

func mcpInputWidth(width int) int {
	return max(12, min(80, width-12))
}

func (f *mcpForm) setType(index int) {
	if index < 0 || index >= len(mcpTypeCycle) {
		index = 0
	}
	f.inputs[mcpFieldType].SetValue(mcpTypeCycle[index])
}

func (f mcpForm) currentType() string {
	value := f.inputs[mcpFieldType].Value()
	for _, candidate := range mcpTypeCycle {
		if candidate == value {
			return candidate
		}
	}
	return "stdio"
}

func (f mcpForm) currentApproval() string {
	value := f.inputs[mcpFieldApproval].Value()
	for _, candidate := range mcpApprovalCycle {
		if candidate == value {
			return candidate
		}
	}
	return ""
}

func (f mcpForm) currentExpose() string {
	value := f.inputs[mcpFieldExpose].Value()
	for _, candidate := range mcpExposeCycle {
		if candidate == value {
			return candidate
		}
	}
	return "ondemand"
}

// cycleEnum rotates the enum field under the cursor by delta. Only the
// enum fields (type/approval/expose) respond; text fields ignore it.
func (f *mcpForm) cycleEnum(delta int) {
	var cycle []string
	var target *textinput.Model
	switch f.focus {
	case mcpFieldType:
		cycle, target = mcpTypeCycle, &f.inputs[mcpFieldType]
	case mcpFieldApproval:
		cycle, target = mcpApprovalCycle, &f.inputs[mcpFieldApproval]
	case mcpFieldExpose:
		cycle, target = mcpExposeCycle, &f.inputs[mcpFieldExpose]
	default:
		return
	}
	current := indexOf(cycle, target.Value())
	next := (current + delta + len(cycle)) % len(cycle)
	if current < 0 { // empty value behaves like the first entry
		if delta < 0 {
			next = len(cycle) - 1
		} else {
			next = 0
		}
	}
	target.SetValue(cycle[next])
}

func (f *mcpForm) focusField(index int) tea.Cmd {
	if index < 0 {
		index = mcpFieldCount - 1
	}
	if index >= mcpFieldCount {
		index = 0
	}
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.focus = index
	return f.inputs[index].Focus()
}

func (f *mcpForm) nextField(delta int) tea.Cmd {
	return f.focusField((f.focus + delta + mcpFieldCount) % mcpFieldCount)
}

func (f *mcpForm) setWidth(width int) {
	for i := range f.inputs {
		f.inputs[i].SetWidth(mcpInputWidth(width))
	}
}

func (f mcpForm) update(msg tea.Msg) (mcpForm, tea.Cmd) {
	var cmd tea.Cmd
	// Enum fields are display-only; keep keystrokes out of them.
	if f.focus != mcpFieldType && f.focus != mcpFieldApproval && f.focus != mcpFieldExpose {
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	}
	return f, cmd
}

// approvalLabel renders the stored approval flag for the form ("" = none).
func approvalLabel(approval string) string {
	if approval == "always" {
		return "always"
	}
	return ""
}

// indexOf returns the first index of value in list, or -1.
func indexOf(list []string, value string) int {
	for i, candidate := range list {
		if candidate == value {
			return i
		}
	}
	return -1
}

// indexDefault is indexOf with a fallback for unknown values.
func indexDefault(list []string, value, fallback string) string {
	if indexOf(list, value) >= 0 {
		return value
	}
	return fallback
}

// splitArgs parses the args field: whitespace-separated tokens with
// double-quote grouping ("--flag" "some value" → two tokens).
func splitArgs(text string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}
	for _, r := range text {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return args
}

// joinArgs renders args back into the form field, quoting tokens that
// contain whitespace or quotes.
func joinArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			parts = append(parts, strconv.Quote(arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

func (f mcpForm) values() (mcpFormValues, error) {
	values := mcpFormValues{
		Name:     strings.TrimSpace(f.inputs[mcpFieldName].Value()),
		Type:     f.currentType(),
		Command:  strings.TrimSpace(f.inputs[mcpFieldCommand].Value()),
		Args:     splitArgs(f.inputs[mcpFieldArgs].Value()),
		URL:      strings.TrimSpace(f.inputs[mcpFieldURL].Value()),
		EnvJSON:  strings.TrimSpace(f.inputs[mcpFieldEnv].Value()),
		Approval: f.currentApproval(),
		Expose:   f.currentExpose(),
	}
	if values.Name == "" && !f.edit {
		return values, fmt.Errorf("%s", t(f.loc, "mcpform.nameRequired"))
	}
	switch values.Type {
	case "stdio":
		if values.Command == "" {
			return values, fmt.Errorf("%s", t(f.loc, "mcpform.commandRequired"))
		}
	case "http", "sse":
		if values.URL == "" {
			return values, fmt.Errorf("%s", t(f.loc, "mcpform.urlRequired"))
		}
		if _, err := parseHTTPURL(values.URL); err != nil {
			return values, fmt.Errorf("%s", t(f.loc, "mcpform.urlInvalid"))
		}
	}
	if values.EnvJSON != "" {
		var env map[string]any
		if err := json.Unmarshal([]byte(values.EnvJSON), &env); err != nil || env == nil {
			return values, fmt.Errorf("%s", t(f.loc, "mcpform.envInvalid"))
		}
	}
	if text := strings.TrimSpace(f.inputs[mcpFieldTimeout].Value()); text != "" {
		seconds, err := strconv.Atoi(text)
		if err != nil || seconds < 0 {
			return values, fmt.Errorf("%s", t(f.loc, "mcpform.timeoutInvalid"))
		}
		values.TimeoutMs = seconds * 1000
	}
	return values, nil
}

// parseHTTPURL validates an http(s) endpoint (small wrapper so the form has
// one place that knows the rule).
func parseHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("not an http(s) URL")
	}
	return parsed, nil
}

func (f mcpForm) view(width int) string {
	var out strings.Builder
	title := t(f.loc, "mcpform.addTitle")
	if f.edit {
		title = t(f.loc, "mcpform.editTitle", f.name)
	}
	out.WriteString(headerStyle.Render(title))
	out.WriteString("\n\n")
	meta := t(f.loc, "mcpform.addMeta")
	if f.edit {
		meta = t(f.loc, "mcpform.editMeta", f.name)
	}
	out.WriteString(metaStyle.Render(meta))
	out.WriteString("\n\n")
	for i := range f.inputs {
		out.WriteString(f.inputs[i].View())
		out.WriteByte('\n')
	}
	if f.err != "" {
		out.WriteByte('\n')
		out.WriteString(errorStyle.Render(f.err))
		out.WriteByte('\n')
	}
	if f.saving {
		out.WriteString(metaStyle.Render(t(f.loc, "mcpform.saving")))
	} else {
		out.WriteString(metaStyle.Render(t(f.loc, "mcpform.keys")))
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1, 2).
		Width(max(24, min(width-6, 88))).
		Render(out.String())
	return panel
}
