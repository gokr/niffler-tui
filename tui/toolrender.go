// Per-tool presentation for tool-run cards (the medium and full detail
// levels): instead of one generic "name args + result JSON" line, each tool
// gets a call header and a body shaped like its output — bash shows its
// command with the tail of the log and its duration, edit shows the
// old/new text as a colored diff, read shows the head of the file.
//
// Previews are deliberately approximate: the TUI is a bus client, so it
// renders from the call's arguments and the tool result the model saw (the
// result object's "text" projection), never from the filesystem.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// toolPreview is one call's presentation: a styled header (the card adds the
// status glyph) and body lines, already styled.
type toolPreview struct {
	head string
	body []string
}

// Preview sizes. bash shows its tail (failures land at the end), read and
// generic output show their head, diffs show the start of the change.
const (
	toolPreviewLines = 5
	toolReadLines    = 12
	toolDiffLines    = 12
)

// toolTitleStyle styles a preview call line (command, path, tool name).
func toolTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.tool))
}

// toolDiffAddStyle/toolDiffDelStyle colour changed diff lines; they borrow
// the assistant (green) and error (red) accents so every theme keeps a
// readable pair without new palette entries.
func toolDiffAddStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.assistant))
}

func toolDiffDelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.error))
}

// toolArgs decodes a call's arguments; an invalid or empty object yields an
// empty map so previews degrade instead of failing.
func toolArgs(c *toolCall) map[string]any {
	var args map[string]any
	if len(c.args) > 0 {
		_ = json.Unmarshal(c.args, &args)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args
}

func argString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func argInt(args map[string]any, key string) int {
	if value, ok := args[key].(float64); ok {
		return int(value)
	}
	return 0
}

// toolResultText is the human-facing result text: the result object's "text"
// field when present (the same projection the model sees), else the compact
// JSON, else — for replayed results, which are stored as plain text — the
// text itself.
func toolResultText(c *toolCall) string {
	if len(c.result) == 0 {
		return ""
	}
	var object struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(c.result, &object); err == nil && object.Text != "" {
		return object.Text
	}
	if compact := compactJSON(c.result); compact != "null" {
		return compact
	}
	return ""
}

// resultLines splits a tool result into trimmed output lines.
func resultLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// collapseLines fits lines into limit rows, keeping the tail when tail is
// true (bash output: the failure is at the end) or the head otherwise. The
// returned skipped count drives the "... (N … lines, ctrl+e to expand)"
// hint; hints are added by the callers. limit <= 0 shows everything (the
// full detail level).
func collapseLines(lines []string, limit int, tail bool) (kept []string, skipped int) {
	if limit <= 0 || len(lines) <= limit {
		return lines, 0
	}
	skipped = len(lines) - limit
	if tail {
		return lines[len(lines)-limit:], skipped
	}
	return lines[:limit], skipped
}

// previewLimit returns the collapse limit for a detail level: 0 (show all)
// at full detail, the preview size otherwise.
func previewLimit(full bool, preview int) int {
	if full {
		return 0
	}
	return preview
}

// renderToolPreview builds one call's preview. full reports the card is at
// full detail, where output is shown in its entirety; previews collapse long
// output and add an expand hint.
func (m model) renderToolPreview(c *toolCall, full bool) toolPreview {
	switch c.name {
	case "bash", "shell":
		return m.renderShellPreview(c, full)
	case "edit":
		return m.renderEditPreview(c, full)
	case "read":
		return m.renderReadPreview(c, full)
	case "write":
		return m.renderWritePreview(c, full)
	default:
		return m.renderGenericPreview(c, full)
	}
}

// renderShellPreview is the bash card: "$ command", the tail of the output,
// and the exit status plus duration on a closing line.
func (m model) renderShellPreview(c *toolCall, full bool) toolPreview {
	args := toolArgs(c)
	command := argString(args, "command")
	if command == "" {
		command = c.name
	}
	head := toolTitleStyle().Render("$ " + command)

	status, output := splitShellStatus(toolResultText(c))
	lines := resultLines(output)

	var body []string
	if c.err != "" {
		body = append(body, errorStyle.Render(c.err))
	} else {
		kept, skipped := collapseLines(lines, previewLimit(full, toolPreviewLines), true)
		if skipped > 0 && !full {
			body = append(body, metaStyle.Render(
				t(m.loc, "tool.earlierLines", fmt.Sprint(skipped))))
		}
		body = append(body, kept...)
	}

	footer := status
	if c.durationMs > 0 {
		took := t(m.loc, "tool.took", fmt.Sprintf("%.1f", float64(c.durationMs)/1000))
		if footer != "" {
			footer += "  "
		}
		footer += took
	}
	if footer != "" {
		style := metaStyle
		if status != "" && !strings.Contains(status, "exit 0") && !strings.Contains(status, "exit 130") {
			style = errorStyle
		}
		body = append(body, style.Render(footer))
	}
	return toolPreview{head: head, body: body}
}

// splitShellStatus separates the bash result's leading "(exit N…)" line from
// the command output (core writes the status first).
func splitShellStatus(text string) (status, output string) {
	if !strings.HasPrefix(text, "(exit ") && !strings.HasPrefix(text, "(signal ") {
		return "", text
	}
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		return text[:newline], text[newline+1:]
	}
	return text, ""
}

