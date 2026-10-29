package discussionsession

import (
	"context"
	"testing"
)

func TestServiceDirectConversationStreamCreatesActionableRuntimeApprovalWhenBlocked(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	responder := &streamingDiscussionAgentMentionResponder{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "I need approval before running this command."},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{
			ToolCallID: "cmd-1",
			Name:       "exec_command",
			Status:     "blocked",
			Arguments:  map[string]string{"command": "python -c \"print('needs approval')\""},
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "I need approval before running this command.",
			Status:           "blocked",
			RuntimeKind:      "codex",
			SessionID:        "thread-1",
			ProviderMetadata: map[string]string{
				"approval_source":     "codex",
				"approval_request_id": "cmd-1",
				"approval_kind":       "command_execution",
				"approval_type":       "command_execution",
				"approval_tool_name":  "exec_command",
				"approval_session_id": "thread-1",
				"approval_turn_id":    "turn-1",
				"command":             "python -c \"print('needs approval')\"",
				"sandbox_permissions": "require_escalated",
			},
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "cmd-1",
				Name:       "exec_command",
				Status:     "blocked",
				Arguments:  map[string]string{"command": "python -c \"print('needs approval')\""},
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder:  responder,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-direct", "msg-direct"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-stream-approval",
	})
	if err != nil {
		t.Fatal(err)
	}

	var finalSnapshot *ConversationDTO
	var sawToolCall bool
	var sawRunCompleted bool
	for event := range events {
		if event.Kind == StreamEventError {
			t.Fatalf("blocked direct approval must not emit stream error: %+v", event)
		}
		if event.Kind == StreamEventToolCall && event.ToolCall != nil && event.ToolCall.ToolCallID == "cmd-1" {
			sawToolCall = true
			if event.RunID != "run-direct" {
				t.Fatalf("tool_call run_id = %q, want run-direct", event.RunID)
			}
		}
		if event.Kind == StreamEventRunCompleted && event.Run != nil && event.Run.RunID == "run-direct" {
			sawRunCompleted = true
		}
		if event.Kind == StreamEventSnapshot {
			finalSnapshot = event.Conversation
		}
	}
	if !responder.streamCalled {
		t.Fatalf("direct responder stream path was not used")
	}
	if !sawToolCall || !sawRunCompleted {
		t.Fatalf("missing direct stream tool/run events: sawToolCall=%v sawRunCompleted=%v", sawToolCall, sawRunCompleted)
	}
	if finalSnapshot == nil || finalSnapshot.Status != "blocked" || finalSnapshot.ConversationKind != "direct" {
		t.Fatalf("final direct approval snapshot = %+v", finalSnapshot)
	}
	if len(finalSnapshot.Events) != 1 {
		t.Fatalf("approval events = %+v", finalSnapshot.Events)
	}
	approval := finalSnapshot.Events[0]
	if approval.Kind != "discussion_runtime_approval_requested" || approval.Status != "pending" || len(approval.AvailableActions) == 0 {
		t.Fatalf("direct stream approval should be actionable pending, got %+v", approval)
	}
	if approval.Metadata["approval_run_id"] != "run-direct" || approval.Metadata["approval_request_id"] != "cmd-1" {
		t.Fatalf("approval metadata = %+v", approval.Metadata)
	}
	invalidations := realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)
	if len(invalidations) == 0 {
		t.Fatalf("direct runtime approval should publish snapshot invalidation")
	}
}
