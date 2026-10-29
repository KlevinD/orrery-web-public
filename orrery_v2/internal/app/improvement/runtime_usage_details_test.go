package improvement

import (
	"context"
	agentcore "orrery_v2/internal/agent/core"
	"reflect"
	"testing"
)

func TestRuntimeEvidencePersistsUsageDetailsAndRejectsImpossibleSubsets(t *testing.T) {
	ctx := context.Background()
	_, e := runtimeEvidenceFixture(t, "attempt-usage")
	runtime := NewRuntimeEvidenceStore(testStore(t).core)
	if err := runtime.Capture(ctx, e); err != nil {
		t.Fatal(err)
	}
	terminal := runtimeEvidenceTerminal()
	want := agentcore.TokenUsage{InputTokens: 1000, OutputTokens: 100, CachedInputTokens: 800, ReasoningTokens: 40, CachedInputTokensKnown: true, ReasoningTokensKnown: true}
	terminal.Response.Usage["model-a"] = want
	if _, err := runtime.Complete(ctx, e, terminal); err != nil {
		t.Fatal(err)
	}
	stored, err := runtime.GetTerminal(ctx, e.Scope.TenantID, e.AttemptID)
	if err != nil || !reflect.DeepEqual(stored.Usage["model-a"], want) {
		t.Fatalf("usage details lost in persisted evidence: %+v %v", stored, err)
	}
	want.CachedInputTokens = 1001
	terminal.Response.Usage["model-a"] = want
	if _, err := buildRuntimeTerminal(e, terminal, "succeeded"); err == nil {
		t.Fatal("impossible cached subset accepted")
	}
}
