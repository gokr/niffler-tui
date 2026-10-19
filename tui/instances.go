package main

// Instance profiles are client-local routing authority, never prompt context or
// store data. A malformed registry refuses explicit remote selection; callers
// may still offer implicit local startup, but must never substitute it for a
// named target. Provisioning receipts import only after catalog verification.
import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type instanceExpectation struct {
	ID      string `json:"instanceId"`
	Name    string `json:"name"`
	Root    string `json:"root"`
	GitHash string `json:"gitHash,omitempty"`
}

type instanceProfile struct {
	URL       string              `json:"url,omitempty"`
	SSH       string              `json:"ssh,omitempty"`
	Lima      string              `json:"lima,omitempty"`
	Incus     string              `json:"incus,omitempty"`
	Host      string              `json:"host,omitempty"`
	Workspace string              `json:"workspace,omitempty"`
	Expect    instanceExpectation `json:"expect"`
}

// UnmarshalJSON rejects legacy routing authority even when another route exists.
func (p *instanceProfile) UnmarshalJSON(data []byte) error {
	type alias instanceProfile
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, exists := fields["multipass"]; exists {
		return fmt.Errorf("Multipass profiles are no longer supported; provision Lima and import a new verified receipt; do not rename the field")
	}
	return json.Unmarshal(data, (*alias)(p))
}

type instanceRegistry struct {
	Version   int                        `json:"version"`
	Order     []string                   `json:"order"`
	Instances map[string]instanceProfile `json:"instances"`
}

var instanceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
var instanceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var instanceTargetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]*$`)

func instanceRegistryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "niffler", "instances.json"), nil
}

func (r instanceRegistry) validate() error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported instance registry version %d", r.Version)
	}
	if len(r.Order) != len(r.Instances) {
		return fmt.Errorf("instance order must name every profile once")
	}
	seen := map[string]bool{}
	for _, name := range r.Order {
		p, exists := r.Instances[name]
		if !instanceNamePattern.MatchString(name) || !exists || seen[name] {
			return fmt.Errorf("invalid or duplicate instance %q", name)
		}
		seen[name] = true
		count := 0
		for _, target := range []string{p.URL, p.SSH, p.Lima, p.Incus} {
			if target != "" {
				count++
			}
		}
		if p.Host != "" && (!instanceTargetPattern.MatchString(p.Host) || (p.Lima == "" && p.Incus == "")) {
			return fmt.Errorf("host requires a guest transport and valid SSH target")
		}
		if (p.Incus != "" || p.Lima != "") && p.Expect.Root != "/home/ubuntu/niffler" {
			return fmt.Errorf("guest root must be /home/ubuntu/niffler")
		}
		if p.Incus != "" && (!regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(p.Incus) || p.Expect.Root != "/home/ubuntu/niffler") {
			return fmt.Errorf("Incus needs a valid container name and /home/ubuntu/niffler root")
		}
		if name == "local" && count == 0 {
			continue
		}
		if count != 1 {
			return fmt.Errorf("instance %s needs exactly one transport", name)
		}
		if !instanceIDPattern.MatchString(p.Expect.ID) {
			return fmt.Errorf("instance %s lacks a pinned identity", name)
		}
		if p.Expect.Root == "" || !strings.HasPrefix(p.Expect.Root, "/") {
			return fmt.Errorf("instance %s lacks a remote root", name)
		}
		if p.Workspace != "" && !strings.HasPrefix(p.Workspace, "/") {
			return fmt.Errorf("instance %s needs an absolute remote workspace", name)
		}
		if p.SSH != "" && !instanceTargetPattern.MatchString(p.SSH) {
			return fmt.Errorf("invalid SSH target")
		}
		if p.Lima != "" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(p.Lima) {
			return fmt.Errorf("invalid Lima target")
		}
		if p.URL != "" {
			u, err := url.Parse(p.URL)
			if err != nil {
				return err
			}
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 || u.Scheme != "nats" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("direct bus must be a loopback host:port; use SSH for remote transport")
			}
		}
	}
	return nil
}

func loadInstanceRegistry(path string) (instanceRegistry, error) {
	fallback := instanceRegistry{Version: 1, Order: []string{"local"}, Instances: map[string]instanceProfile{"local": {}}}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fallback, fmt.Errorf("instance registry must be a private regular file (0600)")
	}
	if info.Size() > 1<<20 {
		return fallback, fmt.Errorf("instance registry too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback, err
	}
	var r instanceRegistry
	if err = json.Unmarshal(data, &r); err != nil {
		return fallback, err
	}
	if err = r.validate(); err != nil {
		return fallback, err
	}
	return r, nil
}

func saveInstanceRegistry(path string, r instanceRegistry) error {
	if err := r.validate(); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular registry")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(path), ".instances-")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	if _, err = staged.Write(append(data, '\n')); err != nil {
		staged.Close()
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	return os.Rename(staged.Name(), path)
}

func verifyInstance(p instanceProfile, got harnessIdentity) error {
	if !instanceIDPattern.MatchString(p.Expect.ID) || got.InstanceID != p.Expect.ID {
		return fmt.Errorf("instance identity mismatch: expected %s, got %s; refusing attachment", p.Expect.ID, got.InstanceID)
	}
	// Root, label and revision may drift while durable identity stays fixed.
	return nil
}

// instanceStateKey is independent of tunnel port, mutable label and revision.
// A replaced instance never inherits a prior instance's conversations/drafts.
func instanceStateKey(p instanceProfile) string { return "instance:" + p.Expect.ID }

// profileFromReceipt validates transport and identity against a live read-only
// catalog probe before the caller can persist it in the registry.
func profileFromReceipt(data []byte, got harnessIdentity) (instanceProfile, error) {
	var receipt struct {
		Version   int                 `json:"version"`
		Transport string              `json:"transport"`
		Host      string              `json:"host,omitempty"`
		Target    string              `json:"target"`
		Root      string              `json:"root"`
		Expect    instanceExpectation `json:"expect"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return instanceProfile{}, err
	}
	p := instanceProfile{Workspace: receipt.Root, Expect: receipt.Expect, Host: receipt.Host}
	switch receipt.Transport {
	case "ssh":
		p.SSH = receipt.Target
	case "multipass":
		return p, fmt.Errorf("Multipass receipts are no longer supported; provision Lima and import a new verified receipt; never rename the transport")
	case "lima":
		p.Lima = receipt.Target
	case "incus":
		p.Incus = receipt.Target
	default:
		return p, fmt.Errorf("unknown receipt transport")
	}
	if receipt.Version != 1 || receipt.Root != receipt.Expect.Root {
		return p, fmt.Errorf("invalid provisioning receipt")
	}
	r := instanceRegistry{Version: 1, Order: []string{"remote"}, Instances: map[string]instanceProfile{"remote": p}}
	if err := r.validate(); err != nil {
		return p, err
	}
	if err := verifyInstance(p, got); err != nil {
		return p, err
	}
	return p, nil
}
