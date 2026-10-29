package pi

import (
	"encoding/json"
	"fmt"
	"strings"
)

type streamEvent struct {
	Type                  string                `json:"type"`
	ID                    string                `json:"id,omitempty"`
	SessionID             string                `json:"sessionId,omitempty"`
	RequestID             string                `json:"request_id,omitempty"`
	Command               string                `json:"command,omitempty"`
	Success               bool                  `json:"success,omitempty"`
	Error                 string                `json:"error,omitempty"`
	ErrorMessage          string                `json:"errorMessage,omitempty"`
	Message               piMessage             `json:"message,omitempty"`
	Messages              []piMessage           `json:"messages,omitempty"`
	AssistantMessageEvent assistantMessageEvent `json:"assistantMessageEvent,omitempty"`
	ToolCallID            string                `json:"toolCallId,omitempty"`
	ToolID                string                `json:"tool_id,omitempty"`
	ToolName              string                `json:"toolName,omitempty"`
	ToolNameSnake         string                `json:"tool_name,omitempty"`
	Args                  map[string]any        `json:"args,omitempty"`
	Parameters            json.RawMessage       `json:"parameters,omitempty"`
	Input                 json.RawMessage       `json:"input,omitempty"`
	Payload               json.RawMessage       `json:"payload,omitempty"`
	PartialResult         any                   `json:"partialResult,omitempty"`
	Result                any                   `json:"result,omitempty"`
	IsError               bool                  `json:"isError,omitempty"`
	WillRetry             bool                  `json:"willRetry,omitempty"`
	Aborted               bool                  `json:"aborted,omitempty"`
	Method                string                `json:"method,omitempty"`
	Title                 string                `json:"title,omitempty"`
	Options               []string              `json:"options,omitempty"`
	NotifyType            string                `json:"notifyType,omitempty"`
}

type assistantMessageEvent struct {
	Type         string     `json:"type"`
	Delta        string     `json:"delta,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	ToolCall     piToolCall `json:"toolCall,omitempty"`
}

type piMessage struct {
	Role         string  `json:"role,omitempty"`
	Content      any     `json:"content,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	Usage        piUsage `json:"usage,omitempty"`
	StopReason   string  `json:"stopReason,omitempty"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
	UIText       string  `json:"-"`
}

func (m *piMessage) UnmarshalJSON(data []byte) error {
	if len(data) != 0 && data[0] == '"' {
		return json.Unmarshal(data, &m.UIText)
	}
	type alias piMessage
	return json.Unmarshal(data, (*alias)(m))
}

type piUsage struct {
	Input      int64 `json:"input,omitempty"`
	Output     int64 `json:"output,omitempty"`
	CacheRead  int64 `json:"cacheRead,omitempty"`
	CacheWrite int64 `json:"cacheWrite,omitempty"`
}

type piToolCall struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

func assistantMessageText(message piMessage) string {
	switch content := message.Content.(type) {
	case string:
		return strings.TrimSpace(content)
	case []any:
		var text strings.Builder
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			if value, ok := block["text"].(string); ok {
				text.WriteString(value)
			}
		}
		return strings.TrimSpace(text.String())
	default:
		return ""
	}
}

func contentText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		if output, ok := typed["output"].(string); ok {
			return output
		}
		return contentText(typed["content"])
	case []any:
		var parts []string
		for _, item := range typed {
			if text := contentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(typed)
	}
}

func jsonString(value any) string {
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return ""
	}
	return string(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func piTextEquivalent(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return strings.Join(strings.Fields(left), " ") == strings.Join(strings.Fields(right), " ")
}
