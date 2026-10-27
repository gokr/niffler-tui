package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	sdk "niffler.dev/sdk"
)

// fleetMsg is the sole async ingress. A session ID is not a fleet identity:
// independent stores can use the same IDs. Generation also rejects late replies
// from a connection that has been replaced.
type fleetMsg struct {
	name       string
	generation uint64
	msg        tea.Msg
}
type fleetSwitchRequest struct{ name string }
type fleetReadyMsg struct {
	name  string
	scope string
	model model
	claim *instanceClaim
	err   error
}

// fleetActivityRescanMsg asks for one durable completion scan (connect or
// reconnect). It is never periodic: the push lane is the steady state.
type fleetActivityRescanMsg struct{}

// ringSweep is one alt+n / alt+shift+n traversal. Its candidate list is
// captured when the sweep starts, so a peer that fails cannot trap the ring
// (the next candidate is tried) and a registry that changes mid-sweep cannot
// make it revisit an instance it already failed on.
type ringSweep struct {
	active    bool
	direction int
	tried     map[string]bool
	order     []string
}

type fleetModel struct {
	ring                      ringSweep
	models                    map[string]model
	generations               map[string]uint64
	claims                    map[string]*instanceClaim
	cancels                   map[string]context.CancelFunc
	scopes                    map[string]string
	order                     []string
	ledger                    *activityLedger
	ledgerPath                string
	ledgerErr                 string
	active, pending, localDir string
	send                      func(tea.Msg)
	width, height             int
}

func newFleet(m model, localDir, initialScope string) *fleetModel {
	m.fleetManaged = true
	if initialScope == "" {
		initialScope = localScope
	}
	order := []string{"local"}
	if m.instanceName != "local" {
		order = append(order, m.instanceName)
	}
	f := &fleetModel{models: map[string]model{m.instanceName: m}, generations: map[string]uint64{m.instanceName: 1}, claims: map[string]*instanceClaim{}, cancels: map[string]context.CancelFunc{}, scopes: map[string]string{m.instanceName: initialScope}, order: order, active: m.instanceName, localDir: localDir}
	f.loadLedger()
	return f
}

// localScope is a fixed key: the local bus URL changes with a restarted core,
// and an unread badge must not reset (or fork) when the port moves.
const localScope = "local"

// localTarget is the local harness this client may return to, plus the root it
// must be serving. A local switch is an explicit selection, so it is verified
// rather than trusted: attaching to whichever core happens to answer would be
// the foreign-bus mistake the identity rules exist to prevent.
type localTarget struct {
	url, root string
}

