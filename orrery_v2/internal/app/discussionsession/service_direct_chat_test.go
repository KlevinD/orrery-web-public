package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceSendMessageStreamEmitsDirectConversationLifecycleEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "我来单独处理。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "msg-direct"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{
		Content:        "这个实现你单独看下",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-stream",
	})
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	var finalSnapshot *ConversationDTO
	for event := range events {
		switch event.Kind {
		case StreamEventSnapshot:
			kinds = append(kinds, "snapshot")
			finalSnapshot = event.Conversation
		case StreamEventRunStarted:
			if event.Run != nil {
				kinds = append(kinds, "run_started:"+event.Run.AgentID)
			}
		case StreamEventMessageCompleted:
			if event.Message != nil {
				kinds = append(kinds, "message:"+event.Message.AuthorID)
			}
		case StreamEventRunCompleted:
			if event.Run != nil {
				kinds = append(kinds, "run_completed:"+event.Run.AgentID)
			}
		}
	}

	want := []string{"snapshot", "run_started:staffing-agent", "message:staffing-agent", "run_completed:staffing-agent", "snapshot"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("direct stream lifecycle = %v, want %v", kinds, want)
	}
	if !responder.called || responder.request.Mode != "direct" || responder.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("direct responder request = %+v", responder.request)
	}
	if finalSnapshot == nil || finalSnapshot.ConversationKind != "direct" || finalSnapshot.DirectAgentID != "staffing-agent" {
		t.Fatalf("final snapshot = %+v", finalSnapshot)
	}
}

func TestServiceDirectConversationAppendsRealtimeOutboxEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "I will handle this directly.",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder:  responder,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-direct", "msg-direct"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing",
	}); err != nil {
		t.Fatal(err)
	}

	wantTypes := []string{
		RealtimeEventDiscussionMessageCreated,
		RealtimeEventUserDiscussionListInvalidated,
		RealtimeEventDiscussionTurnStarted,
		RealtimeEventDiscussionRunStarted,
		RealtimeEventDiscussionMessageCompleted,
		RealtimeEventDiscussionRunCompleted,
		RealtimeEventDiscussionTurnCompleted,
		RealtimeEventUserDiscussionListInvalidated,
	}
	if got := strings.Join(realtime.eventTypes(), ","); got != strings.Join(wantTypes, ",") {
		t.Fatalf("realtime event types = %v, want %v", realtime.eventTypes(), wantTypes)
	}
	messageEvents := realtime.eventsByType(RealtimeEventDiscussionMessageCompleted)
	if len(messageEvents) != 1 || realtimePayloadString(t, messageEvents[0], "message_id") != "msg-direct" {
		t.Fatalf("message completed events = %+v", messageEvents)
	}
	runEvents := realtime.eventsByType(RealtimeEventDiscussionRunCompleted)
	if len(runEvents) != 1 || realtimePayloadString(t, runEvents[0], "run_id") != "run-direct" {
		t.Fatalf("run completed events = %+v", runEvents)
	}
}

