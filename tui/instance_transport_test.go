package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInstanceRelayCommands(t *testing.T) {
	p := testInstance()
	cmd, err := instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"ForwardAgent=no", "BatchMode=yes", "-W 127.0.0.1:4222", "ubuntu@worker"} {
		if !strings.Contains(args, want) {
			t.Fatal(args)
		}
	}
	if strings.Contains(args, "StrictHostKeyChecking=no") {
		t.Fatal(args)
	}
	p.SSH = ""
	p.Lima = "worker"
	cmd, err = instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"limactl", "shell", "--workdir=/home/ubuntu", "worker", p.Expect.Root + "/var/bin/cli", "relay", "4222"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("relay argv = %q, want %q", cmd.Args, want)
	}
	root := p.Expect.Root
	for _, invalid := range []string{"", "relative", "/home/ubuntu/has space", "/home/ubuntu/$(id)", "/home/ubuntu/x;id", "/home/ubuntu/x\n"} {
		p.Expect.Root = invalid
		if _, err := instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222"); err == nil {
			t.Fatalf("invalid relay root accepted: %q", invalid)
		}
	}
	p.Expect.Root = root
	if _, err = instanceRelayCommand(context.Background(), p, "nats://0.0.0.0:4222"); err == nil {
		t.Fatal("public relay accepted")
	}
	p.Lima = "-bad"
	if _, err = instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222"); err == nil {
		t.Fatal("option injection")
	}
}

func TestInstanceRelayCopiesAndReaps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, server := net.Pipe()
	done := make(chan struct{})
	cmd := exec.CommandContext(ctx, "cat")
	go func() { relayInstanceSocket(ctx, server, cmd, nil); close(done) }()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	go func() { _, _ = client.Write([]byte("hello")) }()
	data := make([]byte, 5)
	if _, err := io.ReadFull(client, data); err != nil || string(data) != "hello" {
		t.Fatalf("%q %v", data, err)
	}
	cancel()
	client.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not reap")
	}
	if cmd.ProcessState == nil {
		t.Fatal("child not waited")
	}
}

// Explicit opt-in only: uses an already-running VM, never provisions or stops it.
func TestLiveLimaTransport(t *testing.T) {
	vm := os.Getenv("NIF_TEST_LIMA")
	if vm == "" {
		t.Skip("set NIF_TEST_LIMA for approved read-only VM probe")
	}
	p := instanceProfile{Lima: vm, Expect: instanceExpectation{ID: os.Getenv("NIF_TEST_INSTANCE_ID"), Root: "/home/ubuntu/niffler"}}
	transport, id, err := openInstanceTransport(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if id.InstanceID != p.Expect.ID {
		t.Fatal("wrong identity")
	}
	endpoint := transport.URL
	transport.Close()
	transport.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(endpoint, "nats://"), time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("listener survived close")
	}
	p.Expect.ID = strings.Repeat("0", 32)
	if tr, _, err := openInstanceTransport(context.Background(), p); err == nil {
		tr.Close()
		t.Fatal("identity drift accepted")
	}
}

// A refused guest dial is reported by the relay child on stderr, never by the
// socket the client holds: without it a switch to a stopped harness can only
// say "EOF".
func TestRelayReportsChildDiagnosis(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, server := net.Pipe()
	defer client.Close()
	cmd := exec.CommandContext(ctx, "bash", "-c", "echo 'cli relay: Connection refused' >&2; exit 1")
	var notes []string
	done := make(chan struct{})
	go func() {
		relayInstanceSocket(ctx, server, cmd, func(note string) { notes = append(notes, note) })
		close(done)
	}()
	go func() { _, _ = io.Copy(io.Discard, client) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not reap")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "cli relay: Connection refused") {
		t.Fatalf("notes = %q", notes)
	}
}

// A target whose harness is stopped: the guest bus is not listening, the relay
// child says so on stderr, and the client only sees a closed lane. The switch
// error must carry that cause, because "EOF" alone tells a human nothing.
func TestDeadTargetIsExplained(t *testing.T) {
	oldLookup, oldLaunch := instanceEndpointLookup, instanceRelayLaunch
	defer func() { instanceEndpointLookup, instanceRelayLaunch = oldLookup, oldLaunch }()
	instanceEndpointLookup = func(context.Context, instanceProfile) (string, error) {
		return "nats://127.0.0.1:1", nil // nothing is listening there
	}
	instanceRelayLaunch = func(ctx context.Context, _ instanceProfile, _ string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "bash", "-c", "echo 'cli relay: Connection refused' >&2; exit 1"), nil
	}
	_, _, err := openInstanceTransport(context.Background(), testInstance())
	if err == nil {
		t.Fatal("dead target accepted")
	}
	for _, want := range []string{"no harness answering at worker", "cli relay: Connection refused", "is its harness running?"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q lacks %q", err, want)
		}
	}
}

// An identity refusal is precise on its own: reporting it as an unreachable
// target would send a human looking for a stopped harness instead.
func TestIdentityRefusalIsNotReportedAsUnreachable(t *testing.T) {
	endpoint, id := catalogFixture(t)
	id.Store(strings.Repeat("b", 32)) // the bus now serves a replaced instance
	p := instanceProfile{URL: endpoint, Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker", Name: "worker"}}
	_, _, err := openInstanceTransport(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "instance identity mismatch") {
		t.Fatalf("identity drift reported as %v", err)
	}
	if strings.Contains(err.Error(), "no harness answering") {
		t.Fatalf("identity drift reported as unreachable: %v", err)
	}
}

// A target whose relay cannot even reach its own bus endpoint (stopped Incus
// container, revoked SSH route) must name that failure rather than the closed
// socket it causes.
func TestUnreadableGuestEndpointIsExplained(t *testing.T) {
	oldLookup := instanceEndpointLookup
	defer func() { instanceEndpointLookup = oldLookup }()
	instanceEndpointLookup = func(context.Context, instanceProfile) (string, error) {
		return "", errors.New("ssh: connect to host wowbagger port 22: Connection refused")
	}
	_, _, err := openInstanceTransport(context.Background(), testInstance())
	if err == nil {
		t.Fatal("unreachable guest accepted")
	}
	for _, want := range []string{"var/nats-url", "ssh: connect to host"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q lacks %q", err, want)
		}
	}
}