// renderEditPreview is the edit card: "edit path" with the old/new text as a
// colored diff. The tool result only carries the summary and line counts, so
// the diff is reconstructed from the call's arguments.
func (m model) renderEditPreview(c *toolCall, full bool) toolPreview {
	args := toolArgs(c)
	path := argString(args, "path", "file_path", "filePath")
	if path == "" {
		path = "?"
	}
	head := toolTitleStyle().Render("edit ") + path

	type editPair struct{ old, new string }
	var pairs []editPair
	if edits, ok := args["edits"].([]any); ok {
		for _, raw := range edits {
			if obj, ok := raw.(map[string]any); ok {
				oldText, _ := obj["old_string"].(string)
				newText, _ := obj["new_string"].(string)
				pairs = append(pairs, editPair{old: oldText, new: newText})
			}
		}
	}
	if len(pairs) == 0 {
		oldText := argString(args, "old_string", "old_str", "oldText")
		newText := argString(args, "new_string", "new_str", "newText")
		if oldText != "" || newText != "" {
			pairs = append(pairs, editPair{old: oldText, new: newText})
		}
	}

	// Result line counts (+A -R) when the result is the structured object;
	// replayed results are plain text and skip the suffix.
	if len(c.result) > 0 {
		var stats struct {
			Added   int `json:"added_lines"`
			Removed int `json:"removed_lines"`
		}
		if err := json.Unmarshal(c.result, &stats); err == nil && (stats.Added > 0 || stats.Removed > 0) {
			head += "  " + toolDiffAddStyle().Render(fmt.Sprintf("+%d", stats.Added)) +
				" " + toolDiffDelStyle().Render(fmt.Sprintf("-%d", stats.Removed))
		}
	}

	var body []string
	if c.err != "" {
		body = append(body, errorStyle.Render(c.err))
	}
	for _, pair := range pairs {
		for _, line := range resultLines(pair.old) {
			body = append(body, toolDiffDelStyle().Render("- "+line))
		}
		for _, line := range resultLines(pair.new) {
			body = append(body, toolDiffAddStyle().Render("+ "+line))
		}
	}
	if len(body) > toolDiffLines && !full {
		extra := len(body) - toolDiffLines
		body = append(body[:toolDiffLines],
			metaStyle.Render(t(m.loc, "tool.moreLines", fmt.Sprint(extra))))
	}
	return toolPreview{head: head, body: body}
}

// renderReadPreview is the read card: "read path:start-end" with the head of
// the file (the first lines are where reading starts).
func (m model) renderReadPreview(c *toolCall, full bool) toolPreview {
	args := toolArgs(c)
	path := argString(args, "path", "file_path", "filePath")
	if path == "" {
		path = "?"
	}
	rangeSuffix := ""
	if offset, limit := argInt(args, "offset"), argInt(args, "limit"); offset > 0 || limit > 0 {
		start := offset
		if start == 0 {
			start = 1
		}
		end := ""
		if limit > 0 {
			end = fmt.Sprintf("-%d", start+limit-1)
		}
		rangeSuffix = metaStyle.Render(fmt.Sprintf(":%d%s", start, end))
	}
	head := toolTitleStyle().Render("read ") + path + rangeSuffix

	var body []string
	if c.err != "" {
		body = append(body, errorStyle.Render(c.err))
	} else {
		lines := resultLines(toolResultText(c))
		kept, skipped := collapseLines(lines, previewLimit(full, toolReadLines), false)
		body = append(body, kept...)
		if skipped > 0 && !full {
			body = append(body, metaStyle.Render(
				t(m.loc, "tool.moreLines", fmt.Sprint(skipped))))
		}
	}
	return toolPreview{head: head, body: body}
}

// renderWritePreview is the write card: "write path" with the head of the
// written content.
func (m model) renderWritePreview(c *toolCall, full bool) toolPreview {
	args := toolArgs(c)
	path := argString(args, "path", "file_path", "filePath")
	if path == "" {
		path = "?"
	}
	head := toolTitleStyle().Render("write ") + path

	var body []string
	if c.err != "" {
		body = append(body, errorStyle.Render(c.err))
	} else {
		lines := resultLines(argString(args, "content"))
		kept, skipped := collapseLines(lines, previewLimit(full, toolReadLines), false)
		body = append(body, kept...)
		if skipped > 0 && !full {
			body = append(body, metaStyle.Render(
				t(m.loc, "tool.moreLines", fmt.Sprint(skipped))))
		}
	}
	return toolPreview{head: head, body: body}
}

// renderGenericPreview is the fallback for tools without a bespoke renderer:
// name plus compact args, then the head of the result.
func (m model) renderGenericPreview(c *toolCall, full bool) toolPreview {
	head := toolStyle.Render(c.name)
	if args := compactJSON(c.args); args != "" && args != "{}" && args != "null" {
		head += "  " + truncate(args, toolPreviewLen)
	}
	if c.pending {
		head += "…"
	}

	var body []string
	if c.err != "" {
		body = append(body, errorStyle.Render(c.err))
	} else {
		lines := resultLines(toolResultText(c))
		kept, skipped := collapseLines(lines, previewLimit(full, toolReadLines), false)
		body = append(body, kept...)
		if skipped > 0 && !full {
			body = append(body, metaStyle.Render(
				t(m.loc, "tool.moreLines", fmt.Sprint(skipped))))
		}
	}
	return toolPreview{head: head, body: body}
}