func resolveLocalTarget() localTarget {
	root := strings.TrimSpace(os.Getenv("NIF_ROOT"))
	if url := strings.TrimSpace(os.Getenv("NIF_NATS_URL")); url != "" {
		return localTarget{url: url, root: root}
	}
	// Mirror the SDK's discovery order, keeping the root the discovery file
	// came from so the answering core can be checked against it.
	paths := []string{}
	if root != "" {
		paths = append(paths, filepath.Join(root, "var", "nats-url"))
	}
	paths = append(paths, filepath.Join("var", "nats-url"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if url := strings.TrimSpace(string(data)); url != "" {
			discovered := filepath.Dir(filepath.Dir(path))
			if abs, err := filepath.Abs(discovered); err == nil {
				discovered = abs
			}
			return localTarget{url: url, root: discovered}
		}
	}
	return localTarget{url: defaultNATSURL, root: root}
}

// checkLocalTarget refuses a bus that is not the local harness we expect. An
// unknown expected root is not a licence to attach blindly: the answering core
// must at least present a coherent identity.
func checkLocalTarget(t localTarget, id harnessIdentity) error {
	if id.InstanceID == "" || id.Root == "" {
		return fmt.Errorf("local bus %s did not report a harness identity", t.url)
	}
	if t.root != "" && id.Root != t.root {
		return fmt.Errorf("local bus %s serves %s, not %s", t.url, id.Root, t.root)
	}
	return nil
}

func (f *fleetModel) loadLedger() {
	path, err := activityLedgerPath()
	if err != nil {
		f.ledger, f.ledgerErr = newActivityLedger(), err.Error()
		return
	}
	ledger, err := loadActivityLedger(path)
	if err != nil {
		f.ledger, f.ledgerErr = newActivityLedger(), err.Error()
		return
	}
	f.ledger, f.ledgerPath = ledger, path
}

func (f *fleetModel) persistLedger() {
	if f.ledger == nil || f.ledgerPath == "" {
		return
	}
	if err := f.ledger.save(f.ledgerPath); err != nil {
		f.ledgerErr = err.Error()
	}
}

// baselineFleetInboxCmd marks every settlement that already exists as seen,
// once, on the first connect. It records FleetBaseline so a later connect does
// not adopt a settlement that arrived while the client was away.
func (f *fleetModel) baselineFleetInboxCmd(name string, generation uint64, comp *sdk.Component) tea.Cmd {
	return scopeCmd(name, generation, func() tea.Msg {
		msg := fleetInboxCmd(comp, 100)()
		batch, ok := msg.(fleetInboxMsg)
		if !ok || batch.Err != nil {
			return fleetInboxBaselineMsg{Err: fmt.Errorf("baseline unavailable")}
		}
		return fleetInboxBaselineMsg{Items: batch.Items}
	})
}

// fleetInboxBaselineMsg completes the first-connect baseline.
type fleetInboxBaselineMsg struct {
	Items []fleetInboxItem
	Err   error
}

// seenKeys is the render-time view of fleet seen markers (never mutates).
func (f *fleetModel) seenKeys() map[string]bool {
	if f.ledger == nil {
		return nil
	}
	return f.ledger.fleetSeenKeys()
}

func (f *fleetModel) scopeOf(name string) string {
	return f.scopes[name]
}

// mergeActivity records one completion. `viewed` is true only for the
// conversation actually on screen: a snapshot must never clear the badge of a
// conversation the human has not looked at.
//
// An instance whose scope is not yet known is skipped: its identity arrives
// with the completed switch, and the connection-time snapshot (which runs
// after it) carries every completion the harness has, so nothing is lost.
// Inventing a scope from the display name would fork the ledger and make a
// later rename look like a second instance.
func (f *fleetModel) mergeActivity(name string, a fleetActivity, viewed bool) {
	if f.ledger == nil || f.scopes[name] == "" {
		return
	}
	if !f.ledger.merge(f.scopeOf(name), a, viewed) && !viewed {
		return
	}
	f.persistLedger()
}

// markViewed clears the badge for the conversation this instance is showing.
func (f *fleetModel) markViewed(name string) {
	m, ok := f.models[name]
	if !ok || f.ledger == nil || m.session == "" {
		return
	}
	f.ledger.markRead(f.scopeOf(name), m.session)
	f.persistLedger()
}

// trackInstance appends a visited instance to the switch ring exactly once.
func (f *fleetModel) trackInstance(name string) {
	for _, existing := range f.order {
		if existing == name {
			return
		}
	}
	f.order = append(f.order, name)
}

// scopeCmd wraps batches recursively before Bubble Tea executes them. Sequence
// is an unexported slice type in Bubble Tea; preserve its ordering/type while
// replacing its commands too. Terminal control messages must reach the host,
// not an inactive textarea/model.
func scopeCmd(name string, generation uint64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		switch msg.(type) {
		case tea.QuitMsg:
			return msg
		}
		// Terminal commands return private Bubble Tea messages, which the host
		// must receive directly (not wrapped as an instance's model event).
		// Derive their types from the public commands rather than naming SDK
		// internals. Clipboard writes are global terminal effects.
		typ := reflect.TypeOf(msg)
		if typ == reflect.TypeOf(tea.SetClipboard("")()) ||
			typ == reflect.TypeOf(tea.SetPrimaryClipboard("")()) {
			return msg
		}
		v := reflect.ValueOf(msg)
		if v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
			out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := 0; i < v.Len(); i++ {
				out.Index(i).Set(reflect.ValueOf(scopeCmd(name, generation, v.Index(i).Interface().(tea.Cmd))))
			}
			return out.Interface()
		}
		return fleetMsg{name, generation, msg}
	}
}
func (f *fleetModel) Init() tea.Cmd {
	return scopeCmd(f.active, f.generations[f.active], f.models[f.active].Init())
}
func (f *fleetModel) updateInstance(name string, msg tea.Msg) tea.Cmd {
	m, ok := f.models[name]
	if !ok {
		return nil
	}
	// An invisible client must not auto-grant, nor acknowledge that a human saw
	// a prompt. Keep it for attention until resolved/expired by the harness.
	if req, ok := msg.(approvalEventMsg); ok && name != f.active {
		for _, old := range m.approvals {
			if old.id == req.req.id {
				return nil
			}
		}
		m.approvals = append(m.approvals, req.req)
		f.models[name] = m
		return nil
	}
	updated, cmd := m.Update(msg)
	// Defensive, not decorative: model.Update returns tea.Model, and the whole
	// fleet design stores each instance as a concrete `model`. A single handler
	// with a pointer receiver is enough to make this assertion panic (that is
	// exactly how the reported crash happened), and a crash in one background
	// instance must not take down the terminal. Keep the last good value and
	// carry on.
	if next, ok := updated.(model); ok {
		f.models[name] = next
	}
	return scopeCmd(name, f.generations[name], cmd)
}
func (f *fleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fleetMsg:
		if f.generations[msg.name] != msg.generation {
			return f, nil
		}
		if req, ok := msg.msg.(fleetSwitchRequest); ok {
			if msg.name != f.active {
				return f, nil
			}
			if req.name == "" {
				f.listInstances()
				return f, nil
			}
			// A deliberate selection ends any sweep in progress; only the
			// ring's own continuation carries a sweep forward.
			f.ring = ringSweep{}
			if req.name == "local" {
				// An explicit return to local is a supported selection now; only
				// the implicit fallback ownership is gone.
				return f, f.switchTo("local")
			}
			return f, f.switchTo(req.name)
		}
		if handled, cmd := f.activityMessage(msg.name, msg.msg); handled {
			return f, cmd
		}
		cmd := f.updateInstance(msg.name, msg.msg)
		// A fresh connection scans durable completions once; the push lane
		// covers everything after that (and OnReconnected covers reconnects).
		if _, connected := msg.msg.(connectedMsg); connected {
			if m, ok := f.models[msg.name]; ok && m.comp != nil {
				return f, tea.Batch(cmd, scopeCmd(msg.name, msg.generation, loadFleetActivityCmd(m.comp)))
			}
		}
		if _, connected := msg.msg.(connectedMsg); connected {
			// The FIRST connect baselines the fleet inbox: a client opened
			// against a long job history must not present all of it as unread
			// work. Later settlements are news (docs/FLEET-INBOX.md).
			if m, ok := f.models[msg.name]; ok && m.comp != nil && f.ledger != nil &&
				!f.ledger.FleetBaseline {
				return f, tea.Batch(cmd, f.baselineFleetInboxCmd(msg.name, msg.generation, m.comp))
			}
		}
		return f, cmd
	case fleetReadyMsg:
		f.pending = ""
		if msg.err != nil {
			m := f.models[f.active]
			m.addBlock(blockError, "Instance switch refused: "+msg.err.Error())
			m.syncViewport(true)
			f.models[f.active] = m
			// A ring sweep moves on to the next candidate: one unreachable or
			// misconfigured peer must not strand alt+n on it forever.
			if f.ring.active {
				next, exhausted := f.continueRing(msg.name)
				if exhausted {
					m := f.models[f.active]
					m.addBlock(blockMeta, "Instance ring: no further instance reachable; staying on "+f.active)
					m.syncViewport(true)
					f.models[f.active] = m
					return f, nil
				}
				return f, f.switchTo(next)
			}
			return f, nil
		}
		f.ring = ringSweep{}
		f.models[msg.name] = msg.model
		f.generations[msg.name] = 1
		f.claims[msg.name] = msg.claim
		f.scopes[msg.name] = msg.scope
		f.trackInstance(msg.name)
		f.active = msg.name
		m := f.models[msg.name]
		m.width = f.width
		m.height = max(0, f.height-1)
		m.layout()
		f.models[msg.name] = m
		// Opening an instance shows its conversation: that conversation's badge
		// clears, and only that one.
		f.markViewed(msg.name)
		return f, scopeCmd(msg.name, 1, tea.Batch(m.focusCmd, m.armSpinner(), leaseTickCmd(), func() tea.Msg { return connectedMsg{} }))
	case tea.WindowSizeMsg:
		f.width, f.height = msg.Width, msg.Height
		cmds := []tea.Cmd{}
		for name := range f.models {
			cmds = append(cmds, f.updateInstance(name, tea.WindowSizeMsg{Width: msg.Width, Height: max(0, msg.Height-1)}))
		}
		return f, tea.Batch(cmds...)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "alt+n":
			return f, f.switchTo("next")
		case "alt+shift+n", "alt+N":
			return f, f.switchTo("previous")
		}
	}
	return f, f.updateInstance(f.active, msg)
}

