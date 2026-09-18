// Stored conversation history: startup and /session switching rebuild the
// output area from the persisted message documents (store kind "message",
// id <convId>:<seq>) so the previous conversation is visible and
// scrollable instead of starting blank.
//
// The documents are core's persistMsg payloads: the OpenAI message plus
// storage-only telemetry. Assistant rounds keep their tool_calls (id, name,
// arguments), the following tool messages keep the outcomes, so tool cards
// come back with args and grouped runs — the same shape the live event
// stream produces — rather than one flat line per tool message.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	sdk "niffler.dev/sdk"
)

// storedMessage is one persisted conversation message. Content is null on
// pure tool-call assistant rounds (decodes to ""); reasoning and tool_calls
// are optional. Unknown telemetry fields (turnId, durationMs, ...) are
// ignored.
type storedMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	Reasoning  string           `json:"reasoning"`
	ToolCallID string           `json:"tool_call_id"`
	Name       string           `json:"name"`
	ToolCalls  []storedToolCall `json:"tool_calls"`
	DurationMs int              `json:"durationMs"`
	Notice     json.RawMessage  `json:"notice"`
}

// storedToolFunction is a stored tool call's function object (the normalized
// OpenAI shape core persists).
type storedToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// storedToolCall is one entry of an assistant message's tool_calls array in
// the normalized OpenAI shape core persists.
type storedToolCall struct {
	ID       string             `json:"id"`
	Function storedToolFunction `json:"function"`
}

// conversationHistoryMsg carries one conversation's replayed transcript.
// Session+Gen identify the load; a response that lands after a further
// session switch (or after A→B→A) is dropped. Anchor is the block index the
// replay must be inserted at — recorded when the load started, so a message
// the user sends while the store read is in flight stays below the history
// instead of being overwritten.
type conversationHistoryMsg struct {
	Session string
	Gen     int
	Anchor  int
	Blocks  []transcriptBlock
	Err     error
}

// historyPageSize is the store list tool's cap: one response may carry at
// most this many documents (core's resume loader uses the same bound, and a
// bigger response could outgrow the bus payload).
const historyPageSize = 1000

// loadConversationHistory lists a conversation's message documents in
// stored order (ids are zero-padded, so key order == chronological order).
//
// A single store page carries at most historyPageSize documents, so a
// conversation that fills the page would otherwise be replayed from its
// oldest messages. When the first page is full we binary-search the padded
// id space for the highest stored sequence (tiny one-item probes) and read
// the tail window with the store's id cursor: resuming a long conversation
// must land on its latest messages. Older messages stay available in the
// store for core (and for recall).
func loadConversationHistory(comp *sdk.Component, session string) ([]storedMessage, error) {
	fetch := func(prefix string, limit int) ([]storedMessage, error) {
		return listConversationMessages(comp, session, prefix, limit)
	}
	messages, err := fetch("", historyPageSize)
	if err != nil {
		return nil, err
	}
	if len(messages) < historyPageSize {
		return messages, nil // complete history
	}
	last, err := lastMessageSeq(fetch)
	if err != nil {
		return nil, err
	}
	if cursor := tailCursor(session, last, historyPageSize); cursor != "" {
		window, err := listConversationMessagesAfter(comp, session, cursor, historyPageSize)
		if err != nil {
			return nil, err
		}
		if len(window) > 0 {
			return window, nil
		}
	}
	return messages, nil // the full page was already the whole history
}

// tailCursor is the store cursor that starts the window of `size` messages
// ending at `last`: the id of the message one sequence BEFORE the window, which
// is what the store's exclusive `after` takes. It is "" when a read from the
// beginning already covers the conversation.
//
// The window is selected by cursor and never by a longer idPrefix: the store's
// `idPrefix` is a literal prefix scan ("<conv>:000015" matches that one
// message, not everything from it on), so a prefix-built window silently
// returned a single message — long replayed conversations showed their
// thousandth message instead of their last thousand (docs/MANUAL.md "Reading
// a conversation").
func tailCursor(session string, last, size int) string {
	if size <= 0 || last <= size {
		return ""
	}
	return session + ":" + seqPrefix(last-size)
}

