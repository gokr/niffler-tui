// Snippets: transcript text collected with alt+drag (a plain drag only
// copies; alt additionally saves). The selection is stored in the Niffler
// store as a kind "snippet" document — chronological ids, the source
// conversation and a creation time in the value — so the human recalls the
// collection with /snippets and the model reads the same documents through
// the store's own tools (store.list with kind "snippet", reachable via
// discover + invoke) without any new harness-side contract.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"niffler.dev/sdk"
)

// snippetValue is the stored document: what was selected and where from.
type snippetValue struct {
	Text      string  `json:"text"`
	Session   string  `json:"session,omitempty"`
	Source    string  `json:"source,omitempty"`
	CreatedAt float64 `json:"createdAt,omitempty"`
}

// snippetDoc pairs a store item id with its decoded value.
type snippetDoc struct {
	ID    string
	Value snippetValue
}

// snippetID builds the store id: "snippet-" + fixed-width hex nanoseconds.
// Lexicographic id order is therefore chronological, and the store's list
// (ascending by id) hands the collection back oldest-first.
func snippetID(at time.Time) string {
	return fmt.Sprintf("snippet-%016x", at.UnixNano())
}

// saveSnippetCmd persists the collected text. Best effort: a store failure
// surfaces as a note, and the clipboard copy has already happened either
// way.
func (m model) saveSnippetCmd(text string) tea.Cmd {
	comp, session := m.comp, m.session
	now := time.Now()
	value := snippetValue{
		Text:      text,
		Session:   session,
		Source:    "tui-alt-select",
		CreatedAt: float64(now.UnixMilli()) / 1e3,
	}
	id := snippetID(now)
	return func() tea.Msg {
		if comp == nil {
			return snippetSavedMsg{err: errors.New("store unreachable")}
		}
		_, err := comp.StorePut("snippet", id, value, 0, controlTimeout)
		return snippetSavedMsg{id: id, err: err}
	}
}

// snippetSavedMsg reports an alt+drag snippet save.
type snippetSavedMsg struct {
	id  string
	err error
}

// snippetsMsg carries a resolved /snippets subcommand: the fetched
// documents plus the action to apply to entry n (1-based, newest first).
type snippetsMsg struct {
	action    string // "", "show", "ins", "del"
	n         int
	docs      []snippetDoc
	deletedID string // set after a successful del
	session   string // conversation id at invocation time
	err       error
}

// localSnippets runs /snippets: bare lists the collection, show/ins/del <n>
// act on the numbered entry (1 = newest). Every variant reads the store
// fresh, so the numbering always matches the list the human last saw.
func localSnippets(m model, cmd slashCommand, argument string) (tea.Model, tea.Cmd) {
	if !m.connected {
		m.contextNote = t(m.loc, "note.notConnected")
		return m, nil
	}
	action, n, err := parseSnippetsArg(argument)
	if err != nil {
		m.addBlock(blockError, "/snippets: "+err.Error())
		m.syncViewport(true)
		return m, nil
	}
	m.addBlock(blockMeta, "→ /snippets"+slashArgText(argument))
	m.syncViewport(true)
	comp, session := m.comp, m.session
	return m, func() tea.Msg {
		docs, err := loadSnippets(comp)
		if err != nil {
			return snippetsMsg{action: action, n: n, session: session, err: err}
		}
		if action == "del" {
			ordered := newestFirstSnippets(docs)
			doc, ok := pickSnippet(ordered, n)
			if !ok {
				return snippetsMsg{action: action, n: n, docs: docs, session: session,
					err: errors.New(t(LocaleEN, "snippets.noSuch", strconv.Itoa(n)))}
			}
			derr := comp.StoreDel("snippet", doc.ID, controlTimeout)
			return snippetsMsg{action: action, n: n, docs: docs, session: session,
				deletedID: doc.ID, err: derr}
		}
		return snippetsMsg{action: action, n: n, docs: docs, session: session}
	}
}

// parseSnippetsArg splits the argument into an action and the 1-based
// entry number. A bare /snippets lists; anything else must be
// show|ins|del|rm <n>.
func parseSnippetsArg(argument string) (action string, n int, err error) {
	fields := strings.Fields(argument)
	if len(fields) == 0 {
		return "", 0, nil
	}
	action = strings.ToLower(fields[0])
	if action == "rm" {
		action = "del"
	}
	if action != "show" && action != "ins" && action != "del" {
		return "", 0, errors.New("expected show|ins|del <n> — a bare /snippets lists the collection")
	}
	if len(fields) != 2 {
		return "", 0, fmt.Errorf("%s needs an entry number", action)
	}
	n, convErr := strconv.Atoi(fields[1])
	if convErr != nil || n < 1 {
		return "", 0, fmt.Errorf("%s needs an entry number", action)
	}
	return action, n, nil
}

