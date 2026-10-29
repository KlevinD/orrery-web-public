package profilemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const responsesReasoningKey = "orrery_responses_reasoning"

func usesResponsesAPI(provider string) bool {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(provider)), "_", "-") {
	case "openai-responses", "custom-openai-responses", "openai-responses-compatible":
		return true
	}
	return false
}

func openAICompatibleResponsesURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/responses") {
		base = strings.TrimSuffix(base, "/responses") + "/chat/completions"
	}
	endpoint, err := openAICompatibleChatCompletionsURL(base)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(endpoint, "/chat/completions") + "/responses", nil
}

type responsesRequest struct {
	PromptCacheKey string           `json:"prompt_cache_key,omitempty"`
	Model          string           `json:"model"`
	Input          []any            `json:"input"`
	Tools          []map[string]any `json:"tools,omitempty"`
	ToolChoice     string           `json:"tool_choice,omitempty"`
	Reasoning      map[string]any   `json:"reasoning,omitempty"`
	Include        []string         `json:"include,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	Store          bool             `json:"store"`
	Stream         bool             `json:"stream"`
}

// Reuse the existing bounded context and tool conversion, then translate the
// wire protocol explicitly. Responses tool definitions are flat; call IDs link
// function_call_output items to the assistant's function_call items.
func responsesRequestFromChat(body openAICompatibleChatCompletionRequest) responsesRequest {
	out := responsesRequest{Model: body.Model, Input: []any{}, Temperature: body.Temperature, PromptCacheKey: body.PromptCacheKey, Include: []string{"reasoning.encrypted_content"}}
	if body.ReasoningEffort != "" {
		out.Reasoning = map[string]any{"effort": body.ReasoningEffort}
	}
	for _, message := range body.Messages {
		if message.Role == "tool" {
			out.Input = append(out.Input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
			continue
		}
		if message.Role == "assistant" {
			for _, raw := range message.Reasoning {
				var item map[string]any
				if json.Unmarshal(raw, &item) == nil && item["type"] == "reasoning" {
					out.Input = append(out.Input, item)
				}
			}
		}
		if message.Content != "" || len(message.ContentParts) > 0 {
			out.Input = append(out.Input, map[string]any{"role": message.Role, "content": message.responsesContent()})
		}
		for _, call := range message.ToolCalls {
			out.Input = append(out.Input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
		}
	}
	for _, tool := range body.Tools {
		out.Tools = append(out.Tools, map[string]any{"type": "function", "name": tool.Function.Name, "description": tool.Function.Description, "parameters": tool.Function.Parameters, "strict": false})
	}
	if len(out.Tools) > 0 {
		out.ToolChoice = body.ToolChoice
	}
	return out
}

type responsesResponse struct {
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Error  json.RawMessage   `json:"error"`
	Output []json.RawMessage `json:"output"`
	Usage  struct {
		InputTokens         int                   `json:"input_tokens"`
		OutputTokens        int                   `json:"output_tokens"`
		TotalTokens         int                   `json:"total_tokens"`
		InputTokensDetails  cachedTokenDetails    `json:"input_tokens_details"`
		OutputTokensDetails reasoningTokenDetails `json:"output_tokens_details"`
	} `json:"usage"`
}

func (m *openAICompatibleChatModel) generateResponses(ctx context.Context, body openAICompatibleChatCompletionRequest) (openAICompatibleChatCompletionResponse, error) {
	var result openAICompatibleChatCompletionResponse
	payload, err := json.Marshal(responsesRequestFromChat(body))
	if err != nil {
		return result, fmt.Errorf("encode responses request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		return result, fmt.Errorf("create responses request")
	}
	req.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	response, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("responses transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if isContextWindowError(response.StatusCode, raw) {
			return result, ErrContextWindowExceeded
		}
		return result, fmt.Errorf("responses returned status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return result, fmt.Errorf("responses body unavailable or too large")
	}
	var decoded responsesResponse
	if json.Unmarshal(raw, &decoded) != nil {
		return result, fmt.Errorf("responses output is invalid")
	}
	return completionFromResponses(decoded)
}

func completionFromResponses(decoded responsesResponse) (openAICompatibleChatCompletionResponse, error) {
	var result openAICompatibleChatCompletionResponse
	if decoded.Status != "completed" || (len(decoded.Error) > 0 && string(decoded.Error) != "null") {
		return result, fmt.Errorf("responses did not complete successfully")
	}
	choice := openAICompatibleCompletionChoice{FinishReason: "stop"}
	for _, raw := range decoded.Output {
		var item struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &item) != nil {
			return result, fmt.Errorf("responses output is invalid")
		}
		switch item.Type {
		case "reasoning":
			result.Reasoning = append(result.Reasoning, raw)
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					choice.Message.Content += part.Text
				case "refusal":
					choice.Message.Content += part.Refusal
					choice.FinishReason = "content_filter"
				}
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" || !json.Valid([]byte(item.Arguments)) {
				return result, fmt.Errorf("responses tool call is invalid")
			}
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, openAICompatibleToolCall{ID: item.CallID, Type: "function", Function: openAICompatibleToolCallFunction{Name: item.Name, Arguments: item.Arguments}})
		default:
			return result, fmt.Errorf("responses output type is unsupported")
		}
	}
	if len(choice.Message.ToolCalls) > 0 {
		choice.FinishReason = "tool_calls"
	}
	if choice.Message.Content == "" && len(choice.Message.ToolCalls) == 0 && choice.FinishReason != "content_filter" {
		return result, fmt.Errorf("responses output is empty")
	}
	result.Model, result.Choices = decoded.Model, []openAICompatibleCompletionChoice{choice}
	result.Usage = openAICompatibleCompletionUsage{PromptTokens: decoded.Usage.InputTokens, CompletionTokens: decoded.Usage.OutputTokens, TotalTokens: decoded.Usage.TotalTokens, PromptTokensDetails: decoded.Usage.InputTokensDetails, CompletionTokensDetails: decoded.Usage.OutputTokensDetails}
	return result, nil
}