// activityMessage owns the completion feed messages: they update the
// client-local ledger rather than any instance's model. It reports whether it
// consumed the message.
func (f *fleetModel) activityMessage(name string, msg tea.Msg) (bool, tea.Cmd) {
	switch value := msg.(type) {
	case fleetActivityMsg:
		f.mergeActivity(name, value.Activity, f.isViewed(name, value.Activity.SessionID))
		return true, nil
	case fleetActivitySnapshotMsg:
		if value.Err != nil {
			if name == f.active {
				m := f.models[name]
				m.contextNote = "completion feed: " + value.Err.Error()
				f.models[name] = m
			}
			return true, nil
		}
		for _, item := range value.Items {
			f.mergeActivity(name, item, f.isViewed(name, item.SessionID))
		}
		return true, nil
	case fleetInboxBaselineMsg:
		if f.ledger == nil || f.ledger.FleetBaseline {
			return true, nil
		}
		// A failed baseline must NOT latch: leaving FleetBaseline false means
		// the next connect tries again, rather than silently presenting the
		// whole history as unread (or worse, marking it read on a bad read).
		if value.Err != nil {
			return true, nil
		}
		for _, item := range value.Items {
			if item.settledOnly() {
				f.ledger.markFleetSeen(item.seenKey())
			}
		}
		f.ledger.FleetBaseline = true
		f.persistLedger()
		return true, nil
	case fleetSettledMsg:
		// A live settlement: mark it unread and say so in the status line. We
		// do NOT append a chat block — a toast that steals the viewport while
		// you are reading a reply is worse than a marker you can act on.
		m, ok := f.models[name]
		if !ok {
			return true, nil
		}
		item := value.Item
		if item.settledOnly() && f.ledger != nil {
			// A settlement for the conversation currently on screen, from the
			// instance on screen, counts as seen only if it is actually the
			// same job as the open session; otherwise it is news.
			if !f.isViewed(name, item.JobID) {
				note := fmt.Sprintf("fleet: %s finished on %s — /tasks", item.JobID, item.Peer)
				if item.Settled != nil && item.Settled.Status != "" {
					note = fmt.Sprintf("fleet: %s finished on %s (%s) — /tasks",
						oneLine(item.Objective), item.Peer, item.Settled.Status)
				}
				m.contextNote = note
				f.models[name] = m
			}
		}
		return true, nil
	case fleetTaskDetailMsg:
		m, ok := f.models[name]
		if !ok {
			return true, nil
		}
		if value.Err != nil {
			// A detail failure is the NORMAL offline case, not a bug: the inbox
			// promised a verdict, the transcript lives on the peer. Say which.
			m.addBlock(blockMeta, fmt.Sprintf(
				"Fleet task %s: detail unavailable (%v).\n"+
					"The verdict stays in the inbox; the transcript is canonical on the peer.",
				value.JobID, value.Err))
		} else {
			m.addBlock(blockMeta, value.Reply)
		}
		m.syncViewport(true)
		f.models[name] = m
		return true, nil
	case fleetTasksRenderedMsg:
		// The /tasks read lands here rather than in the instance's own model:
		// the seen markers are client state owned by this ledger, and the
		// block must render into whichever instance is on screen. Marking
		// AFTER the render is deliberate — a settlement that arrives while the
		// list is being drawn must not be swallowed as already-read.
		m, ok := f.models[name]
		if !ok {
			return true, nil
		}
		if value.Err != nil {
			m.contextNote = "fleet inbox: " + value.Err.Error()
			f.models[name] = m
			return true, nil
		}
		m.addBlock(blockMeta, renderFleetInbox(value.Items, f.seenKeys()))
		m.syncViewport(true)
		f.models[name] = m
		if f.ledger != nil {
			changed := false
			for _, item := range value.Items {
				if item.settledOnly() && f.ledger.markFleetSeen(item.seenKey()) {
					changed = true
				}
			}
			if changed {
				f.persistLedger()
			}
		}
		return true, nil
	case fleetActivityRescanMsg:
		m, ok := f.models[name]
		if !ok || m.comp == nil {
			return true, nil
		}
		return true, scopeCmd(name, f.generations[name], loadFleetActivityCmd(m.comp))
	}
	return false, nil
}

