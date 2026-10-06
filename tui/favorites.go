// Favorites: provider/model combinations the human marked for quick
// rotation. One store document (kind "modelfavorite", id "favorites")
// carries the ordered list, so the rotation order is stable across clients
// and restarts and the entries reference the harness's own provider
// nicknames — the same store docs the provider component maintains.
// ctrl+x applies the next entry in order (wrapping); /fav manages the list.
// Application is the existing one-call provider+model pin between turns.
package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"niffler.dev/sdk"
)

// favStoreKind/favStoreID name the single favorites document. One doc (not
// one per entry) because rotation is order-sensitive: the array order IS
// the rotation order.
const (
	favStoreKind = "modelfavorite"
	favStoreID   = "favorites"
	favPutTries  = 5
)

// modelFav is one favorited provider/model combination.
type modelFav struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// favDoc is the stored document.
type favDoc struct {
	Items     []modelFav `json:"items"`
	UpdatedAt float64    `json:"updatedAt,omitempty"`
}

// nextFavoriteIdx returns the index rotation should apply next: the entry
// after the current provider/model (wrapping); when the current selection
// is not favorited, the first entry. ok is false for an empty list.
func nextFavoriteIdx(items []modelFav, provider, model string) (int, bool) {
	if len(items) == 0 {
		return 0, false
	}
	for i, fav := range items {
		if fav.Provider == provider && fav.Model == model {
			return (i + 1) % len(items), true
		}
	}
	return 0, true
}

// parseFavDoc decodes the stored document; a missing or malformed doc is
// an empty list (favorites are a preference, not a contract).
func parseFavDoc(raw json.RawMessage) []modelFav {
	var doc favDoc
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	return doc.Items
}

// favsLoadCmd reads the favorites document. render marks the result for
// block rendering (a bare /fav) as opposed to a silent list refresh.
func favsLoadCmd(comp *sdk.Component, render bool) tea.Cmd {
	return func() tea.Msg {
		if comp == nil {
			return favsMsg{err: fmt.Errorf("store unreachable"), render: render}
		}
		item, err := comp.StoreGet(favStoreKind, favStoreID, controlTimeout)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return favsMsg{items: []modelFav{}, render: render}
			}
			return favsMsg{err: err, render: render}
		}
		return favsMsg{items: parseFavDoc(item.Value), render: render}
	}
}

// favsMsg reports the favorites list (after a load or a mutation) plus the
// mutation's note, if any.
type favsMsg struct {
	items  []modelFav
	note   string
	render bool
	err    error
}

// favsMutateCmd runs fn against a freshly-read list with optimistic
// concurrency: the put names the revision it based on, and a lost race
// re-reads and retries (bounded — two UIs racing favorites is rare).
func favsMutateCmd(comp *sdk.Component, fn func(items []modelFav) ([]modelFav, string)) tea.Cmd {
	return func() tea.Msg {
		for try := 0; try < favPutTries; try++ {
			if comp == nil {
				return favsMsg{err: fmt.Errorf("store unreachable")}
			}
			var rev int
			var raw json.RawMessage
			item, err := comp.StoreGet(favStoreKind, favStoreID, controlTimeout)
			if err != nil {
				if !strings.Contains(err.Error(), "not found") {
					return favsMsg{err: err}
				}
			} else {
				raw = item.Value
				rev = item.Rev
			}
			items := parseFavDoc(raw)
			updated, note := fn(items)
			_, err = comp.StorePut(favStoreKind, favStoreID, favDoc{
				Items: updated, UpdatedAt: float64(time.Now().UnixMilli()) / 1e3,
			}, rev, controlTimeout)
			if err != nil {
				if strings.Contains(err.Error(), "conflict") {
					continue // lost the race — re-read and try again
				}
				return favsMsg{err: err}
			}
			return favsMsg{items: updated, note: note}
		}
		return favsMsg{err: fmt.Errorf("favorites are being edited elsewhere — try again")}
	}
}

// localFav runs /fav: bare lists, `add` marks the current provider/model,
// `rm <n>` removes, `<n>` applies the numbered entry to this conversation.
func localFav(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	fields := strings.Fields(argument)
	comp, session := m.comp, m.session
	switch {
	case len(fields) == 0:
		m.addBlock(blockMeta, "→ /fav")
		m.syncViewport(true)
		return m, favsLoadCmd(comp, true)

	case fields[0] == "add":
		if !m.runtime.OK || m.runtime.Provider == "" || m.runtime.Model == "" {
			m.contextNote = t(m.loc, "fav.notReady")
			return m, nil
		}
		entry := modelFav{Provider: m.runtime.Provider, Model: m.runtime.Model}
		m.addBlock(blockMeta, "→ /fav add")
		m.syncViewport(true)
		return m, favsMutateCmd(comp, func(items []modelFav) ([]modelFav, string) {
			for _, fav := range items {
				if fav == entry {
					return items, t(m.loc, "fav.exists", entry.Provider, entry.Model)
				}
			}
			return append(items, entry), t(m.loc, "fav.added", entry.Provider, entry.Model)
		})

	case fields[0] == "rm" && len(fields) == 2:
		n, err := strconv.Atoi(fields[1])
		if err != nil || n < 1 {
			m.addBlock(blockError, t(m.loc, "fav.noSuch", fields[1]))
			m.syncViewport(true)
			return m, nil
		}
		m.addBlock(blockMeta, "→ /fav rm "+fields[1])
		m.syncViewport(true)
		return m, favsMutateCmd(comp, func(items []modelFav) ([]modelFav, string) {
			if n > len(items) {
				return items, t(m.loc, "fav.noSuch", strconv.Itoa(n))
			}
			removed := items[n-1]
			return append(items[:n-1], items[n:]...), t(m.loc, "fav.removed", strconv.Itoa(n)) +
				" — " + removed.Provider + " / " + removed.Model
		})

	default:
		n, err := strconv.Atoi(fields[0])
		if err != nil || n < 1 || len(fields) != 1 {
			m.addBlock(blockError, "/fav: expected add, rm <n> or <n>")
			m.syncViewport(true)
			return m, nil
		}
		// The list refresh and the application run together: the refresh
		// keeps m.favorites current, the application reads the list fresh
		// so the numbering matches what the human saw.
		return m, tea.Batch(favsLoadCmd(comp, false), favApplyCmd(comp, session, n))
	}
}

