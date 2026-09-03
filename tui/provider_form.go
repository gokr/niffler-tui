package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	providerFieldNickname = iota
	providerFieldBaseURL
	providerFieldAPIKey
	providerFieldCatalog
	providerFieldModel
	providerFieldContext
	providerFieldCount
)

type providerForm struct {
	inputs   [providerFieldCount]textinput.Model
	focus    int
	template string
	edit     bool // editing an existing provider (nickname locked, key optional)
	err      string
	saving   bool
	loc      Locale
	// catalogID is the models.dev provider id whose catalog feeds the model
	// prefill/normalization ("synthetic", "deepseek", …); empty for custom
	// providers without a catalog entry.
	catalogID string
	loading   bool // catalog models are still being fetched
	models    []modelSummary
	served    []string // live ids from the provider's /models endpoint
}

func newProviderForm(template *catalogProvider, runtime runtimeResolution, width int, loc Locale) providerForm {
	form := newProviderFormFields(width, loc)
	if template != nil {
		form.template = template.Name
		if form.template == "" {
			form.template = template.ID
		}
		form.catalogID = template.ID
		form.inputs[providerFieldNickname].SetValue(template.ID)
		form.inputs[providerFieldBaseURL].SetValue(template.API)
		form.inputs[providerFieldCatalog].SetValue(template.ID)
		if runtime.Catalog == template.ID {
			form.inputs[providerFieldModel].SetValue(runtime.Model)
		}
	} else {
		form.template = t(loc, "selector.customProvider")
	}
	form.focusField(0)
	return form
}

// newEditProviderForm builds a form pre-filled from an existing provider so the
// user can edit its non-secret settings (and optionally rotate the key) without
// re-typing a credential. The nickname is immutable (the backend updates by it).
func newEditProviderForm(p providerSummary, width int, loc Locale) providerForm {
	form := newProviderFormFields(width, loc)
	form.edit = true
	form.template = p.Nickname
	form.catalogID = p.Catalog
	form.inputs[providerFieldNickname].SetValue(p.Nickname)
	form.inputs[providerFieldBaseURL].SetValue(p.BaseURL)
	form.inputs[providerFieldCatalog].SetValue(p.Catalog)
	form.inputs[providerFieldModel].SetValue(p.Model)
	if p.Context > 0 {
		form.inputs[providerFieldContext].SetValue(strconv.Itoa(p.Context))
	}
	if p.AuthType == "oauth" {
		form.inputs[providerFieldAPIKey].Placeholder = t(loc, "form.oauthLeaveBlank")
	} else {
		form.inputs[providerFieldAPIKey].Placeholder = t(loc, "form.leaveBlankToKeep")
	}
	form.inputs[providerFieldNickname].Prompt = t(loc, "form.prompt.nickname") + " "
	form.focusField(providerFieldBaseURL)
	return form
}

// newProviderFormFields allocates the raw text inputs shared by the add and
// edit forms (masked key field, width, char limits).
func newProviderFormFields(width int, loc Locale) providerForm {
	prompts := []string{
		t(loc, "form.prompt.nickname"),
		t(loc, "form.prompt.baseUrl"),
		t(loc, "form.prompt.apiKey"),
		t(loc, "form.prompt.catalog"),
		t(loc, "form.prompt.model"),
		t(loc, "form.prompt.context"),
	}
	placeholders := []string{
		"work-openrouter",
		"https://openrouter.ai/api/v1",
		t(loc, "form.placeholder.apiKey"),
		t(loc, "form.placeholder.catalog"),
		t(loc, "form.placeholder.model"),
		t(loc, "form.placeholder.context"),
	}
	var form providerForm
	form.loc = loc
	for i := range form.inputs {
		input := textinput.New()
		input.Prompt = prompts[i]
		input.Placeholder = placeholders[i]
		input.CharLimit = 4096
		input.SetWidth(providerInputWidth(width))
		form.inputs[i] = input
	}
	form.inputs[providerFieldAPIKey].EchoMode = textinput.EchoPassword
	form.inputs[providerFieldAPIKey].EchoCharacter = '•'
	form.inputs[providerFieldContext].CharLimit = 12
	return form
}

func (f *providerForm) focusField(index int) tea.Cmd {
	if index < 0 {
		index = providerFieldCount - 1
	}
	if index >= providerFieldCount {
		index = 0
	}
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.focus = index
	return f.inputs[index].Focus()
}

func (f *providerForm) nextField(delta int) tea.Cmd {
	return f.focusField((f.focus + delta + providerFieldCount) % providerFieldCount)
}

