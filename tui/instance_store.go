package main

// Peers come from the harness's fleet registry (fleet_instances, store kind
// "fleetinstance"): the harness verified those routes and identity pins, and it
// is the only copy that stays current across machines. There is deliberately NO
// client-side instance file — the client always has its local harness, so
// nothing needs remembering before the first connection, and a peer that is not
// registered yet is named on the command line for that one launch.
import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdk "niffler.dev/sdk"
)

// fleetInstanceSummary mirrors fleet_instances (components/fleet/registry.nim).
type fleetInstanceSummary struct {
	InstanceID string  `json:"instanceId"`
	Name       string  `json:"name"`
	Root       string  `json:"root"`
	Transport  string  `json:"transport"`
	Target     string  `json:"target"`
	Host       string  `json:"host"`
	GitHash    string  `json:"gitHash"`
	Source     string  `json:"source"`
	UpdatedAt  float64 `json:"updatedAt"`
	Invalid    bool    `json:"invalid"`
	Error      string  `json:"error"`
}

type fleetRegistryResponse struct {
	Count     int                    `json:"count"`
	Instances []fleetInstanceSummary `json:"instances"`
}

type namedProfile struct {
	name    string
	profile instanceProfile
}

// decodeFleetRegistry keeps a malformed reply from reading as "no peers": an
// empty list must mean the harness really knows none.
func decodeFleetRegistry(raw json.RawMessage) (fleetRegistryResponse, error) {
	var response fleetRegistryResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return response, fmt.Errorf("decode fleet_instances: %w", err)
	}
	if response.Instances == nil {
		response.Instances = []fleetInstanceSummary{}
	}
	return response, nil
}

// fetchRegistry asks the connected harness for its peers. Warnings are
// displayable text: one unusable entry must not hide the rest.
func fetchRegistry(comp *sdk.Component) ([]namedProfile, []string, error) {
	if comp == nil {
		return nil, nil, fmt.Errorf("no harness connection")
	}
	raw, err := comp.Request("fleet", "fleet_instances", map[string]any{}, controlTimeout)
	if err != nil {
		return nil, nil, err
	}
	response, err := decodeFleetRegistry(raw)
	if err != nil {
		return nil, nil, err
	}
	var peers []namedProfile
	var warnings []string
	for _, entry := range response.Instances {
		if entry.Invalid {
			warnings = append(warnings, fmt.Sprintf("registry entry %s is invalid: %s", entry.InstanceID, entry.Error))
			continue
		}
		p, err := profileFromSummary(entry)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("registry entry %s: %v", entry.InstanceID, err))
			continue
		}
		peers = append(peers, namedProfile{name: instanceNameFor(entry, peers), profile: p})
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].name < peers[j].name })
	return peers, warnings, nil
}

// profileFromSummary maps one verified receipt document onto the client's
// profile shape. Client-only state is never in the store; the remote root is
// the default workspace, which is what a receipt import produced before.
func profileFromSummary(entry fleetInstanceSummary) (instanceProfile, error) {
	p := instanceProfile{
		Host:      entry.Host,
		Workspace: entry.Root,
		Expect: instanceExpectation{
			ID: entry.InstanceID, Name: entry.Name,
			Root: entry.Root, GitHash: entry.GitHash,
		},
	}
	switch entry.Transport {
	case "ssh":
		p.SSH = entry.Target
	case "lima":
		p.Lima = entry.Target
	case "incus":
		p.Incus = entry.Target
	case "multipass":
		return p, fmt.Errorf("Multipass routes are no longer supported; provision Lima and register the new instance")
	default:
		return p, fmt.Errorf("unsupported transport %q", entry.Transport)
	}
	// The store validated the receipt; this client is the one that will dial
	// it, so its own route rules decide whether the route is usable here.
	if err := validateProfile(p); err != nil {
		return p, err
	}
	return p, nil
}

// instanceNameFor picks a stable, human-usable name. A receipt label is a
// display name and may be empty, duplicated or carry characters the client
// rejects, so the identity — never the label — resolves collisions.
func instanceNameFor(entry fleetInstanceSummary, taken []namedProfile) string {
	base := strings.TrimSpace(entry.Name)
	if !instanceNamePattern.MatchString(base) {
		base = "instance-" + entry.InstanceID[:8]
	}
	exists := func(candidate string) bool {
		for _, p := range taken {
			if p.name == candidate {
				return true
			}
		}
		return false
	}
	if !exists(base) {
		return base
	}
	name := base + "-" + entry.InstanceID[:8]
	for suffix := 2; exists(name); suffix++ {
		name = fmt.Sprintf("%s-%s-%d", base, entry.InstanceID[:8], suffix)
	}
	return name
}

// instanceFromRegistry resolves a named peer from the LOCAL harness's registry,
// which is why the client never needs its own list: local is always available.
func instanceFromRegistry(name string) (instanceProfile, error) {
	if !instanceNamePattern.MatchString(name) {
		return instanceProfile{}, fmt.Errorf("invalid instance name %q", name)
	}
	comp, err := connectLocalHarness()
	if err != nil {
		return instanceProfile{}, fmt.Errorf("cannot read the local harness's fleet registry: %w", err)
	}
	defer comp.Close()
	peers, _, err := fetchRegistry(comp)
	if err != nil {
		return instanceProfile{}, fmt.Errorf("local harness has no fleet registry (spawn the fleet component): %w", err)
	}
	for _, peer := range peers {
		if peer.name == name || peer.profile.Expect.ID == name {
			return peer.profile, nil
		}
	}
	var known []string
	for _, peer := range peers {
		known = append(known, peer.name)
	}
	return instanceProfile{}, fmt.Errorf("unknown instance %q (registered: %s); name a new route with --ssh/--incus/--lima/--bus",
		name, strings.Join(known, ", "))
}

// connectLocalHarness opens a short-lived bus connection to the local harness,
// used for registry reads outside a running UI (launch-time selection).
func connectLocalHarness() (*sdk.Component, error) {
	url := resolveNATSURL()
	// Never registered as a client: a lookup must not keep an autostarted core
	// alive or count in the departure logic (Client stays false).
	comp := sdk.NewWithURL("tui-lookup-"+newUIID(), componentVersion, url)
	if err := comp.Connect(); err != nil {
		comp.Close()
		return nil, err
	}
	return comp, nil
}

// registrySnapshot is the picker/ring input: peers plus any warnings.
type registrySnapshot struct {
	peers    []namedProfile
	warnings []string
}

// registryLookup is the one seam tests replace: peers come from the harness,
// so a unit test that needs a named peer supplies the snapshot directly rather
// than writing a client-side file that no longer exists.
var registryLookup = loadRegistrySnapshotFromHarness

func loadRegistrySnapshot(comp *sdk.Component) registrySnapshot { return registryLookup(comp) }

func loadRegistrySnapshotFromHarness(comp *sdk.Component) registrySnapshot {
	peers, warnings, err := fetchRegistry(comp)
	if err != nil {
		return registrySnapshot{warnings: []string{
			"harness fleet registry unavailable (" + err.Error() + "); only the local harness is reachable"}}
	}
	return registrySnapshot{peers: peers, warnings: warnings}
}

// peerNames is the ring/picker ordering: local first, then registered peers.
func (r registrySnapshot) peerNames() []string {
	names := []string{}
	for _, peer := range r.peers {
		names = append(names, peer.name)
	}
	return names
}

func (r registrySnapshot) profile(name string) (instanceProfile, bool) {
	for _, peer := range r.peers {
		if peer.name == name {
			return peer.profile, true
		}
	}
	return instanceProfile{}, false
}
