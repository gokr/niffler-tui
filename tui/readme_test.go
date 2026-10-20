package main

import (
	"os"
	"regexp"
	"testing"
)

// readmeCommandRe matches a local-command bullet: "- `/name …` — …" (the
// declared usage follows the name inside the same code span). The full
// reference lives in MANUAL.md; introductory READMEs intentionally show only
// everyday controls. Check the reference against the registry to prevent drift.
var readmeCommandRe = regexp.MustCompile("(?m)^- `/([a-zA-Z?]+)[^`]*`")

// TestReadmeCommandList keeps the manual's command list in step with the
// registry: every canonical built-in documented, with no invented commands.
func TestReadmeCommandList(t *testing.T) {
	var want []string
	for _, cmd := range builtinSlashCommands() {
		if cmd.aliasOf == "" {
			want = append(want, cmd.Name)
		}
	}
	for _, file := range []string{"../MANUAL.md", "../MANUAL.zh.md", "../MANUAL.zh-TW.md"} {
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
