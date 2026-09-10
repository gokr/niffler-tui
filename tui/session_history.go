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

// loadConversationHistory lists a conversation's message documents in
// stored order (ids are zero-padded, so key order == chronological order).
// The store list tool caps a response at 1000 entries — the same cap core's
// resume loader uses.
func loadConversationHistory(comp *sdk.Component, session string) ([]storedMessage, error) {
	var response struct {
		Items []struct {
			Value storedMessage `json:"value"`
		} `json:"items"`
	}
	err := requestInto(comp, "store", "list", map[string]any{
		"kind": "message", "idPrefix": session + ":", "limit": 1000,
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