// loadSnippets reads every snippet document (pageStoreList bounds the
// read) and decodes the values, oldest first.
func loadSnippets(comp *sdk.Component) ([]snippetDoc, error) {
	raws, err := pageStoreList(comp, "snippet")
	if err != nil {
		return nil, err
	}
	docs := make([]snippetDoc, 0, len(raws))
	for _, raw := range raws {
		var row struct {
			ID    string          `json:"id"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		var value snippetValue
		if json.Unmarshal(row.Value, &value) != nil {
			continue
		}
		docs = append(docs, snippetDoc{ID: row.ID, Value: value})
	}
	return docs, nil
}

// newestFirstSnippets reverses the store's id order into the display
// order: entry 1 is the most recently collected snippet.
func newestFirstSnippets(docs []snippetDoc) []snippetDoc {
	ordered := make([]snippetDoc, len(docs))
	for i, doc := range docs {
		ordered[len(docs)-1-i] = doc
	}
	return ordered
}

// pickSnippet returns the 1-based entry of a newest-first list.
func pickSnippet(ordered []snippetDoc, n int) (snippetDoc, bool) {
	if n < 1 || n > len(ordered) {
		return snippetDoc{}, false
	}
	return ordered[n-1], true
}

// applySnippetsMsg renders a /snippets result in the transcript.
func (m *model) applySnippetsMsg(msg snippetsMsg) {
	if msg.session != m.session {
		return // conversation switched mid-fetch: the result belongs elsewhere
	}
	if msg.err != nil {
		m.addBlock(blockError, "/snippets: "+msg.err.Error())
		m.syncViewport(true)
		return
	}
	ordered := newestFirstSnippets(msg.docs)
	switch msg.action {
	case "":
		if len(ordered) == 0 {
			m.addBlock(blockMeta, t(m.loc, "snippets.none"))
			break
		}
		var b strings.Builder
		b.WriteString(t(m.loc, "snippets.header", strconv.Itoa(len(ordered))))
		for i, doc := range ordered {
			from := ""
			if doc.Value.Session != "" && doc.Value.Session != m.session {
				from = "  (from " + doc.Value.Session + ")"
			}
			fmt.Fprintf(&b, "\n  %d  %s  %s%s", i+1, snippetAge(doc), snippetPreview(doc), from)
		}
		m.addBlock(blockMeta, b.String())
	case "show":
		doc, ok := pickSnippet(ordered, msg.n)
		if !ok {
			m.addBlock(blockError, t(m.loc, "snippets.noSuch", strconv.Itoa(msg.n)))
			break
		}
		m.addBlock(blockMeta, doc.Value.Text)
	case "ins":
		doc, ok := pickSnippet(ordered, msg.n)
		if !ok {
			m.addBlock(blockError, t(m.loc, "snippets.noSuch", strconv.Itoa(msg.n)))
			break
		}
		text := doc.Value.Text
		if cur := m.input.Value(); cur != "" && !strings.HasSuffix(cur, " ") &&
			!strings.HasSuffix(cur, "\n") {
			text = " " + text
		}
		m.input.InsertString(text)
	case "del":
		if msg.deletedID != "" {
			m.contextNote = t(m.loc, "snippets.deleted", strconv.Itoa(msg.n))
			m.addBlock(blockMeta, "✓ "+t(m.loc, "snippets.deleted", strconv.Itoa(msg.n)))
		}
	}
	m.syncViewport(true)
}

// snippetAge renders the age of a snippet as an elapsed duration (empty
// when the record carries no usable timestamp).
func snippetAge(doc snippetDoc) string {
	if doc.Value.CreatedAt <= 0 {
		return "—"
	}
	d := time.Since(time.UnixMilli(int64(doc.Value.CreatedAt * 1e3)))
	return fmtElapsed(d)
}

// snippetPreview renders a list line: the first line of the text, cut to a
// bounded cell width.
func snippetPreview(doc snippetDoc) string {
	line := doc.Value.Text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i] + " …"
	}
	line = strings.TrimSpace(line)
	const maxCells = 72
	if ansi.StringWidth(line) > maxCells {
		line = ansi.Cut(line, 0, maxCells-1) + "…"
	}
	return line
}
