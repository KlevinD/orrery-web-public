package profilemodel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestManagedContextPreservesHistoryAndUsesTheActualModelWindow(t *testing.T) {
	m := &openAICompatibleChatModel{managedContext: true, profile: llmprofile.LLMProfile{ContextLength: 64000}}
	messages := []*llm.Message{llm.SystemMessage(strings.Repeat("policy ", 100)), llm.UserMessage(strings.Repeat("original evidence ", 3000)), llm.UserMessage("continue")}
	request, err := m.buildRequest(messages)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Messages, openAICompatibleMessages(messages)) {
		t.Fatal("managed history was silently trimmed")
	}
	usage, err := m.ContextUsage(messages, nil)
	if err != nil || usage.ContextWindowTokens != 64000 || usage.InputTokenLimit != 64000-4096 {
		t.Fatalf("model window ignored: %+v %v", usage, err)
	}
	m.profile.ContextLength = 1024
	if _, err := m.buildRequest(messages); !errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("oversized managed history accepted: %v", err)
	}
}

func TestManagedContextAccountsForToolSchemasAndRejectsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{Client: server.Client(), Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Model: "test", BaseURL: server.URL, APIKey: "test", Enabled: true, ContextLength: 1024}}}})
	chat, err := factory.ModelForRequest(context.Background(), ModelRequest{ManagedContext: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = chat.Generate(context.Background(), []*llm.Message{llm.UserMessage("short")}, llm.WithTools([]*llm.Tool{{Name: "lookup", Description: strings.Repeat("large schema ", 1000)}}))
	if !errors.Is(err, ErrContextWindowExceeded) || requests.Load() != 0 {
		t.Fatalf("tool schema exceeded window: requests=%d err=%v", requests.Load(), err)
	}
}

func TestContextWindowErrorsAreTypedOnlyForExplicitProviderCodes(t *testing.T) {
	for _, test := range []struct {
		status int
		body   string
		window bool
	}{
		{400, `{"error":{"code":"context_length_exceeded"}}`, true},
		{422, `{"error":{"type":"context_window_exceeded"}}`, true},
		{400, `{"error":{"message":"unrelated bad request"}}`, false},
		{401, `{"error":{"code":"context_length_exceeded"}}`, false},
		{500, `{"error":{"code":"context_length_exceeded"}}`, false},
	} {
		if got := isContextWindowError(test.status, []byte(test.body)); got != test.window {
			t.Fatalf("status=%d body=%s got=%v", test.status, test.body, got)
		}
	}
}

func TestJSONCompletionPreservesCachedAndReasoningUsage(t *testing.T) {
	var completion openAICompatibleChatCompletionResponse
	if err := json.Unmarshal([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":10}}}`), &completion); err != nil {
		t.Fatal(err)
	}
	message, err := messageFromOpenAICompatibleCompletion(completion)
	if err != nil || message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 80 || message.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 10 {
		t.Fatalf("usage lost: %+v %v", message, err)
	}
}
