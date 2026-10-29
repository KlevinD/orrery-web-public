package profilemodel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestPromptCacheKeyIsExplicitAndDoesNotLeakBetweenModels(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{Client: server.Client(), Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "test-only", Model: "test", Enabled: true}}}})
	for _, key := range []string{"", "orrery-scoped-session", ""} {
		chat, err := factory.ModelForRequest(context.Background(), ModelRequest{PromptCacheKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Generate(context.Background(), []*llm.Message{llm.UserMessage("hello")}); err != nil {
			t.Fatal(err)
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("requests=%d", len(bodies))
	}
	if _, exists := bodies[0]["prompt_cache_key"]; exists {
		t.Fatal("cache enabled by default")
	}
	if bodies[1]["prompt_cache_key"] != "orrery-scoped-session" {
		t.Fatal("scoped cache key not forwarded")
	}
	if _, exists := bodies[2]["prompt_cache_key"]; exists {
		t.Fatal("cache identity leaked to another model instance")
	}
}

func TestModelIdentityIncludesResolvedProfileAndCredential(t *testing.T) {
	profile := llmprofile.LLMProfile{ProfileID: "stable-id", Provider: "openai", BaseURL: "https://models.example/v1", Model: "model-a", Enabled: true}
	m := &openAICompatibleChatModel{profile: profile, apiKey: "credential-a"}
	original := m.ModelIdentity()
	m.promptCacheKey = "different-session"
	if m.ModelIdentity() != original || len(original) != 64 {
		t.Fatal("session-specific data changed model identity")
	}
	m.apiKey = "credential-b"
	if m.ModelIdentity() == original {
		t.Fatal("credential rotation did not invalidate model identity")
	}
	m.apiKey = "credential-a"
	m.profile.Model = "model-b"
	if m.ModelIdentity() == original {
		t.Fatal("profile model update did not invalidate identity")
	}
}
