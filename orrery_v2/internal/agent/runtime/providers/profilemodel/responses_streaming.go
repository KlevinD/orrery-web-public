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

const maxResponsesEventBytes = 4 * 1024 * 1024

// Text is provisional until response.completed. Tools and encrypted reasoning
// are committed from the validated terminal snapshot, never partial arguments.
// JSON-only gateways may return the same inference as one chunk; no replay.
func (m *openAICompatibleChatModel) streamResponsesCompletion(ctx context.Context, input []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.client == nil {
		return nil, errors.New("responses model is not configured")
	}
	body, err := m.buildRequest(input, opts...)
	if err != nil {
		return nil, err
	}
	request := responsesRequestFromChat(body)
	request.Stream = true
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("encode responses request")
	}
	streamCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, errors.New("create responses request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("responses transport failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		if isContextWindowError(resp.StatusCode, raw) {
			return nil, ErrContextWindowExceeded
		}
		return nil, fmt.Errorf("responses returned status %d", resp.StatusCode)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "text/event-stream" {
		defer cancel()
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponsesEventBytes+1))
		if err != nil || len(raw) > maxResponsesEventBytes {
			return nil, errors.New("responses body unavailable or too large")
		}
		var decoded responsesResponse
		if json.Unmarshal(raw, &decoded) != nil {
			return nil, errors.New("responses output is invalid")
		}
		message, err := responsesStreamFinal(decoded, body.ToolChoice)
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
		err := readResponsesSSE(resp.Body, body.ToolChoice, func(message *llm.Message) bool { return !writer.Send(message, nil) })
		if err != nil {
			if streamCtx.Err() != nil {
				err = streamCtx.Err()
			}
			writer.Send(nil, err)
		}
	}()
	return reader, nil
}

func responsesStreamFinal(decoded responsesResponse, toolChoice string) (*llm.Message, error) {
	completion, err := completionFromResponses(decoded)
	if err != nil {
		return nil, err
	}
	message, err := messageFromOpenAICompatibleCompletion(completion)
	if err != nil {
		return nil, err
	}
	// Whitespace is meaningful across streamed fragments.
	message.Content = completion.Choices[0].Message.Content
	if toolChoice == "required" && len(message.ToolCalls) == 0 {
		return nil, errors.New("llm did not return a required tool call")
	}
	return message, nil
}

func readResponsesSSE(body io.Reader, toolChoice string, send func(*llm.Message) bool) error {
	scanner := bufio.NewScanner(io.LimitReader(body, 16*1024*1024+1))
	scanner.Buffer(make([]byte, 4096), maxResponsesEventBytes+1)
	var data, text strings.Builder
	events, total := 0, 0
	process := func(raw string) (bool, error) {
		events++
		if events > 4096 {
			return false, errors.New("responses stream event limit exceeded")
		}
		var event struct {
			Type     string            `json:"type"`
			Delta    string            `json:"delta"`
			Response responsesResponse `json:"response"`
			Error    json.RawMessage   `json:"error"`
		}
		if json.Unmarshal([]byte(raw), &event) != nil || event.Type == "" {
			return false, errors.New("invalid responses stream event")
		}
		switch event.Type {
		case "error", "response.failed", "response.incomplete":
			nested, _ := json.Marshal(map[string]json.RawMessage{"error": event.Response.Error})
			if isContextWindowError(http.StatusBadRequest, []byte(raw)) || isContextWindowError(http.StatusBadRequest, nested) {
				return false, ErrContextWindowExceeded
			}
			return false, errors.New("responses stream did not complete successfully")
		case "response.output_text.delta", "response.refusal.delta":
			if text.Len()+len(event.Delta) > maxResponsesEventBytes {
				return false, errors.New("responses text exceeds limit")
			}
			text.WriteString(event.Delta)
			if event.Delta != "" && !send(&llm.Message{Role: llm.Assistant, Content: event.Delta}) {
				return true, nil
			}
		case "response.completed":
			message, err := responsesStreamFinal(event.Response, toolChoice)
			if err != nil {
				return false, err
			}
			if !strings.HasPrefix(message.Content, text.String()) {
				return false, errors.New("responses stream text differs from completed output")
			}
			message.Content = strings.TrimPrefix(message.Content, text.String())
			send(message)
			return true, nil
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			return false, errors.New("responses stream reported an error")
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > 16*1024*1024 {
			return errors.New("responses stream exceeds limit")
		}
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
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if data.Len()+len(value)+1 > maxResponsesEventBytes {
				return errors.New("responses stream event exceeds limit")
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("responses stream read failed")
	}
	return io.ErrUnexpectedEOF
}
