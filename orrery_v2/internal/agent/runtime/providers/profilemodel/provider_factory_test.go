package profilemodel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
)

func TestProfileModelFactorySelectsProfileFromLLMProfileIDConfigRefThenGlobalDefault(t *testing.T) {
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{
		Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
			{ProfileID: "default", Provider: "openai", BaseURL: "https://api.example.com/v1", APIKey: "sk-default", Model: "gpt-default", Enabled: true, GlobalDefault: true},
			{ProfileID: "specific", Provider: "openai", BaseURL: "https://api.example.com/v1", APIKey: "sk-specific", Model: "gpt-specific", Enabled: true},
		}},
	})

	profile, err := factory.selectProfile(context.Background(), ModelRequest{LLMProfileID: "specific", ConfigRef: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProfileID != "specific" {
		t.Fatalf("profile = %+v", profile)
	}
	profile, err = factory.selectProfile(context.Background(), ModelRequest{ConfigRef: "specific"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProfileID != "specific" {
		t.Fatalf("profile = %+v", profile)
	}
	profile, err = factory.selectProfile(context.Background(), ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProfileID != "default" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestProfileModelFactoryBuildsOpenAICompatibleModel(t *testing.T) {
	var captured struct {
		Authorization string
		Body          openAICompatibleChatCompletionRequest
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		captured.Authorization = req.Header.Get("Authorization")
		if req.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&captured.Body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"gpt-test",
			"choices":[{
				"finish_reason":"tool_calls",
				"message":{
					"content":"calling tool",
					"tool_calls":[{
						"id":"call-1",
						"type":"function",
						"function":{"name":"mission_prepare","arguments":"{\"title\":\"Launch\"}"}
					}]
				}
			}],
			"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}
		}`))
	}))
	defer server.Close()
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{
		Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID:   "llm-test",
			Provider:    "openai",
			BaseURL:     server.URL + "/v1",
			APIKey:      "sk-test",
			Model:       "gpt-test",
			Temperature: 0.2,
			Enabled:     true,
		}}},
		Client: server.Client(),
	})

	chatModel, err := factory.ModelForRequest(context.Background(), ModelRequest{
		LLMProfileID:    "llm-test",
		Model:           "gpt-override",
		ReasoningEffort: "max",
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := chatModel.Generate(context.Background(), []*llm.Message{
		llm.SystemMessage("system prompt"),
		llm.UserMessage("user prompt"),
	}, llm.WithTools([]*llm.Tool{{
		Name:        "mission_prepare",
		Description: "Prepare mission",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "description": "Title"}}, "required": []string{"title"}},
	}}))
	if err != nil {
		t.Fatal(err)
	}

	if captured.Authorization != "Bearer sk-test" {
		t.Fatalf("authorization = %q", captured.Authorization)
	}
	if captured.Body.Model != "gpt-override" || captured.Body.ReasoningEffort != "max" || captured.Body.Temperature == nil || *captured.Body.Temperature != 0.2 {
		t.Fatalf("model/temperature = %+v", captured.Body)
	}
	if len(captured.Body.Messages) != 2 || captured.Body.Messages[0].Role != "system" || captured.Body.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v", captured.Body.Messages)
	}
	if len(captured.Body.Tools) != 1 || captured.Body.Tools[0].Function.Name != "mission_prepare" || captured.Body.ToolChoice != "auto" {
		t.Fatalf("tools = %+v tool_choice = %q", captured.Body.Tools, captured.Body.ToolChoice)
	}
	required, _ := captured.Body.Tools[0].Function.Parameters["required"].([]any)
	if len(required) == 0 {
		t.Fatalf("tool schema missing required fields: %+v", captured.Body.Tools[0].Function.Parameters)
	}
	if message.Content != "calling tool" || len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "mission_prepare" {
		t.Fatalf("message = %+v", message)
	}
	if message.ResponseMeta == nil || message.ResponseMeta.Usage == nil || message.ResponseMeta.Usage.PromptTokens != 12 || message.ResponseMeta.Usage.CompletionTokens != 7 {
		t.Fatalf("response meta = %+v", message.ResponseMeta)
	}
}

func TestOpenAICompatibleModelRetriesWhenRequiredToolCallIsMissing(t *testing.T) {
	var captured []openAICompatibleChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body openAICompatibleChatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		captured = append(captured, body)
		w.Header().Set("Content-Type", "application/json")
		if len(captured) == 1 {
			_, _ = w.Write([]byte(`{"model":"gpt-test","choices":[{"message":{"content":"<think>I should search first.</think>"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"gpt-test","choices":[{"message":{"tool_calls":[{"id":"search-1","type":"function","function":{"name":"search_documents","arguments":"{}"}}]}}]}`))
	}))
	defer server.Close()
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{
		Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "llm-test", Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "sk-test", Model: "gpt-test", Enabled: true,
		}}},
		Client: server.Client(),
	})
	chatModel, err := factory.ModelForRequest(context.Background(), ModelRequest{
		LLMProfileID: "llm-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := chatModel.Generate(
		context.Background(),
		[]*llm.Message{llm.UserMessage("question")},
		llm.WithTools([]*llm.Tool{{Name: "search_documents"}}),
		llm.WithToolChoice(llm.ToolChoiceForced),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured requests = %d, want retry", len(captured))
	}
	if captured[1].ToolChoice != "required" || len(captured[1].Messages) != 2 || captured[1].Messages[1].Role != "system" {
		t.Fatalf("retry request = %+v", captured[1])
	}
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "search_documents" {
		t.Fatalf("message = %+v", message)
	}
}

func TestOpenAICompatibleModelRejectsNonASCIIAPIKeyBeforeHTTPCall(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
	}))
	defer server.Close()
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{
		Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "llm-test",
			Provider:  "openai",
			BaseURL:   server.URL + "/v1",
			APIKey:    "检查你像：剪贴板上有一些不可见的白色字符。",
			Model:     "gpt-test",
			Enabled:   true,
		}}},
		Client: server.Client(),
	})

	_, err := factory.ModelForRequest(context.Background(), ModelRequest{LLMProfileID: "llm-test"})
	if err == nil {
		t.Fatal("expected invalid api key error")
	}
	if called {
		t.Fatal("HTTP endpoint should not be called for invalid api key")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Fatalf("error = %v", err)
	}
}
