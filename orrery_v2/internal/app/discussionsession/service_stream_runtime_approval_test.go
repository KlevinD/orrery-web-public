package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestServiceSendMessageStreamForwardsLiveRuntimeApproval(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamApprovalRequested, Approval: &EventDTO{
			EventID:        "approval-live",
			ConversationID: "conv-live",
			Kind:           discussionRuntimeApprovalEventKind,
			Status:         "pending",
			Metadata: map[string]string{
				runtimecore.ApprovalMetadataRequestID: "call-live",
				runtimecore.ApprovalMetadataToolName:  "bash",
			},
			CreatedAt: testTime,
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Done",
			Status:           "completed",
			RuntimeKind:      "pi",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-live", "msg-user", "run-live", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
		Content:        "Inspect files",
		IdempotencyKey: "send-live-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	var approval *StreamEventDTO
	for event := range events {
		if event.Kind == StreamEventApprovalRequested {
			eventCopy := event
			approval = &eventCopy
		}
	}
	if approval == nil || approval.RunID != "run-live" || approval.EventID != "approval-live" || approval.Approval == nil || approval.Approval.Metadata[runtimecore.ApprovalMetadataRequestID] != "call-live" {
		t.Fatalf("live approval stream event = %+v", approval)
	}
}

func TestServiceSendMessageStreamPersistsBlockedRuntimeApprovalWithoutRunningToolLoop(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "I will read the Symphony spec first."},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{
			ToolCallID: "cmd-1",
			Name:       "exec_command",
			Status:     "blocked",
			Arguments:  map[string]string{"command": "curl -sL https://example.com/spec.md"},
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "I will read the Symphony spec first.",
			Status:           "blocked",
			RuntimeKind:      "codex",
			SessionID:        "session-1",
			ProviderMetadata: map[string]string{
				"approval_source":     "codex",
				"approval_request_id": "cmd-1",
				"approval_kind":       "command_execution",
				"approval_tool_name":  "exec_command",
				"command":             "curl -sL https://example.com/spec.md",
			},
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "cmd-1",
				Name:       "exec_command",
				Status:     "blocked",
				Arguments:  map[string]string{"command": "curl -sL https://example.com/spec.md"},
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Read the spec", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var gotKinds []string
	for event := range events {
		gotKinds = append(gotKinds, event.Kind)
		if event.Kind == StreamEventError {
			t.Fatalf("blocked approval must not emit stream error: %+v", event)
		}
	}
	wantKinds := []string{
		StreamEventSnapshot,
		StreamEventTurnStarted,
		StreamEventRunStarted,
		StreamEventTokenDelta,
		StreamEventToolCall,
		StreamEventMessageCompleted,
		StreamEventRunCompleted,
		StreamEventTurnCompleted,
		StreamEventSnapshot,
	}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("stream kinds = %v, want %v", gotKinds, wantKinds)
	}

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "blocked" || len(detail.Runs) != 1 || detail.Runs[0].Status != "blocked" {
		t.Fatalf("blocked conversation detail = %+v", detail)
	}
	if len(detail.Messages) != 2 || !strings.Contains(detail.Messages[1].Content, "Symphony spec") {
		t.Fatalf("assistant pre-approval text should be preserved: %+v", detail.Messages)
	}
	if len(detail.Events) != 1 || detail.Events[0].Kind != "discussion_runtime_approval_requested" || detail.Events[0].Status != "pending" {
		t.Fatalf("approval events = %+v", detail.Events)
	}
	if detail.Events[0].Metadata["command"] != "curl -sL https://example.com/spec.md" ||
		detail.Events[0].Metadata["approval_run_id"] != "run-1" {
		t.Fatalf("approval metadata = %+v", detail.Events[0].Metadata)
	}
	runEvents := realtime.eventsByType(RealtimeEventDiscussionRunCompleted)
	if len(runEvents) != 1 || realtimePayloadString(t, runEvents[0], "status") != "blocked" {
		t.Fatalf("run completed realtime events = %+v", runEvents)
	}
	invalidations := realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)
	if len(invalidations) != 1 || realtimePayloadString(t, invalidations[0], "reason") != "runtime_approval_requested" {
		t.Fatalf("snapshot invalidation events = %+v", invalidations)
	}
}

