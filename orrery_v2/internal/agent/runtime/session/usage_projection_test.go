package session

import (
	agentcore "orrery_v2/internal/agent/core"
	"testing"
	"time"
)

func TestUsageProjectionReportsSubsetsWithoutDoubleCountingAndTracksCoverage(t *testing.T) {
	for _, missingFirst := range []bool{false, true} {
		p := NewResponseProjection(agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{Model: "test"}}, agentcore.RuntimeKindManaged, time.Now())
		cached, reasoning := uint64(800), uint64(40)
		known := &agentcore.UsageDeltaEvent{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100, CachedInputTokens: &cached, ReasoningTokens: &reasoning}
		missing := &agentcore.UsageDeltaEvent{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100}
		sequence := []*agentcore.UsageDeltaEvent{known, known, missing, known}
		if missingFirst {
			sequence = []*agentcore.UsageDeltaEvent{missing, known}
		}
		for i, delta := range sequence {
			if err := p.Fold(agentcore.SessionEnvelope{Event: agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: delta}}); err != nil {
				t.Fatal(err)
			}
			u := p.Response(time.Now()).Usage["test"]
			wantKnown := !missingFirst && i < 2
			if u.CachedInputTokensKnown != wantKnown || u.ReasoningTokensKnown != wantKnown {
				t.Fatalf("missing delta must keep coverage unknown: %+v", u)
			}
			if u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens != int64((i+1)*1100) {
				t.Fatal("subset counted twice")
			}
		}
		u := p.Response(time.Now()).Usage["test"]
		if u.CachedInputTokens == 0 || u.ReasoningTokens == 0 {
			t.Fatal("observed partial details lost")
		}
	}
	zero := uint64(0)
	p := NewResponseProjection(agentcore.RuntimeRequest{}, agentcore.RuntimeKindManaged, time.Now())
	if err := p.Fold(agentcore.SessionEnvelope{Event: agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: &agentcore.UsageDeltaEvent{CachedInputTokens: &zero, ReasoningTokens: &zero}}}); err != nil {
		t.Fatal(err)
	}
	u := p.Response(time.Now()).Usage[string(agentcore.RuntimeKindManaged)]
	if !u.CachedInputTokensKnown || !u.ReasoningTokensKnown {
		t.Fatal("reported zero lost")
	}
}
