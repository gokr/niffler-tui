package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	sdk "niffler.dev/sdk"
)

const instanceClaimEnv = "NIF_TUI_INSTANCE_CLAIM"

// The reservation is renewed while the current UI tears down, its release is
// retried and the successor starts — seconds, even when no handoff record or
// released lease was going to be needed. A package var so tests can shorten
// the clock instead of sleeping through it.
var instanceClaimRenewEvery = 5 * time.Second

type instanceClaimHandoff struct {
	Scope     string `json:"scope"`
	Workspace string `json:"workspace"`
	UI        string `json:"ui"`
	Session   string `json:"session"`
}

// A separate, explicitly addressed connection avoids changing the SDK's
// process-global bus environment while the current UI is still running.
type instanceClaim struct {
	handoff   instanceClaimHandoff
	transport *instanceTransport
	nc        *nats.Conn
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
}

func instanceUIRequest(nc *nats.Conn, op, ui, session string) error {
	args, _ := json.Marshal(map[string]string{"op": op, "ui": ui, "session": session})
	data, _ := json.Marshal(sdk.Envelope{V: 1, ID: sdk.NewID(), Kind: sdk.KindCall, Tool: "ui", Args: args})
	reply, err := nc.Request("svc.core.call", data, controlTimeout)
	if err != nil {
		return err
	}
	var env sdk.Envelope
	if err = json.Unmarshal(reply.Data, &env); err != nil {
		return err
	}
	if env.Kind == sdk.KindError {
		return fmt.Errorf("target UI registry refused %s: %s", op, env.Error.Message)
	}
	if env.Kind != sdk.KindResult {
		return fmt.Errorf("target UI registry refused %s", op)
	}
	var result struct {
		OK     bool `json:"ok"`
		Number int  `json:"number"`
	}
	if err = json.Unmarshal(env.Args, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("target UI %s refused (owner Niffler %d)", op, result.Number)
	}
	return nil
}

func reserveInstance(ctx context.Context, p instanceProfile) (*instanceClaim, error) {
	tr, _, err := openInstanceTransport(ctx, p)
	if err != nil {
		return nil, err
	}
	workspace := p.Workspace
	if workspace == "" {
		workspace = p.Expect.Root
	}
	return reserveInstanceTransport(tr, instanceStateKey(p), workspace)
}

func reserveLocalInstance(ctx context.Context, url, workspace string) (*instanceClaim, error) {
	return reserveInstanceTransport(&instanceTransport{URL: url}, url, workspace)
}

func reserveInstanceTransport(tr *instanceTransport, scope, workspace string) (*instanceClaim, error) {
	nc, err := nats.Connect(tr.URL, nats.Timeout(10*time.Second), nats.NoReconnect())
	if err != nil {
		// Same wording as the attach path: a target whose harness stopped
		// between the probe and this claim is still a target with no harness.
		notes := tr.diagnoses()
		tr.Close()
		return nil, unreachableError(targetName(tr.label, tr.URL), err, notes)
	}
	session := loadLastSession(scope, workspace)
	if session == "" {
		session = newSessionID()
	}
	c := &instanceClaim{handoff: instanceClaimHandoff{scope, workspace, newUIID(), session}, transport: tr, nc: nc, stop: make(chan struct{}), done: make(chan struct{})}
	// Even an ambiguous timeout may have registered/claimed: release that id.
	if err = instanceUIRequest(nc, "register", c.handoff.UI, ""); err == nil {
		err = instanceUIRequest(nc, "claim", c.handoff.UI, session)
	}
	if err != nil {
		_ = instanceUIRequest(nc, "release", c.handoff.UI, "")
		nc.Close()
		tr.Close()
		return nil, err
	}
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(instanceClaimRenewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				_ = instanceUIRequest(nc, "renew", c.handoff.UI, "")
			}
		}
	}()
	return c, nil
}

func (c *instanceClaim) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		close(c.stop)
		<-c.done
		_ = instanceUIRequest(c.nc, "release", c.handoff.UI, "")
		c.nc.Close()
		c.transport.Close()
	})
}

func instanceClaimEnvironment(env []string, h instanceClaimHandoff) []string {
	out := []string{}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "NIF_SESSION=") && !strings.HasPrefix(entry, instanceClaimEnv+"=") {
			out = append(out, entry)
		}
	}
	data, _ := json.Marshal(h)
	return append(out, instanceClaimEnv+"="+string(data))
}

// Direct exec inheritance, not a shared workspace file: another terminal must
// never consume a switch's identity. Remove it before any later child launch.
func consumeInstanceClaim(scope, workspace string) (instanceClaimHandoff, error) {
	raw := os.Getenv(instanceClaimEnv)
	_ = os.Unsetenv(instanceClaimEnv)
	var h instanceClaimHandoff
	if raw == "" {
		return h, nil
	}
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		return h, err
	}
	if h.Scope != scope || h.Workspace != workspace || h.UI == "" || h.Session == "" || sanitizeSessionID(h.UI) != h.UI || sanitizeSessionID(h.Session) != h.Session {
		return instanceClaimHandoff{}, fmt.Errorf("instance claim handoff does not match target")
	}
	return h, nil
}

func verifyInstanceClaim(url string, h instanceClaimHandoff) error {
	nc, err := nats.Connect(url, nats.Timeout(10*time.Second), nats.NoReconnect())
	if err != nil {
		return err
	}
	defer nc.Close()
	if err = instanceUIRequest(nc, "register", h.UI, ""); err == nil {
		err = instanceUIRequest(nc, "claim", h.UI, h.Session)
	}
	if err != nil {
		_ = instanceUIRequest(nc, "release", h.UI, "")
	}
	return err
}
