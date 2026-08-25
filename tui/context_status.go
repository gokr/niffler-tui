package main

import (
	"fmt"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
)

func formatTokens(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	case value > 0:
		return fmt.Sprintf("%d", value)
	default:
		return "—"
	}
}

func contextPercent(used, limit int) float64 {
	if used <= 0 || limit <= 0 {
		return 0
	}
	return float64(used) / float64(limit)
}

func contextBar(percent float64, width int) string {
	if width <= 0 {
		return ""
	}
	color := lipgloss.Color("6")
	if percent >= 0.9 {
		color = lipgloss.Color("9")
	} else if percent >= 0.75 {
		color = lipgloss.Color("3")
	}
	bar := progress.New(
		progress.WithWidth(width),
		progress.WithColors(color),
		progress.WithFillCharacters('█', '░'),
		progress.WithoutPercentage(),
	)
	bar.EmptyColor = lipgloss.Color("8")
	return bar.ViewAs(max(0.0, min(1.0, percent)))
}

func runtimeStatusLine(runtime runtimeResolution, modelOverride string, used int, width int) string {
	provider := runtime.Provider
	if provider == "" {
		provider = "provider?"
	}
	modelName := runtime.Model
	if modelName == "" {
		modelName = "model?"
	}
	source := ""
	if runtime.ProviderSource == "environment" {
		source = " [env]"
	} else if runtime.ProviderSource == "store" {
		source = " [global]"
	}
	selection := provider + source + " › " + modelName
	if modelOverride != "" {
		selection += " [session]"
	}

	contextText := "ctx —"
	if runtime.Context > 0 {
		pct := contextPercent(used, runtime.Context)
		if used > 0 {
			contextText = fmt.Sprintf("ctx %s %3.0f%% %s/%s",
				contextBar(pct, 10), pct*100, formatTokens(used), formatTokens(runtime.Context))
		} else {
			contextText = fmt.Sprintf("ctx %s —/%s", contextBar(0, 10), formatTokens(runtime.Context))
		}
	}
	return truncate(selection+"  │  "+contextText, max(1, width-1))
}