// isViewed is deliberately narrow: only the conversation on screen counts as
// read. Anything else stays unread until the human opens it.
func (f *fleetModel) isViewed(name, session string) bool {
	if name != f.active || session == "" {
		return false
	}
	m, ok := f.models[name]
	return ok && m.session == session
}

// listInstances renders the explicit picker: every instance this client can
// reach, with the unread and attention markers the status line uses, plus the
// hint for a name that is not registered yet.
func (f *fleetModel) listInstances() {
	var comp *sdk.Component
	if active, ok := f.models[f.active]; ok {
		comp = active.comp
	}
	snapshot := loadRegistrySnapshot(comp)
	names := append([]string{}, f.ordering()...)
	for _, name := range snapshot.peerNames() {
		known := false
		for _, existing := range names {
			if existing == name {
				known = true
			}
		}
		if !known {
			names = append(names, name)
		}
	}
	lines := []string{"Instances (/niffler NAME, next or previous; Alt+N / Alt+Shift+N):"}
	for _, name := range names {
		lines = append(lines, "  "+f.instanceLine(name))
	}
	for _, warning := range snapshot.warnings {
		lines = append(lines, "  "+warning)
	}
	m := f.models[f.active]
	m.addBlock(blockMeta, strings.Join(lines, "\n"))
	m.syncViewport(true)
	f.models[f.active] = m
}

