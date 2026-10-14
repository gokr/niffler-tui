package main

import (
	"encoding/json"
	"testing"
)

func TestToolsetCanonicalAndProfileAlias(t *testing.T) {
	var canonical, alias *slashCommand
	commands := builtinSlashCommands()
	for i := range commands {
		switch commands[i].Name {
		case "toolset":
			canonical = &commands[i]
		case "profile":
			alias = &commands[i]
		}
	}
	if canonical == nil || canonical.aliasOf != "" || alias == nil || alias.aliasOf != "toolset" {
		t.Fatal("toolset must be canonical and profile its alias")
	}
}

func TestToolsetResponsePrefersCanonicalKey(t *testing.T) {
	for _, raw := range []string{
		`{"toolsets":[{"name":"new"}],"profiles":[{"name":"old"}]}`,
		`{"profiles":[{"name":"new"}]}`,
	} {
		var response profilesResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Profiles) != 1 || response.Profiles[0].Name != "new" {
			t.Fatalf("decoded %#v", response)
		}
	}
	var response profilesResponse
	if err := json.Unmarshal([]byte(`{"toolsets":[],"profiles":[{"name":"old"}]}`), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Profiles) != 0 {
		t.Fatal("explicit empty canonical list must win")
	}
}
