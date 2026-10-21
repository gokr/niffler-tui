package main

// Instance profiles are client-local routing authority, never prompt context or
// store data. A malformed registry refuses explicit remote selection; callers
// may still offer implicit local startup, but must never substitute it for a
// named target. Provisioning receipts import only after catalog verification.
import (
	"encoding/json"
	"fmt"
	"net/url"
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

var instanceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
var instanceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var instanceTargetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]*$`)

// validateProfile is the one route/identity validator. It used to live behind
// an instanceRegistry wrapper because the registry was a FILE; now that routes
// come from the harness, the profile is validated directly and the wrapper is
// gone with the file.
func validateProfile(p instanceProfile) error {
	return validateRouteShape(p, true)
}

// validateRouteShape is validateProfile with the identity pin made optional: a
// route named on the command line has no pin until the harness it reaches
// answers with one.
func validateRouteShape(p instanceProfile, requireIdentity bool) error {
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
	if p.Incus != "" && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(p.Incus) {
		return fmt.Errorf("Incus needs a valid container name")
	}
	if count != 1 {
		return fmt.Errorf("an instance needs exactly one transport")
	}
	if requireIdentity && !instanceIDPattern.MatchString(p.Expect.ID) {
		return fmt.Errorf("instance lacks a pinned identity")
	}
	if p.Expect.Root == "" || !strings.HasPrefix(p.Expect.Root, "/") {
		return fmt.Errorf("instance lacks a remote root")
	}
	if p.Workspace != "" && !strings.HasPrefix(p.Workspace, "/") {
		return fmt.Errorf("instance needs an absolute remote workspace")
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
	return nil
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
