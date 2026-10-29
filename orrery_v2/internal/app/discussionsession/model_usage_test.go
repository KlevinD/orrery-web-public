package discussionsession

import (
	"encoding/json"
	agentcore "orrery_v2/internal/agent/core"
	"strings"
	"testing"
)

func TestDiscussionUsageReportPreservesSubsetsAndCoverage(t *testing.T) {
	usage := modelUsageFromRuntimeUsage(map[string]agentcore.TokenUsage{"model": {InputTokens: 1000, OutputTokens: 100, CachedInputTokens: 800, ReasoningTokens: 40, CachedInputTokensKnown: true, ReasoningTokensKnown: true}})
	cloned := cloneModelUsageMap(usage)
	raw, _ := json.Marshal(cloned)
	if !strings.Contains(string(raw), `"cached_input_tokens":800`) || !strings.Contains(string(raw), `"cached_input_tokens_known":true`) || cloned["model"].TotalTokens != 1100 {
		t.Fatalf("report lost details or double counted: %s", raw)
	}
	merged := mergeModelUsageMaps(usage, usage)["model"]
	if merged.CachedInputTokens != 1600 || merged.ReasoningTokens != 80 || !merged.CachedInputTokensKnown || merged.TotalTokens != 2200 {
		t.Fatalf("incorrect report aggregation: %+v", merged)
	}
	unknown := ModelUsageMap{"model": {InputTokens: 1000, OutputTokens: 100}}
	for _, sequence := range [][]ModelUsageMap{{unknown, usage}, {usage, unknown, usage}} {
		merged := mergeModelUsageMaps(sequence...)["model"]
		if merged.CachedInputTokensKnown || merged.ReasoningTokensKnown {
			t.Fatal("incomplete report claims full coverage")
		}
	}
}
