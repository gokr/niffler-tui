package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A tiny catalog-only NATS fixture, not a real harness: enough protocol to
// exercise fresh client connections and mutable identity without a VM.
func catalogFixture(t *testing.T) (string, *atomic.Value) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	identity := new(atomic.Value)
	identity.Store(strings.Repeat("a", 32))
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(15 * time.Second))
				fmt.Fprint(c, "INFO {\"server_id\":\"fixture\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":8388608}\r\n")
				reader := bufio.NewReader(c)
				subscriptions := map[string]string{}
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					fields := strings.Fields(line)
					if len(fields) == 0 {
						continue
					}
					switch fields[0] {
					case "PING":
						fmt.Fprint(c, "PONG\r\n")
					case "SUB":
						if len(fields) >= 3 {
							subscriptions[fields[1]] = fields[len(fields)-1]
						}
					case "PUB":
						size, _ := strconv.Atoi(fields[len(fields)-1])
						body := make([]byte, size+2)
						if _, err = io.ReadFull(reader, body); err != nil {
							return
						}
						if len(fields) < 4 {
							continue
						}
						reply := fields[2]
						sid := ""
						for subject, id := range subscriptions {
							if subject == reply || strings.HasSuffix(subject, "*") && strings.HasPrefix(reply, strings.TrimSuffix(subject, "*")) {
								sid = id
								break
							}
						}
						if sid == "" {
							continue
						}
						payload, _ := json.Marshal(map[string]any{"v": 1, "kind": "result", "args": map[string]any{"instanceId": identity.Load(), "name": "fixture", "root": "/worker"}})
						fmt.Fprintf(c, "MSG %s %s %d\r\n%s\r\n", reply, sid, len(payload), payload)
					}
				}
			}()
		}
	}()
	return "nats://" + listener.Addr().String(), identity
}

func TestReconnectVerifiesEachNewLane(t *testing.T) {
	endpoint, id := catalogFixture(t)
	p := instanceProfile{URL: endpoint, Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker"}}
	tr, _, err := openInstanceTransport(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if _, err = probeInstanceURL(tr.URL); err != nil {
		t.Fatal("same instance reconnect refused", err)
	}
	id.Store(strings.Repeat("b", 32))
	if _, err = probeInstanceURL(tr.URL); err == nil {
		t.Fatal("replacement instance accepted")
	}
	id.Store(p.Expect.ID)
	if _, err = probeInstanceURL(tr.URL); err != nil {
		t.Fatal("original instance did not recover", err)
	}
}
