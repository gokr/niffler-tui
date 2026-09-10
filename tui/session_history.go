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
}

// storedToolCall is one entry of an assistant message's tool_calls array in
// the normalized OpenAI shape core persists.
type storedToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
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
// The store list tool returns the FIRST page and has no cursor, so a
// conversation that fills the page would otherwise be replayed from its
// oldest messages. When the first page is full we binary-search the padded
// id space for the highest stored sequence (tiny one-item probes) and fetch
// the tail window instead: resuming a long conversation must land on its
// latest messages. Older messages stay available in the store for core.
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
	if start := historyTailStart(last); start != "" {
		return fetch(start, historyPageSize)
	}
	return messages, nil // the full page was already the whole history
}

// historyTailStart returns the id suffix of the tail window ending at last,
// or "" when the first page already covers the conversation.
func historyTailStart(last int) string {
	if last <= historyPageSize {
		return ""
	}
	return seqPrefix(last - historyPageSize + 1)
}

// listConversationMessages fetches one page of a conversation's messages
// starting at the given id suffix ("" = from the first message).
func listConversationMessages(comp *sdk.Component, session, suffix string, limit int) ([]storedMessage, error) {
	var response struct {
		Items []struct {
			Value storedMessage `json:"value"`
		} `json:"items"`
	}
	err := requestInto(comp, "store", "list", map[string]any{
		"kind": "message", "idPrefix": session + ":" + suffix, "limit": limit,
	}, &response)
	if err != nil {
		return nil, err
	}
	messages := make([]storedMessage, 0, len(response.Items))
	for _, item := range response.Items {
		messages = append(messages, item.Value)
	}
	return messages, nil
}

// lastMessageSeq finds the highest stored message sequence for a
// conversation. The list tool returns the first item at-or-after a prefix,
// which makes "an item exists >= N" monotone in N: a binary search over the
// six-digit sequence space needs ~20 one-item probes. Sequences beyond
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
