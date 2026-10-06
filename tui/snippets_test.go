package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestSnippetIDOrdersChronologically(t *testing.T) {
	early := snippetID(time.Now().Add(-time.Minute))
	late := snippetID(time.Now())
	if early >= late {
		t.Fatalf("snippet ids do not sort chronologically: %q >= %q", early, late)
	}
	if !strings.HasPrefix(early, "snippet-") {
		t.Fatalf("snippet id %q lacks the kind prefix", early)
	}
}

func TestParseSnippetsArg(t *testing.T) {
	if action, n, err := parseSnippetsArg(""); action != "" || n != 0 || err != nil {
		t.Fatalf("bare argument = (%q, %d, %v), want a plain list", action, n, err)
	}
	cases := []struct {
		argument string
		action   string
		n        int
		wantErr  bool
	}{
		{"show 2", "show", 2, false},
		{"INS 1", "ins", 1, false},
		{"rm 3", "del", 3, false},
		{"del 0", "", 0, true},
		{"ins", "", 0, true},
		{"show x", "", 0, true},
		{"bogus 1", "", 0, true},
	}
	for _, tc := range cases {
		action, n, err := parseSnippetsArg(tc.argument)
		if tc.wantErr && err == nil {
			t.Fatalf("parseSnippetsArg(%q) = (%q, %d, nil), want an error", tc.argument, action, n)
		}
		if !tc.wantErr && (err != nil || action != tc.action || n != tc.n) {
			t.Fatalf("parseSnippetsArg(%q) = (%q, %d, %v), want (%q, %d, nil)", tc.argument, action, n, err, tc.action, tc.n)
		}
	}
}

func snippetsTestModel(t *testing.T) model {
	t.Helper()
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.session = "s1"
	m.connected = true
	m.layout()
	return m
}

func TestApplySnippetsListsNewestFirst(t *testing.T) {
	m := snippetsTestModel(t)
	older := snippetDoc{ID: "snippet-001", Value: snippetValue{Text: "older text", Session: "s1"}}
	newer := snippetDoc{ID: "snippet-002", Value: snippetValue{
		Text: "newer text\nsecond line", Session: "s1", CreatedAt: float64(time.Now().UnixMilli()) / 1e3,
	}}

	m.applySnippetsMsg(snippetsMsg{docs: []snippetDoc{older, newer}, session: "s1"})
	last := m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "2 snippet") {
		t.Fatalf("list header missing: %q", last)
	}
	// Entry 1 is the newest, and multi-line texts preview as one line.
	if !strings.Contains(last, "1  ") || !strings.Contains(last, "newer text …") {
		t.Fatalf("newest entry not first: %q", last)
	}
	if strings.Contains(last, "second line") {
		t.Fatalf("list preview should stop at the first line: %q", last)
	}
	// Entries from other conversations are marked, not hidden.
	foreign := snippetDoc{ID: "snippet-003", Value: snippetValue{Text: "elsewhere", Session: "s9"}}
	m.applySnippetsMsg(snippetsMsg{docs: []snippetDoc{foreign}, session: "s1"})
	last = m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "(from s9)") {
		t.Fatalf("foreign snippet not marked: %q", last)
	}
}

func TestApplySnippetsInsertsIntoInput(t *testing.T) {
	m := snippetsTestModel(t)
	doc := snippetDoc{ID: "snippet-001", Value: snippetValue{Text: "collected text"}}

	m.applySnippetsMsg(snippetsMsg{action: "ins", n: 1, docs: []snippetDoc{doc}, session: "s1"})
	if got := m.input.Value(); got != "collected text" {
		t.Fatalf("input = %q after ins, want the snippet text", got)
	}

	// A second ins joins what is already there.
	m.applySnippetsMsg(snippetsMsg{action: "ins", n: 1, docs: []snippetDoc{doc}, session: "s1"})
	if got := m.input.Value(); got != "collected text collected text" {
		t.Fatalf("input = %q after a second ins", got)
	}
}

func TestApplySnippetsShowAndDelAndGuard(t *testing.T) {
	m := snippetsTestModel(t)
	doc := snippetDoc{ID: "snippet-001", Value: snippetValue{Text: "full text body"}}

	m.applySnippetsMsg(snippetsMsg{action: "show", n: 1, docs: []snippetDoc{doc}, session: "s1"})
	if got := m.blocks[len(m.blocks)-1].text; got != "full text body" {
		t.Fatalf("show rendered %q", got)
	}

	m.applySnippetsMsg(snippetsMsg{action: "del", n: 1, docs: []snippetDoc{doc},
		deletedID: doc.ID, session: "s1"})
	if !strings.Contains(m.contextNote, "1") {
		t.Fatalf("del did not note the entry: %q", m.contextNote)
	}

	// A result for another conversation is dropped entirely.
	before := len(m.blocks)
	m.applySnippetsMsg(snippetsMsg{docs: []snippetDoc{doc}, session: "s2"})
	if len(m.blocks) != before {
		t.Fatal("a stale /snippets result changed this conversation")
	}

	// Out-of-range entries say so.
	m.applySnippetsMsg(snippetsMsg{action: "show", n: 5, docs: []snippetDoc{doc}, session: "s1"})
	last := m.blocks[len(m.blocks)-1].text
	if !strings.Contains(last, "5") {
		t.Fatalf("missing entry not reported: %q", last)
	}
}

func TestAltDragSavesSnippetBeyondTheCopy(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.layout()
	m.addBlock(blockMeta, "collect https://example.com/a later")
	m.syncViewport(true)
	row := renderedRowContaining(m.View().Content, "https://example.com/a")

	// alt+drag: the release command must carry a snippet save alongside the
	// clipboard copies.
	updated, _ := m.Update(tea.MouseClickMsg{X: 6, Y: row, Button: tea.MouseLeft, Mod: tea.ModAlt})
	m = updated.(model)
	updated, _ = m.Update(tea.MouseMotionMsg{X: 24, Y: row, Button: tea.MouseLeft, Mod: tea.ModAlt})
	m = updated.(model)
	updated, cmd := m.Update(tea.MouseReleaseMsg{X: 24, Y: row, Button: tea.MouseLeft, Mod: tea.ModAlt})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("alt+drag produced no command")
	}
	if !batchCarriesSnippetSave(cmd()) {
		t.Fatal("alt+drag release did not carry a snippet save")
	}

	// A plain drag copies but does not collect.
	m.clearMouseSelection()
	updated, _ = m.Update(tea.MouseClickMsg{X: 6, Y: row, Button: tea.MouseLeft})
	m = updated.(model)
	updated, cmd = m.Update(tea.MouseMotionMsg{X: 24, Y: row, Button: tea.MouseLeft})
	m = updated.(model)
	updated, cmd = m.Update(tea.MouseReleaseMsg{X: 24, Y: row, Button: tea.MouseLeft})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("plain drag produced no clipboard command")
	}
	if batchCarriesSnippetSave(cmd()) {
		t.Fatal("plain drag collected a snippet")
	}
}

// batchCarriesSnippetSave executes a command's message (walking a batch)
// and reports whether a snippetSavedMsg came out.
func batchCarriesSnippetSave(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case snippetSavedMsg:
		return true
	case tea.BatchMsg:
		for _, cmd := range msg {
			if batchCarriesSnippetSave(cmd()) {
				return true
			}
		}
	}
	return false
}
