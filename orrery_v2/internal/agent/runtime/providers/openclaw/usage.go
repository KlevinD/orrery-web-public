package openclaw

import agentcore "orrery_v2/internal/agent/core"

func parseUsage(data map[string]any) agentcore.TokenUsage {
	return agentcore.TokenUsage{
		InputTokens:      int64First(data, "input", "inputTokens", "input_tokens", "prompt_tokens", "promptTokens"),
		OutputTokens:     int64First(data, "output", "outputTokens", "output_tokens", "completion_tokens", "completionTokens"),
		CacheReadTokens:  int64First(data, "cacheRead", "cachedInputTokens", "cached_input_tokens", "cache_read", "cache_read_input_tokens"),
		CacheWriteTokens: int64First(data, "cacheWrite", "cacheCreationInputTokens", "cache_creation_input_tokens", "cache_write"),
	}
}

func addUsage(dst *agentcore.TokenUsage, src agentcore.TokenUsage) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
}

func int64First(data map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := data[key].(type) {
		case float64:
			if value != 0 {
				return int64(value)
			}
		case int64:
			if value != 0 {
				return value
			}
		case int:
			if value != 0 {
				return int64(value)
			}
		}
	}
	return 0
}