func TestServicePersonalAssistantPersistsBlockedRuntimeApprovalWithoutAgentMember(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "Approval is required.",
			Status:           "blocked",
			RuntimeKind:      "pi",
			ProviderMetadata: map[string]string{
				"approval_request_id": "approval-1",
				"approval_kind":       "command_execution",
				"approval_tool_name":  "exec_command",
			},
			ToolCalls: []ToolCallDTO{{ToolCallID: "approval-1", Name: "exec_command", Status: "blocked"}},
		},
	}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-assistant", "msg-user", "run-assistant", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindHuman}

	events, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{
		ConversationKind: ConversationKindPersonalAssistant,
		Content:          "Run the command",
		IdempotencyKey:   "send-assistant-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Kind == StreamEventError {
			t.Fatalf("blocked personal assistant approval emitted an error: %+v", event)
		}
	}
	detail, err := service.GetConversation(context.Background(), viewer, "conv-assistant")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Members) != 1 || len(detail.Messages) != 2 || detail.Messages[1].AuthorName != "Personal Assistant" || detail.Messages[1].Content != "Approval is required." {
		t.Fatalf("blocked personal assistant detail = %+v", detail)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "blocked" || detail.Runs[0].AgentName != "Personal Assistant" {
		t.Fatalf("blocked personal assistant runs = %+v", detail.Runs)
	}

	_, err = service.SendMessageStream(context.Background(), viewer, SendMessageCommand{
		ConversationID: "conv-assistant",
		Content:        "Try again",
		IdempotencyKey: "send-assistant-retry-before-approval",
	})
	if !errors.Is(err, ErrRuntimeApprovalPending) {
		t.Fatalf("retry before approval error = %v, want ErrRuntimeApprovalPending", err)
	}
	afterRetry, err := service.GetConversation(context.Background(), viewer, "conv-assistant")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRetry.Messages) != 2 || len(afterRetry.Runs) != 1 || len(afterRetry.Events) != 1 {
		t.Fatalf("retry before approval mutated conversation: messages=%d runs=%d events=%d", len(afterRetry.Messages), len(afterRetry.Runs), len(afterRetry.Events))
	}
}

func TestServiceSendMessageStreamReopensCompletedConversationBeforeRuntimeApproval(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-existing", Title: "Existing", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-existing", "user-1")
	if err := repo.SaveMessage(context.Background(), MessageRecord{MessageID: "msg-before", ConversationID: "conv-existing", Role: "assistant", AuthorID: "lead-agent", AuthorName: "顾明澈", Content: "done", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			Status:      "blocked",
			RuntimeKind: "claude",
			SessionID:   "session-1",
			ProviderMetadata: map[string]string{
				"approval_source":     "claude",
				"approval_request_id": "approval-1",
				"approval_kind":       "tool_use",
				"approval_tool_name":  "WebSearch",
			},
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "approval-1",
				Name:       "WebSearch",
				Status:     "blocked",
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:       fixedIDGen("msg-user", "run-approval"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-existing", Content: "search first", IdempotencyKey: "send-approval"})
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Kind == StreamEventError {
			t.Fatalf("blocked approval must not emit stream error: %+v", event)
		}
	}

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-existing")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "blocked" || len(detail.Runs) != 1 || detail.Runs[0].Status != "blocked" {
		t.Fatalf("conversation should be blocked on approval, got %+v", detail)
	}
	if len(detail.Events) != 1 {
		t.Fatalf("approval events = %+v", detail.Events)
	}
	approval := detail.Events[0]
	if approval.Status != "pending" || len(approval.AvailableActions) == 0 {
		t.Fatalf("approval should remain actionable pending, got %+v", approval)
	}
}
