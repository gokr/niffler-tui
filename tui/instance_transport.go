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

func instanceRemoteCommand(ctx context.Context, p instanceProfile, script string) (*exec.Cmd, error) {
	if p.SSH != "" {
		if !instanceTargetPattern.MatchString(p.SSH) {
			return nil, fmt.Errorf("invalid SSH target")
		}
		cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=15", "--", p.SSH, "bash -s")
		cmd.Stdin = strings.NewReader(script)
		return cmd, nil
	}
	if p.Multipass != "" {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(p.Multipass) {
			return nil, fmt.Errorf("invalid VM name")
		}
		cmd := exec.CommandContext(ctx, "multipass", "exec", p.Multipass, "--", "bash", "-s")
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

// Multipass has no port-forward command. Its exec channel runs a tiny stdlib
// socket bridge, with no SSH key extraction, host mounts or public VM listener.
const multipassSocketBridge = `import os,socket,sys,threading
s=socket.create_connection(('127.0.0.1',int(sys.argv[1])),15)
s.settimeout(None)
def upstream():
 try:
  while True:
   b=os.read(0,65536)
   if not b: break
   s.sendall(b)
 except OSError: pass
 try: s.shutdown(socket.SHUT_WR)
 except OSError: pass
threading.Thread(target=upstream,daemon=True).start()
try:
 while True:
  b=s.recv(65536)
  if not b: break
  sys.stdout.buffer.write(b);sys.stdout.buffer.flush()
finally: s.close()
`

func instanceRelayCommand(ctx context.Context, p instanceProfile, endpoint string) (*exec.Cmd, error) {
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
	if p.Multipass != "" {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(p.Multipass) {
			return nil, fmt.Errorf("invalid VM name")
		}
		return exec.CommandContext(ctx, "multipass", "exec", p.Multipass, "--", "python3", "-u", "-c", multipassSocketBridge, port), nil
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

func openInstanceTransport(parent context.Context, p instanceProfile) (*instanceTransport, harnessIdentity, error) {
	var identity harnessIdentity
	r := instanceRegistry{Version: 1, Order: []string{"remote"}, Instances: map[string]instanceProfile{"remote": p}}
	if err := r.validate(); err != nil {
		return nil, identity, err
	}
	ctx, cancel := context.WithCancel(parent)
	t := &instanceTransport{URL: p.URL, cancel: cancel}
	if p.URL == "" {
		resolveCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		endpoint, err := resolveInstanceEndpoint(resolveCtx, p)
		stop()
		if err != nil {
			t.Close()
			return nil, identity, err
		}
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
				cmd, err := instanceRelayCommand(ctx, p, endpoint)
				if err != nil {
					conn.Close()
					continue
				}
				t.wg.Add(1)
				go func() { defer t.wg.Done(); relayInstanceSocket(ctx, conn, cmd) }()
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
