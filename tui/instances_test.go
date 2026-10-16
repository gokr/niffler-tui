package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testInstance() instanceProfile {
	return instanceProfile{SSH: "ubuntu@worker", Workspace: "/srv/work", Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/home/ubuntu/niffler", Name: "worker"}}
}

func TestInstanceRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "instances.json")
	r, err := loadInstanceRegistry(path)
	if err != nil || len(r.Order) != 1 || r.Order[0] != "local" {
		t.Fatalf("fallback: %+v %v", r, err)
	}
	r.Order = append(r.Order, "worker")
	r.Instances["worker"] = testInstance()
	if err = saveInstanceRegistry(path, r); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("registry not private")
	}
	got, err := loadInstanceRegistry(path)
	if err != nil || got.Instances["worker"].Expect.ID != testInstance().Expect.ID {
		t.Fatalf("round trip: %v", err)
	}
}

func TestInstanceRegistryRefusals(t *testing.T) {
	cases := map[string]func(*instanceRegistry){
		"duplicate":      func(r *instanceRegistry) { r.Order = append(r.Order, "worker") },
		"missing pin":    func(r *instanceRegistry) { p := r.Instances["worker"]; p.Expect.ID = ""; r.Instances["worker"] = p },
		"two transports": func(r *instanceRegistry) { p := r.Instances["worker"]; p.Multipass = "vm"; r.Instances["worker"] = p },
		"relative workspace": func(r *instanceRegistry) {
			p := r.Instances["worker"]
			p.Workspace = "local"
			r.Instances["worker"] = p
		},
		"ssh injection": func(r *instanceRegistry) {
			p := r.Instances["worker"]
			p.SSH = "-oProxyCommand=bad"
			r.Instances["worker"] = p
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := instanceRegistry{Version: 1, Order: []string{"worker"}, Instances: map[string]instanceProfile{"worker": testInstance()}}
			change(&r)
			if r.validate() == nil {
				t.Fatal("accepted invalid registry")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "instances.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadInstanceRegistry(path); err == nil {
		t.Fatal("accepted public registry")
	}
	os.Remove(path)
	os.Symlink("missing", path)
	if _, err := loadInstanceRegistry(path); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestInstanceIdentityAndStateIsolation(t *testing.T) {
	p := testInstance()
	got := harnessIdentity{InstanceID: p.Expect.ID, Root: "/moved", Name: "renamed", GitHash: "new"}
	if err := verifyInstance(p, got); err != nil {
		t.Fatal(err)
	}
	key := instanceStateKey(p)
	p.URL = "nats://127.0.0.1:12345"
	p.Expect.Name = "renamed"
	if key != instanceStateKey(p) {
		t.Fatal("tunnel/label drift changes state")
	}
	got.InstanceID = strings.Repeat("b", 32)
	if verifyInstance(p, got) == nil {
		t.Fatal("accepted replacement instance")
	}
	p.Expect.ID = got.InstanceID
	if key == instanceStateKey(p) {
		t.Fatal("replacement inherits state")
	}
}

func TestImportProvisioningReceiptRequiresLiveIdentity(t *testing.T) {
	p := testInstance()
	data, _ := json.Marshal(map[string]any{"version": 1, "transport": "multipass", "target": "worker", "root": p.Expect.Root, "expect": p.Expect})
	imported, err := profileFromReceipt(data, harnessIdentity{InstanceID: p.Expect.ID})
	if err != nil || imported.Multipass != "worker" || imported.Workspace != p.Expect.Root {
		t.Fatalf("import: %+v %v", imported, err)
	}
	if _, err = profileFromReceipt(data, harnessIdentity{InstanceID: strings.Repeat("b", 32)}); err == nil {
		t.Fatal("imported mismatched receipt")
	}
}
