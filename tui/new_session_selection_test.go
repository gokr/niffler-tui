package main

import "testing"

func TestNewSessionRetainsSelection(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		m := newTestModel()
		m.session = "old"
		m.runtime = runtimeResolution{OK: true, Provider: "chosen", Model: "chosen-model"}
		if pinned {
			m.providerOverride, m.modelOverride = "chosen", "chosen-model"
		}
		updated, cmd := localNew(m, slashCommand{}, "")
		got := updated.(model)
		if cmd == nil || got.session == "old" || !got.sessionNeedsCreate {
			t.Fatalf("new session not initialized: %+v", got)
		}
		if got.providerOverride != "chosen" || got.modelOverride != "chosen-model" {
			t.Fatalf("pinned=%v: selection lost: %q / %q", pinned, got.providerOverride, got.modelOverride)
		}
		msg := inheritNewSessionSelection(bootstrapMsg{Session: got.session}, got.providerOverride, got.modelOverride, got.runtime)
		// The normal bootstrap handler must not erase the inherited selection.
		msg.Providers.Providers = []providerSummary{{Nickname: "chosen"}}
		updated, _ = got.Update(msg)
		got = updated.(model)
		if got.providerOverride != "chosen" || got.modelOverride != "chosen-model" || got.runtime.Model != "chosen-model" {
			t.Fatalf("bootstrap erased inherited selection: %q / %q", got.providerOverride, got.modelOverride)
		}
	}
}

func TestNewSessionInheritanceDoesNotOverwriteExistingHeader(t *testing.T) {
	msg := bootstrapMsg{ConversationExists: true}
	msg.Conversation.ProviderOverride = "saved"
	msg.Conversation.ModelOverride = "saved-model"
	msg.Runtime = runtimeResolution{Provider: "saved", Model: "saved-model"}
	got := inheritNewSessionSelection(msg, "outgoing", "outgoing-model", runtimeResolution{})
	if got.Conversation.ProviderOverride != "saved" || got.Conversation.ModelOverride != "saved-model" || got.Runtime.Model != "saved-model" {
		t.Fatalf("existing header overwritten: %+v", got)
	}
}

func TestExplicitNewIDLoadsItsOwnSelection(t *testing.T) {
	m := newTestModel()
	m.modelOverride = "outgoing-model"
	updated, _ := localNew(m, slashCommand{}, "existing")
	if got := updated.(model); got.session != "existing" || got.modelOverride != "" {
		t.Fatalf("explicit session inherited outgoing model: %q / %q", got.session, got.modelOverride)
	}
}
