package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Multipass is still refused in the two places a legacy route can arrive:
// a stored profile document (the registry path) and a harness registry entry.
func TestMultipassMigrationRefuses(t *testing.T) {
	for _, field := range []string{`"multipass":"old"`, `"multipass":""`} {
		var p instanceProfile
		if err := json.Unmarshal([]byte(`{`+field+`,"ssh":"worker"}`), &p); err == nil || !strings.Contains(err.Error(), "new verified receipt") {
			t.Fatalf("legacy profile accepted: %+v %v", p, err)
		}
	}
	// A harness registry entry that still says multipass is refused by the
	// transport mapping, so an old route cannot become usable by registering it.
	if _, err := profileFromSummary(fleetInstanceSummary{
		InstanceID: strings.Repeat("a", 32), Name: "worker",
		Root: "/home/ubuntu/niffler", Transport: "multipass", Target: "worker"}); err == nil {
		t.Fatal("legacy registry entry accepted")
	}
}
