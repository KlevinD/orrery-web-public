package projection

import (
	agentcore "orrery_v2/internal/agent/core"
	"testing"
)

func TestMissionUsageAggregationPreservesSubsetsAndCoverage(t *testing.T) {
	known := map[string]agentcore.TokenUsage{"model": {InputTokens: 1000, OutputTokens: 100, CachedInputTokens: 800, ReasoningTokens: 40, CachedInputTokensKnown: true, ReasoningTokensKnown: true}}
	usage := mergeRuntimeUsage(cloneRuntimeUsage(known), known)
	u := usage["model"]
	if u.CachedInputTokens != 1600 || u.ReasoningTokens != 80 || !u.CachedInputTokensKnown || !u.ReasoningTokensKnown || u.InputTokens+u.OutputTokens != 2200 || u.CacheReadTokens != 0 {
		t.Fatalf("incorrect mission usage: %+v", u)
	}
	usage = mergeRuntimeUsage(usage, map[string]agentcore.TokenUsage{"model": {InputTokens: 1000, OutputTokens: 100}})
	usage = mergeRuntimeUsage(usage, known)
	if usage["model"].CachedInputTokensKnown || usage["model"].ReasoningTokensKnown {
		t.Fatal("incomplete mission usage claims full coverage")
	}
}
