// Restart handoff: /restart re-runs this binary in place (it quits with
// restartExitCode and the installed wrapper loops), and the successor has to
// keep the identity of the client it replaces. Core's ui registry gives a
// conversation to one live UI at a time and holds a claim until its lease
// expires (20s) or the owner releases it, so a successor that registered as a
// brand-new client was refused the conversation whenever the predecessor's
// best-effort release did not land — a request lost on a busy bus, a crash, a
// SIGKILL, a killed terminal — and a refusal is obeyed by opening a NEW, empty
// conversation. That is the "I restarted and my conversation was gone"
// failure, and it bites hardest in the case /restart exists for: picking up a
// rebuilt install, where the predecessor may well be an older build.
//
// The predecessor therefore leaves a handoff record naming the registry uiID
// (and the conversation) it was using. A start within handoffTTL adopts that
// uiID for its first register+claim: re-registering a warm id keeps the
// display number and a claim by the owner is idempotent, so both the
// conversation and the "Niffler N" label carry over even when nothing was
// released. The record is a sibling of the last-session file (same bus+cwd
// key) and is consumed by the first start that reads it, so no identity is
// inherited twice and ordinary launches still mint a fresh id — two terminals
// in one workspace cannot silently share a conversation.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handoffTTL bounds how old a record may be and still be adopted. The wrapper
// re-runs the client immediately, so a healthy restart consumes it within a
// second or two; the generous window only covers a slow launcher. A record
// that outlives its registry entry is harmless — the claim stays idempotent
// and only the display number moves.
const handoffTTL = 120 * time.Second

type restartHandoff struct {
	UI      string  `json:"ui"`
	Session string  `json:"session"`
	At      float64 `json:"at"` // unix seconds
}

// handoffFilePath is the restart handoff for one harness AND workspace: the
// sibling of sessionFilePath, named after the same key, so a restart in one
// project never hands its identity to another.
func handoffFilePath(natsURL, workspace string) string {
	session := sessionFilePath(natsURL, workspace)
	if session == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(session),
		"handoff"+strings.TrimPrefix(filepath.Base(session), "session"))
}

// writeHandoff records this client's registry identity for the successor a
// /restart will start. Best effort: without the record the successor starts as
// a new client, which is the behavior before handoffs existed.
func writeHandoff(natsURL, workspace, uiID, session string) {
	if uiID == "" {
		return
	}
	path := handoffFilePath(natsURL, workspace)
	if path == "" {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	data, err := json.Marshal(restartHandoff{
		UI:      uiID,
		Session: session,
		At:      float64(time.Now().UnixNano()) / 1e9,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o600)
}

// consumeHandoff returns the identity a /restart predecessor left behind and
// deletes the record: one restart, one inheritance. A record older than
// handoffTTL, or one that does not parse, is dropped unread. Both ids are
// sanitized on the way out, so a hand-edited or truncated record cannot inject
// a value that is unsafe on the bus or as a state-file name.
func consumeHandoff(natsURL, workspace string, now time.Time) (uiID, session string) {
	path := handoffFilePath(natsURL, workspace)
	if path == "" {
		return "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	// Consume before parsing: a record that is handed over must never be
	// inherited twice, not even when it turns out to be unusable.
	_ = os.Remove(path)
	var rec restartHandoff
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", ""
	}
	if rec.At > 0 {
		// Only a positive age is bounded: a timestamp in the future (a clock
		// step backwards, a hand-edited file) must not pin the record forever.
		if age := now.Sub(time.Unix(int64(rec.At), 0)); age > handoffTTL {
			return "", ""
		}
	}
	return sanitizeSessionID(rec.UI), sanitizeSessionID(rec.Session)
}
