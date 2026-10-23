package main

// A schema-driven editor for the harness's configuration surface
// (config_list / config_get / config_patch / config_reset in the niffler
// repo, docs/research/CONFIG-CONTRACT.md).
//
// The rule this file exists to honour: NO per-owner forms. The TUI knows
// nothing about repomap, fetch, logfile, models, lsp or observe. It reads a
// declaration's JSON Schema subset and renders a field per property, so a new
// owner needs zero client changes — the same reason LSP language support is
// registry data rather than code.
//
// Two client-side concerns that belong here rather than in the harness:
//   - the patch is assembled from typed edits and sent with expectRev, so a
//     concurrent change is a visible conflict instead of a lost update;
//   - secrets are never held: a secret field shows "set"/"unset" and only
//     transmits a value the human types.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdk "niffler.dev/sdk"
)

// configDomain mirrors one entry of config_list.
type configDomain struct {
	Domain         string `json:"domain"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Owner          string `json:"owner"`
	OwnerTool      string `json:"ownerTool"`
	Version        int    `json:"version"`
	Scope          string `json:"scope"`
	Application    string `json:"application"`
	Properties     int    `json:"properties"`
	Secrets        int    `json:"secrets"`
	OwnerAvailable bool   `json:"ownerAvailable"`
}

type configListResponse struct {
	OK      bool           `json:"ok"`
	Count   int            `json:"count"`
	Domains []configDomain `json:"domains"`
}

// configField is one editable property, already reduced to what the editor
// needs: nothing in the UI reads the raw schema.
type configField struct {
	Name        string
	Title       string
	Description string
	Kind        string // string | boolean | integer | number | other
	Secret      bool
	ReadOnly    bool
	Application string
	Default     any
	Enum        []any
	Minimum     *float64
	Maximum     *float64
	Value       any  // current effective value (redacted for secrets)
	Set         bool // an explicit value exists (secrets: {"set": true})
}

// configDetail mirrors config_get.
type configDetail struct {
	OK             bool                       `json:"ok"`
	Domain         string                     `json:"domain"`
	Title          string                     `json:"title"`
	Description    string                     `json:"description"`
	Owner          string                     `json:"owner"`
	OwnerAvailable bool                       `json:"ownerAvailable"`
	Rev            int                        `json:"rev"`
	Values         map[string]json.RawMessage `json:"values"`
	Defaults       map[string]json.RawMessage `json:"defaults"`
	Schema         json.RawMessage            `json:"schema"`
	Application    string                     `json:"application"`
	Code           string                     `json:"code"`
	Error          string                     `json:"error"`
}

// configSchema is the subset config enforces. Declarations using anything else
// are refused by config at declare time, so the editor will not meet them.
type configSchema struct {
	Type       any `json:"type"`
	Properties map[string]struct {
		Type    any               `json:"type"`
		Enum    []any             `json:"enum"`
		Minimum *float64          `json:"minimum"`
		Maximum *float64          `json:"maximum"`
		Default any               `json:"default"`
		XConfig *configXConfig    `json:"x-config"`
	} `json:"properties"`
}

type configXConfig struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	ReadOnly    bool   `json:"readOnly"`
	Application string `json:"application"`
}

// configFields turns a config_get reply into editor fields, in declaration
// order (the schema's own key order is lost in a map, so names are sorted for
// stability — a form whose rows jump between refreshes is unusable).
func configFields(detail configDetail) []configField {
	var schema configSchema
	if len(detail.Schema) > 0 {
		_ = json.Unmarshal(detail.Schema, &schema)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]configField, 0, len(names))
	for _, name := range names {
		prop := schema.Properties[name]
		f := configField{Name: name, Title: name, Default: prop.Default,
			Enum: prop.Enum, Minimum: prop.Minimum, Maximum: prop.Maximum,
			Application: detail.Application, Kind: configKind(prop.Type)}
		if prop.XConfig != nil {
			if prop.XConfig.Title != "" {
				f.Title = prop.XConfig.Title
			}
			f.Description = prop.XConfig.Description
			f.Secret = prop.XConfig.Secret
			f.ReadOnly = prop.XConfig.ReadOnly
			if prop.XConfig.Application != "" {
				f.Application = prop.XConfig.Application
			}
		}
		raw, ok := detail.Values[name]
		if ok && f.Secret {
			// A secret never carries its value: config reports {"set": bool}.
			f.Set = secretIsSet(raw)
		} else if ok {
			f.Value = decodeAny(raw)
			f.Set = true
		}
		fields = append(fields, f)
	}
	return fields
}

// configKind reduces a JSON Schema "type" (which may be a list) to the single
// editor kind the field needs. "other" means the editor shows the value
// read-only rather than guessing at a widget.
func configKind(t any) string {
	name, ok := t.(string)
	if !ok {
		if list, isList := t.([]any); isList {
			for _, item := range list {
				if s, isStr := item.(string); isStr && s != "null" {
					name = s
					break
				}
			}
		}
	}
	switch name {
	case "string", "boolean", "integer", "number":
		return name
	default:
		return "other"
	}
}

func decodeAny(raw json.RawMessage) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func secretIsSet(raw json.RawMessage) bool {
	var probe struct {
		Set bool `json:"set"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Set
}