func providerInputWidth(width int) int {
	return max(12, min(80, width-12))
}

func (f *providerForm) setWidth(width int) {
	for i := range f.inputs {
		f.inputs[i].SetWidth(providerInputWidth(width))
	}
}

func (f providerForm) update(msg tea.Msg) (providerForm, tea.Cmd) {
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return f, cmd
}

func (f providerForm) values() (providerFormValues, error) {
	values := providerFormValues{
		Nickname: strings.TrimSpace(f.inputs[providerFieldNickname].Value()),
		BaseURL:  strings.TrimSpace(f.inputs[providerFieldBaseURL].Value()),
		APIKey:   strings.TrimSpace(f.inputs[providerFieldAPIKey].Value()),
		Catalog:  strings.TrimSpace(f.inputs[providerFieldCatalog].Value()),
		Model:    strings.TrimSpace(f.inputs[providerFieldModel].Value()),
	}
	if values.Nickname == "" && !f.edit {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.nicknameRequired"))
	}
	if values.APIKey == "" && !f.edit {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.apiKeyRequired"))
	}
	if values.BaseURL == "" {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.baseUrlRequired"))
	}
	parsed, err := url.Parse(values.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.baseUrlInvalid"))
	}
	if values.Model == "" {
		// No model typed: fall back to the live list or catalog auto-pick so
		// connecting a provider never requires typing a model id.
		if picked := pickDefaultServedModel(f.served); picked != "" {
			values.Model = picked
		} else if picked := pickDefaultModel(f.models); picked != "" {
			values.Model = picked
		} else {
			return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.modelRequired"))
		}
		f.inputs[providerFieldModel].SetValue(values.Model)
	} else if match := matchServedModel(f.served, values.Model); match != "" {
		// Live ids are the authority on spelling: repair against them first.
		values.Model = match
		f.inputs[providerFieldModel].SetValue(match)
	} else if exact := findModel(f.models, values.Model); exact != "" {
		values.Model = exact
	} else if match := findModelBySuffix(f.models, values.Model); match != "" {
		// Repair hand-typed ids missing the vendor prefix against the catalog.
		values.Model = match
		f.inputs[providerFieldModel].SetValue(match)
	}
	contextText := strings.TrimSpace(f.inputs[providerFieldContext].Value())
	if contextText != "" && contextText != "0" {
		contextSize, err := strconv.Atoi(contextText)
		if err != nil || contextSize < 0 {
			return providerFormValues{}, fmt.Errorf("%s", t(f.loc, "form.contextInvalid"))
		}
		values.Context = contextSize
	}
	return values, nil
}

// setServedModels feeds the live id list from the provider's own /models
// endpoint. Same prefill/normalize contract as setCatalogModels, but with
// authoritative ids — preferred over catalog data on submit.
func (f *providerForm) setServedModels(ids []string) {
	if len(ids) == 0 {
		return
	}
	f.served = ids
	field := &f.inputs[providerFieldModel]
	if field.Value() == "" {
		if picked := pickDefaultServedModel(ids); picked != "" {
			field.SetValue(picked)
		}
		return
	}
	if match := matchServedModel(ids, field.Value()); match != "" {
		field.SetValue(match)
	}
}

// pickDefaultServedModel picks the newest-looking id from a live list. The
// endpoint gives no metadata, so "newest" falls back to heuristics: prefer
// ids whose version tail sorts highest ("glm-5.3" over "glm-4.7").
func pickDefaultServedModel(ids []string) string {
	best := ""
	for _, id := range ids {
		if best == "" || servedModelSortKey(id) > servedModelSortKey(best) {
			best = id
		}
	}
	return best
}

// servedModelSortKey extracts a comparable version tail from a model id:
// digits-and-dots run from the id tail ("hf:zai-org/GLM-5.3-Flash" →
// "5.3"), lower-cased so "5.10" style ids still compare by segments first
// through zero-padding.
func servedModelSortKey(id string) string {
	tail := id
	if i := strings.LastIndexByte(tail, '/'); i >= 0 {
		tail = tail[i+1:]
	}
	var digits []byte
	for i := len(tail) - 1; i >= 0; i-- {
		c := tail[i]
		if c >= '0' && c <= '9' || c == '.' && len(digits) > 0 {
			digits = append([]byte{c}, digits...)
			continue
		}
		if len(digits) > 0 {
			break
		}
	}
	// Zero-pad each segment to 4 digits for numeric comparison.
	digitRun := string(digits)
	if digitRun == "" {
		return ""
	}
	parts := strings.Split(digitRun, ".")
	for i, part := range parts {
		for len(part) < 4 {
			part = "0" + part
		}
		parts[i] = part
	}
	return strings.Join(parts, "")
}