func TestServiceExistingDirectConversationInfersTargetWhenMetadataMissing(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-direct",
		ConversationKind: "direct",
		Title:            "智员a",
		Status:           "completed",
		CreatedBy:        "local-user",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-direct", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "智员a", Role: "私聊对象", CreatedAt: testTime},
		{ConversationID: "conv-direct", MemberKind: "user", MemberID: "local-user", DisplayName: "Local User", Role: "话题发起人", CreatedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-before", ConversationID: "conv-direct", Role: "assistant", AuthorID: "staffing-agent", AuthorName: "智员a", Content: "在。", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run", Status: "completed", RuntimeKind: "managed"}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "继续私聊回复。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("msg-user", "run-direct", "msg-direct"),
	})

	dto, err := service.SendMessage(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		ConversationID: "conv-direct",
		Content:        "你会啥？你能干啥",
		IdempotencyKey: "direct-existing",
	})
	if err != nil {
		t.Fatal(err)
	}

	if lead.called {
		t.Fatalf("lead discussion agent should not run for existing direct conversation: %+v", lead.request)
	}
	if !responder.called || responder.request.Mode != "direct" || responder.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("direct responder request = %+v", responder.request)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].AgentID != "staffing-agent" {
		t.Fatalf("direct continuation runs = %+v", dto.Runs)
	}
	if dto.DirectTargetKind != "agent" || dto.DirectTargetID != "staffing-agent" || dto.DirectAgentID != "staffing-agent" {
		t.Fatalf("direct metadata = kind %q target %q agent %q", dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	debug, err := service.GetLatestRunDebug(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, "conv-direct")
	if err != nil {
		t.Fatal(err)
	}
	if debug.Selected.AgentID != "staffing-agent" {
		t.Fatalf("direct debug selected agent = %+v", debug.Selected)
	}
}

func TestServiceDirectConversationCreatesRuntimeApprovalEventWhenBlocked(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "blocked",
		RuntimeKind: "codex",
		SessionID:   "thread-1",
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
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-approval",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.Status != "blocked" {
		t.Fatalf("conversation status = %q, want blocked", dto.Status)
	}
	var approval *EventDTO
	for i := range dto.Events {
		if dto.Events[i].Kind == "discussion_runtime_approval_requested" {
			approval = &dto.Events[i]
			break
		}
	}
	if approval == nil {
		t.Fatalf("runtime approval event not found in events: %+v", dto.Events)
	}
	if approval.Status != "pending" {
		t.Fatalf("approval status = %q, want pending", approval.Status)
	}
	if approval.Metadata["approval_request_id"] != "cmd-1" || approval.Metadata["approval_run_id"] != "run-direct" || approval.Metadata["approval_conversation_id"] != "conv-1" {
		t.Fatalf("approval metadata = %+v", approval.Metadata)
	}
	if approval.Metadata["approval_type"] != "command_execution" || approval.Metadata["approval_tool_name"] != "exec_command" {
		t.Fatalf("approval type metadata = %+v", approval.Metadata)
	}
	if !strings.Contains(approval.Metadata["command"], "needs approval") {
		t.Fatalf("approval command metadata = %+v", approval.Metadata)
	}
}

func TestServiceDirectConversationStreamEmitsToolProgressBeforeReply(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &streamingDiscussionAgentMentionResponder{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "direct-tool-1", Name: "discussion_talent_list", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "direct-tool-1", Name: "discussion_talent_list", Status: "completed", Result: map[string]string{"agents": "staffing-agent"}}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage:  "我确认过工具结果了。",
			Status:            "completed",
			RuntimeKind:       "codex",
			ToolLoopCompleted: true,
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "direct-tool-1",
				Name:       "discussion_talent_list",
				Status:     "completed",
				Result:     map[string]string{"agents": "staffing-agent"},
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "msg-direct"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-stream-tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for event := range events {
		switch event.Kind {
		case StreamEventToolCall:
			if event.ToolCall != nil && event.ToolCall.ToolCallID == "direct-tool-1" {
				if event.RunID != "run-direct" {
					t.Fatalf("tool_call run_id = %q, want run-direct", event.RunID)
				}
				order = append(order, "tool_call")
			}
		case StreamEventToolResult:
			if event.ToolCall != nil && event.ToolCall.ToolCallID == "direct-tool-1" {
				if event.RunID != "run-direct" {
					t.Fatalf("tool_result run_id = %q, want run-direct", event.RunID)
				}
				order = append(order, "tool_result")
			}
		case StreamEventMessageCompleted:
			if event.Message != nil && event.Message.AuthorID == "staffing-agent" {
				order = append(order, "message")
			}
		}
	}

	if !responder.streamCalled {
		t.Fatalf("direct responder stream path was not used")
	}
	if strings.Join(order, ",") != "tool_call,tool_result,message" {
		t.Fatalf("direct stream order = %v, want tool progress before reply", order)
	}
}