// beginRing starts (or continues) a sweep and returns the next untried
// candidate after the active instance, or "" when every candidate has been
// tried. The active instance is never a candidate: rotating onto the
// conversation already on screen is not a switch.
func (f *fleetModel) beginRing(direction int, order []string) string {
	if !f.ring.active || f.ring.direction != direction {
		f.ring = ringSweep{active: true, direction: direction,
			tried: map[string]bool{}, order: order}
	}
	return f.nextRingCandidate()
}

func (f *fleetModel) nextRingCandidate() string {
	order := f.ring.order
	if len(order) == 0 {
		return ""
	}
	start := 0
	for i, n := range order {
		if n == f.active {
			start = i
		}
	}
	for step := 1; step <= len(order); step++ {
		index := ((start+f.ring.direction*step)%len(order) + len(order)) % len(order)
		candidate := order[index]
		if candidate == f.active || f.ring.tried[candidate] {
			continue
		}
		return candidate
	}
	return ""
}

// continueRing advances past a failed candidate. It reports the summary text
// when the sweep is over — every peer was tried, and the client stays where it
// was rather than silently moving somewhere unintended.
func (f *fleetModel) continueRing(failed string) (next string, exhausted bool) {
	f.ring.tried[failed] = true
	next = f.nextRingCandidate()
	if next == "" {
		f.ring = ringSweep{}
		return "", true
	}
	return next, false
}

func (f *fleetModel) switchTo(name string) tea.Cmd {
	if f.pending != "" {
		return nil
	}
	var active *sdk.Component
	if current, ok := f.models[f.active]; ok {
		active = current.comp
	}
	snapshot := loadRegistrySnapshot(active)
	// Rotation follows the ring the user actually moves through (local plus
	// every visited instance), falling back to the registry only for instances
	// never opened in this process. Using the registry order alone would skip a
	// visited instance the moment the registry listing changes underneath us.
	order := append([]string{}, f.ordering()...)
	for _, n := range snapshot.peerNames() {
		known := false
		for _, existing := range order {
			if existing == n {
				known = true
			}
		}
		if !known {
			order = append(order, n)
		}
	}
	if name == "next" || name == "previous" {
		delta := 1
		if name == "previous" {
			delta = -1
		}
		name = f.beginRing(delta, order)
		if name == "" {
			// Nothing reachable to rotate to: say so instead of doing nothing.
			f.ring = ringSweep{}
			m := f.models[f.active]
			m.addBlock(blockError, "no other instance to switch to")
			m.syncViewport(true)
			f.models[f.active] = m
			return nil
		}
	}
	if name == f.active {
		return nil
	}
	if _, ok := f.models[name]; ok {
		f.active = name
		f.markViewed(name)
		return nil
	}
	p, ok := snapshot.profile(name)
	if name == "local" {
		ok = true
	}
	if !ok {
		return func() tea.Msg { return fleetReadyMsg{err: fmt.Errorf("unknown instance %q", name)} }
	}
	f.pending = name
	localDir, send := f.localDir, f.send
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		fail := func(err error) tea.Msg { cancel(); return fleetReadyMsg{name: name, err: err} }
		var claim *instanceClaim
		var err error
		if name == "local" {
			// Local is a deliberate selection, never a remote failure fallback,
			// and never "whichever core answers".
			target := resolveLocalTarget()
			identity, err := probeInstanceURL(target.url)
			if err != nil {
				return fail(unreachableError("the local bus "+target.url, err, nil))
			}
			if err := checkLocalTarget(target, identity); err != nil {
				return fail(err)
			}
			claim, err = reserveLocalInstance(ctx, target.url, localDir)
		} else {
			claim, err = reserveInstance(ctx, p)
		}
		if err != nil {
			return fail(err)
		}
		uiName := "tui-" + newUIID()
		comp := sdk.NewWithURL(uiName, componentVersion, claim.transport.URL)
		comp.Client = true
		m := newModel(ctx, comp, claim.handoff.Session, claim.transport.URL)
		m.fleetManaged = true
		m.instanceName = name
		m.instanceState = claim.handoff.Scope
		m.launchDir = claim.handoff.Workspace
		m.cwd = m.launchDir
		m.remoteInstance = name != "local"
		m.uiID = claim.handoff.UI
		m.uiName = uiName
		m.gate = newSessionEventGate(m.session)
		bindInstanceEvents(ctx, comp, m.gate, uiName, func(msg tea.Msg) {
			if send != nil {
				send(fleetMsg{name, 1, msg})
			}
		})
		if err = comp.Connect(); err != nil {
			comp.Close()
			claim.Close()
			return fail(err)
		}
		// Cancellation is connection lifetime only, never invoked on selection.
		m.fleetCancel = cancel
		// Short NATS reconnects do not trip the SDK's outage grace, so the
		// reconciliation trigger is this explicit hook, not a poll.
		comp.OnReconnected(func(*sdk.Component) {
			if send != nil {
				send(fleetMsg{name, 1, fleetActivityRescanMsg{}})
			}
		})
		scope := claim.handoff.Scope
		if name == "local" || scope == "" {
			scope = localScope
			if name != "local" {
				scope = "instance:" + name
			}
		}
		return fleetReadyMsg{name: name, scope: scope, model: m, claim: claim}
	}
}

