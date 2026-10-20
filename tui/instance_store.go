package main

// The harness's fleet registry (fleet component, store kind "fleetinstance")
// is the authoritative source for peer ROUTES and identity pins. The local
// ~/.niffler/instances.json file remains a BOOTSTRAP input: the route needed
// to reach the first harness, plus any client-only entry (a direct URL, a
// per-connection workspace override) that no harness could know about.
//
// Merge rule: identity is the key. A stored entry matching a local entry's
// pinned identity takes over the route fields (the harness verified them);
// the local name and client-only fields survive. A stored entry with no local
// counterpart is added under a derived name, and a stored entry that cannot be
// validated is skipped with a warning rather than breaking the picker — one
// bad registry document must not make every instance unreachable.
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

// storeInstanceRegistry reads the harness's fleet registry. A missing fleet
// component (not spawned) is reported, never silently treated as "no peers".
func storeInstanceRegistry(comp *sdk.Component) (instanceRegistry, []string, error) {
	empty := instanceRegistry{Version: 1, Order: []string{}, Instances: map[string]instanceProfile{}}
	if comp == nil {
		return empty, nil, fmt.Errorf("no harness connection")
	}
	raw, err := comp.Request("fleet", "fleet_instances", map[string]any{}, controlTimeout)
	if err != nil {
		return empty, nil, err
	}
	response, err := decodeFleetRegistry(raw)
	if err != nil {
		return empty, nil, err
	}
	registry := instanceRegistry{Version: 1, Order: []string{}, Instances: map[string]instanceProfile{}}
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
		name := instanceNameFor(entry, registry)
		registry.Order = append(registry.Order, name)
		registry.Instances[name] = p
	}
	return registry, warnings, nil
}

// profileFromSummary maps one verified receipt document onto the client's
// profile shape. The store never carries client-only state (workspace
// overrides stay local); the remote root becomes the default workspace, which
// is what a receipt import produced before.
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
	default:
		return p, fmt.Errorf("unsupported transport %q", entry.Transport)
	}
	// Per-entry validation: the store validated the receipt, but this client
	// is the one that will dial it, so its own rules decide whether the route
	// is usable here.
	one := instanceRegistry{Version: 1, Order: []string{"peer"},
		Instances: map[string]instanceProfile{"peer": p}}
	if err := one.validate(); err != nil {
		return p, err
	}
	return p, nil
}

// instanceNameFor picks a stable, human-usable name for a stored instance. The
// receipt's label is a display name and may be empty, duplicated or carry
// characters the client rejects, so the identity is the fallback and the
// suffix, never the name, decides collisions.
func instanceNameFor(entry fleetInstanceSummary, taken instanceRegistry) string {
	base := strings.TrimSpace(entry.Name)
	if !instanceNamePattern.MatchString(base) {
		base = "instance-" + entry.InstanceID[:8]
	}
	if _, exists := taken.Instances[base]; !exists {
		return base
	}
	name := base + "-" + entry.InstanceID[:8]
	for suffix := 2; ; suffix++ {
		if _, exists := taken.Instances[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s-%s-%d", base, entry.InstanceID[:8], suffix)
	}
}

// mergeInstanceRegistries overlays the store registry onto the bootstrap one.
// Store routes win for a matching identity; local names and client-only fields
// are preserved; absent identities are added. Entries are ordered
// deterministically (local order first, then stored instances by name) so the
// ring and the picker do not reshuffle between launches.
func mergeInstanceRegistries(local, stored instanceRegistry) instanceRegistry {
	merged := instanceRegistry{Version: 1, Order: []string{}, Instances: map[string]instanceProfile{}}
	for _, name := range local.Order {
		merged.Order = append(merged.Order, name)
		merged.Instances[name] = local.Instances[name]
	}
	byIdentity := map[string]string{}
	for name, p := range merged.Instances {
		if instanceIDPattern.MatchString(p.Expect.ID) {
			byIdentity[p.Expect.ID] = name
		}
	}
	var added []string
	for _, name := range stored.Order {
		storedProfile := stored.Instances[name]
		target, known := byIdentity[storedProfile.Expect.ID]
		if !known {
			// A name collision with a DIFFERENT identity must not overwrite a
			// local pin: that is how a client silently starts talking to the
			// wrong machine under a familiar name.
			if existing, exists := merged.Instances[name]; exists && existing.Expect.ID != storedProfile.Expect.ID {
				name = name + "-" + storedProfile.Expect.ID[:8]
			}
			if _, exists := merged.Instances[name]; exists {
				continue
			}
			merged.Order = append(merged.Order, name)
			merged.Instances[name] = storedProfile
			byIdentity[storedProfile.Expect.ID] = name
			added = append(added, name)
			continue
		}
		localProfile := merged.Instances[target]
		// The harness verified this route and pin; keep them. Client-only
		// fields (an explicit workspace override) survive the overlay.
		route := storedProfile
		if localProfile.Workspace != "" && localProfile.Workspace != localProfile.Expect.Root {
			route.Workspace = localProfile.Workspace
		}
		merged.Instances[target] = route
	}
	sort.Strings(added) // deterministic ring order for newly discovered peers
	return merged
}

// loadInstanceRegistryMerged is the accessor every UI path uses: the bootstrap
// file, overlaid with the harness's registry when a connection exists. The
// warning is displayable text, not an error: a reachable harness with no fleet
// component still lets the client use its own bootstrap entries.
func loadInstanceRegistryMerged(comp *sdk.Component) (instanceRegistry, string, error) {
	path, err := instanceRegistryPath()
	if err != nil {
		return instanceRegistry{}, "", err
	}
	local, err := loadInstanceRegistry(path)
	if err != nil {
		return instanceRegistry{}, "", err
	}
	stored, warnings, err := storeInstanceRegistry(comp)
	if err != nil {
		return local, "harness fleet registry unavailable (" + err.Error() + "); using local bootstrap instances only", nil
	}
	merged := mergeInstanceRegistries(local, stored)
	if len(warnings) > 0 {
		return merged, strings.Join(warnings, "; "), nil
	}
	return merged, "", nil
}

// Decode helper kept next to the shape it mirrors: a registry reply that is
// not an object would otherwise decode into an empty list and read as "this
// harness knows no peers".
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
