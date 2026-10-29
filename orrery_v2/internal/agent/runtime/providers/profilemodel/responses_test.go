package profilemodel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestManagedStreamingPreservesResponsesTransport(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if r.URL.Path != "/v1/responses" || json.NewDecoder(r.Body).Decode(&body) != nil || body["input"] == nil || body["messages"] != nil || body["stream"] != true {
			t.Error("AgentCore streaming replaced the Responses request protocol")
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"response preserved"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`))
	}))
	defer server.Close()
	factory := NewProfileModelFactory(ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{Enabled: true, Provider: "custom-openai-responses", BaseURL: server.URL + "/v1", APIKey: "test"}}}})
	chat, err := factory.ModelForRequest(context.Background(), ModelRequest{ManagedContext: true, StreamResponses: true})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := chat.Stream(context.Background(), []*llm.Message{llm.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	reply, err := stream.Recv()
	if err != nil || reply.Content != "response preserved" || reply.ResponseMeta.Usage.TotalTokens != 5 || requests != 1 {
		t.Fatalf("response=%v err=%v requests=%d", reply, err, requests)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("stream did not finish: %v", err)
	}
}

func TestResponsesRejectsIncompleteAndFailedResults(t *testing.T) {
	for _, body := range []string{
		`{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
		`{"status":"failed","error":{"message":"private credential"},"output":[]}`,
		`{"status":"completed","output":[]}`,
		`{"status":"completed","output":[{"type":"function_call","name":"search","arguments":"{}"}]}`,
		`{"status":"completed","output":[{"type":"web_search_call"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			factory := NewProfileModelFactory(ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{Enabled: true, ProfileID: "test", Provider: "custom-openai-responses", BaseURL: server.URL + "/v1", APIKey: "test"}}}})
			chat, err := factory.ModelForRequest(context.Background(), ModelRequest{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = chat.Generate(context.Background(), []*llm.Message{llm.UserMessage("hello")})
			if err == nil || strings.Contains(err.Error(), "private credential") {
				t.Fatalf("unsafe result: %v", err)
			}
		})
	}
}

func TestResponsesPlainReplyAndEndpoint(t *testing.T) {
	for _, suffix := range []string{"", "/v1", "/v1/responses", "/v1/chat/completions"} {
		t.Run(suffix, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" {
					t.Errorf("path=%s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello back"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`))
			}))
			defer server.Close()
			factory := NewProfileModelFactory(ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{Enabled: true, Provider: "custom_openai_responses", BaseURL: server.URL + suffix, APIKey: "test"}}}})
			chat, err := factory.ModelForRequest(context.Background(), ModelRequest{})
			if err != nil {
				t.Fatal(err)
			}
			reply, err := chat.Generate(context.Background(), []*llm.Message{llm.UserMessage("hello")})
			if err != nil || reply.Content != "hello back" || reply.ResponseMeta.Usage.TotalTokens != 5 || reply.ResponseMeta.FinishReason != "stop" {
				t.Fatalf("reply=%v error=%v", reply, err)
			}
		})
	}
}