// favApplyCmd applies the nth favorite (1-based) as this conversation's
// provider+model pin.
func favApplyCmd(comp *sdk.Component, session string, n int) tea.Cmd {
	return func() tea.Msg {
		if comp == nil {
			return favApplyMsg{n: n, session: session, err: fmt.Errorf("store unreachable")}
		}
		item, err := comp.StoreGet(favStoreKind, favStoreID, controlTimeout)
		if err != nil {
			return favApplyMsg{n: n, session: session, err: err}
		}
		items := parseFavDoc(item.Value)
		if n > len(items) {
			return favApplyMsg{n: n, session: session,
				err: fmt.Errorf("%s", t(LocaleEN, "fav.noSuch", strconv.Itoa(n)))}
		}
		return favApplyMsg{n: n, session: session, fav: items[n-1]}
	}
}

// favApplyMsg carries a numbered /fav application; the handler runs the
// same provider+model pin /model uses (one persisted selection, no
// inference call).
type favApplyMsg struct {
	n       int
	fav     modelFav
	session string
	err     error
}

// pinFavoriteCmd builds the one-call provider+model pin for an entry,
// reporting the current selection as the rollback point.
func (m model) pinFavoriteCmd(entry modelFav) tea.Cmd {
	prevModel := m.modelOverride
	if prevModel == "" {
		prevModel = m.runtime.Model
	}
	prevProvider := m.providerOverride
	if prevProvider == "" {
		prevProvider = m.runtime.Provider
	}
	return setConversationProviderModelCmd(m.comp, m.session, entry.Provider, entry.Model,
		prevProvider, prevModel)
}

// rotateFavorites is the ctrl+x handler: apply the next favorite in order.
// Value receiver and return — Update's model value must come back intact
// (a pointer receiver would return *model as the tea.Model interface).
func (m model) rotateFavorites() (model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	if m.busy {
		m.contextNote = t(m.loc, "note.betweenTurnsModel")
		return m, nil
	}
	if m.controlPending {
		return m, nil
	}
	idx, ok := nextFavoriteIdx(m.favorites, m.runtime.Provider, m.runtime.Model)
	if !ok {
		m.contextNote = t(m.loc, "fav.none")
		return m, nil
	}
	entry := m.favorites[idx]
	m.controlPending = true
	m.contextNote = t(m.loc, "fav.rotated", strconv.Itoa(idx+1), entry.Provider, entry.Model)
	return m, m.pinFavoriteCmd(entry)
}

// applyFavsMsg stores the fresh list and, for a bare /fav, renders it.
func (m *model) applyFavsMsg(msg favsMsg) {
	if msg.err != nil {
		m.contextNote = t(m.loc, "fav.loadFailed", msg.err.Error())
		return
	}
	m.favorites = msg.items
	m.favoritesLoaded = true
	if msg.note != "" {
		m.contextNote = msg.note
	}
	if !msg.render {
		return
	}
	if len(msg.items) == 0 {
		m.addBlock(blockMeta, t(m.loc, "fav.none"))
		m.syncViewport(true)
		return
	}
	var b strings.Builder
	b.WriteString(t(m.loc, "fav.header", strconv.Itoa(len(msg.items))))
	for i, fav := range msg.items {
		current := ""
		if fav.Provider == m.runtime.Provider && fav.Model == m.runtime.Model {
			current = "  ←"
		}
		fmt.Fprintf(&b, "\n  %d  %s / %s%s", i+1, fav.Provider, fav.Model, current)
	}
	m.addBlock(blockMeta, b.String())
	m.syncViewport(true)
}

// applyFavApplyMsg runs (or reports) a numbered /fav application.
func (m *model) applyFavApplyMsg(msg favApplyMsg) tea.Cmd {
	if msg.session != m.session {
		return nil // conversation switched mid-read: nothing to pin here
	}
	if msg.err != nil {
		m.addBlock(blockError, "/fav: "+msg.err.Error())
		m.syncViewport(true)
		return nil
	}
	m.controlPending = true
	return m.pinFavoriteCmd(msg.fav)
}
