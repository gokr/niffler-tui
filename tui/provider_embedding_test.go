package main

import "testing"

func TestEmbeddingOnlyProviderForm(t *testing.T) {
	f := newProviderForm(nil, runtimeResolution{}, 100, LocaleEN)
	f.inputs[providerFieldNickname].SetValue("localnomic")
	f.inputs[providerFieldBaseURL].SetValue("http://127.0.0.1:18100/v1")
	f.inputs[providerFieldEmbedModel].SetValue("local-nomic-q4")
	f.setServedModels([]string{"local-nomic-q4"})
	m := newTestModel()
	if m.providerActionChangedProvider(providerActionMsg{Action: "add", Nickname: "localnomic", EmbeddingOnly: true}) {
		t.Fatal("embedding add would clear chat pin")
	}
	v, err := f.values()
	if err != nil {
		t.Fatal(err)
	}
	if v.Model != "" || v.APIKey != "" || v.EmbedModel != "local-nomic-q4" {
		t.Fatalf("unexpected values: %#v", v)
	}
	f.inputs[providerFieldBaseURL].SetValue("https://remote.example/v1")
	if _, err = f.values(); err == nil {
		t.Fatal("remote keyless endpoint accepted")
	}
}

func TestEmbeddingProviderEditRoundTrip(t *testing.T) {
	f := newEditProviderForm(providerSummary{Nickname: "synthetic", BaseURL: "https://api.example/v1", Model: "chat", EmbedModel: "nomic", EmbedBaseURL: "http://localhost:18100/v1"}, 100, LocaleEN)
	v, err := f.values()
	if err != nil {
		t.Fatal(err)
	}
	if v.EmbedModel != "nomic" || v.EmbedBaseURL != "http://localhost:18100/v1" {
		t.Fatalf("embedding fields lost: %#v", v)
	}
	f.inputs[providerFieldEmbedBaseURL].SetValue("file:///secret")
	if _, err = f.values(); err == nil {
		t.Fatal("invalid embedding URL accepted")
	}
}
