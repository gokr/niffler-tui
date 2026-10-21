// The pre-claim slice: a switch reserves the target instance's conversation
// (ui register + claim) and hands that identity to the re-executed client, so
// the current UI is released only after the target is secured and the
// successor adopts the same claim instead of racing the old one for it.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "niffler.dev/sdk"
)

// uiFixture is a catalog + ui registry stand-in: enough of core's surface for
// a real reservation (register/claim/renew/release) without a harness. It is
// the target's registry, so the test can also play "another client already
// holds the conversation".
type uiFixture struct {
	mu       sync.Mutex
	sessions map[string]string // sessionId -> owner uiID
	number   map[string]int
	next     int
	refused  map[string]bool // sessions the fixture refuses to anyone
}

func newUiFixture() *uiFixture {
	return &uiFixture{sessions: map[string]string{}, number: map[string]int{}, refused: map[string]bool{}}
}

func (f *uiFixture) owner(session string) (string, int) {
	if id, ok := f.sessions[session]; ok {
		return id, f.number[id]
	}
	return "", 0
}

// serve answers svc.core.call, delegating to the fixture's registry.
func (f *uiFixture) handle(args json.RawMessage) any {
	var a struct {
		Op      string `json:"op"`
		UI      string `json:"ui"`
		Session string `json:"session"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return map[string]any{"error": "bad args"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch a.Op {
	case "register":
		if a.UI == "" {
			return map[string]any{"error": "ui register needs ui"}
		}
		if _, ok := f.number[a.UI]; !ok {
			f.next++
			f.number[a.UI] = f.next
		}
		return map[string]any{"ok": true, "number": f.number[a.UI]}
	case "renew":
		if _, ok := f.number[a.UI]; !ok {
			return map[string]any{"ok": false}
		}
		return map[string]any{"ok": true}
	case "release":
		delete(f.number, a.UI)
		for session, owner := range f.sessions {
			if owner == a.UI {
				delete(f.sessions, session)
			}
		}
		return map[string]any{"ok": true}
	case "claim":
		if _, ok := f.number[a.UI]; !ok {
			return map[string]any{"error": "ui claim needs a live lease (register first)"}
		}
		if owner, number := f.owner(a.Session); owner != "" && owner != a.UI {
			return map[string]any{"ok": false, "owner": owner, "number": number}
		}
		if f.refused[a.Session] {
			return map[string]any{"ok": false, "owner": "other", "number": 9}
		}
		f.sessions[a.Session] = a.UI
		return map[string]any{"ok": true}
	}
	return map[string]any{"error": "unknown ui op"}
}

// serveUiFixture accepts fresh client connections (each reservation and probe
// dials its own) and answers catalog probes plus ui calls.
func serveUiFixture(t *testing.T, f *uiFixture) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(20 * time.Second))
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
						var env struct {
							Tool string          `json:"tool"`
							Args json.RawMessage `json:"args"`
						}
						_ = json.Unmarshal(body[:size], &env)
						result := map[string]any{"instanceId": strings.Repeat("a", 32), "name": "fixture", "root": "/worker"}
						if env.Tool == "ui" {
							result = f.handle(env.Args).(map[string]any)
						}
						payload, _ := json.Marshal(map[string]any{"v": 1, "kind": "result", "args": result})
						fmt.Fprintf(c, "MSG %s %s %d\r\n%s\r\n", reply, sid, len(payload), payload)
					}
				}
			}()
		}
	}()
	return "nats://" + listener.Addr().String()
}

// TestInstanceSwitchReservesTargetBeforeQuitting pins the ordering the switch
// exists for: verification, then a target reservation, and only then the quit
// that lets main() release the current UI and hand the claim to the successor.
func TestInstanceSwitchReservesTargetBeforeQuitting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	fixture := newUiFixture()
	endpoint := serveUiFixture(t, fixture)

	p := instanceProfile{URL: endpoint, Workspace: "/worker", Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker"}}
	withRegistryPeer(t, "worker", p)
	t.Cleanup(func() { os.Unsetenv(instanceClaimEnv) })

	m := newTestModel()
	m.ctx = context.Background()
	m.instanceName = "local"
	m.uiID = "tui-aaaaaaaabbbb"

	updated, cmd := m.executeLocalCommand("/niffler worker")
	got := updated.(model)
	if cmd == nil || !got.instanceSwitchPending {
		t.Fatal("switch was not started")
	}
	msg, ok := cmd().(instanceSwitchMsg)
	if !ok || msg.err != nil || msg.claim == nil {
		t.Fatalf("reservation failed: %+v", msg)
	}
	defer msg.claim.Close()

	h := msg.claim.handoff
	if h.Scope != instanceStateKey(p) || h.Workspace != "/worker" || h.UI == "" || h.Session == "" {
		t.Fatalf("handoff = %+v", h)
	}
	// The target conversation is ours before the current UI is released: the
	// claim lives in the target registry and a second client is refused it.
	owner, _ := fixture.owner(h.Session)
	if owner != h.UI {
		t.Fatalf("target not reserved: owner=%q want %q", owner, h.UI)
	}
	if err := verifyInstanceClaim(endpoint, instanceClaimHandoff{Scope: h.Scope, Workspace: h.Workspace, UI: "tui-cccc11112222", Session: h.Session}); err == nil {
		t.Fatal("a second client took a reserved conversation")
	}

	// The model consumes the message and quits, carrying the claim for main().
	updated, quit := got.Update(msg)
	got = updated.(model)
	if !isQuit(quit) || got.switchInstance != "worker" || got.switchClaim != msg.claim {
		t.Fatalf("switch did not hand the claim on: %q %v", got.switchInstance, got.switchClaim)
	}
	owner, _ = fixture.owner(h.Session)
	if owner != h.UI {
		t.Fatalf("reservation dropped before handoff: owner=%q", owner)
	}
}

// TestInstanceSwitchReleasesReservationWhenRefused: a target that refuses the
// claim must leave the current UI untouched (no quit, no switch) and must not
// leave a half-registered client behind in the target's registry.
func TestInstanceSwitchReleasesReservationWhenRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	fixture := newUiFixture()
	fixture.refused["taken"] = true
	endpoint := serveUiFixture(t, fixture)

	p := instanceProfile{URL: endpoint, Workspace: "/worker", Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker"}}
	withRegistryPeer(t, "worker", p)
	t.Cleanup(func() { os.Unsetenv(instanceClaimEnv) })

	m := newTestModel()
	m.ctx = context.Background()
	m.instanceName = "local"
	// A hand-edited last-session file names the conversation the target refuses.
	persistSession(instanceStateKey(p), "/worker", "taken")

	updated, cmd := m.executeLocalCommand("/niffler worker")
	got := updated.(model)
	msg, ok := cmd().(instanceSwitchMsg)
	if !ok || msg.err == nil || msg.claim != nil {
		t.Fatalf("refused claim accepted: %+v", msg)
	}
	updated, quit := got.Update(msg)
	got = updated.(model)
	if quit != nil || got.switchInstance != "" || got.instanceSwitchPending || got.switchClaim != nil {
		t.Fatal("refused switch changed the current UI")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.number) != 0 {
		t.Fatalf("reservation leaked in the target registry: %v", fixture.number)
	}
}

// TestInstanceClaimHandoffRoundTrip: the successor's inheritance is one-shot,
// keyed to the exact target, and sanitized like every other recorded id.
func TestInstanceClaimHandoffRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const scope = "instance:abc"

	os.Setenv(instanceClaimEnv, `{"scope":"instance:abc","workspace":"/worker","ui":"tui-111122223333","session":"conv-1"}`)
	t.Cleanup(func() { os.Unsetenv(instanceClaimEnv) })
	h, err := consumeInstanceClaim(scope, "/worker")
	if err != nil || h.UI != "tui-111122223333" || h.Session != "conv-1" {
		t.Fatalf("handoff = %+v %v", h, err)
	}
	// Consumed by the first start that reads it: a stale environment entry
	// must not adopt the same identity twice.
	if h, err = consumeInstanceClaim(scope, "/worker"); h.UI != "" || err != nil {
		t.Fatalf("claim inherited twice: %+v %v", h, err)
	}
	// Wrong instance or workspace: refuse rather than silently start fresh.
	for _, tc := range []struct{ scope, workspace string }{{"instance:other", "/worker"}, {scope, "/elsewhere"}} {
		os.Setenv(instanceClaimEnv, `{"scope":"instance:abc","workspace":"/worker","ui":"tui-111122223333","session":"conv-1"}`)
		if _, err = consumeInstanceClaim(tc.scope, tc.workspace); err == nil {
			t.Fatalf("claim for another target accepted: %+v", tc)
		}
	}
	os.Setenv(instanceClaimEnv, "{not json")
	if _, err = consumeInstanceClaim(scope, "/worker"); err == nil {
		t.Fatal("corrupt claim accepted")
	}
	os.Setenv(instanceClaimEnv, `{"scope":"instance:abc","workspace":"/worker","ui":"../../etc/passwd","session":"weird/../id"}`)
	if _, err = consumeInstanceClaim(scope, "/worker"); err == nil {
		t.Fatal("unsanitized claim accepted")
	}
	os.Unsetenv(instanceClaimEnv)
	if h, err = consumeInstanceClaim(scope, "/worker"); h.UI != "" || err != nil {
		t.Fatalf("missing claim produced %+v %v", h, err)
	}
}

// TestInstanceClaimEnvironmentReplacesSession keeps the exec environment
// single-sourced: the reserved session travels in the claim blob, never twice.
func TestInstanceClaimEnvironmentReplacesSession(t *testing.T) {
	h := instanceClaimHandoff{Scope: "instance:abc", Workspace: "/worker", UI: "tui-111122223333", Session: "conv-1"}
	env := instanceClaimEnvironment([]string{"PATH=/bin", "NIF_SESSION=stale", instanceClaimEnv + "=old"}, h)
	seen := map[string]int{}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		seen[key]++
	}
	if seen["NIF_SESSION"] != 0 || seen[instanceClaimEnv] != 1 || seen["PATH"] != 1 {
		t.Fatalf("environment = %v", env)
	}
}

// TestInstanceClaimRenewalKeepsTargetReserved: the wrapper may take seconds
// (release retries, a slow successor), and core's lease is 20s — the
// reservation must keep renewing itself until the handoff lands.
func TestInstanceClaimRenewalKeepsTargetReserved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := newUiFixture()
	endpoint := serveUiFixture(t, fixture)
	old := instanceClaimRenewEvery
	instanceClaimRenewEvery = 25 * time.Millisecond
	t.Cleanup(func() { instanceClaimRenewEvery = old })

	p := instanceProfile{URL: endpoint, Workspace: "/worker", Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker"}}
	claim, err := reserveInstance(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		fixture.mu.Lock()
		owner := fixture.sessions[claim.handoff.Session]
		fixture.mu.Unlock()
		if owner != claim.handoff.UI {
			t.Fatalf("reservation lost while renewing: owner=%q", owner)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ui := claim.handoff.UI
	claim.Close()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if _, ok := fixture.number[ui]; ok {
		t.Fatal("closed reservation kept its lease")
	}
	if len(fixture.sessions) != 0 {
		t.Fatal("closed reservation kept a claim")
	}
}

// TestNifflerSwitchIgnoresRepeatedCommand: a switch in flight owns the UI
// until it answers, so a second /niffler cannot start an overlapping
// reservation (and cannot quit the UI with a claim nobody hands on).
func TestNifflerSwitchIgnoresRepeatedCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	p := instanceProfile{URL: "nats://127.0.0.1:1", Workspace: "/worker", Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker"}}
	withRegistryPeer(t, "worker", p)

	m := newTestModel()
	m.ctx = context.Background()
	m.instanceName = "local"
	updated, _ := m.executeLocalCommand("/niffler worker")
	got := updated.(model)
	if !got.instanceSwitchPending {
		t.Fatal("switch not pending")
	}
	updated, cmd := got.executeLocalCommand("/niffler worker")
	if cmd != nil || !updated.(model).instanceSwitchPending {
		t.Fatal("a second switch started while one was in flight")
	}
}

// TestReservationSharesItsLeaseWithTheSuccessor pins the ordering that makes
// the handoff safe: the target reservation is taken on the TARGET's registry
// (the current UI's release goes to the bus being left, so it cannot void it),
// and the successor re-registers and re-claims on the SAME ui id — keeping the
// claim, the display number and one live lease instead of racing the old one
// for the conversation.
func TestReservationSharesItsLeaseWithTheSuccessor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixture := newUiFixture()
	endpoint := serveUiFixture(t, fixture)

	p := instanceProfile{URL: endpoint, Workspace: "/worker", Expect: instanceExpectation{ID: strings.Repeat("a", 32), Root: "/worker"}}
	claim, err := reserveInstance(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	h := claim.handoff
	fixture.mu.Lock()
	number, session := fixture.number[h.UI], fixture.sessions[h.Session]
	fixture.mu.Unlock()
	if session != h.UI {
		t.Fatalf("reservation absent: %q", session)
	}
	// The successor start (main): verify + adopt the reservation.
	if err = verifyInstanceClaim(endpoint, h); err != nil {
		t.Fatalf("successor refused its own reservation: %v", err)
	}
	fixture.mu.Lock()
	if fixture.number[h.UI] != number {
		t.Fatalf("shared lease changed the display number: %d -> %d", number, fixture.number[h.UI])
	}
	fixture.mu.Unlock()
	// Still one client holding it: adopting by id is idempotent, so no window
	// exists in which the target conversation is ownerless ("Open in Niffler N"
	// for the successor, or a stranger's claim).
	fixture.mu.Lock()
	owner, stillNumber := fixture.sessions[h.Session], fixture.number[h.UI]
	fixture.mu.Unlock()
	if owner != h.UI || stillNumber != number {
		t.Fatalf("adoption changed the reservation: owner=%q number=%d", owner, stillNumber)
	}
	// Rolling the reservation back is what the failed-relaunch path does, and
	// it must leave the target's registry clean (no half-registered client).
	claim.Close()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.sessions) != 0 || len(fixture.number) != 0 {
		t.Fatalf("rollback leaked: %v %v", fixture.sessions, fixture.number)
	}
}

// withRegistryPeer supplies one named peer through the registry seam. Routes
// live on the harness now, so a test must not fabricate a client-side file.
func withRegistryPeer(t *testing.T, name string, p instanceProfile) {
	t.Helper()
	previous := registryLookup
	registryLookup = func(*sdk.Component) registrySnapshot {
		return registrySnapshot{peers: []namedProfile{{name: name, profile: p}}}
	}
	t.Cleanup(func() { registryLookup = previous })
}
