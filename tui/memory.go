package main

import (
	"charm.land/lipgloss/v2"
	"encoding/json"
	"fmt"
	"strings"
)

// Memory is display metadata, never a user instruction or proof of turn state.
type memoryActivity struct {
	ID        string          `json:"id"`
	Operation string          `json:"operation"`
	Phase     string          `json:"phase"`
	Subject   string          `json:"subject"`
	Scope     string          `json:"scope"`
	Text      string          `json:"text"`
	Injected  bool            `json:"injected"`
	Result    json.RawMessage `json:"result"`
}

func (m *model) appendMemory(a memoryActivity) {
	if a.Operation == "" {
		return
	} // legacy metadata-only frame
	raw, err := json.Marshal(a)
	if err != nil {
		return
	}
	for _, b := range m.blocks {
		if b.kind != blockMemory {
			continue
		}
		var previous memoryActivity
		if json.Unmarshal([]byte(b.text), &previous) == nil && a.ID != "" && previous.ID == a.ID {
			return
		}
	}
	m.addBlock(blockMemory, string(raw))
}

func (m model) renderMemory(block *transcriptBlock) string {
	var a memoryActivity
	if json.Unmarshal([]byte(block.text), &a) != nil {
		return ""
	}
	var r struct {
		Ranker   string `json:"ranker"`
		Count    int    `json:"count"`
		Revision int    `json:"revision"`
		Warning  string `json:"warning"`
		Error    string `json:"error"`
		Items    []struct {
			Subject string `json:"subject"`
			Text    string `json:"text"`
		} `json:"items"`
	}
	_ = json.Unmarshal(a.Result, &r)
	phase := a.Phase
	if a.Injected {
		phase = "recalled · applied to context"
	} else if a.Operation == "recall" && a.Phase == "completed" {
		phase = "looked up · not injected"
	}
	if a.Operation == "search" && a.Phase == "completed" {
		phase = fmt.Sprintf("searched · %d matches", r.Count)
	}
	if a.Operation == "forget" && a.Phase == "completed" {
		phase = "forgotten"
	}
	head := "◇ Memory · " + phase
	if r.Warning != "" {
		head += " · fallback/warning"
	}
	for _, label := range []string{a.Subject, a.Scope, r.Ranker} {
		if label != "" {
			head += " · " + label
		}
	}
	if r.Revision > 0 {
		head += fmt.Sprintf(" · revision %d", r.Revision)
	}
	// Use the theme's accent; the diamond + explicit label distinguish memory
	// even in monochrome. Refusals/fallback remain warnings, never success green.
	color := currentTheme.link
	if a.Phase == "refused" || r.Warning != "" {
		color = currentTheme.barWarn
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	detail, visible := m.toolDetail()
	if !visible || detail == detailBrief {
		return style.Render(head)
	}
	body := a.Text
	for _, item := range r.Items {
		body += "\n[" + item.Subject + "] " + item.Text
	}
	if r.Warning != "" {
		body += "\nWarning: " + r.Warning
	}
	if r.Error != "" {
		body += "\n" + r.Error
	}
	lines, skipped := collapseLines(resultLines(strings.TrimSpace(body)), previewLimit(detail == detailFull, toolPreviewLines), false)
	for _, line := range lines {
		head += "\n    " + line
	}
	if skipped > 0 {
		head += fmt.Sprintf("\n    … (%d more lines, ctrl+e to expand)", skipped)
	}
	return style.Render(head)
}
