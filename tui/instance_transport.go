package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	sdk "niffler.dev/sdk"
)

// instanceTransport owns only client-side sockets and forwarding children. It
// never starts/stops the peer. A dead relay closes this endpoint rather than
// allowing discovery or reconnect to choose another instance.
type instanceTransport struct {
	URL      string
	listener net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
}

func (t *instanceTransport) Close() {
	t.once.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.listener != nil {
			_ = t.listener.Close()
		}
		t.wg.Wait()
	})
}

var remoteRootPattern = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var loopbackEndpointPattern = regexp.MustCompile(`^nats://127\.0\.0\.1:[0-9]+$`)

// guestCommand preserves binary stdio locally and quotes every argv word when
// crossing SSH's remote-shell boundary. Host credentials never enter the guest.
func instanceGuestCommand(ctx context.Context, p instanceProfile, argv []string) (*exec.Cmd, error) {
	registry := instanceRegistry{Version: 1, Order: []string{"guest"}, Instances: map[string]instanceProfile{"guest": p}}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	var args []string
	if p.Incus != "" {
		args = []string{"incus", "--force-local", "--project", "default", "exec", p.Incus, "--mode=non-interactive", "--", "runuser", "-u", "ubuntu", "--", "env", "HOME=/home/ubuntu", "USER=ubuntu", "LOGNAME=ubuntu"}
	} else if p.Multipass != "" {
		args = []string{"multipass", "exec", p.Multipass, "--"}
	} else {
		return nil, fmt.Errorf("no guest transport")
	}
	args = append(args, argv...)
	if p.Host == "" {
		return exec.CommandContext(ctx, args[0], args[1:]...), nil
	}
	words := make([]string, len(args))
	for i, arg := range args {
		words[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "--", p.Host, strings.Join(words, " ")), nil
}

func instanceRemoteCommand(ctx context.Context, p instanceProfile, script string) (*exec.Cmd, error) {
	registry := instanceRegistry{Version: 1, Order: []string{"peer"}, Instances: map[string]instanceProfile{"peer": p}}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	if p.SSH != "" {
		if !instanceTargetPattern.MatchString(p.SSH) {
			return nil, fmt.Errorf("invalid SSH target")
		}
		cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=15", "--", p.SSH, "bash -s")
		cmd.Stdin = strings.NewReader(script)
		return cmd, nil
	}
	if p.Multipass != "" || p.Incus != "" {
		cmd, err := instanceGuestCommand(ctx, p, []string{"bash", "-s"})
		if err != nil {
			return nil, err
		}
		cmd.Stdin = strings.NewReader(script)
		return cmd, nil
	}
	return nil, fmt.Errorf("profile has no remote transport")
}

func resolveInstanceEndpoint(ctx context.Context, p instanceProfile) (string, error) {
	if !remoteRootPattern.MatchString(p.Expect.Root) {
		return "", fmt.Errorf("invalid remote root")
	}
	cmd, err := instanceRemoteCommand(ctx, p, "set -eu\nread -r endpoint < '"+p.Expect.Root+"/var/nats-url'\nprintf '%s' \"$endpoint\"\n")
	if err != nil {
		return "", err
	}
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("remote endpoint unavailable: %w", err)
	}
	endpoint := strings.TrimSpace(string(data))
	if !loopbackEndpointPattern.MatchString(endpoint) {
		return "", fmt.Errorf("peer did not report a loopback endpoint")
	}
	if _, _, err = net.SplitHostPort(strings.TrimPrefix(endpoint, "nats://")); err != nil {
		return "", err
	}
	return endpoint, nil
}

// Multipass has no port-forward command. Use the guest's native CLI relay,
// built for its own architecture, with no key extraction, mounts or listener.
// An outdated/missing CLI fails closed; never fall back to another transport.
func instanceRelayCommand(ctx context.Context, p instanceProfile, endpoint string) (*exec.Cmd, error) {
	registry := instanceRegistry{Version: 1, Order: []string{"peer"}, Instances: map[string]instanceProfile{"peer": p}}
	if err := registry.validate(); err != nil {
		return nil, err
	}
	if !loopbackEndpointPattern.MatchString(endpoint) {
		return nil, fmt.Errorf("invalid relay endpoint")
	}
	_, port, err := net.SplitHostPort(strings.TrimPrefix(endpoint, "nats://"))
	if err != nil {
		return nil, err
	}
	if p.SSH != "" {
		if !instanceTargetPattern.MatchString(p.SSH) {
			return nil, fmt.Errorf("invalid SSH target")
		}
		return exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-W", "127.0.0.1:"+port, "--", p.SSH), nil
	}
	if p.Multipass != "" || p.Incus != "" {
		if !remoteRootPattern.MatchString(p.Expect.Root) {
			return nil, fmt.Errorf("invalid remote root")
		}
		return instanceGuestCommand(ctx, p, []string{p.Expect.Root + "/var/bin/cli", "relay", port})
	}
	return nil, fmt.Errorf("no relay transport")
}

