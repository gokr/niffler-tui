package main

import (
	"os"
	"regexp"
	"testing"
)

// readmeCommandRe matches a local-command bullet: "- `/name …` — …" (the
// declared usage follows the name inside the same code span). The READMEs
// document the commands as a hand-written list in three languages, which is
// exactly the list that had fallen behind (/mcp, /components, /discover,
// /profile were all missing), so it is checked against the registry.
var readmeCommandRe = regexp.MustCompile("(?m)^- `/([a-zA-Z?]+)[^`]*`")

// TestReadmeCommandList keeps the three README command lists in step with the
// registry: every canonical built-in documented, and nothing documented that
// is not a command.
func TestReadmeCommandList(t *testing.T) {
	var want []string
	for _, cmd := range builtinSlashCommands() {
		if cmd.aliasOf == "" {
			want = append(want, cmd.Name)
		}
	}
	for _, file := range []string{"../README.md", "../README.zh.md", "../README.zh-TW.md"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		documented := map[string]bool{}
		for _, match := range readmeCommandRe.FindAllStringSubmatch(string(raw), -1) {
			documented[match[1]] = true
		}
		if len(documented) == 0 {
			t.Fatalf("%s: no local-command bullets found — did the list move?", file)
		}
		for _, cmd := range want {
			if !documented[cmd] {
				t.Errorf("%s does not document /%s", file, cmd)
			}
		}
		for cmd := range documented {
			if !containsStr(want, cmd) {
				t.Errorf("%s documents /%s, which is not a canonical command", file, cmd)
			}
		}
	}
}
