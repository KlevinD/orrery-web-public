package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageDetailsKeepLegacyJSONAndTraceSafety(t *testing.T) {
	legacy := `{"InputTokens":1000,"OutputTokens":100,"CacheReadTokens":0,"CacheWriteTokens":0}`
	var usage TokenUsage
	if err := json.Unmarshal([]byte(legacy), &usage); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(usage)
	if string(raw) != legacy {
		t.Fatalf("legacy evidence digest would change: %s", raw)
	}
	usage.CachedInputTokens, usage.ReasoningTokens = 800, 40
	usage.CachedInputTokensKnown, usage.ReasoningTokensKnown = true, true
	raw, _ = json.Marshal(SanitizeTraceValue(map[string]any{"usage": usage, "api_token": "private"}, TraceSanitizerOptions{}))
	if !strings.Contains(string(raw), `"CachedInputTokens":800`) || !strings.Contains(string(raw), `"CachedInputTokensKnown":true`) || strings.Contains(string(raw), "private") {
		t.Fatalf("metrics redacted or secret leaked: %s", raw)
	}
	bad := uint64(1001)
	event := SessionEvent{Kind: EventUsageDelta, UsageDelta: &UsageDeltaEvent{InputTokens: 1000, TotalTokens: 1000, CachedInputTokens: &bad}}
	if event.Validate() == nil {
		t.Fatal("impossible subset accepted")
	}
}
