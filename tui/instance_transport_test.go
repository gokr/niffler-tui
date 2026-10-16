package main

import (
	"context"
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
	p.Multipass = "worker"
	cmd, err = instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"multipass", "exec", "worker", "--", p.Expect.Root + "/var/bin/cli", "relay", "4222"}
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
	p.Multipass = "-bad"
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
	go func() { relayInstanceSocket(ctx, server, cmd); close(done) }()
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
func TestLiveMultipassTransport(t *testing.T) {
	vm := os.Getenv("NIF_TEST_MULTIPASS")
	if vm == "" {
		t.Skip("set NIF_TEST_MULTIPASS for approved read-only VM probe")
	}
	p := instanceProfile{Multipass: vm, Expect: instanceExpectation{ID: os.Getenv("NIF_TEST_INSTANCE_ID"), Root: "/home/ubuntu/niffler"}}
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
