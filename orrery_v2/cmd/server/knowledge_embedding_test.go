package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/knowledge/ingest"
)

func TestKnowledgeEmbeddingProviderSelectsProfileAndResolvesSecret(t *testing.T) {
	var gotAuth string
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("embedding path = %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = body.Model
		_, _ = w.Write([]byte(`{"model":"resolved-embedding","data":[{"embedding":[0.2,0.8]}]}`))
	}))
	defer server.Close()

	provider := newKnowledgeEmbeddingProvider(
		llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
			{
				ProfileID: "chat",
				BaseURL:   "https://chat.invalid/v1",
				APIKey:    "sk-chat",
				Model:     "gpt-5-mini",
				Enabled:   true,
			},
			{
				ProfileID: "embedding",
				BaseURL:   server.URL + "/v1",
				APIKeyRef: "secret://llm/embedding",
				Model:     "text-embedding-3-small",
				Enabled:   true,
			},
		}},
		llmprofile.StaticSecretResolver{Values: map[string]string{
			"secret://llm/embedding": "sk-embedding",
		}},
		server.Client(),
	)

	resp, err := provider.Embed(context.Background(), ingest.EmbedRequest{
		ProfileRef: "embedding",
		Inputs:     []string{"hello"},
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if gotAuth != "Bearer sk-embedding" || gotModel != "text-embedding-3-small" {
		t.Fatalf("profile not resolved, auth=%q model=%q", gotAuth, gotModel)
	}
	if resp.Model != "resolved-embedding" || resp.Dimensions != 2 || len(resp.Vectors) != 1 {
		t.Fatalf("embedding response mismatch: %#v", resp)
	}
}
