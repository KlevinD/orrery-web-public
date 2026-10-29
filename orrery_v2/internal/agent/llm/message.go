// Package llm defines the model boundary owned by Orrery. Providers translate
// their wire formats into these values; the agent loop never imports a model SDK.
package llm

import "encoding/json"

type Role string

const (
	System    Role = "system"
	User      Role = "user"
	Assistant Role = "assistant"
	ToolRole  Role = "tool"
)

type Message struct {
	// Field order and empty text preserve the hashes of existing checkpoints.
	Role                     Role               `json:"role"`
	Content                  string             `json:"content"`
	MultiContent             []json.RawMessage  `json:"multi_content,omitempty"`
	UserInputMultiContent    []MessageInputPart `json:"user_input_multi_content,omitempty"`
	AssistantGenMultiContent []json.RawMessage  `json:"assistant_output_multi_content,omitempty"`
	Name                     string             `json:"name,omitempty"`
	ToolCalls                []ToolCall         `json:"tool_calls,omitempty"`
	ToolCallID               string             `json:"tool_call_id,omitempty"`
	ToolName                 string             `json:"tool_name,omitempty"`
	ResponseMeta             *ResponseMeta      `json:"response_meta,omitempty"`
	ReasoningContent         string             `json:"reasoning_content,omitempty"`
	Extra                    map[string]any     `json:"extra,omitempty"`
}

const (
	ChatMessagePartTypeText     = "text"
	ChatMessagePartTypeImageURL = "image_url"
)

// These fields retain the existing checkpoint wire format. The runtime accepts
// text and validated host image bytes; unsupported model output stays visible
// in the raw content fields so protocol validation can reject it.
type MessageInputPart struct {
	Type  string             `json:"type"`
	Text  string             `json:"text,omitempty"`
	Image *MessageInputImage `json:"image,omitempty"`
	Extra map[string]any     `json:"extra,omitempty"`
}

type MessagePartCommon struct {
	URL        *string        `json:"url,omitempty"`
	Base64Data *string        `json:"base64data,omitempty"`
	MIMEType   string         `json:"mime_type,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

type MessageInputImage struct {
	MessagePartCommon
	Detail string `json:"detail,omitempty"`
}

type ToolCall struct {
	Index    *int           `json:"index,omitempty"`
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function FunctionCall   `json:"function"`
	Extra    map[string]any `json:"extra,omitempty"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type ResponseMeta struct {
	FinishReason string          `json:"finish_reason,omitempty"`
	Usage        *TokenUsage     `json:"usage,omitempty"`
	LogProbs     json.RawMessage `json:"logprobs,omitempty"`
}

type TokenUsage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	PromptTokenDetails      PromptTokenDetails      `json:"prompt_token_details"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_token_details"`
}

type PromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

func SystemMessage(text string) *Message { return &Message{Role: System, Content: text} }
func UserMessage(text string) *Message   { return &Message{Role: User, Content: text} }
func AssistantMessage(text string, calls []ToolCall) *Message {
	return &Message{Role: Assistant, Content: text, ToolCalls: calls}
}
func ToolMessage(text, callID string) *Message {
	return &Message{Role: ToolRole, Content: text, ToolCallID: callID}
}
