package discussionsession

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func modelUsageFromRuntimeUsage(usage map[string]agentcore.TokenUsage) ModelUsageMap {
	if len(usage) == 0 {
		return nil
	}
	out := ModelUsageMap{}
	for model, item := range usage {
		addModelUsageDTO(out, model, ModelUsageDTO{
			CachedInputTokens:      item.CachedInputTokens,
			ReasoningTokens:        item.ReasoningTokens,
			CachedInputTokensKnown: item.CachedInputTokensKnown,
			ReasoningTokensKnown:   item.ReasoningTokensKnown,
			InputTokens:            item.InputTokens,
			OutputTokens:           item.OutputTokens,
			CacheReadTokens:        item.CacheReadTokens,
			CacheWriteTokens:       item.CacheWriteTokens,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneModelUsageMap(src ModelUsageMap) ModelUsageMap {
	if len(src) == 0 {
		return nil
	}
	out := ModelUsageMap{}
	for model, usage := range src {
		addModelUsageDTO(out, model, usage)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeModelUsageMaps(values ...ModelUsageMap) ModelUsageMap {
	out := ModelUsageMap{}
	for _, value := range values {
		for model, usage := range value {
			addModelUsageDTO(out, model, usage)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func addModelUsageDTO(dst ModelUsageMap, model string, usage ModelUsageDTO) {
	if dst == nil || usage.empty() {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}
	current, seen := dst[model]
	current.CachedInputTokens += usage.CachedInputTokens
	current.ReasoningTokens += usage.ReasoningTokens
	current.CachedInputTokensKnown = usage.CachedInputTokensKnown && (!seen || current.CachedInputTokensKnown)
	current.ReasoningTokensKnown = usage.ReasoningTokensKnown && (!seen || current.ReasoningTokensKnown)
	current.InputTokens += usage.InputTokens
	current.OutputTokens += usage.OutputTokens
	current.CacheReadTokens += usage.CacheReadTokens
	current.CacheWriteTokens += usage.CacheWriteTokens
	current.TotalTokens = usageTotalTokens(current)
	dst[model] = current
}

func (u ModelUsageDTO) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 && u.TotalTokens == 0 && u.CachedInputTokens == 0 && u.ReasoningTokens == 0 && !u.CachedInputTokensKnown && !u.ReasoningTokensKnown
}

func usageTotalTokens(usage ModelUsageDTO) int64 {
	if usage.TotalTokens > 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return usage.TotalTokens
	}
	total := usage.InputTokens + usage.OutputTokens
	if total > 0 {
		return total
	}
	return usage.TotalTokens
}

func durationMsBetween(startedAt, completedAt time.Time) int64 {
	if startedAt.IsZero() || completedAt.IsZero() {
		return 0
	}
	duration := completedAt.Sub(startedAt)
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}
