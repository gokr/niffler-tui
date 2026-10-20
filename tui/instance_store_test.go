package main

// The store registry is authoritative for ROUTES; the local file is bootstrap
// plus client-only state. These tests pin the merge rules that decide which
// machine the client actually dials, because getting them wrong is silent: the
// client would look fine and talk to the wrong instance.

import (
	"strings"
	"testing"
)

func reg(name, id, ssh string) instanceProfile {
	return instanceProfile{
		SSH:    ssh,
		Expect: instanceExpectation{ID: id, Name: name, Root: "/srv/niffler"},
	}
}

func registryOf(order []string, profiles map[string]instanceProfile) instanceRegistry {
	return instanceRegistry{Version: 1, Order: order, Instances: profiles}
}

const (
	idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestMergeStoreRouteWinsForSameIdentity(t *testing.T) {
	local := registryOf([]string{"local", "peer"},
		map[string]instanceProfile{"local": {}, "peer": reg("peer", idA, "old-host")})
	stored := registryOf([]string{"peer"}, map[string]instanceProfile{"peer": reg("peer", idA, "new-host")})
	merged := mergeInstanceRegistries(local, stored)
	if got := merged.Instances["peer"].SSH; got != "new-host" {
		t.Fatalf("store route did not win: %q", got)
	}
	if merged.Order[0] != "local" || merged.Order[1] != "peer" {
		t.Fatalf("local order lost: %v", merged.Order)
	}
}

func TestMergeLocalNameCollisionWithDifferentIdentityNeverOverwrites(t *testing.T) {
	local := registryOf([]string{"peer"}, map[string]instanceProfile{"peer": reg("peer", idA, "host-a")})
	stored := registryOf([]string{"peer"}, map[string]instanceProfile{"peer": reg("peer", idB, "host-b")})
	merged := mergeInstanceRegistries(local, stored)
	if got := merged.Instances["peer"].Expect.ID; got != idA {
		t.Fatalf("a local pin was replaced by a different identity: %q", got)
	}
	found := false
	for name, p := range merged.Instances {
		if p.Expect.ID == idB && name != "peer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the second identity was dropped instead of renamed: %v", merged.Instances)
	}
}

func TestMergeAddsUnknownStoredInstance(t *testing.T) {
	local := registryOf([]string{"local"}, map[string]instanceProfile{"local": {}})
	stored := registryOf([]string{"worker"}, map[string]instanceProfile{"worker": reg("worker", idB, "host-b")})
	merged := mergeInstanceRegistries(local, stored)
	if _, ok := merged.Instances["worker"]; !ok {
		t.Fatalf("stored instance not added: %v", merged.Order)
	}
	if len(merged.Order) != 2 {
		t.Fatalf("order = %v", merged.Order)
	}
}

func TestMergeKeepsClientWorkspaceOverride(t *testing.T) {
	local := instanceProfile{SSH: "host", Workspace: "/custom/ws",
		Expect: instanceExpectation{ID: idA, Name: "peer", Root: "/srv/niffler"}}
	stored := reg("peer", idA, "host")
	merged := mergeInstanceRegistries(
		registryOf([]string{"peer"}, map[string]instanceProfile{"peer": local}),
		registryOf([]string{"peer"}, map[string]instanceProfile{"peer": stored}))
	if got := merged.Instances["peer"].Workspace; got != "/custom/ws" {
		t.Fatalf("client-only workspace override lost: %q", got)
	}
}

func TestProfileFromSummaryValidatesEachEntry(t *testing.T) {
	ok := fleetInstanceSummary{InstanceID: idA, Name: "peer", Root: "/srv/niffler",
		Transport: "ssh", Target: "user@host", GitHash: strings.Repeat("b", 40)}
	if _, err := profileFromSummary(ok); err != nil {
		t.Fatalf("valid entry refused: %v", err)
	}
	for name, mutate := range map[string]func(*fleetInstanceSummary){
		"unknown transport": func(e *fleetInstanceSummary) { e.Transport = "telnet" },
		"bad identity":      func(e *fleetInstanceSummary) { e.InstanceID = "short" },
		"ssh injection":     func(e *fleetInstanceSummary) { e.Target = "-oProxyCommand=bad" },
		"guest without root": func(e *fleetInstanceSummary) {
			e.Transport, e.Target = "incus", "worker"
		},
	} {
		entry := ok
		mutate(&entry)
		if _, err := profileFromSummary(entry); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestInstanceNameForAvoidsCollisions(t *testing.T) {
	taken := registryOf([]string{"peer"}, map[string]instanceProfile{"peer": reg("peer", idA, "h")})
	if got := instanceNameFor(fleetInstanceSummary{InstanceID: idB, Name: "peer"}, taken); got != "peer-"+idB[:8] {
		t.Fatalf("collision name = %q", got)
	}
	if got := instanceNameFor(fleetInstanceSummary{InstanceID: idB, Name: "-- bad name"}, taken); !strings.HasPrefix(got, "instance-") {
		t.Fatalf("invalid label not derived from identity: %q", got)
	}
}

func TestDecodeFleetRegistryRejectsSilentEmpty(t *testing.T) {
	if _, err := decodeFleetRegistry([]byte(`"not an object"`)); err == nil {
		t.Fatal("a non-object reply decoded as an empty registry")
	}
	response, err := decodeFleetRegistry([]byte(`{"count":0}`))
	if err != nil || response.Instances == nil {
		t.Fatalf("missing instances must decode to an empty list: %v %v", response, err)
	}
}
