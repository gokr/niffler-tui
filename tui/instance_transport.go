package main

import (
	"context"
	"encoding/json"
	"errors"
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
	label    string // target name for client-facing failures ("" = the URL)
	listener net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
	problems relayDiagnosis
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

// relayDiagnosisGrace is how long a failed probe waits for the relay goroutine
// to report why the lane closed: the child's stderr is drained only after the
// socket is gone, so the probe error always arrives first.
var relayDiagnosisGrace = 2 * time.Second

// relayDiagnosis records the relay-side reasons a lane closed. A client that
// dials a transport whose guest bus is dead sees only a bare EOF ("no servers
// available"); the child's own stderr — a refused guest dial, an SSH failure, a
// missing guest CLI — is the only place the actual cause appears, and it is the
// difference between "EOF" and something a human can act on.
type relayDiagnosis struct {
	mu       sync.Mutex
	messages []string
	stamps   []time.Time
}

const relayDiagnosisLimit = 4

func (d *relayDiagnosis) note(message string) {
	// One line per message: it has to fit inside a switch error.
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return
	}
	if len(message) > 300 {
		message = message[:300] + "…"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, existing := range d.messages {
		if existing == message {
			d.stamps[i] = time.Now()
			return
		}
	}
	d.messages = append(d.messages, message)
	d.stamps = append(d.stamps, time.Now())
	if len(d.messages) > relayDiagnosisLimit {
		d.messages = d.messages[1:]
		d.stamps = d.stamps[1:]
	}
}

// recent drops stale notes: a lane that failed long ago must not explain an
// unrelated failure now.
func (d *relayDiagnosis) recent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Second)
	var out []string
	for i, at := range d.stamps {
		if at.After(cutoff) {
			out = append(out, d.messages[i])
		}
	}
	return out
}

