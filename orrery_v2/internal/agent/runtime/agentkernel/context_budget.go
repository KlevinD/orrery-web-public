package agentkernel

import (
	"encoding/json"
	"orrery_v2/internal/agent/llm"

	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

// KernelUsage is cumulative within the scoped checkpoint. Cached tokens are a
// subset of input tokens, and reasoning tokens a subset of output tokens.
type KernelUsage struct {
	InputTokens       uint64 `json:"input_tokens"`
	OutputTokens      uint64 `json:"output_tokens"`
	CachedInputTokens uint64 `json:"cached_input_tokens"`
	ReasoningTokens   uint64 `json:"reasoning_tokens"`
}

type ContextObservation struct {
	Tokens     int `json:"tokens"`
	Messages   int `json:"messages"`
	Compaction int `json:"compaction"`
}

func validModelUsage(usage *llm.TokenUsage) bool {
	// Reject invalid usage in every provider event before combining the stream.
	return usage.PromptTokens >= 0 && usage.CompletionTokens >= 0 && usage.TotalTokens >= 0 &&
		usage.PromptTokenDetails.CachedTokens >= 0 && usage.PromptTokenDetails.CachedTokens <= usage.PromptTokens &&
		usage.CompletionTokensDetails.ReasoningTokens >= 0 && usage.CompletionTokensDetails.ReasoningTokens <= usage.CompletionTokens
}

func (r *Runner) summaryLimitBytes() int {
	limit := min(r.config.CompactSummaryBytes, r.config.MaxResponseBytes, r.config.MaxContextBytes/4)
	if usage, ok := r.contextUsage(nil, nil); ok {
		limit = min(limit, usage.InputTokenLimit*3/4)
	}
	return max(1, limit)
}

func (r *Runner) contextUsage(messages []*llm.Message, tools []*llm.Tool) (profilemodel.ModelContextUsage, bool) {
	budget, ok := r.model.(profilemodel.ContextBudgetModel)
	if !ok {
		return profilemodel.ModelContextUsage{}, false
	}
	usage, err := budget.ContextUsage(messages, tools)
	if err != nil || usage.InputTokenLimit <= 0 || usage.EstimatedInputTokens < 0 {
		// An advertised budget that cannot be evaluated must fail closed.
		return profilemodel.ModelContextUsage{}, true
	}
	return usage, true
}

func (r *Runner) contextFits(messages []*llm.Message, tools []*llm.Tool, byteLimit int) bool {
	raw, err := json.Marshal(messages)
	if err != nil {
		return false
	}
	textBytes, err := contextMessageBytes(messages, len(raw))
	if err != nil || textBytes > byteLimit {
		return false
	}
	usage, ok := r.contextUsage(messages, tools)
	return !ok || usage.InputTokenLimit > 0 && usage.EstimatedInputTokens <= usage.InputTokenLimit
}

// Actual usage anchors the estimate until history is rewritten. Counting the
// appended tail separately prevents an optimistic heuristic from hiding growth.
func (r *Runner) activeContextTokens(messages []*llm.Message) (int, int) {
	usage, ok := r.contextUsage(messages, r.infos)
	if !ok {
		return 0, 0
	}
	estimate := usage.EstimatedInputTokens
	o := r.observation
	if o.Tokens > 0 && o.Compaction == r.compactions && o.Messages > 0 && o.Messages <= len(messages) {
		if tail, ok := r.contextUsage(messages[o.Messages:], nil); ok {
			estimate = max(estimate, o.Tokens+tail.EstimatedInputTokens)
		}
	}
	return estimate, usage.InputTokenLimit
}

func (r *Runner) observeContext(input []*llm.Message, reply *llm.Message) {
	if reply.ResponseMeta == nil || reply.ResponseMeta.Usage == nil {
		return
	}
	u := reply.ResponseMeta.Usage
	if u.PromptTokens > 0 {
		r.observation = ContextObservation{Tokens: u.PromptTokens + u.CompletionTokens, Messages: len(input) + 1, Compaction: r.compactions}
	}
}
