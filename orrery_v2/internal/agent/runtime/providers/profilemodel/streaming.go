package profilemodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"orrery_v2/internal/agent/llm"
	"strings"
	"time"
)

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string         `json:"role"`
			Content   string         `json:"content"`
			ToolCalls []llm.ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAICompatibleCompletionUsage `json:"usage"`
	Error json.RawMessage                  `json:"error"`
}

// Streaming is opt-in for AgentCore. Other Eino callers retain their existing
// transport, and a JSON-only gateway can complete the same request without a
// second inference. No network request is automatically replayed after output.
func (m *openAICompatibleChatModel) streamCompletion(ctx context.Context, input []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
	body, err := m.buildRequest(input, opts...)
	if err != nil {
		return nil, err
	}
	body.Stream, body.StreamOptions = true, &openAIStreamOptions{IncludeUsage: true}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	// The kernel also owns a shorter per-model deadline. This cap bounds an
	// abandoned reader even when a different caller forgets to cancel its context.
	streamCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		if isContextWindowError(resp.StatusCode, raw) {
			return nil, ErrContextWindowExceeded
		}
		return nil, fmt.Errorf("llm returned status %d", resp.StatusCode)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "text/event-stream" {
		defer cancel()
		defer resp.Body.Close()
		var completion openAICompatibleChatCompletionResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&completion); err != nil {
			return nil, err
		}
		message, err := messageFromOpenAICompatibleCompletion(completion)
		if err != nil {
			return nil, err
		}
		return llm.NewMessageStream([]*llm.Message{message}), nil
	}
	reader, writer := llm.MessagePipe(0)
	go func() {
		defer writer.Close()
		defer cancel()
		defer resp.Body.Close()
		stop := context.AfterFunc(streamCtx, func() { resp.Body.Close() })
		defer stop()
		if err := readCompletionSSE(resp.Body, func(message *llm.Message) bool { return !writer.Send(message, nil) }); err != nil {
			writer.Send(nil, err)
		}
	}()
	return reader, nil
}

func readCompletionSSE(body io.Reader, send func(*llm.Message) bool) error {
	scanner := bufio.NewScanner(io.LimitReader(body, 2*1024*1024))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	var data strings.Builder
	finished, used, events := false, false, 0
	process := func(raw string) (bool, error) {
		if raw == "[DONE]" {
			if !finished {
				return false, errors.New("model stream ended without a finish reason")
			}
			return true, nil
		}
		events++
		if events > 4096 {
			return false, errors.New("model stream event limit exceeded")
		}
		var chunk openAIStreamChunk
		if json.Unmarshal([]byte(raw), &chunk) != nil || len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return false, errors.New("invalid model stream event")
		}
		if len(chunk.Choices) > 1 {
			return false, errors.New("multiple model stream choices")
		}
		message := &llm.Message{Role: llm.Assistant}
		if len(chunk.Choices) == 1 {
			choice := chunk.Choices[0]
			if choice.Index != 0 || choice.Delta.Role != "" && choice.Delta.Role != "assistant" || finished {
				return false, errors.New("invalid model stream sequence")
			}
			message.Content, message.ToolCalls = choice.Delta.Content, choice.Delta.ToolCalls
			for _, call := range message.ToolCalls {
				if call.Index == nil || *call.Index < 0 || *call.Index >= 64 {
					return false, errors.New("invalid streamed tool index")
				}
			}
			if choice.FinishReason != "" {
				finished = true
				message.ResponseMeta = &llm.ResponseMeta{FinishReason: choice.FinishReason}
			}
		}
		if chunk.Usage != nil {
			if used || !finished {
				return false, errors.New("invalid model usage sequence")
			}
			used = true
			if message.ResponseMeta == nil {
				message.ResponseMeta = &llm.ResponseMeta{}
			}
			message.ResponseMeta.Usage = chunk.Usage.schemaUsage()
			chunk.Usage.markDetails(message)
		}
		if message.Content != "" || len(message.ToolCalls) > 0 || message.ResponseMeta != nil {
			if !send(message) {
				return true, nil
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			done, err := process(strings.TrimSuffix(data.String(), "\n"))
			data.Reset()
			if err != nil || done {
				return err
			}
		} else if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if data.Len()+len(value) > 256*1024 {
				return errors.New("model stream event exceeds limit")
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