// diagnoses collects whatever the relay path reported while the probe failed.
// It waits out the whole grace window: a generic probe failure lands before the
// child's stderr has been read, and the specific cause is worth the couple of
// seconds on a switch that is already failing.
func (t *instanceTransport) diagnoses() []string {
	deadline := time.Now().Add(relayDiagnosisGrace)
	for {
		if notes := t.problems.recent(); len(notes) > 0 {
			// Collect whatever the dying relay adds, then report. This runs on a
			// failing switch only, never on a successful attach.
			for time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			return t.problems.recent()
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// targetName is how a target appears in a client-facing failure: its display
// name when it has one, otherwise the bus it tried to reach.
func targetName(label, url string) string {
	if label != "" {
		return label
	}
	return url
}

// unreachableError is the switch-time wording for a target whose bus cannot be
// reached: name the target, keep the transport error, and append the relay's
// own diagnosis when there is one, because a bare "EOF" tells a human nothing.
// A refusal the relay already made in words (identity mismatch) is returned
// as-is: it is more precise than the closed lane it caused.
func unreachableError(where string, err error, notes []string) error {
	for _, note := range notes {
		if strings.Contains(note, "refusing attachment") {
			return errors.New(note)
		}
	}
	message := "no harness answering at " + where + ": "
	if len(notes) > 0 {
		// The notes carry the transport error themselves; repeating it would
		// only add a second "EOF" to the same sentence.
		message += strings.Join(notes, "; ")
	} else {
		message += err.Error()
	}
	return errors.New(message + " — is its harness running?")
}

var remoteRootPattern = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var loopbackEndpointPattern = regexp.MustCompile(`^nats://127\.0\.0\.1:[0-9]+$`)

// guestCommand preserves binary stdio locally and quotes every argv word when
// crossing SSH's remote-shell boundary. Host credentials never enter the guest.
func instanceGuestCommand(ctx context.Context, p instanceProfile, argv []string) (*exec.Cmd, error) {
	if err := validateProfile(p); err != nil {
		return nil, err
	}
	var args []string
	if p.Incus != "" {
		args = []string{"incus", "--force-local", "--project", "default", "exec", p.Incus, "--mode=non-interactive", "--", "runuser", "-u", "ubuntu", "--", "env", "HOME=/home/ubuntu", "USER=ubuntu", "LOGNAME=ubuntu"}
	} else if p.Lima != "" {
		args = []string{"limactl", "shell", "--workdir=/home/ubuntu", p.Lima}
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
	if err := validateProfile(p); err != nil {
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
	if p.Lima != "" || p.Incus != "" {
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

// Guest transports use a private stdio lane instead of automatic port forwarding. Use the guest's native CLI relay,
// built for its own architecture, with no key extraction, mounts or listener.
// An outdated/missing CLI fails closed; never fall back to another transport.
func instanceRelayCommand(ctx context.Context, p instanceProfile, endpoint string) (*exec.Cmd, error) {
	if err := validateProfile(p); err != nil {
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
	if p.Lima != "" || p.Incus != "" {
		if !remoteRootPattern.MatchString(p.Expect.Root) {
			return nil, fmt.Errorf("invalid remote root")
		}
		return instanceGuestCommand(ctx, p, []string{p.Expect.Root + "/var/bin/cli", "relay", port})
	}
	return nil, fmt.Errorf("no relay transport")
}

// stderrTail keeps the last kilobytes of a relay child's stderr, the only place
// a refused guest dial or an SSH failure is reported.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailLimit = 2048

func (w *stderrTail) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > stderrTailLimit {
		w.buf = w.buf[len(w.buf)-stderrTailLimit:]
	}
	return len(p), nil
}

func (w *stderrTail) lastLine() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := strings.Split(string(w.buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// relayInstanceSocket pumps one lane through a child process. note (optional)
// receives the child's own failure diagnosis: the lane's socket dies with the
// child, so without it the caller can only report that the connection dropped.
func relayInstanceSocket(ctx context.Context, conn net.Conn, cmd *exec.Cmd, note func(string)) {
	defer conn.Close()
	input, err := cmd.StdinPipe()
	if err != nil {
		if note != nil {
			note("relay could not start: " + err.Error())
		}
		return
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		if note != nil {
			note("relay could not start: " + err.Error())
		}
		return
	}
	var stderr stderrTail
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		if note != nil {
			note("relay could not start: " + err.Error())
		}
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
	if note == nil {
		return
	}
	// An ordinary teardown (transport close, UI exit) kills the child too, so a
	// diagnostic it printed is what separates a failure from a normal exit.
	if line := stderr.lastLine(); line != "" {
		note("relay to the guest failed: " + line)
		return
	}
	if state := cmd.ProcessState; state != nil && state.Exited() && !state.Success() {
		note("relay to the guest exited: " + state.String())
	}
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

// Guest process seams: production always uses the real transport commands; the
// tests substitute a fake guest, which is the only way to reproduce a dead
// harness without a VM.
var (
	instanceEndpointLookup = resolveInstanceEndpoint
	instanceRelayLaunch    = instanceRelayCommand
)

// checkedInstanceRelay verifies each new forwarding lane before exposing it to
// the SDK. The probe uses a separate connection: this is routing protection,
// not authentication against a malicious peer swapping endpoints mid-handshake.
// Every rejection is reported through note, because the caller only observes a
// closed socket.
func checkedInstanceRelay(ctx context.Context, conn net.Conn, p instanceProfile, note func(string)) {
	defer conn.Close()
	checkCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	endpoint := p.URL
	var err error
	if endpoint == "" {
		endpoint, err = instanceEndpointLookup(checkCtx, p)
	}
	if err != nil {
		note("cannot read the guest's bus endpoint from " + p.Expect.Root + "/var/nats-url: " + err.Error())
		return
	}
	probeURL := endpoint
	var probeListener net.Listener
	if p.URL == "" {
		probeListener, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			note("cannot open a local relay socket: " + err.Error())
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
			cmd, commandErr := instanceRelayLaunch(checkCtx, p, endpoint)
			if commandErr != nil {
				c.Close()
				note("cannot build the guest relay command: " + commandErr.Error())
				return
			}
			relayInstanceSocket(checkCtx, c, cmd, note)
		}()
	}
	identity, err := probeInstanceURL(probeURL)
	if err != nil {
		note("the guest's advertised bus (" + strings.TrimPrefix(endpoint, "nats://") + ") answered no catalog probe: " + err.Error())
		return
	}
	if err = verifyInstance(p, identity); err != nil {
		note(err.Error())
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
	if err != nil {
		note("cannot build the guest relay command: " + err.Error())
		return
	}
	relayInstanceSocket(ctx, conn, cmd, note)
}

func openInstanceTransport(parent context.Context, p instanceProfile) (*instanceTransport, harnessIdentity, error) {
	var identity harnessIdentity
	if err := validateProfile(p); err != nil {
		return nil, identity, err
	}
	ctx, cancel := context.WithCancel(parent)
	t := &instanceTransport{URL: p.URL, cancel: cancel, label: p.Expect.Name}
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
				go func() {
					defer t.wg.Done()
					checkedInstanceRelay(ctx, conn, p, t.problems.note)
				}()
			}
		}()
	}
	identity, err := probeInstanceURL(t.URL)
	if err != nil {
		// The lane is gone: report what the relay saw, not just the symptom.
		notes := t.diagnoses()
		t.Close()
		return nil, identity, unreachableError(targetName(t.label, t.URL), err, notes)
	}
	if err = verifyInstance(p, identity); err != nil {
		// An identity refusal is precise on its own; do not present it as an
		// unreachable target.
		t.Close()
		return nil, identity, err
	}
	return t, identity, nil
}