func TestServiceDirectConversationStreamPersistsToolProgressBeforeAbort(t *testing.T) {
	repo := newMemoryRepository(t)
	events := make(chan DiscussionAgentStreamEvent)
	responder := &streamingDiscussionAgentMentionResponder{events: nil, streamEvents: events}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "event-abort"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}

	stream, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-stream-abort",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventSnapshot {
		t.Fatalf("snapshot = %+v", event)
	}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventTurnStarted {
		t.Fatalf("turn started = %+v", event)
	}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventRunStarted {
		t.Fatalf("run started = %+v", event)
	}
	events <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "direct-tool-1", Name: "exec_command", Status: "running", Arguments: map[string]string{"command": "python -c \"print('hi')\""}}}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventToolCall || event.ToolCall == nil || event.ToolCall.ToolCallID != "direct-tool-1" {
		t.Fatalf("tool call = %+v", event)
	}
	events <- claudeTaskPlanStreamEvent()
	assertClaudePlanStreamEvent(t, readLeadStreamEvent(t, stream))

	dto, err := service.AbortConversation(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	close(events)
	drainLeadStream(stream)

	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 1 || dto.Runs[0].ToolCalls[0].ToolCallID != "direct-tool-1" {
		t.Fatalf("aborted direct conversation should keep streamed tool progress, runs = %+v", dto.Runs)
	}
	assertCancelledClaudeRunProgress(t, dto.Runs[0])
}

func TestServiceDecideRuntimeApprovalResumesBlockedDirectConversation(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		{
			Status:      "blocked",
			RuntimeKind: "codex",
			SessionID:   "thread-1",
			ProviderMetadata: map[string]string{
				"approval_source":     "codex",
				"approval_request_id": "cmd-1",
				"approval_kind":       "command_execution",
				"approval_type":       "command_execution",
				"approval_tool_name":  "exec_command",
				"approval_session_id": "thread-1",
				"approval_turn_id":    "turn-1",
			},
		},
		{
			AssistantMessage: "Approved command completed.",
			Status:           "completed",
			RuntimeKind:      "codex",
			SessionID:        "thread-1",
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "run-approval-continuation", "msg-continuation"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-approval",
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideRuntimeApproval(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "approve_session",
		Message:        "yes",
		Remember:       true,
		IdempotencyKey: "test-idempotency-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.Status != "completed" {
		t.Fatalf("conversation status = %q, want completed", dto.Status)
	}
	if len(responder.requests) != 2 {
		t.Fatalf("responder requests = %d, want 2", len(responder.requests))
	}
	continuation := responder.requests[1]
	if continuation.RunID != "run-approval-continuation" || continuation.Mode != "approval_continuation" {
		t.Fatalf("continuation request = %+v", continuation)
	}
	if continuation.TargetAgent.ID != "staffing-agent" || continuation.ResumeSessionID != "thread-1" {
		t.Fatalf("continuation target/session = %+v", continuation)
	}
	if !strings.Contains(continuation.Content, "approved") || !strings.Contains(continuation.Content, "Resume from the prior provider session") {
		t.Fatalf("continuation content = %q", continuation.Content)
	}
	if len(dto.Runs) != 2 || dto.Runs[1].RunID != "run-approval-continuation" || dto.Runs[1].AgentID != "staffing-agent" || dto.Runs[1].RuntimeKind != "codex" || dto.Runs[1].SessionID != "thread-1" {
		t.Fatalf("continuation run = %+v", dto.Runs)
	}
	var decided *EventDTO
	for i := range dto.Events {
		if dto.Events[i].Kind == "discussion_runtime_approval_requested" {
			decided = &dto.Events[i]
			break
		}
	}
	if decided == nil || decided.Status != "approved" {
		t.Fatalf("decided approval event = %+v", decided)
	}
	if decided.Metadata["approval_continuation_run_id"] != "run-approval-continuation" || decided.Metadata["approval_decision"] != "approved" {
		t.Fatalf("decided approval metadata = %+v", decided.Metadata)
	}
}

func TestServiceDecideRuntimeApprovalDeniesBlockedDirectConversationAndPublishesRealtime(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	responder := &capturingDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{{
		Status:      "blocked",
		RuntimeKind: "codex",
		SessionID:   "thread-1",
		ProviderMetadata: map[string]string{
			"approval_source":     "codex",
			"approval_request_id": "cmd-1",
			"approval_kind":       "command_execution",
			"approval_type":       "command_execution",
			"approval_tool_name":  "exec_command",
			"approval_session_id": "thread-1",
			"approval_turn_id":    "turn-1",
		},
	}}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder:  responder,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-direct"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-approval",
	}); err != nil {
		t.Fatal(err)
	}
	runEventsBefore := len(realtime.eventsByType(RealtimeEventDiscussionRunCompleted))

	dto, err := service.DecideRuntimeApproval(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "deny",
		Message:        "do not run it",
		IdempotencyKey: "approval-decision-deny",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.Status != "completed" {
		t.Fatalf("conversation status = %q, want completed", dto.Status)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" {
		t.Fatalf("denied approval should complete blocked run: %+v", dto.Runs)
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-1").Events, "cmd-1")
	if !ok || event.Status != "denied" || event.Metadata["approval_decision"] != "denied" || event.Metadata["approval_decided_by"] != "local-user" {
		t.Fatalf("decided approval event = %+v, ok=%v", event, ok)
	}
	runEvents := realtime.eventsByType(RealtimeEventDiscussionRunCompleted)
	if len(runEvents) <= runEventsBefore || realtimePayloadString(t, runEvents[len(runEvents)-1], "status") != "completed" {
		t.Fatalf("run completed realtime events = %+v, before=%d", runEvents, runEventsBefore)
	}
	invalidations := realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)
	if len(invalidations) == 0 || realtimePayloadString(t, invalidations[len(invalidations)-1], "reason") != "runtime_approval_decided" {
		t.Fatalf("snapshot invalidations = %+v", invalidations)
	}
	if len(realtime.eventsByType(RealtimeEventUserDiscussionListInvalidated)) == 0 {
		t.Fatal("expected user discussion list invalidation after denied approval")
	}
}