// conversationMessagePage is one store page of a conversation's messages plus
// the cursor that continues past it. The cursor is the id of the last item
// read — the store's own exclusive `after` contract; `nextAfter` is only
// reported while another page exists, so the last page must take it from the
// item.
type conversationMessagePage struct {
	Messages []storedMessage
	Cursor   string
	HasMore  bool
}

// listConversationPage fetches one page of a conversation's messages: from a
// sequence suffix ("" = from the first message), and/or continuing past an id
// cursor ("" = no cursor). Both selectors compose, which is what lets the
// ctrl+o pane read a window once and afterwards only what landed since.
func listConversationPage(comp *sdk.Component, session, suffix, after string, limit int) (conversationMessagePage, error) {
	var response struct {
		Items []struct {
			ID    string        `json:"id"`
			Value storedMessage `json:"value"`
		} `json:"items"`
		HasMore bool `json:"hasMore"`
	}
	args := map[string]any{
		"kind": "message", "idPrefix": session + ":" + suffix, "limit": limit,
	}
	if after != "" {
		args["after"] = after
	}
	if err := requestInto(comp, "store", "list", args, &response); err != nil {
		return conversationMessagePage{}, err
	}
	page := conversationMessagePage{HasMore: response.HasMore}
	for _, item := range response.Items {
		page.Messages = append(page.Messages, item.Value)
		page.Cursor = item.ID
	}
	return page, nil
}

