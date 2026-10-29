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

func responsesEvent(value any) string {
	raw, _ := json.Marshal(value)
	return "event: ignored-framing-name\ndata: " + string(raw) + "\n\n"
}

func responsesDelta(text string) string {
	return responsesEvent(map[string]any{"type": "response.output_text.delta", "delta": text, "output_index": 0, "content_index": 0})
}

func responsesCompleted(text string) string {
	return responsesEvent(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}}}})
}

func TestResponsesSSEDeliversTextBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["stream"] != true || body["store"] != false || body["prompt_cache_key"] != "scoped-cache" || body["messages"] != nil {
			t.Error("Responses request contract changed")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		io.WriteString(w, ": keepalive\r\n\r\n"+responsesDelta(" 首段 "))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, responsesDelta("第二段\n")+responsesCompleted(" 首段 第二段\n"))
	}))
	defer server.Close()
	defer finish()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &openAICompatibleChatModel{client: server.Client(), endpoint: server.URL, responses: true, streamResponses: true, promptCacheKey: "scoped-cache", profile: llmprofile.LLMProfile{Model: "test"}}
	stream, err := m.Stream(ctx, []*llm.Message{llm.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil || first.Content != " 首段 " || first.ResponseMeta != nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	finish() // Upstream cannot complete until the first delta is received.
	chunks := []*llm.Message{first}
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk)
	}
	message, err := llm.MergeChunks(chunks)
	if err != nil || message.Content != " 首段 第二段\n" || message.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("merged=%+v err=%v", message, err)
	}
}

func TestResponsesSSECommitsToolsReasoningAndUsageOnce(t *testing.T) {
	terminal := `{"type":"response.completed","response":{"status":"completed","model":"test","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-test","summary":[]},{"type":"message","content":[{"type":"output_text","text":"查找中"}]},{"type":"function_call","call_id":"a","name":"lookup","arguments":"{\"id\":1}"},{"type":"function_call","call_id":"b","name":"lookup","arguments":"{\"id\":2}"}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":80},"output_tokens_details":{"reasoning_tokens":0}}}}`
	wire := responsesDelta("查找中") + responsesEvent(map[string]any{"type": "response.function_call_arguments.delta", "delta": "{", "output_index": 2}) + "data: " + terminal + "\n\n"
	var chunks []*llm.Message
	if err := readResponsesSSE(strings.NewReader(wire), "required", func(m *llm.Message) bool { chunks = append(chunks, m); return true }); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || len(chunks[0].ToolCalls) != 0 {
		t.Fatal("partial tool arguments were committed")
	}
	message, err := llm.MergeChunks(chunks)
	if err != nil || len(message.ToolCalls) != 2 || message.ToolCalls[0].ID != "a" || message.ToolCalls[1].Function.Arguments != `{"id":2}` || message.Content != "查找中" {
		t.Fatalf("merged=%+v err=%v", message, err)
	}
	usage := message.ResponseMeta.Usage
	cached, reasoning := ModelUsageDetailsKnown(message)
	if usage.TotalTokens != 120 || usage.PromptTokenDetails.CachedTokens != 80 || usage.CompletionTokensDetails.ReasoningTokens != 0 || !cached || !reasoning || message.ResponseMeta.FinishReason != "tool_calls" {
		t.Fatalf("usage=%+v cached=%v reasoning=%v", usage, cached, reasoning)
	}
	request := responsesRequestFromChat(openAICompatibleChatCompletionRequest{Messages: openAICompatibleMessages([]*llm.Message{message})})
	encoded, _ := json.Marshal(request)
	if !strings.Contains(string(encoded), `"encrypted_content":"opaque-test"`) || !strings.Contains(string(encoded), `"call_id":"b"`) {
		t.Fatalf("continuation data was lost: %s", encoded)
	}
}

func TestResponsesSSERejectsInvalidOrInterruptedOutput(t *testing.T) {
	for name, wire := range map[string]string{
		"truncated":              responsesDelta("partial"),
		"done-without-completed": "data: [DONE]\n\n",
		"bad-json":               "data: nope\n\n",
		"missing-type":           "data: {}\n\n",
		"failed":                 responsesDelta("partial") + "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"private credential\"}}}\n\n",
		"incomplete":             "data: {\"type\":\"response.incomplete\"}\n\n",
		"mismatched-text":        responsesDelta("wrong") + responsesCompleted("correct"),
		"empty":                  responsesCompleted(""),
		"invalid-tool":           "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"x\",\"name\":\"lookup\",\"arguments\":\"{\"}]}}\n\n",
		"oversized":              "data: " + strings.Repeat("x", maxResponsesEventBytes+1) + "\n\n",
		"event-count":            strings.Repeat("data: {\"type\":\"response.in_progress\"}\n\n", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			finalized := false
			err := readResponsesSSE(strings.NewReader(wire), "", func(m *llm.Message) bool {
				finalized = finalized || m.ResponseMeta != nil || len(m.ToolCalls) > 0
				return true
			})
			if err == nil || strings.Contains(err.Error(), "private credential") || finalized {
				t.Fatalf("invalid response committed: finalized=%v err=%v", finalized, err)
			}
		})
	}
	if err := readResponsesSSE(strings.NewReader(responsesCompleted("no tool")), "required", func(*llm.Message) bool { return true }); err == nil {
		t.Fatal("required tool was not enforced")
	}
}

func TestResponsesSSECancellationClosesUpstream(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsesDelta("first"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &openAICompatibleChatModel{client: server.Client(), endpoint: server.URL, responses: true, streamResponses: true}
	stream, err := m.Stream(ctx, []*llm.Message{llm.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream stayed open")
	}
	if _, err = stream.Recv(); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestResponsesStreamContextWindowErrors(t *testing.T) {
	for _, wire := range []string{
		`{"type":"error","error":{"code":"context_length_exceeded"}}`,
		`{"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded"}}}`,
	} {
		if err := readResponsesSSE(strings.NewReader("data: "+wire+"\n\n"), "", func(*llm.Message) bool { return true }); !errors.Is(err, ErrContextWindowExceeded) {
			t.Fatalf("stream window error=%v", err)
		}
	}
	for _, status := range []int{400, 413, 422} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"code":"context_length_exceeded","message":"private credential"}}`)
		}))
		m := &openAICompatibleChatModel{client: server.Client(), endpoint: server.URL, responses: true, streamResponses: true}
		_, err := m.Stream(context.Background(), []*llm.Message{llm.UserMessage("hello")})
		server.Close()
		if !errors.Is(err, ErrContextWindowExceeded) {
			t.Fatalf("status=%d error=%v", status, err)
		}
	}
}

func TestResponsesSSENonStreamingOptOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request responsesRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Stream {
			t.Error("non-streaming caller changed")
		}
		io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"unchanged"}]}]}`)
	}))
	defer server.Close()
	m := &openAICompatibleChatModel{client: server.Client(), endpoint: server.URL, responses: true}
	stream, err := m.Stream(context.Background(), []*llm.Message{llm.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	message, err := stream.Recv()
	if err != nil || message.Content != "unchanged" {
		t.Fatalf("message=%v err=%v", message, err)
	}
}
