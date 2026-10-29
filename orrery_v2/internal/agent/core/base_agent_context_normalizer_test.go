package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeAgentResultUsesContextAwareNormalizer(t *testing.T) {
	normalizer := &recordingContextResultNormalizer{}
	result, err := normalizeAgentResult(context.Background(), normalizer, AgentInput{RunID: "run-1"}, RuntimeResponse{}, time.Now())
	if err != nil {
		t.Fatalf("normalizeAgentResult: %v", err)
	}
	if !normalizer.contextCalled || normalizer.legacyCalled || result.ID != "result-context" {
		t.Fatalf("normalizer calls context=%t legacy=%t result=%+v", normalizer.contextCalled, normalizer.legacyCalled, result)
	}
}

type recordingContextResultNormalizer struct {
	contextCalled bool
	legacyCalled  bool
}

func (n *recordingContextResultNormalizer) Normalize(AgentInput, RuntimeResponse, time.Time) (AgentResult, error) {
	n.legacyCalled = true
	return AgentResult{}, errors.New("legacy normalizer should not be called")
}

func (n *recordingContextResultNormalizer) NormalizeContext(context.Context, AgentInput, RuntimeResponse, time.Time) (AgentResult, error) {
	n.contextCalled = true
	return AgentResult{ID: "result-context"}, nil
}
