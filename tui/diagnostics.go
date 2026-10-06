package main

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// storedDiagnostics recognizes the exact historical harness wrapper. History
// remains untouched; only its presentation changes, including older sessions.
func storedDiagnostics(content string) (path, text string, ok bool) {
	const prefix = "[lsp diagnostics for "
	const suffix = " — asynchronously delivered after your edit, when the server answered]"
	head, body, found := strings.Cut(content, "\n")
	if !found || !strings.HasPrefix(head, prefix) || !strings.HasSuffix(head, suffix) {
		return "", "", false
	}
	path = strings.TrimSuffix(strings.TrimPrefix(head, prefix), suffix)
	return path, body, path != ""
}

// Diagnostics are their own asynchronous block, never grouped with actual
// tool invocations or marked successful by a tool-run glyph.
func (m *model) appendDiagnostics(path, text string) {
	if path == "" || text == "" {
		return // metadata-only events from older harnesses have no body
	}
	m.addBlock(blockDiagnostics, path+"\n"+text)
}

// Diagnostic severity is taken from the LSP renderer's line format, not
// arbitrary occurrences of "error" in a diagnostic's explanatory text.
var diagnosticErrorLine = regexp.MustCompile(`(?m)^.+:[0-9]+:[0-9]+  error  `)
var diagnosticErrorSummary = regexp.MustCompile(`(?m)^.+: [0-9]+ diagnostics \([1-9][0-9]* errors\)`)

func diagnosticsColor(text string) string {
	if diagnosticErrorLine.MatchString(text) || diagnosticErrorSummary.MatchString(text) {
		return currentTheme.error
	}
	if strings.Contains(text, "no diagnostics — clean") {
		return currentTheme.barOK
	}
	// Warnings, indexing failures and unknown results are not a clean bill.
	return currentTheme.barWarn
}

func (m model) renderDiagnostics(block *transcriptBlock) string {
	detail, visible := m.toolDetail()
	if !visible {
		return ""
	}
	path, text, _ := strings.Cut(block.text, "\n")
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(diagnosticsColor(text)))
	head := "▸ diagnostics(" + path + ")"
	if detail == detailBrief {
		return style.Render(head)
	}
	lines, skipped := collapseLines(resultLines(text), previewLimit(detail == detailFull, toolPreviewLines), false)
	var b strings.Builder
	b.WriteString("▾ diagnostics(" + path + ")")
	for _, line := range lines {
		b.WriteString("\n    " + line)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "\n    … (%d more lines, ctrl+e to expand)", skipped)
	}
	return style.Render(b.String())
}
