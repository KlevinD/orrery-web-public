package discussionsession

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeResponseWithResultSummaryDoesNotAddFallbackWhenRuntimeHasNoVisibleMessage(t *testing.T) {
	response := runtimeResponseWithResultSummary(agentcore.RuntimeResponse{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{
			Type:   agentcore.RuntimeMessageStatus,
			Status: "completed",
		}},
	}, agentcore.AgentResult{Summary: agentcore.Summary{Text: EmptyRuntimeDiscussionSummaryText}})

	if got := runtimeAssistantMessage(response.Messages); got != "" {
		t.Fatalf("assistant message = %q, want no fallback message", got)
	}
	if !runtimeCompletedWithoutDiscussionOutput(response) {
		t.Fatalf("response = %+v, want completed runtime without discussion output", response)
	}
}

func TestRuntimeResponseWithResultSummaryKeepsPlanOnlyResponseAsPlan(t *testing.T) {
	response := runtimeResponseWithResultSummary(agentcore.RuntimeResponse{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{
			Type: agentcore.RuntimeMessagePlan,
			Plan: &agentcore.RuntimePlanState{
				PlanID: "plan-1",
				Status: "active",
				Items:  []agentcore.RuntimePlanItem{{ID: "step-1", Text: "Inspect runtime", Status: "in_progress"}},
			},
		}},
	}, agentcore.AgentResult{Summary: agentcore.Summary{Text: EmptyRuntimeDiscussionSummaryText}})

	if got := runtimeAssistantMessage(response.Messages); got != "" {
		t.Fatalf("assistant message = %q, want plan-only response without fallback text", got)
	}
	if len(response.Messages) != 1 || response.Messages[0].Type != agentcore.RuntimeMessagePlan {
		t.Fatalf("messages = %+v, want original plan message only", response.Messages)
	}
}

func TestRuntimeAssistantMessagePrefersFinalTextSnapshot(t *testing.T) {
	messages := []agentcore.RuntimeMessage{
		{Type: agentcore.RuntimeMessageText, Content: "draft "},
		{Type: agentcore.RuntimeMessageText, Content: "tokens"},
		{Type: agentcore.RuntimeMessageText, Content: "final answer", Status: agentcore.RuntimeMessageStatusFinal},
	}

	if got := runtimeAssistantMessage(messages); got != "final answer" {
		t.Fatalf("assistant message = %q, want final snapshot", got)
	}
}

func TestRuntimeResponseDebugPrefersFinalTextSnapshot(t *testing.T) {
	response := agentcore.RuntimeResponse{Messages: []agentcore.RuntimeMessage{
		{Type: agentcore.RuntimeMessageText, Content: "最"},
		{Type: agentcore.RuntimeMessageText, Content: "终"},
		{Type: agentcore.RuntimeMessageText, Content: "最终答案", Status: agentcore.RuntimeMessageStatusFinal},
	}}

	if got := runtimeResponseDebugText(response); got != "最终答案" {
		t.Fatalf("runtime response debug = %q, want only the final text snapshot", got)
	}
}

func TestRuntimeResponseDebugExcludesRawReasoning(t *testing.T) {
	response := agentcore.RuntimeResponse{Messages: []agentcore.RuntimeMessage{
		{Type: agentcore.RuntimeMessageThinking, Content: "private reasoning", ReasoningKind: agentcore.ReasoningContentRaw},
		{Type: agentcore.RuntimeMessageThinking, Content: "safe summary", ReasoningKind: agentcore.ReasoningContentSummary},
		{Type: agentcore.RuntimeMessageText, Content: "answer", Status: agentcore.RuntimeMessageStatusFinal},
	}}

	got := runtimeResponseDebugText(response)
	if got != "safe summary\n\nanswer" {
		t.Fatalf("runtime response debug = %q", got)
	}
}

func TestMapRunsHidesUnmarkedLegacyThinking(t *testing.T) {
	legacy := mapRuns([]RunRecord{{RunID: "legacy", ThinkingSummary: "private reasoning"}})
	if len(legacy) != 1 || legacy[0].ThinkingSummary != "" {
		t.Fatalf("legacy run exposed thinking: %+v", legacy)
	}

	marked := mapRuns([]RunRecord{{RunID: "marked", ThinkingSummary: "safe summary", ProviderMetadata: map[string]string{discussionReasoningSummaryKey: discussionReasoningSummaryPublic}}})
	if len(marked) != 1 || marked[0].ThinkingSummary != "safe summary" {
		t.Fatalf("marked run summary = %+v", marked)
	}
}
