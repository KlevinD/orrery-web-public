package profilemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestResponsesCacheKeyAndUsageDetailsReachHTTPAndModel(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, key := range []string{"", "tenant-agent-session-scope"} {
			for _, details := range []string{"", `,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}`, `,"input_tokens_details":{"cached_tokens":800},"output_tokens_details":{"reasoning_tokens":40}`} {
				t.Run(fmt.Sprintf("stream=%t/key=%s/details=%s", stream, key, details), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Path != "/v1/responses" {
							t.Error("invalid Responses request")
						}
						value, present := body["prompt_cache_key"]
						if (key == "" && present) || (key != "" && value != key) {
							t.Errorf("cache scope not preserved: %v", value)
						}
						fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100%s}}`, details)
					}))
					defer server.Close()
					factory := NewProfileModelFactory(ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{Enabled: true, Provider: "custom-openai-responses", BaseURL: server.URL + "/v1", APIKey: "fixture"}}}})
					chat, err := factory.ModelForRequest(context.Background(), ModelRequest{ManagedContext: true, StreamResponses: stream, PromptCacheKey: key})
					if err != nil {
						t.Fatal(err)
					}
					var message *llm.Message
					if stream {
						reader, streamErr := chat.Stream(context.Background(), []*llm.Message{llm.UserMessage("hello")})
						if streamErr != nil {
							t.Fatal(streamErr)
						}
						defer reader.Close()
						message, err = reader.Recv()
					} else {
						message, err = chat.Generate(context.Background(), []*llm.Message{llm.UserMessage("hello")})
					}
					if err != nil {
						t.Fatal(err)
					}
					cached, reasoning := ModelUsageDetailsKnown(message)
					if cached != (details != "") || reasoning != (details != "") || message.ResponseMeta.Usage.TotalTokens != 1100 {
						t.Fatalf("presence or total lost: %+v", message)
					}
					if strings.Contains(details, "800") && (message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 800 || message.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 40) {
						t.Fatal("usage subsets lost")
					}
				})
			}
		}
	}
}

func TestChatAndSSEUsagePreserveMissingVersusZeroDetails(t *testing.T) {
	for _, details := range []string{"", `,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}`, `,"prompt_tokens_details":{"cached_tokens":800},"completion_tokens_details":{"reasoning_tokens":40}`} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, details), func(t *testing.T) {
				usage := fmt.Sprintf(`{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100%s}`, details)
				var message *llm.Message
				var err error
				if stream {
					wire := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n" + "data: {\"choices\":[],\"usage\":" + usage + "}\n\ndata: [DONE]\n\n"
					var chunks []*llm.Message
					if err := readCompletionSSE(strings.NewReader(wire), func(m *llm.Message) bool { chunks = append(chunks, m); return true }); err != nil {
						t.Fatal(err)
					}
					message, err = llm.MergeChunks(chunks)
				} else {
					var completion openAICompatibleChatCompletionResponse
					if err := json.Unmarshal([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}],"usage":`+usage+`}`), &completion); err != nil {
						t.Fatal(err)
					}
					message, err = messageFromOpenAICompatibleCompletion(completion)
				}
				if err != nil && err != io.EOF {
					t.Fatal(err)
				}
				cached, reasoning := ModelUsageDetailsKnown(message)
				if cached != (details != "") || reasoning != (details != "") {
					t.Fatal("zero and absent details became indistinguishable")
				}
				if strings.Contains(details, "800") && (message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 800 || message.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 40) {
					t.Fatal("subsets lost")
				}
			})
		}
	}
}
