package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	sdk "niffler.dev/sdk"
	"strings"
	"time"
)

// Each connection closes over its own immutable routing identity.
func bindInstanceEvents(ctx context.Context, comp *sdk.Component, gate *sessionEventGate, uiName string, send func(tea.Msg)) {
	// Human completion feed: the payload is the durable activity item, published
	// only after the receipt commits. A malformed or unknown item is ignored
	// rather than surfaced as a completion. This is not an agent notice: the
	// ledger it feeds never acknowledges anything on the harness side.
	comp.On("ev.activity", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		var item fleetActivity
		if json.Unmarshal(payload, &item) == nil && validFleetActivity(item) {
			send(fleetActivityMsg{Activity: item})
		}
	})
	// Fleet settlements (docs/FLEET-INBOX.md): a job this harness submitted to
	// a remote instance finished. This is the human feed, so it lands in the
	// client-local ledger and never acknowledges anything on the harness: the
	// event exists to tell a connected client, not to be confirmed.
	comp.On("ev.fleet.settled", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		var item fleetInboxItem
		if json.Unmarshal(payload, &item) == nil && item.JobID != "" {
			send(fleetSettledMsg{Item: item})
		}
	})
	comp.On("ev.session.>", func(_ *sdk.Component, subject string, payload json.RawMessage) {
		// Subjects are per-conversation (ev.session.<sessionId>.<kind>); the
		// gate filters on the subject's session token before the payload is
		// parsed. A two-token subject (legacy ev.session.<kind>) falls back
		// to the payload's sessionId.
		parts := strings.Split(subject, ".")
		haveSubjectSession := len(parts) >= 3
		if haveSubjectSession && !gate.interested(parts[2]) {
			return
		}
		var event sessionEvent
		if err := json.Unmarshal(payload, &event); err != nil || send == nil {
			return
		}
		if !haveSubjectSession && !gate.interested(event.SessionID) {
			return
		}
		kind := parts[len(parts)-1]
		if kind == "token" {
			gate.addToken(event)
			return
		}
		// Structural events flush the session's token tail first so the
		// merged frame lands ahead of them (per-subject wire order holds).
		if tail := gate.flushSession(event.SessionID); tail != nil {
			send(sessionEventMsg{kind: "token", event: *tail})
		}
		send(sessionEventMsg{kind: kind, event: event})
	})
	// Bound the queue rate: merged token events reach the UI on a fixed
	// cadence instead of per wire frame.
	go func() {
		ticker := time.NewTicker(sessionTokenFlushEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if send == nil {
				continue
			}
			for _, ev := range gate.flushAll() {
				send(sessionEventMsg{kind: "token", event: ev})
			}
		}
	}()
	comp.On("ev.provider.>", func(_ *sdk.Component, subject string, payload json.RawMessage) {
		if send == nil {
			return
		}
		changed := false
		if subject == "ev.provider.switch" {
			var event struct {
				Nickname string `json:"nickname"`
				Previous string `json:"previous"`
			}
			if json.Unmarshal(payload, &event) == nil && event.Nickname != event.Previous {
				changed = true
			}
		}
		send(providerBusEventMsg{Changed: changed})
	})
	comp.On("ev.models.updated", func(_ *sdk.Component, _ string, _ json.RawMessage) {
		if send != nil {
			send(modelsCatalogUpdatedMsg{})
		}
	})
	comp.On("ev.agent.started", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		var event agentEventMsg
		if json.Unmarshal(payload, &event) == nil {
			send(event)
		}
	})
	comp.On("ev.agent.notice", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		var event agentEventMsg
		if json.Unmarshal(payload, &event) == nil {
			send(event)
		}
	})
	// Catalog changes (components registering/departing) refresh the slash
	// registry: core checkpoints the merged table to the store before this
	// event goes out, so a store-first reload is consistent.
	comp.On("ev.catalog.updated", func(_ *sdk.Component, _ string, _ json.RawMessage) {
		if send != nil {
			send(catalogUpdatedMsg{})
		}
	})
	// Human approval gate: directed requests arrive on this UI's own
	// subject (core derives it from the call envelope's caller — the unique
	// "tui-<hex>" name makes it private to this terminal), broadcast
	// requests are direct calls or fallbacks.
	comp.On("svc.approval."+uiName+".request", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		if req, ok := parseApprovalPayload(payload); ok {
			send(approvalEventMsg{req: req, directed: true})
		}
	})
	comp.On("ev.approval.request", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		if req, ok := parseApprovalPayload(payload); ok {
			send(approvalEventMsg{req: req, directed: false})
		}
	})
	comp.On("ev.approval.resolved", func(_ *sdk.Component, _ string, payload json.RawMessage) {
		if send == nil {
			return
		}
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(payload, &p); err == nil && p.ID != "" {
			send(approvalResolvedMsg{id: p.ID})
		}
	})

}
