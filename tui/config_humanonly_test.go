package main

// Two things the config editor owes a human, and nothing else:
//
//   - a humanOnly property is announced as such (it is a decision the harness
//     refuses agents, so presenting it as a routine toggle would be a lie of
//     omission), and
//   - a non-immediate application timing is stated after a save, because a
//     human who reads "saved" and then sees the OLD behaviour assumes the edit
//     failed.
//
// Both are pure functions of the harness's reply, so they are tested without a
// bus.

import (
	"encoding/json"
	"strings"
	"testing"
)

// humanOnlyDetail is a declaration with one humanOnly gate plus an ordinary
// setting, shaped like config_get returns it.
func humanOnlyDetail() configDetail {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"paidCalls": map[string]any{
				"type": "boolean", "default": false,
				"x-config": map[string]any{
					"title": "Allow paid API calls", "humanOnly": true,
					"application": "next turn"}},
			"retries": map[string]any{
				"type": "integer", "default": 3},
		},
	}
	raw, _ := json.Marshal(schema)
	return configDetail{OK: true, Domain: "demo.main", Rev: 1,
		Application: "immediate", Schema: raw}
}

func TestConfigHumanOnlyIsParsed(tt *testing.T) {
	fields := configFields(humanOnlyDetail())
	byName := map[string]configField{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	if !byName["paidCalls"].HumanOnly {
		tt.Fatal("paidCalls should carry humanOnly from x-config")
	}
	if byName["retries"].HumanOnly {
		tt.Fatal("retries is an ordinary setting and must not be marked humanOnly")
	}
}

// A humanOnly property is still editable HERE: the restriction is on a model,
// not on the human reading the form. Marking it read-only would be the wrong
// half of the rule.
func TestConfigHumanOnlyIsStillEditableByTheHuman(tt *testing.T) {
	for _, f := range configFields(humanOnlyDetail()) {
		if f.Name != "paidCalls" {
			continue
		}
		if !f.editable() {
			tt.Fatal("humanOnly must not imply read-only: a human may change it")
		}
		return
	}
	tt.Fatal("paidCalls field missing")
}

func TestConfigFieldListAnnouncesHumanOnly(tt *testing.T) {
	items := configFieldItems(LocaleEN, configFields(humanOnlyDetail()))
	var paid, retries string
	for _, it := range items {
		row := it.(configFieldItem)
		switch row.field.Name {
		case "paidCalls":
			paid = row.description
		case "retries":
			retries = row.description
		}
	}
	if !strings.Contains(paid, t(LocaleEN, "config.humanOnly")) {
		tt.Fatalf("humanOnly marker missing from the list row: %q", paid)
	}
	if strings.Contains(retries, t(LocaleEN, "config.humanOnly")) {
		tt.Fatalf("an ordinary setting was marked humanOnly: %q", retries)
	}
}

// configApplyNote must stay SILENT for immediate changes. A note that always
// carries a timing trains the human to ignore it, and then it fails to warn
// them on the turn it matters.
func TestConfigApplyNoteStaysSilentForImmediate(tt *testing.T) {
	if got := configApplyNote(map[string]string{"retries": "immediate"}); got != "" {
		tt.Fatalf("immediate change produced a note: %q", got)
	}
	if got := configApplyNote(nil); got != "" {
		tt.Fatalf("empty applies produced a note: %q", got)
	}
}

func TestConfigApplyNoteNamesNonImmediateTimings(tt *testing.T) {
	got := configApplyNote(map[string]string{
		"paidCalls": "next turn",
		"ring":      "component restart",
		"retries":   "immediate",
	})
	if !strings.Contains(got, "next turn") || !strings.Contains(got, "component restart") {
		tt.Fatalf("note %q should name both non-immediate timings", got)
	}
	if strings.Contains(got, "immediate") {
		tt.Fatalf("note %q should not mention the immediate property", got)
	}
	// Deduplicated: several properties sharing a timing is one phrase.
	dedup := configApplyNote(map[string]string{
		"a": "next turn", "b": "next turn"})
	if strings.Count(dedup, "next turn") != 1 {
		tt.Fatalf("note %q repeated a timing", dedup)
	}
}

func TestConfigSavedNoteUsesTheTiming(tt *testing.T) {
	// Mirrors the message handler: the saved note gains the timing phrase only
	// when there is one, and keeps the property label either way.
	note := t(LocaleEN, "config.saved")
	if when := configApplyNote(map[string]string{"paidCalls": "next turn"}); when != "" {
		note = t(LocaleEN, "config.savedWhen", note, when)
	}
	note += " — Allow paid API calls"
	if !strings.Contains(note, "next turn") {
		tt.Fatalf("saved note lost the timing: %q", note)
	}
	if !strings.Contains(note, "Allow paid API calls") {
		tt.Fatalf("saved note lost the property label: %q", note)
	}
}

// Every key the editor now uses must exist in all three catalogs, or a human
// switching to zh/zh-TW silently sees an English fallback mid-sentence.
func TestConfigHumanOnlyKeysExistInEveryLocale(tt *testing.T) {
	for _, key := range []string{"config.humanOnly", "config.humanOnlyNote",
		"config.savedWhen"} {
		for loc := range catalogs {
			if _, ok := catalogs[loc][key]; !ok {
				tt.Fatalf("locale %s is missing %s", loc, key)
			}
		}
	}
}