// listConversationMessages is the page primitive for callers that only want
// the messages (the transcript replay and the sequence probes).
func listConversationMessages(comp *sdk.Component, session, suffix string, limit int) ([]storedMessage, error) {
	page, err := listConversationPage(comp, session, suffix, "", limit)
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

// listConversationMessagesAfter continues a conversation's read from an id
// cursor — the form that selects a range (the prefix form selects only keys
// starting with the literal prefix).
func listConversationMessagesAfter(comp *sdk.Component, session, after string, limit int) ([]storedMessage, error) {
	page, err := listConversationPage(comp, session, "", after, limit)
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

// lastMessageSeq finds the highest stored message sequence for a
// conversation. Message ids are contiguous from 1 and padded to six digits, so
// the store's literal-prefix probe for sequence N answers with message N
// exactly while N <= last: "message N exists" is monotone in N, and a binary
// search over the sequence space needs ~20 one-item probes. Sequences beyond
// 999999 are out of scope at conversation scale.
func lastMessageSeq(fetch func(prefix string, limit int) ([]storedMessage, error)) (int, error) {
	lo, hi := 0, 999_999
	for lo < hi {
		mid := (lo + hi + 1) / 2
		items, err := fetch(seqPrefix(mid), 1)
		if err != nil {
			return 0, err
		}
		if len(items) > 0 {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, nil
}

// seqPrefix renders a message sequence as the zero-padded id suffix core
// uses (six digits), so lexicographic key order matches numeric order.
func seqPrefix(seq int) string {
	return fmt.Sprintf("%06d", seq)
}

func conversationHistoryCmd(comp *sdk.Component, session string, gen, anchor int) tea.Cmd {
	return func() tea.Msg {
		messages, err := loadConversationHistory(comp, session)
		if err != nil {
			return conversationHistoryMsg{Session: session, Gen: gen, Anchor: anchor, Err: err}
		}
		return conversationHistoryMsg{
			Session: session, Gen: gen, Anchor: anchor,
			Blocks: replayConversation(messages),
		}
	}
}

// startHistoryLoad stamps a replay request for the current session and
// records the current block count as the insertion anchor. Pointer
// receiver: the generation must advance on the model that is returned from
// Update, or a stale reply could still match.
func (m *model) startHistoryLoad() tea.Cmd {
	m.historyGen++
	return conversationHistoryCmd(m.comp, m.session, m.historyGen, len(m.blocks))
}

// switchSessionWithHistory switches to another conversation and queues its
// stored transcript replay, so /session (and the browser's session entries)
// show the previous conversation instead of a blank output area.
func (m model) switchSessionWithHistory(id string) (model, tea.Cmd) {
	// Ownership: claim the target for this UI before switching. A live UI
	// already holding the conversation is reported instead of silently
	// joining its stream (core's ui registry; coordination only — when the
	// registry is unreachable the switch proceeds uncoordinated). Our
	// previous claim is released only after the new one is secured, so a
	// failed switch never leaves us conversationless.
	if m.uiID != "" && m.comp != nil {
		claimed, ownerNumber, err := uiClaimRequest(m.comp, m.uiID, id)
		if err == nil && !claimed {
			m.addBlock(blockMeta, fmt.Sprintf(
				"Open in Niffler %d — pick another conversation or run /new.", ownerNumber))
			m.syncViewport(true)
			return m, nil
		}
		if err == nil && m.session != "" && m.session != id {
			_, _ = m.comp.Request("core", "ui", map[string]any{
				"op": "release_session", "ui": m.uiID, "session": m.session},
				controlTimeout)
		}
	}
	m = m.switchSession(id)
	cmd := m.startHistoryLoad()
	return m, cmd
}

// replayConversation converts stored messages into transcript blocks in
// stored order: user content, assistant reasoning/answers, tool cards, and
// turn errors. Replayed blocks are finalized so a later turn's token stream
// can never append into them.
func replayConversation(messages []storedMessage) []transcriptBlock {
	// A scratch model reuses the live block bookkeeping (appendToolCall and
	// completeToolCall), so replayed tool runs group and match exactly like
	// streamed ones.
	var scratch model
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			if msg.Content == "" {
				continue
			}
			if msg.Notice != nil {
				// Runtime machinery the runner folded in (subagent-settled,
				// process-exited, or a wake turn's prompt): render dim and
				// attributed, never as a plain user bubble (docs/WIRE.md
				// "Settlement notices").
				scratch.addBlock(blockNotice, msg.Content)
				continue
			}
			scratch.addBlock(blockUser, msg.Content)

		case "assistant":
			if strings.TrimSpace(msg.Reasoning) != "" {
				scratch.blocks = append(scratch.blocks, transcriptBlock{
					kind: blockThinking, text: msg.Reasoning, finalized: true,
				})
			}
			if msg.Content != "" {
				scratch.blocks = append(scratch.blocks, transcriptBlock{
					kind: blockAssistant, text: msg.Content, finalized: true,
				})
			}
			for _, call := range msg.ToolCalls {
				scratch.appendToolCall(toolCall{
					name:    call.Function.Name,
					args:    storedToolArgs(call.Function.Arguments),
					callID:  call.ID,
					pending: true, // completed by the following tool message
				})
			}

		case "tool":
			result, errText := storedToolOutcome(msg.Content)
			if !scratch.completeToolCall(msg.ToolCallID, msg.Name, nil, result, errText, msg.DurationMs) {
				// No pending call matched (legacy entry, or the list was
				// capped before the requesting assistant message): keep the
				// outcome visible as an already-complete card.
				scratch.appendToolCall(toolCall{
					name: msg.Name, callID: msg.ToolCallID,
					result: result, err: errText,
				})
			}

		case "error":
			// Turn errors are audit records, not provider messages; the
			// live transcript renders them as error blocks.
			if msg.Content != "" {
				scratch.addBlock(blockError, msg.Content)
			}
		}
	}
	return scratch.blocks
}

// storedToolArgs turns the tool_calls arguments string (JSON encoded inside
// the stored message) into the raw JSON the live events carry. Empty or
// invalid arguments yield nil — the card then shows no preview rather than
// a bogus fragment.
func storedToolArgs(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" || !json.Valid([]byte(arguments)) {
		return nil
	}
	return json.RawMessage(arguments)
}

// storedToolOutcome splits a persisted tool message's content into the
// result and error the card renders. Core encodes failures as
// "ERROR: <message>" in the content (the document carries no error field).
func storedToolOutcome(content string) (json.RawMessage, string) {
	const prefix = "ERROR: "
	if strings.HasPrefix(content, prefix) {
		return nil, strings.TrimPrefix(content, prefix)
	}
	if content == "" {
		return nil, ""
	}
	return json.RawMessage(content), ""
}
