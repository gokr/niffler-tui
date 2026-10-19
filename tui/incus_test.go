package main

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGuestRoutes(t *testing.T) {
	for _, kind := range []string{"incus", "multipass"} {
		for _, host := range []string{"", "gokr@wowbagger.krampe.se"} {
			t.Run(kind+host, func(t *testing.T) {
				p := testInstance()
				p.SSH = ""
				p.Host = host
				if kind == "incus" {
					p.Incus = "niffler1"
				} else {
					p.Multipass = "niffler1"
				}
				cmd, err := instanceRemoteCommand(context.Background(), p, "printf ready\n")
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(cmd.Stdin)
				if string(data) != "printf ready\n" {
					t.Fatal(string(data))
				}
				want := []string{kind, "exec", "niffler1", "--"}
				if kind == "incus" {
					want = []string{"incus", "--force-local", "--project", "default", "exec", "niffler1", "--mode=non-interactive", "--", "runuser", "-u", "ubuntu", "--", "env", "HOME=/home/ubuntu", "USER=ubuntu", "LOGNAME=ubuntu"}
				}
				want = append(want, "bash", "-s")
				if host == "" {
					if !reflect.DeepEqual(cmd.Args, want) {
						t.Fatalf("%q != %q", cmd.Args, want)
					}
				} else {
					if cmd.Args[0] != "ssh" || cmd.Args[len(cmd.Args)-2] != host {
						t.Fatal(cmd.Args)
					}
					// Execute only the generated shell parsing, never SSH or Incus.
					parsed, err := exec.Command("bash", "-c", "set -- "+cmd.Args[len(cmd.Args)-1]+"; printf '%s\\n' \"$@\"").Output()
					if err != nil || !reflect.DeepEqual(strings.Split(strings.TrimSuffix(string(parsed), "\n"), "\n"), want) {
						t.Fatalf("%q %v", parsed, err)
					}
				}
				relay, err := instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:14222")
				if err != nil {
					t.Fatal(err)
				}
				relayWant := append(append([]string{}, want[:len(want)-2]...), p.Expect.Root+"/var/bin/cli", "relay", "14222")
				if host == "" {
					if !reflect.DeepEqual(relay.Args, relayWant) {
						t.Fatalf("relay %q != %q", relay.Args, relayWant)
					}
				} else {
					parsed, parseErr := exec.Command("bash", "-c", "set -- "+relay.Args[len(relay.Args)-1]+"; printf '%s\\n' \"$@\"").Output()
					if parseErr != nil || !reflect.DeepEqual(strings.Split(strings.TrimSuffix(string(parsed), "\n"), "\n"), relayWant) {
						t.Fatalf("relay %q %v", parsed, parseErr)
					}
				}
				receipt, _ := json.Marshal(map[string]any{"version": 1, "transport": kind, "target": "niffler1", "host": host, "root": p.Expect.Root, "expect": p.Expect})
				got, err := profileFromReceipt(receipt, harnessIdentity{InstanceID: p.Expect.ID})
				if err != nil || got.Host != host || got.Incus != p.Incus || got.Multipass != p.Multipass {
					t.Fatalf("%+v %v", got, err)
				}
				path := filepath.Join(t.TempDir(), "instances.json")
				registry := instanceRegistry{Version: 1, Order: []string{"worker"}, Instances: map[string]instanceProfile{"worker": got}}
				if err := saveInstanceRegistry(path, registry); err != nil {
					t.Fatal(err)
				}
				saved, err := loadInstanceRegistry(path)
				if err != nil || !reflect.DeepEqual(saved.Instances["worker"], got) {
					t.Fatalf("registry %+v %v", saved, err)
				}
				if _, err := profileFromReceipt(receipt, harnessIdentity{InstanceID: strings.Repeat("b", 32)}); err == nil {
					t.Fatal("accepted changed identity")
				}
			})
		}
	}
}

func TestGuestRouteRefusals(t *testing.T) {
	for _, mutate := range []func(*instanceProfile){
		func(p *instanceProfile) { p.Host = "-bad" },
		func(p *instanceProfile) { p.Host = "user@host;id" },
		func(p *instanceProfile) { p.Host = "host\nmalicious" },
		func(p *instanceProfile) { p.Incus = "-bad" },
		func(p *instanceProfile) { p.Incus = "x;id" },
		func(p *instanceProfile) { p.Expect.Root = "/root/niffler" },
		func(p *instanceProfile) { p.Multipass = "other" },
		func(p *instanceProfile) { p.SSH = "other" },
		func(p *instanceProfile) { p.Incus = ""; p.Host = "host" },
	} {
		p := testInstance()
		p.SSH = ""
		p.Incus = "niffler1"
		mutate(&p)
		if _, err := instanceRemoteCommand(context.Background(), p, ""); err == nil {
			t.Fatalf("accepted %+v", p)
		}
		if _, err := instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222"); err == nil {
			t.Fatalf("accepted relay %+v", p)
		}
	}
	p := testInstance()
	p.Host = "host"
	if _, err := instanceRelayCommand(context.Background(), p, "nats://127.0.0.1:4222"); err == nil {
		t.Fatal("host on direct SSH")
	}
}