// matchServedModel matches a typed id against the live list: exact, then
// case-insensitive suffix on the tail ("glm-5.3-flash" →
// "hf:zai-org/GLM-5.3-Flash").
func matchServedModel(ids []string, id string) string {
	for _, candidate := range ids {
		if candidate == id {
			return candidate
		}
	}
	tail := modelIDTail(id)
	if tail == "" {
		return ""
	}
	for _, candidate := range ids {
		if strings.EqualFold(modelIDTail(candidate), tail) {
			return candidate
		}
	}
	return ""
}

func (f *providerForm) clearSecret() {
	f.inputs[providerFieldAPIKey].SetValue("")
}

// setCatalogModels feeds the fetched catalog for the form's provider. The
// first call prefills the model field with an auto-picked default (newest
// tool-call model) so connecting does not require typing a model id, and
// normalizes an inherited value to its exact catalog spelling.
func (f *providerForm) setCatalogModels(catalogID string, models []modelSummary) {
	if catalogID != f.catalogID || len(models) == 0 {
		return
	}
	f.models = models
	f.loading = false
	field := &f.inputs[providerFieldModel]
	if field.Value() == "" {
		if picked := pickDefaultModel(models); picked != "" {
			field.SetValue(picked)
		}
		return
	}
	// Normalize what is already in the field: a case-insensitive suffix
	// match repairs hand-typed ids missing the vendor prefix
	// ("glm-5.3-flash" → "hf:zai-org/GLM-5.3-Flash"). An exact catalog id
	// is kept as-is; anything else is left untouched.
	if exact := findModel(models, field.Value()); exact != "" {
		field.SetValue(exact)
		return
	}
	if match := findModelBySuffix(models, field.Value()); match != "" {
		field.SetValue(match)
	}
}

// pickDefaultModel chooses a sensible default from the catalog: the newest
// tool-call-capable text model, so providers whose ids need vendor prefixes
// (e.g. Synthetic's "hf:zai-org/GLM-5.3-Flash") still connect out of the box.
func pickDefaultModel(models []modelSummary) string {
	best := ""
	var bestDate string
	for _, candidate := range models {
		if !candidate.ToolCall {
			continue
		}
		date := candidate.ReleaseDate
		if best == "" || date > bestDate {
			best, bestDate = candidate.ID, date
		}
	}
	if best != "" {
		return best
	}
	// No tool-call filter match: fall back to the newest model overall.
	for _, candidate := range models {
		if best == "" || candidate.ReleaseDate > bestDate {
			best, bestDate = candidate.ID, candidate.ReleaseDate
		}
	}
	return best
}

// findModel returns the exact catalog id matching the given model id (the
// catalog is the spelling authority), or "" when absent.
func findModel(models []modelSummary, id string) string {
	for _, candidate := range models {
		if candidate.ID == id {
			return candidate.ID
		}
	}
	return ""
}

// findModelBySuffix matches case-insensitively on the tail of catalog ids
// after the last "/" and ":" — "GLM-5.3-flash" matches
// "hf:zai-org/GLM-5.3-Flash". Returns the catalog spelling.
func findModelBySuffix(models []modelSummary, id string) string {
	tail := modelIDTail(id)
	if tail == "" {
		return ""
	}
	for _, candidate := range models {
		if strings.EqualFold(modelIDTail(candidate.ID), tail) {
			return candidate.ID
		}
	}
	return ""
}

// modelIDTail returns the last path segment of a model id, keeping any
// hf: style scheme prefix ("hf:zai-org/GLM-5.3-Flash" → "hf:GLM-5.3-Flash").
// Comparing tails keeps scheme and vendor spellings out of the match.
func modelIDTail(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	return id
}

func (f providerForm) view(width int) string {
	var out strings.Builder
	title := t(f.loc, "form.connectTitle", f.template)
	if f.edit {
		title = t(f.loc, "form.editTitle", f.template)
	}
	out.WriteString(headerStyle.Render(title))
	out.WriteString("\n\n")
	meta := t(f.loc, "form.connectMeta")
	if f.edit {
		meta = t(f.loc, "form.editMeta", f.template)
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
		out.WriteString(metaStyle.Render(t(f.loc, "form.saving")))
	} else {
		out.WriteString(metaStyle.Render(t(f.loc, "form.keys")))
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1, 2).
		Width(max(24, min(width-6, 88))).
		Render(out.String())
	return panel
}
