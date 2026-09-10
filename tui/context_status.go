package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	color := lipgloss.Color(currentTheme.barOK)
	if percent >= 0.9 {
		color = lipgloss.Color(currentTheme.barCrit)
	} else if percent >= 0.75 {
		color = lipgloss.Color(currentTheme.barWarn)
	}
	bar := progress.New(
		progress.WithWidth(width),
		progress.WithColors(color),
		progress.WithFillCharacters('█', '░'),
		progress.WithoutPercentage(),
	)
	bar.EmptyColor = lipgloss.Color(currentTheme.barEmpty)
	return bar.ViewAs(max(0.0, min(1.0, percent)))
}

func runtimeStatusLine(loc Locale, runtime runtimeResolution, modelOverride string, used int, stats string, width int) string {
	provider := runtime.Provider
	if provider == "" {
		provider = t(loc, "runtime.provider")
	}
	modelName := runtime.Model
	if modelName == "" {
		modelName = t(loc, "runtime.model")
	}
	source := ""
	if runtime.ProviderSource == "environment" {
		source = t(loc, "runtime.env")
	} else if runtime.ProviderSource == "store" {
		source = t(loc, "runtime.global")
	}
	selection := provider + source + " › " + modelName
	if modelOverride != "" {
		selection += t(loc, "runtime.session")
	}

	contextText := t(loc, "runtime.ctxNone")
	if runtime.Context > 0 {
		pct := contextPercent(used, runtime.Context)
		if used > 0 {
			contextText = t(loc, "runtime.ctxPct",
				contextBar(pct, 10), fmt.Sprintf("%3.0f%%", pct*100),
				formatTokens(used), formatTokens(runtime.Context))
		} else {
			contextText = t(loc, "runtime.ctxEmpty", contextBar(0, 10), formatTokens(runtime.Context))
		}
	}
	limit := max(1, width-1)
	full := selection + "  │  " + contextText + statsSuffix(stats)
	if ansi.StringWidth(full) <= limit {
		return full
	}
	// Tight header: shrink the provider/model text before the live measures so
	// the context gauge and the usage chip survive on narrow terminals. The
	// model name is the more useful half of the selection, so it outlives the
	// provider prefix.
	minimal := "› " + modelName
	if modelOverride != "" {
		minimal += t(loc, "runtime.session")
	}
	shrink := func(room int) string {
		if ansi.StringWidth(selection) <= room {
			return selection
		}
		if ansi.StringWidth(minimal) <= room {
			return minimal
		}
		return truncate(minimal, room)
	}
	const minSelection = 8
	tail := "  │  " + contextText + statsSuffix(stats)
	if room := limit - ansi.StringWidth(tail); room >= minSelection {
		return shrink(room) + tail
	}
	tail = "  │  " + contextText
	if room := limit - ansi.StringWidth(tail); room >= minSelection {
		return shrink(room) + tail
	}
	return truncate(full, limit)
}

// statsSuffix appends the session usage chip (↑ in ↓ out, cache hit rate) to
// the runtime line when there is anything to show.
func statsSuffix(stats string) string {
	if stats == "" {
		return ""
	}
	return "  │  " + stats
}

// usageChip is the session usage summary shown in the header next to the
// context gauge: cumulative tokens in/out (↑/↓) and the prompt-cache hit
// rate when the provider has reported cached-input details.
func (m model) usageChip() string {
	var parts []string
	if m.inputTokens > 0 || m.outputTokens > 0 {
		parts = append(parts, t(m.loc, "runtime.tokens",
			formatTokens(m.inputTokens), formatTokens(m.outputTokens)))
	}
	if m.cachePrompt > 0 {
		pct := float64(m.cacheHits) / float64(m.cachePrompt) * 100
		parts = append(parts, t(m.loc, "runtime.cache", fmt.Sprintf("%.0f", pct)))
	}
	return strings.Join(parts, "  ")
}
