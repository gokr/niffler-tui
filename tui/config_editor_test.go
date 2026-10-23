package main

// The config editor is deliberately schema-driven, so these tests pin the part
// that matters: a declaration's schema becomes editable fields without the
// client knowing the domain. Anything owner-specific in here would be the bug.

import (
	"encoding/json"
	"testing"
)

// A declaration shaped like a real owner's (repomap.gates), reduced to what
// config_get returns.
func testConfigDetail() configDetail {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"autoAppend": map[string]any{
				"type": "boolean", "default": true,
				"x-config": map[string]any{"title": "Append the map automatically",
					"description": "Publish a map to a new conversation's workspace."}},
			"minCensus": map[string]any{
				"type": "integer", "minimum": 1, "maximum": 100000, "default": 50,
				"x-config": map[string]any{"application": "next turn"}},
			"token": map[string]any{
				"type": "string", "x-config": map[string]any{"secret": true}},
			"uptime": map[string]any{
				"type": "integer", "x-config": map[string]any{"readOnly": true}},
		},
	}
	raw, _ := json.Marshal(schema)
	return configDetail{
		OK: true, Domain: "repomap.gates", Rev: 3, Application: "immediate",
		Schema:  raw,
		Values: map[string]json.RawMessage{
			"minCensus": json.RawMessage(`25`),
			"token":     json.RawMessage(`{"set":true}`),
		},
		Defaults: map[string]json.RawMessage{
			"autoAppend": json.RawMessage(`true`),
			"minCensus":  json.RawMessage(`50`),
		},
	}
}

func TestConfigFieldsFollowTheSchema(t *testing.T) {
	fields := configFields(testConfigDetail())
	if len(fields) != 4 {
		t.Fatalf("expected 4 fields, got %d", len(fields))
	}
	byName := map[string]configField{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	if f := byName["autoAppend"]; f.Kind != "boolean" || f.Set || f.ReadOnly {
		t.Fatalf("autoAppend: %+v", f)
	}
	if f := byName["autoAppend"]; f.Title != "Append the map automatically" {
		t.Fatalf("title did not come from x-config: %q", f.Title)
	}
	// An unset boolean with a declared default renders the default, never
	// "true" as if it were stored.
	if got := byName["autoAppend"].rendered(); got != "true (default)" {
		t.Fatalf("default rendering: %q", got)
	}
	// Per-property timing overrides the domain's.
	if f := byName["minCensus"]; f.Application != "next turn" {
		t.Fatalf("per-property application lost: %q", f.Application)
	}
	if f := byName["minCensus"]; f.Kind != "integer" || !f.Set || f.Minimum == nil || *f.Minimum != 1 {
		t.Fatalf("minCensus: %+v", f)
	}
	if got := byName["minCensus"].rendered(); got != "25" {
		t.Fatalf("stored value rendering: %q", got)
	}
	// A secret is never carried, only its set-ness.
	if f := byName["token"]; !f.Secret || !f.Set || f.Value != nil {
		t.Fatalf("secret must be redacted: %+v", f)
	}
	if got := byName["token"].rendered(); got != "•••••• (set)" {
		t.Fatalf("secret rendering: %q", got)
	}
	if byName["uptime"].editable() {
		t.Fatal("read-only field must not be editable")
	}
	if !byName["minCensus"].editable() {
		t.Fatal("a mutable integer must be editable")
	}
}

// A form whose rows reorder between refreshes is unusable, so field order must
// be stable even though the schema arrives as a JSON object.
func TestConfigFieldOrderIsStable(t *testing.T) {
	first := configFields(testConfigDetail())
	for i := 0; i < 20; i++ {
		again := configFields(testConfigDetail())
		for j := range first {
			if first[j].Name != again[j].Name {
				t.Fatalf("order changed: %d %q != %q", j, first[j].Name, again[j].Name)
			}
		}
	}
	names := make([]string, len(first))
	for i, f := range first {
		names[i] = f.Name
	}
	want := []string{"autoAppend", "minCensus", "token", "uptime"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order %v, want %v", names, want)
		}
	}
}

func TestConfigParseValueUsesTheDeclaredKind(t *testing.T) {
	boolean := configField{Name: "autoAppend", Kind: "boolean"}
	if v, err := configParseValue(boolean, "on"); err != nil || v != true {
		t.Fatalf("boolean: %v %v", v, err)
	}
	if _, err := configParseValue(boolean, "maybe"); err == nil {
		t.Fatal("a non-boolean was accepted")
	}
	integer := configField{Name: "minCensus", Kind: "integer"}
	if v, err := configParseValue(integer, "25"); err != nil || v != int64(25) {
		t.Fatalf("integer must stay an integer (a string would be refused by config): %T %v", v, v)
	}
	if _, err := configParseValue(integer, "many"); err == nil {
		t.Fatal("a non-integer was accepted")
	}
	number := configField{Name: "cacheTtlSeconds", Kind: "number"}
	if v, err := configParseValue(number, "300"); err != nil || v != float64(300) {
		t.Fatalf("number: %T %v %v", v, v, err)
	}
	text := configField{Name: "extractor", Kind: "string"}
	if v, _ := configParseValue(text, " off "); v != " off " {
		// Strings are passed through untouched: trimming is the owner's
		// business, and silently rewriting a value is worse than a round trip.
		t.Fatalf("string was altered: %q", v)
	}
}

// A declaration the editor cannot render must not be guessed at.
func TestConfigUnknownKindIsNotEditable(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"mapping": map[string]any{"type": "object"},
		"list":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}}
	raw, _ := json.Marshal(schema)
	fields := configFields(configDetail{OK: true, Schema: raw})
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	for _, f := range fields {
		if f.editable() {
			t.Fatalf("%s (%s) must not be editable by a field editor", f.Name, f.Kind)
		}
	}
}

// A union type ("integer" | "null") must still resolve to a single edit kind.
func TestConfigUnionTypeResolves(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"limit": map[string]any{"type": []any{"null", "integer"}},
	}}
	raw, _ := json.Marshal(schema)
	fields := configFields(configDetail{OK: true, Schema: raw})
	if len(fields) != 1 || fields[0].Kind != "integer" {
		t.Fatalf("union kind: %+v", fields)
	}
}