func (f *fleetModel) ordering() []string {
	names := append([]string{}, f.order...)
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	var extra []string
	for name := range f.models {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(names, extra...)
}

func (f *fleetModel) instanceLine(name string) string {
	m, ok := f.models[name]
	if !ok {
		return name
	}
	marker := "○"
	if name == f.active {
		marker = "●"
	}
	line := marker + " " + name
	if name != f.active {
		switch {
		case !m.connected:
			line += " …"
		case m.comp != nil && !m.comp.Connected():
			line += " ⚠"
		}
	}
	if f.ledger != nil {
		if unread := len(f.ledger.unread(f.scopeOf(name))); unread > 0 {
			line += fmt.Sprintf(" ✓%d", unread)
		}
	}
	if len(m.approvals) > 0 {
		line += " !approval"
	}
	return line
}

// View appends the instance status line to whatever the active model renders,
// so the indicator stays on the bottom row for panels and modals too.
func (f *fleetModel) View() tea.View {
	m := f.models[f.active]
	view := m.View()
	parts := make([]string, 0, len(f.order)+2)
	for _, name := range f.ordering() {
		parts = append(parts, f.instanceLine(name))
	}
	if f.pending != "" {
		parts = append(parts, "opening "+f.pending)
	}
	label := strings.Join(parts, "   ")
	if f.ledgerErr != "" {
		label = "completion feed: " + f.ledgerErr + "   " + label
	}
	label += "   Alt+N next · Alt+Shift+N prev · /niffler"
	if f.width > 0 {
		label = ansi.Truncate(label, max(0, f.width-1), "")
	}
	// Pad to keep the indicator at the physical bottom even for a modal.
	lines := strings.Count(view.Content, "\n") + 1
	view.Content += strings.Repeat("\n", max(1, f.height-lines)) + metaStyle.Render(label)
	return view
}
func (f *fleetModel) close(restart bool) {
	f.persistLedger()
	for name, m := range f.models {
		// On /restart the successor adopts the active instance's registry
		// identity through the handoff record, so that registration must stay
		// warm: releasing it would make the successor a stranger core refuses
		// the conversation to. Its transport is still torn down — the process is
		// about to be replaced, and a forwarding child must not be left behind.
		if restart && name == f.active {
			if m.fleetCancel != nil {
				m.fleetCancel()
			}
			continue
		}
		if m.fleetManaged {
			releaseUI(m.comp, m.uiID)
		}
		if m.fleetCancel != nil {
			m.fleetCancel()
		}
		if m.comp != nil {
			m.comp.Close()
		}
		if claim := f.claims[name]; claim != nil {
			claim.Close()
		}
	}
}
func instanceLocalArgs(args []string) []string {
	out := instanceSwitchArgs(args, "local")
	return out[:len(out)-2]
}
