package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMultipassMigrationRefuses(t *testing.T) {
	for _, field := range []string{`"multipass":"old"`, `"multipass":""`} {
		var p instanceProfile
		if err := json.Unmarshal([]byte(`{`+field+`,"ssh":"worker"}`), &p); err == nil || !strings.Contains(err.Error(), "new verified receipt") {
			t.Fatalf("legacy registry accepted: %+v %v", p, err)
		}
	}
	p := testInstance()
	data, _ := json.Marshal(map[string]any{"version": 1, "transport": "multipass", "target": "worker", "root": p.Expect.Root, "expect": p.Expect})
	if _, err := profileFromReceipt(data, harnessIdentity{InstanceID: p.Expect.ID}); err == nil || !strings.Contains(err.Error(), "never rename") {
		t.Fatalf("legacy receipt accepted: %v", err)
	}
}