// configRendered is one line of the editor as the human sees it.
func (f configField) rendered() string {
	if f.Secret {
		if f.Set {
			return "•••••• (set)"
		}
		return "(unset)"
	}
	if !f.Set {
		if f.Default != nil {
			return fmt.Sprintf("%v (default)", f.Default)
		}
		return "(unset)"
	}
	return fmt.Sprintf("%v", f.Value)
}

// editable reports whether the human may change this field.
func (f configField) editable() bool {
	return !f.ReadOnly && f.Kind != "other"
}

// configParseValue converts typed input to the JSON value for its kind, so the
// harness sees an integer where the schema says integer (a string would be
// refused by config's validation, correctly).
func configParseValue(f configField, input string) (any, error) {
	switch f.Kind {
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		}
		return nil, fmt.Errorf("%s is a boolean: type true or false", f.Name)
	case "integer":
		var n int64
		if _, err := fmt.Sscanf(strings.TrimSpace(input), "%d", &n); err != nil {
			return nil, fmt.Errorf("%s is an integer", f.Name)
		}
		return n, nil
	case "number":
		var n float64
		if _, err := fmt.Sscanf(strings.TrimSpace(input), "%g", &n); err != nil {
			return nil, fmt.Errorf("%s is a number", f.Name)
		}
		return n, nil
	}
	return input, nil
}

// configPatchResult is what config_patch/config_reset reply.
type configPatchResult struct {
	OK     bool   `json:"ok"`
	Domain string `json:"domain"`
	Rev    int    `json:"rev"`
	Code   string `json:"code"`
	Error  string `json:"error"`
}

// configApply sends one validated patch. expectRev pins the revision the human
// was looking at: a stale form is a conflict, never a silent overwrite.
func configApply(comp *sdk.Component, domain string, patch map[string]any,
	expectRev int) (configPatchResult, error) {
	var out configPatchResult
	args := map[string]any{"domain": domain, "patch": patch}
	if expectRev >= 0 {
		args["expectRev"] = expectRev
	}
	if err := requestInto(comp, "config", "config_patch", args, &out); err != nil {
		return out, err
	}
	if !out.OK {
		return out, fmt.Errorf("%s", configPatchMessage(out))
	}
	return out, nil
}

// configReset drops explicit values so declared defaults apply again.
func configReset(comp *sdk.Component, domain string,
	properties []string, expectRev int) (configPatchResult, error) {
	var out configPatchResult
	args := map[string]any{"domain": domain}
	if len(properties) > 0 {
		args["properties"] = properties
	}
	if expectRev >= 0 {
		args["expectRev"] = expectRev
	}
	if err := requestInto(comp, "config", "config_reset", args, &out); err != nil {
		return out, err
	}
	if !out.OK {
		return out, fmt.Errorf("%s", configPatchMessage(out))
	}
	return out, nil
}

// configPatchMessage prefers the harness's own explanation: "refused by the
// owner" and "revision conflict" are different situations for the human.
func configPatchMessage(r configPatchResult) string {
	if r.Code == "" {
		return "the change was refused"
	}
	return r.Code + ": " + r.Error
}

// configDomains is the list a picker shows, with the owner's reachability
// spelled out — a durable declaration whose owner is stopped is listable but
// not editable, and the UI must say so rather than fail on submit.
func configDomains(comp *sdk.Component) ([]configDomain, error) {
	var out configListResponse
	if err := requestInto(comp, "config", "config_list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("config_list refused")
	}
	return out.Domains, nil
}

func configDetailFor(comp *sdk.Component, domain string) (configDetail, error) {
	var out configDetail
	if err := requestInto(comp, "config", "config_get",
		map[string]any{"domain": domain}, &out); err != nil {
		return out, err
	}
	if !out.OK {
		msg := out.Error
		if out.Code != "" {
			msg = out.Code + ": " + msg
		}
		if msg == "" {
			msg = "config_get refused"
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}