func relayInstanceSocket(ctx context.Context, conn net.Conn, cmd *exec.Cmd) {
	defer conn.Close()
	input, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(input, conn); input.Close(); close(copied) }()
	_, _ = io.Copy(conn, output)
	conn.Close()
	<-copied
	_ = cmd.Wait()
	close(done)
}

func probeInstanceURL(url string) (harnessIdentity, error) {
	var identity harnessIdentity
	nc, err := nats.Connect(url, nats.Timeout(10*time.Second), nats.NoReconnect())
	if err != nil {
		return identity, err
	}
	defer nc.Close()
	data, err := json.Marshal(sdk.Envelope{V: 1, ID: sdk.NewID(), Kind: sdk.KindCall, Tool: "catalog", Args: json.RawMessage(`{"op":"list"}`)})
	if err != nil {
		return identity, err
	}
	reply, err := nc.Request("svc.core.call", data, 10*time.Second)
	if err != nil {
		return identity, err
	}
	var result struct {
		Kind string          `json:"kind"`
		Args json.RawMessage `json:"args"`
	}
	if err = json.Unmarshal(reply.Data, &result); err != nil {
		return identity, err
	}
	if result.Kind != "result" {
		return identity, fmt.Errorf("catalog probe refused")
	}
	err = json.Unmarshal(result.Args, &identity)
	return identity, err
}

// checkedInstanceRelay verifies each new forwarding lane before exposing it to
// the SDK. The probe uses a separate connection: this is routing protection,
// not authentication against a malicious peer swapping endpoints mid-handshake.
func checkedInstanceRelay(ctx context.Context, conn net.Conn, p instanceProfile) {
	defer conn.Close()
	checkCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	endpoint := p.URL
	var err error
	if endpoint == "" {
		endpoint, err = resolveInstanceEndpoint(checkCtx, p)
	}
	if err != nil {
		return
	}
	probeURL := endpoint
	var probeListener net.Listener
	if p.URL == "" {
		probeListener, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return
		}
		defer probeListener.Close()
		probeDone := make(chan struct{})
		defer func() { cancel(); probeListener.Close(); <-probeDone }()
		probeURL = "nats://" + probeListener.Addr().String()
		go func() {
			defer close(probeDone)
			c, acceptErr := probeListener.Accept()
			if acceptErr != nil {
				return
			}
			cmd, commandErr := instanceRelayCommand(checkCtx, p, endpoint)
			if commandErr != nil {
				c.Close()
				return
			}
			relayInstanceSocket(checkCtx, c, cmd)
		}()
	}
	identity, err := probeInstanceURL(probeURL)
	if err != nil || verifyInstance(p, identity) != nil {
		return
	}
	if p.URL != "" {
		upstream, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(endpoint, "nats://"))
		if dialErr != nil {
			return
		}
		defer upstream.Close()
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				conn.Close()
				upstream.Close()
			case <-done:
			}
		}()
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, conn); upstream.Close(); close(copied) }()
		_, _ = io.Copy(conn, upstream)
		conn.Close()
		<-copied
		close(done)
		return
	}
	cmd, err := instanceRelayCommand(ctx, p, endpoint)
	if err == nil {
		relayInstanceSocket(ctx, conn, cmd)
	}
}

func openInstanceTransport(parent context.Context, p instanceProfile) (*instanceTransport, harnessIdentity, error) {
	var identity harnessIdentity
	r := instanceRegistry{Version: 1, Order: []string{"remote"}, Instances: map[string]instanceProfile{"remote": p}}
	if err := r.validate(); err != nil {
		return nil, identity, err
	}
	ctx, cancel := context.WithCancel(parent)
	t := &instanceTransport{URL: p.URL, cancel: cancel}
	{
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Close()
			return nil, identity, err
		}
		t.listener = listener
		t.URL = "nats://" + listener.Addr().String()
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				t.wg.Add(1)
				go func() { defer t.wg.Done(); checkedInstanceRelay(ctx, conn, p) }()
			}
		}()
	}
	identity, err := probeInstanceURL(t.URL)
	if err == nil {
		err = verifyInstance(p, identity)
	}
	if err != nil {
		t.Close()
		return nil, identity, err
	}
	return t, identity, nil
}
