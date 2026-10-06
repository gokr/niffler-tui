package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNextFavoriteIdx(tt *testing.T) {
	items := []modelFav{
		{Provider: "a", Model: "m1"},
		{Provider: "b", Model: "m2"},
		{Provider: "c", Model: "m3"},
	}
	// Current in the middle: the next one wraps at the end.
	if idx, ok := nextFavoriteIdx(items, "a", "m1"); !ok || idx != 1 {
		tt.Fatalf("after a/m1 = %d (%v)", idx, ok)
	}
	if idx, _ := nextFavoriteIdx(items, "c", "m3"); idx != 0 {
		tt.Fatalf("rotation must wrap: %d", idx)
	}
	// Current selection not favorited: start at the top.
	if idx, ok := nextFavoriteIdx(items, "x", "y"); !ok || idx != 0 {
		tt.Fatalf("unfavorited selection = %d (%v)", idx, ok)
	}
	// Empty list: nothing to rotate.
	if _, ok := nextFavoriteIdx(nil, "a", "m1"); ok {
		tt.Fatal("empty list should not rotate")
	}
}

func TestParseFavDoc(tt *testing.T) {
	if got := parseFavDoc(nil); got != nil {
		tt.Fatalf("nil doc = %v", got)
	}
	raw := json.RawMessage(`{"items":[{"provider":"a","model":"m1"}],"updatedAt":12.5}`)
	got := parseFavDoc(raw)
	if len(got) != 1 || got[0].Provider != "a" || got[0].Model != "m1" {
		tt.Fatalf("parse = %+v", got)
	}
	// A malformed document is an empty list, not an error.
	if got := parseFavDoc(json.RawMessage(`nope`)); got != nil {
		tt.Fatalf("malformed doc = %v", got)
	}
}

func TestLocalFavListAndGuards(tt *testing.T) {
	m := snippetsTestModel(tt)

	// Not connected: a note, no command.
	m.connected = false
	if _, cmd := m.executeLocalCommand("/fav"); cmd != nil {
		tt.Fatal("/fav while disconnected produced a command")
	}
	m.connected = true

	// A bare /fav renders the (empty) list as a block via the msg.
	updated, cmd := m.executeLocalCommand("/fav")
	m = updated.(model)
	if cmd == nil {
		tt.Fatal("/fav produced no command")
	}
	m.applyFavsMsg(favsMsg{items: []modelFav{}, render: true})
	last := m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "no favorites") {
		tt.Fatalf("empty list note missing: %q", last)
	}

	// A rendered list marks the current provider/model with an arrow.
	m.runtime = runtimeResolution{OK: true, Provider: "b", Model: "m2"}
	m.applyFavsMsg(favsMsg{items: []modelFav{{Provider: "a", Model: "m1"},
		{Provider: "b", Model: "m2"}}, render: true})
	last = m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "1  a / m1") || !strings.Contains(last, "2  b / m2  ←") {
		tt.Fatalf("list rendering wrong:\n%s", last)
	}
}

func TestLocalFavArgumentParsing(tt *testing.T) {
	m := snippetsTestModel(tt)
	m.connected = true

	// Unknown subcommands and bad numbers render an error block or note.
	for _, bad := range []string{"bogus", "rm x", "rm", "0", "-2", "add extra"} {
		updated, cmd := m.executeLocalCommand("/fav " + bad)
		m = updated.(model)
		if cmd != nil {
			tt.Fatalf("/fav %s produced a command", bad)
		}
		last := m.blocks[len(m.blocks)-1].text
		if !strings.Contains(last, "/fav") && !strings.Contains(last, "no favorite") {
			tt.Fatalf("/fav %s did not report: %q", bad, last)
		}
	}
}

func TestRotateFavoritesGuards(tt *testing.T) {
	m := snippetsTestModel(tt)

	// No favorites: a note, no command.
	updated, cmd := m.rotateFavorites()
	m = updated
	if cmd != nil || !strings.Contains(m.contextNote, "no favorites") {
		tt.Fatalf("rotation with no favorites = (%q, %v)", m.contextNote, cmd)
	}

	// Mid-turn rotation is refused like /model (between-turns rule).
	m.favorites = []modelFav{{Provider: "a", Model: "m1"}}
	m.runtime = runtimeResolution{OK: true, Provider: "b", Model: "m2"}
	m.busy = true
	updated, cmd = m.rotateFavorites()
	m = updated
	if cmd != nil || m.contextNote != t(m.loc, "note.betweenTurnsModel") {
		tt.Fatalf("mid-turn rotation not refused: %q", m.contextNote)
	}

	// Idle with favorites: the note names the applied entry and a pin
	// command comes back (not executed — comp is nil in tests).
	m.busy = false
	updated, cmd = m.rotateFavorites()
	m = updated
	if cmd == nil {
		tt.Fatal("idle rotation produced no command")
	}
	if !strings.Contains(m.contextNote, "a / m1") {
		tt.Fatalf("rotation note wrong: %q", m.contextNote)
	}
}

func TestLocalNameAndCopy(tt *testing.T) {
	m := snippetsTestModel(tt)

	// /name with an argument returns the save command (not executed — the
	// test model has no bus).
	updated, cmd := m.executeLocalCommand("/name my session")
	m = updated.(model)
	if cmd == nil {
		tt.Fatal("/name produced no command")
	}
	// The result renders as a block, guarded by session.
	m.applyNameMsg(nameMsg{session: "s2", name: "elsewhere"})
	if len(m.blocks) > 0 && strings.Contains(m.blocks[len(m.blocks)-1].text, "elsewhere") {
		tt.Fatal("a foreign /name result rendered here")
	}
	m.applyNameMsg(nameMsg{session: "s1", name: "my session"})
	if got := m.blocks[len(m.blocks)-1].text; !strings.Contains(got, "my session") {
		tt.Fatalf("name result not rendered: %q", got)
	}

	// /copy takes the last assistant block's text.
	m.blocks = nil
	m.addBlock(blockAssistant, "the reply text")
	updated, cmd = m.executeLocalCommand("/copy")
	m = updated.(model)
	if cmd == nil {
		tt.Fatal("/copy produced no clipboard command")
	}
	if !strings.Contains(m.contextNote, "the reply") && !strings.Contains(m.contextNote, "copied") {
		tt.Fatalf("copy note wrong: %q", m.contextNote)
	}

	// Nothing to copy: a note, no command.
	m2 := snippetsTestModel(tt)
	m2.blocks = nil
	m2.addBlock(blockUser, "not a reply")
	if _, cmd := m2.executeLocalCommand("/copy"); cmd != nil {
		tt.Fatal("/copy with no assistant reply produced a command")
	}
}

func TestFavsMsgUpdatesModel(tt *testing.T) {
	m := snippetsTestModel(tt)
	m.applyFavsMsg(favsMsg{items: []modelFav{{Provider: "a", Model: "m1"}}})
	if !m.favoritesLoaded || len(m.favorites) != 1 || m.favorites[0].Model != "m1" {
		tt.Fatalf("favsMsg did not update the model: %+v", m.favorites)
	}
	// A failed load leaves the previous list intact and says so.
	m.applyFavsMsg(favsMsg{err: errTestStore})
	if len(m.favorites) != 1 {
		tt.Fatal("a failed load clobbered the favorites")
	}
	if !strings.Contains(m.contextNote, "unavailable") && !strings.Contains(m.contextNote, "store unreachable") {
		tt.Fatalf("load failure not surfaced: %q", m.contextNote)
	}
}

// Compile-time check that the clipboard command type is what we think.
var _ tea.Cmd = (tea.Cmd)(nil)