func TestServiceDecideRuntimeApprovalIsIdempotentAfterDecision(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		{
			Status:      "blocked",
			RuntimeKind: "codex",
			SessionID:   "thread-1",
			ProviderMetadata: map[string]string{
				"approval_source":     "codex",
				"approval_request_id": "cmd-1",
				"approval_kind":       "command_execution",
				"approval_type":       "command_execution",
				"approval_tool_name":  "exec_command",
				"approval_session_id": "thread-1",
				"approval_turn_id":    "turn-1",
			},
		},
		{
			AssistantMessage: "Approved command completed.",
			Status:           "completed",
			RuntimeKind:      "codex",
			SessionID:        "thread-1",
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "run-approval-continuation", "msg-continuation"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}

	if _, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-approval",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideRuntimeApproval(context.Background(), viewer, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "approve_once",
		Message:        "yes",
		IdempotencyKey: "test-idempotency-key",
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideRuntimeApproval(context.Background(), viewer, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "approve_once",
		Message:        "yes again",
		IdempotencyKey: "approval-decision-duplicate",
	})
	if err != nil {
		t.Fatalf("duplicate decision error = %v, want nil", err)
	}
	if dto.Status != "completed" || len(dto.Runs) != 2 {
		t.Fatalf("duplicate decision should return current conversation: %+v", dto)
	}
	if len(responder.requests) != 2 {
		t.Fatalf("duplicate decision should not start another continuation, requests = %d", len(responder.requests))
	}
}

func TestServiceDecideRuntimeApprovalClosesUnavailableCancelledConversation(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: &capturingDiscussionAgentMentionResponder{},
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{
			ConversationID: "conv-1",
			Title:          "Direct",
			Status:         "cancelled",
			CreatedBy:      "local-user",
			CreatedAt:      testTime,
			UpdatedAt:      testTime,
		},
		Runs: []RunRecord{{
			RunID:          "run-direct",
			ConversationID: "conv-1",
			AgentID:        "staffing-agent",
			Status:         "running",
			StartedAt:      testTime,
			CreatedAt:      testTime,
		}},
		Events: []EventRecord{{
			EventID:        "approval-event",
			ConversationID: "conv-1",
			Kind:           discussionRuntimeApprovalEventKind,
			Status:         "pending",
			Summary:        "Runtime approval required for WebFetch",
			Metadata: map[string]string{
				"approval_request_id": "cmd-1",
				"approval_run_id":     "run-direct",
				"approval_tool_name":  "WebFetch",
			},
			CreatedAt: testTime,
		}},
	}
	if err := repo.SaveConversation(context.Background(), view.ConversationRecord); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(context.Background(), view.Runs[0]); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(context.Background(), view.Events[0]); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideRuntimeApproval(context.Background(), viewer, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "approve_once",
		Message:        "yes",
		IdempotencyKey: "approval-decision-stale",
	})
	if err != nil {
		t.Fatalf("stale decision error = %v, want nil", err)
	}
	if len(dto.Events) != 1 || dto.Events[0].Status != "unavailable" || len(dto.Events[0].AvailableActions) != 0 {
		t.Fatalf("stale approval should be closed: %+v", dto.Events)
	}
	if dto.Events[0].Metadata["approval_closed_reason"] != "conversation_cancelled" {
		t.Fatalf("closed approval metadata = %+v", dto.Events[0].Metadata)
	}
}

