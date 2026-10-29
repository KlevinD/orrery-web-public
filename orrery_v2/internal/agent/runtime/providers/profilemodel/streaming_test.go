package profilemodel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestSSEStreamsBeforeCompletionAndCancellationClosesHTTP(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request["stream"] != true || request["prompt_cache_key"] != "scoped-test" {
			t.Error("stream or cache field missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &openAICompatibleChatModel{client: server.Client(), endpoint: server.URL, streamResponses: true, promptCacheKey: "scoped-test", profile: llmprofile.LLMProfile{Model: "test"}}
	stream, err := m.Stream(ctx, []*llm.Message{llm.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil || first.Content != "first" {
		t.Fatalf("first chunk=%+v err=%v", first, err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not close upstream request")
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("cancelled stream became successful")
	}
}

func TestSSEPreservesParallelToolFragmentsFinishAndUsage(t *testing.T) {
	wire := ": keepalive\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\"}},{\"index\":1,\"id\":\"b\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"\\\"id\\\":2}\"}},{\"index\":0,\"function\":{\"arguments\":\"\\\"id\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120,\"prompt_tokens_details\":{\"cached_tokens\":80},\"completion_tokens_details\":{\"reasoning_tokens\":10}}}\n\n" +
		"data: [DONE]\n\n"
	var chunks []*llm.Message
	if err := readCompletionSSE(strings.NewReader(wire), func(m *llm.Message) bool { chunks = append(chunks, m); return true }); err != nil {
		t.Fatal(err)
	}
	message, err := llm.MergeChunks(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.ToolCalls) != 2 || message.ToolCalls[0].ID != "a" || message.ToolCalls[0].Function.Arguments != `{"id":1}` || message.ToolCalls[1].Function.Arguments != `{"id":2}` {
		t.Fatalf("tool fragments lost: %+v", message.ToolCalls)
	}
	if message.ResponseMeta.FinishReason != "tool_calls" || message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 80 || message.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 10 {
		t.Fatalf("metadata lost: %+v", message.ResponseMeta)
	}
}

func TestSSERejectsTruncationMalformedEventsAndInvalidOrdering(t *testing.T) {
	finish := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	usage := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10}}\n\n"
	for name, wire := range map[string]string{
		"truncated": finish, "no-finish": "data: [DONE]\n\n", "bad-json": "data: nope\n\n",
		"error":       "data: {\"error\":{\"code\":\"unavailable\"}}\n\n",
		"late-choice": finish + finish, "early-usage": usage, "duplicate-usage": finish + usage + usage,
		"tool-index": "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"id\":\"missing-index\"}]}}]}\n\n",
		"oversized":  "data: " + strings.Repeat("x", 256*1024) + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := readCompletionSSE(strings.NewReader(wire), func(*llm.Message) bool { return true }); err == nil {
				t.Fatal("invalid stream accepted")
			}
		})
	}
}

func TestSSEJSONGatewayFallbackUsesOneRequest(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	m := &openAICompatibleChatModel{client: server.Client(), endpoint: server.URL, streamResponses: true, profile: llmprofile.LLMProfile{Model: "test"}}
	stream, err := m.Stream(context.Background(), []*llm.Message{llm.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	message, err := stream.Recv()
	if err != nil || message.Content != "done" {
		t.Fatalf("fallback failed: %+v %v", message, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected stream result: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatal("fallback repeated inference")
	}
}
