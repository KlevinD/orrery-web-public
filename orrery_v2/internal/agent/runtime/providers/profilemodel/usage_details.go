package profilemodel

import "orrery_v2/internal/agent/llm"

// Pointers preserve the difference between an explicit zero and an omitted
// provider field. These counts are subsets of input/output, not extra tokens.
type cachedTokenDetails struct {
	CachedTokens *int `json:"cached_tokens"`
}

type reasoningTokenDetails struct {
	ReasoningTokens *int `json:"reasoning_tokens"`
}

const usageDetailsKey = "orrery_usage_details"

func (u openAICompatibleCompletionUsage) schemaUsage() *llm.TokenUsage {
	usage := &llm.TokenUsage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	if u.PromptTokensDetails.CachedTokens != nil {
		usage.PromptTokenDetails.CachedTokens = *u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails.ReasoningTokens != nil {
		usage.CompletionTokensDetails.ReasoningTokens = *u.CompletionTokensDetails.ReasoningTokens
	}
	return usage
}

func (u openAICompatibleCompletionUsage) markDetails(message *llm.Message) {
	if message.Extra == nil {
		message.Extra = map[string]any{}
	}
	message.Extra[usageDetailsKey] = map[string]any{
		"cached_input_reported": u.PromptTokensDetails.CachedTokens != nil,
		"reasoning_reported":    u.CompletionTokensDetails.ReasoningTokens != nil,
	}
}

// ModelUsageDetailsKnown also supports models that populate the counters
// directly. Without provider presence metadata, zero remains unknown.
func ModelUsageDetailsKnown(message *llm.Message) (cached, reasoning bool) {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		return false, false
	}
	u := message.ResponseMeta.Usage
	cached, reasoning = u.PromptTokenDetails.CachedTokens != 0, u.CompletionTokensDetails.ReasoningTokens != 0
	if details, ok := message.Extra[usageDetailsKey].(map[string]any); ok {
		cached = cached || details["cached_input_reported"] == true
		reasoning = reasoning || details["reasoning_reported"] == true
	}
	return cached, reasoning
}