func TestServiceDecideRuntimeApprovalRecordsDecisionBeforeContinuationFailure(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{
		responses: []DiscussionAgentResponse{{
			Status:      "blocked",
			RuntimeKind: "claude",
			SessionID:   "thread-1",
			ProviderMetadata: map[string]string{
				"approval_source":     "claude",
				"approval_request_id": "cmd-1",
				"approval_kind":       "tool_access",
				"approval_type":       "tool_use",
				"approval_tool_name":  "WebFetch",
				"approval_session_id": "thread-1",
				"approval_turn_id":    "turn-1",
				"url":                 "https://github.com/openai/symphony/blob/main/SPEC.md",
			},
		}},
		errors: []error{nil, errors.New("runtime unavailable")},
	}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "run-approval-continuation", "event-failure"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please fetch the spec",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-approval",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := service.DecideRuntimeApproval(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "approve_once",
		Message:        "yes",
		IdempotencyKey: "test-idempotency-key",
	})
	_ = err
	view, getErr := repo.GetConversation(context.Background(), "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	event, ok := findRuntimeApprovalEvent(view.Events, "cmd-1")
	if !ok {
		t.Fatalf("approval event not found: %+v", view.Events)
	}
	if event.Status != "approved" {
		t.Fatalf("approval event status = %q, want approved", event.Status)
	}
	if event.Metadata["approval_decision"] != "approved" || event.Metadata["approval_action_id"] != "approve_once" {
		t.Fatalf("approval event metadata = %+v", event.Metadata)
	}
}

func TestServiceDecideRuntimeApprovalContinuesAfterApprovalToolResults(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_member_add"}},
		responses: []DiscussionAgentResponse{
			{
				Status:      "blocked",
				RuntimeKind: "codex",
				SessionID:   "thread-1",
				ProviderMetadata: map[string]string{
					"approval_source":     "codex",
					"approval_request_id": "cmd-1",
					"approval_kind":       "command_execution",
					"approval_type":       "command_execution",
					"approval_tool_name":  "exec_command",
					"approval_session_id": "thread-1",
					"approval_turn_id":    "turn-1",
				},
			},
			{
				Status:      "completed",
				RuntimeKind: "codex",
				SessionID:   "thread-1",
				ToolCalls: []ToolCallDTO{{
					ToolCallID: "add-member",
					Name:       "discussion_member_add",
					Status:     "completed",
					Arguments:  map[string]string{"agent_ids": "agent-frontend"},
				}},
			},
			{
				AssistantMessage: "已把林前端加入讨论，继续推进。",
				Status:           "completed",
				RuntimeKind:      "codex",
				SessionID:        "thread-1",
			},
		},
	}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-direct", "run-approval-continuation", "msg-continuation"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-approval",
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideRuntimeApproval(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-1",
		ApprovalID:     "cmd-1",
		ActionID:       "approve_session",
		Message:        "yes",
		Remember:       true,
		IdempotencyKey: "test-idempotency-key",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(responder.requests) != 3 {
		t.Fatalf("responder requests = %d, want blocked + approval continuation + tool-result continuation", len(responder.requests))
	}
	continuation := responder.requests[2]
	if continuation.Mode != discussionToolResultContinuationMode || !strings.Contains(continuation.Content, "discussion_member_add") || !strings.Contains(continuation.Content, "added_count=1") {
		t.Fatalf("tool-result continuation request = %+v", continuation)
	}
	if len(dto.Messages) < 2 || dto.Messages[len(dto.Messages)-1].Content != "已把林前端加入讨论，继续推进。" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 2 || len(dto.Runs[1].ToolCalls) != 1 || dto.Runs[1].ToolCalls[0].Result["added_agent_ids"] != "agent-frontend" {
		t.Fatalf("continuation run tool calls = %+v", dto.Runs)
	}
}
