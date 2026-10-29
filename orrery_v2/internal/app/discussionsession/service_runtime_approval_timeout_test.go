package discussionsession

import (
	"context"
	"encoding/json"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestServiceDecideHILRuntimeApprovalResumesCompletedProviderRun(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Wechat draft created.", Status: "completed", RuntimeKind: "pi", SessionID: "session-wechat",
	}}
	service := newTestService(ServiceOptions{
		Repository: repo, MentionResponder: responder, Clock: fixedClock{now: testTime},
		IDGenerator: fixedIDGen("run-wechat-continuation", "msg-wechat-continuation"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-wechat-timeout", ConversationKind: "direct", DirectAgentID: "staffing-agent",
		DirectTargetKind: "agent", DirectTargetID: "staffing-agent", Title: "Wechat", Status: "completed",
		CreatedBy: "human-ding", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-wechat-timeout", "human-ding")
	if err := repo.SaveMessage(ctx, MessageRecord{
		MessageID: "msg-wechat", ConversationID: "conv-wechat-timeout", Role: "user", AuthorID: "human-ding",
		Content: "Create the approved Wechat draft", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID: "run-wechat-timeout", ConversationID: "conv-wechat-timeout", AgentID: "staffing-agent", Status: "completed",
		RuntimeKind: "pi", SessionID: "session-wechat", CreatedAt: testTime, StartedAt: testTime, CompletedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	material, err := json.Marshal(runtimeApprovalMaterial{Version: 1, Kind: "wechat_draft", MaterialHash: "sha256:material", Fields: map[string]string{
		"account_id": "wechat-main", "intent_id": "intent-approved", "intent_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-wechat", ConversationID: "conv-wechat-timeout", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			runtimecore.ApprovalMetadataRequestID: "call-wechat", runtimecore.ApprovalMetadataSessionID: "session-wechat",
			runtimecore.ApprovalMetadataToolName: wechatDraftApprovalTool, "approval_run_id": "run-wechat-timeout",
			"approval_agent_id": "staffing-agent", "approval_runtime_kind": "pi", "hil_decision_id": "hil-wechat",
			approvalMaterialMetadataKey: string(material),
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideHILRuntimeApproval(ctx, ViewerRef{ViewerID: "human-ding", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-wechat-timeout", HILDecisionID: "hil-wechat", ActionID: "approve_once",
		HILDecisionStatus: "resolved", HILReceiptID: "receipt-wechat", HILReceiptEffect: "authorize",
		IdempotencyKey: "hil-runtime-resolution:hil-wechat:receipt-wechat",
	})
	if err != nil {
		t.Fatalf("DecideHILRuntimeApproval: %v", err)
	}
	if dto.Status != "completed" || len(dto.Runs) != 2 || len(responder.requests) != 1 {
		t.Fatalf("continued HIL conversation = %+v requests=%d", dto, len(responder.requests))
	}
	request := responder.requests[0]
	if request.ResumeSessionID != "session-wechat" || request.ProviderSpecific["approval_material_intent_id"] != "intent-approved" ||
		request.ProviderSpecific["hil_receipt_id"] != "receipt-wechat" || request.ProviderSpecific["approval_decided_by"] != "human-ding" {
		t.Fatalf("HIL continuation request = %+v", request)
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-wechat-timeout").Events, "call-wechat")
	if !ok || event.Status != "approved" || event.Metadata["approval_continuation_run_id"] != "run-wechat-continuation" {
		t.Fatalf("continued HIL event = %+v ok=%v", event, ok)
	}
}

func TestServiceDecideHILRuntimeApprovalResumesRunningProviderRunWithoutLiveResponder(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Wechat draft created.", Status: "completed", RuntimeKind: "pi", SessionID: "session-wechat-restart",
	}}
	service := newTestService(ServiceOptions{
		Repository: repo, MentionResponder: responder, Clock: fixedClock{now: testTime},
		IDGenerator: fixedIDGen("run-wechat-restart-continuation", "msg-wechat-restart-continuation"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-wechat-restart", ConversationKind: "direct", DirectAgentID: "staffing-agent",
		DirectTargetKind: "agent", DirectTargetID: "staffing-agent", Title: "Wechat", Status: "running",
		CreatedBy: "human-ding", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-wechat-restart", "human-ding")
	if err := repo.SaveMessage(ctx, MessageRecord{
		MessageID: "msg-wechat-restart", ConversationID: "conv-wechat-restart", Role: "user", AuthorID: "human-ding",
		Content: "Create the approved Wechat draft", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID: "run-wechat-restart", ConversationID: "conv-wechat-restart", AgentID: "staffing-agent", Status: "running",
		RuntimeKind: "pi", SessionID: "session-wechat-restart", CreatedAt: testTime, StartedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	material, err := json.Marshal(runtimeApprovalMaterial{Version: 1, Kind: "wechat_draft", MaterialHash: "sha256:restart-material", Fields: map[string]string{
		"account_id": "wechat-main", "intent_id": "intent-approved-after-restart", "intent_hash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-wechat-restart", ConversationID: "conv-wechat-restart", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			runtimecore.ApprovalMetadataRequestID: "call-wechat-restart", runtimecore.ApprovalMetadataSessionID: "session-wechat-restart",
			runtimecore.ApprovalMetadataToolName: wechatDraftApprovalTool, "approval_run_id": "run-wechat-restart",
			"approval_agent_id": "staffing-agent", "approval_runtime_kind": "pi", "hil_decision_id": "hil-wechat-restart",
			approvalMaterialMetadataKey: string(material),
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideHILRuntimeApproval(ctx, ViewerRef{ViewerID: "human-ding", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-wechat-restart", HILDecisionID: "hil-wechat-restart", ActionID: "approve_once",
		HILDecisionStatus: "resolved", HILReceiptID: "receipt-wechat-restart", HILReceiptEffect: "authorize",
		ResumeWithoutLiveResponder: true, IdempotencyKey: "hil-runtime-resolution:hil-wechat-restart:receipt-wechat-restart",
	})
	if err != nil {
		t.Fatalf("DecideHILRuntimeApproval: %v", err)
	}
	if dto.Status != "completed" || len(dto.Runs) != 2 || len(responder.requests) != 1 {
		t.Fatalf("continued HIL conversation = %+v requests=%d", dto, len(responder.requests))
	}
	request := responder.requests[0]
	if request.ResumeSessionID != "session-wechat-restart" || request.ProviderSpecific["hil_receipt_id"] != "receipt-wechat-restart" {
		t.Fatalf("HIL continuation request = %+v", request)
	}
}

func TestServiceDecideHILRuntimeApprovalPersistsLateReceiptOnApprovedEvent(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-late-hil-receipt", TenantID: "tenant-hil", Title: "Late HIL receipt", Status: "completed",
		CreatedBy: "human-ding", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-late-hil-receipt", ConversationID: "conv-late-hil-receipt", Kind: discussionRuntimeApprovalEventKind, Status: "approved",
		Metadata: map[string]string{
			"approval_hil_required": "true", "hil_decision_id": "hil-late-receipt", "approval_request_id": "call-late-receipt",
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	command := RuntimeApprovalDecisionCommand{
		ConversationID: "conv-late-hil-receipt", HILDecisionID: "hil-late-receipt", ActionID: "approve_once",
		HILDecisionStatus: "resolved", HILReceiptID: "receipt-late", HILReceiptEffect: "authorize",
		IdempotencyKey: "hil-runtime-resolution:hil-late-receipt:receipt-late",
	}
	for range 2 {
		if _, err := service.DecideHILRuntimeApproval(ctx, ViewerRef{ViewerID: "human-ding", ViewerKind: ViewerKindUser}, command); err != nil {
			t.Fatalf("DecideHILRuntimeApproval: %v", err)
		}
	}
	event, ok := findRuntimeApprovalEventByHILDecisionID(mustConversationView(t, repo, "conv-late-hil-receipt").Events, "hil-late-receipt")
	if !ok || event.Status != "approved" || event.Metadata["hil_decision_status"] != "resolved" ||
		event.Metadata["hil_receipt_id"] != "receipt-late" || event.Metadata["hil_receipt_effect"] != "authorize" {
		t.Fatalf("late HIL receipt event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalMappingKeepsBlockedRunActionableWhenConversationIsTerminal(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{
			ConversationID: "conv-timeout",
			Title:          "Direct",
			Status:         "completed",
			CreatedBy:      "local-user",
			CreatedAt:      testTime,
			UpdatedAt:      testTime,
		},
		Runs: []RunRecord{{
			RunID:          "run-timeout",
			ConversationID: "conv-timeout",
			AgentID:        "staffing-agent",
			Status:         "blocked",
			RuntimeKind:    "claude",
			SessionID:      "session-timeout",
			CreatedAt:      testTime,
			StartedAt:      testTime,
			CompletedAt:    testTime,
		}},
		Events: []EventRecord{{
			EventID:        "approval-event",
			ConversationID: "conv-timeout",
			Kind:           discussionRuntimeApprovalEventKind,
			Status:         "pending",
			Summary:        "Runtime approval required for WebSearch",
			Metadata: map[string]string{
				runtimecore.ApprovalMetadataRequestID: "approval-1",
				runtimecore.ApprovalMetadataRunID:     "run-timeout",
				runtimecore.ApprovalMetadataSource:    "claude",
				runtimecore.ApprovalMetadataToolName:  "WebSearch",
			},
			CreatedAt: testTime,
		}},
	}

	dto := mapConversationDTO(view)

	if len(dto.Events) != 1 {
		t.Fatalf("events = %+v", dto.Events)
	}
	event := dto.Events[0]
	if event.Status != "pending" {
		t.Fatalf("approval status = %q, want pending: %+v", event.Status, event)
	}
	if len(event.AvailableActions) == 0 {
		t.Fatalf("blocked approval should stay actionable: %+v", event)
	}
}

func TestRuntimeApprovalMappingKeepsRunningRunActionableWhenConversationIsTerminal(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{
			ConversationID: "conv-timeout",
			Title:          "Direct",
			Status:         "completed",
			CreatedBy:      "local-user",
			CreatedAt:      testTime,
			UpdatedAt:      testTime,
		},
		Runs: []RunRecord{{
			RunID:          "run-timeout",
			ConversationID: "conv-timeout",
			AgentID:        "staffing-agent",
			Status:         "running",
			RuntimeKind:    "claude",
			SessionID:      "session-timeout",
			CreatedAt:      testTime,
			StartedAt:      testTime,
		}},
		Events: []EventRecord{{
			EventID:        "approval-event",
			ConversationID: "conv-timeout",
			Kind:           discussionRuntimeApprovalEventKind,
			Status:         "pending",
			Summary:        "Runtime approval required for WebSearch",
			Metadata: map[string]string{
				runtimecore.ApprovalMetadataRequestID: "approval-1",
				runtimecore.ApprovalMetadataRunID:     "run-timeout",
				runtimecore.ApprovalMetadataSource:    "claude",
				runtimecore.ApprovalMetadataToolName:  "WebSearch",
			},
			CreatedAt: testTime,
		}},
	}

	dto := mapConversationDTO(view)

	if len(dto.Events) != 1 {
		t.Fatalf("events = %+v", dto.Events)
	}
	event := dto.Events[0]
	if event.Status != "pending" {
		t.Fatalf("approval status = %q, want pending: %+v", event.Status, event)
	}
	if len(event.AvailableActions) == 0 {
		t.Fatalf("running approval should stay actionable: %+v", event)
	}
}

func TestRuntimeApprovalMappingMarksCancelledConversationUnavailable(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{
			ConversationID: "conv-cancelled",
			Title:          "Direct",
			Status:         "cancelled",
			CreatedBy:      "local-user",
			CreatedAt:      testTime,
			UpdatedAt:      testTime,
		},
		Runs: []RunRecord{{
			RunID:          "run-cancelled",
			ConversationID: "conv-cancelled",
			AgentID:        "staffing-agent",
			Status:         "running",
			RuntimeKind:    "claude",
			SessionID:      "session-cancelled",
			CreatedAt:      testTime,
			StartedAt:      testTime,
		}},
		Events: []EventRecord{{
			EventID:        "approval-event",
			ConversationID: "conv-cancelled",
			Kind:           discussionRuntimeApprovalEventKind,
			Status:         "pending",
			Summary:        "Runtime approval required for WebSearch",
			Metadata: map[string]string{
				runtimecore.ApprovalMetadataRequestID: "approval-1",
				runtimecore.ApprovalMetadataRunID:     "run-cancelled",
				runtimecore.ApprovalMetadataSource:    "claude",
				runtimecore.ApprovalMetadataToolName:  "WebSearch",
			},
			CreatedAt: testTime,
		}},
	}

	dto := mapConversationDTO(view)

	if len(dto.Events) != 1 {
		t.Fatalf("events = %+v", dto.Events)
	}
	event := dto.Events[0]
	if event.Status != "unavailable" {
		t.Fatalf("approval status = %q, want unavailable: %+v", event.Status, event)
	}
	if len(event.AvailableActions) != 0 {
		t.Fatalf("cancelled approval should not be actionable: %+v", event)
	}
}

func TestServiceDecideRuntimeApprovalResumesBlockedRunWhenConversationIsTerminal(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Approved web search completed.",
		Status:           "completed",
		RuntimeKind:      "claude",
		SessionID:        "session-timeout",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("run-continuation", "msg-continuation"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-timeout",
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		DirectTargetKind: "agent",
		DirectTargetID:   "staffing-agent",
		Title:            "Direct",
		Status:           "completed",
		CreatedBy:        "local-user",
		CreatedByName:    "Local User",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-timeout", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{
		MessageID:      "msg-user",
		ConversationID: "conv-timeout",
		Role:           "user",
		AuthorID:       "local-user",
		AuthorName:     "Local User",
		Content:        "Search the web",
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID:          "run-timeout",
		ConversationID: "conv-timeout",
		AgentID:        "staffing-agent",
		Status:         "blocked",
		RuntimeKind:    "claude",
		SessionID:      "session-timeout",
		ProviderMetadata: map[string]string{
			runtimecore.ApprovalMetadataSource:    "claude",
			runtimecore.ApprovalMetadataRequestID: "approval-1",
			runtimecore.ApprovalMetadataKind:      "tool",
			runtimecore.ApprovalMetadataType:      "tool_use",
			runtimecore.ApprovalMetadataToolName:  "WebSearch",
			runtimecore.ApprovalMetadataSessionID: "session-timeout",
		},
		CreatedAt:   testTime,
		StartedAt:   testTime,
		CompletedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID:        "approval-event",
		ConversationID: "conv-timeout",
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Summary:        "Runtime approval required for WebSearch",
		Metadata: map[string]string{
			runtimecore.ApprovalMetadataSource:    "claude",
			runtimecore.ApprovalMetadataRequestID: "approval-1",
			runtimecore.ApprovalMetadataKind:      "tool",
			runtimecore.ApprovalMetadataType:      "tool_use",
			runtimecore.ApprovalMetadataToolName:  "WebSearch",
			runtimecore.ApprovalMetadataSessionID: "session-timeout",
			"approval_run_id":                     "run-timeout",
			"approval_agent_id":                   "staffing-agent",
			"approval_runtime_kind":               "claude",
		},
		CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-timeout",
		ApprovalID:     "approval-1",
		ActionID:       "approve_session",
		Message:        "approved",
		Remember:       true,
		IdempotencyKey: "approval-decision",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}

	if dto.Status != "completed" || len(dto.Runs) != 2 || dto.Runs[1].RunID != "run-continuation" {
		t.Fatalf("continuation conversation = %+v", dto)
	}
	if len(responder.requests) != 1 {
		t.Fatalf("continuation requests = %d, want 1", len(responder.requests))
	}
	continuation := responder.requests[0]
	if continuation.Mode != discussionApprovalContinuationMode || continuation.ResumeSessionID != "session-timeout" {
		t.Fatalf("continuation request = %+v", continuation)
	}
	if continuation.ProviderSpecific["approval_continuation"] != "true" || continuation.ProviderSpecific[runtimecore.ApprovalMetadataRequestID] != "approval-1" {
		t.Fatalf("continuation provider specific = %+v", continuation.ProviderSpecific)
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-timeout").Events, "approval-1")
	if !ok || event.Status != "approved" || event.Metadata["approval_continuation_run_id"] != "run-continuation" {
		t.Fatalf("approval event = %+v ok=%v", event, ok)
	}
}

func TestServiceDecideRuntimeApprovalBlocksRunningRunBeforeStaleResume(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Approved web search completed.",
		Status:           "completed",
		RuntimeKind:      "claude",
		SessionID:        "session-timeout",
	}}
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		MentionResponder:  responder,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("run-continuation", "msg-continuation"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-stale",
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		DirectTargetKind: "agent",
		DirectTargetID:   "staffing-agent",
		Title:            "Direct",
		Status:           "completed",
		CreatedBy:        "local-user",
		CreatedByName:    "Local User",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-stale", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{
		MessageID:      "msg-user",
		ConversationID: "conv-stale",
		Role:           "user",
		AuthorID:       "local-user",
		AuthorName:     "Local User",
		Content:        "Search the web",
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID:          "run-stale",
		ConversationID: "conv-stale",
		AgentID:        "staffing-agent",
		Status:         "running",
		RuntimeKind:    "claude",
		SessionID:      "session-timeout",
		ProviderMetadata: map[string]string{
			runtimecore.ApprovalMetadataSource:    "claude",
			runtimecore.ApprovalMetadataRequestID: "approval-1",
			runtimecore.ApprovalMetadataKind:      "tool",
			runtimecore.ApprovalMetadataType:      "tool_use",
			runtimecore.ApprovalMetadataToolName:  "WebSearch",
			runtimecore.ApprovalMetadataSessionID: "session-timeout",
		},
		CreatedAt: testTime,
		StartedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID:        "approval-event",
		ConversationID: "conv-stale",
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Summary:        "Runtime approval required for WebSearch",
		Metadata: map[string]string{
			runtimecore.ApprovalMetadataSource:    "claude",
			runtimecore.ApprovalMetadataRequestID: "approval-1",
			runtimecore.ApprovalMetadataKind:      "tool",
			runtimecore.ApprovalMetadataType:      "tool_use",
			runtimecore.ApprovalMetadataToolName:  "WebSearch",
			runtimecore.ApprovalMetadataSessionID: "session-timeout",
			"approval_run_id":                     "run-stale",
			"approval_agent_id":                   "staffing-agent",
			"approval_runtime_kind":               "claude",
		},
		CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-stale",
		ApprovalID:     "approval-1",
		ActionID:       "approve_session",
		Message:        "approved",
		Remember:       true,
		IdempotencyKey: "approval-decision",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}

	if dto.Status != "completed" || len(dto.Runs) != 2 || dto.Runs[1].RunID != "run-continuation" {
		t.Fatalf("continuation conversation = %+v", dto)
	}
	if dto.Runs[0].Status != "blocked" {
		t.Fatalf("stale run status = %q, want blocked before continuation", dto.Runs[0].Status)
	}
	if len(responder.requests) != 1 {
		t.Fatalf("continuation requests = %d, want 1", len(responder.requests))
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-stale").Events, "approval-1")
	if !ok || event.Status != "approved" || event.Metadata["approval_decision"] == "unavailable" {
		t.Fatalf("approval event should be approved, not unavailable: %+v ok=%v", event, ok)
	}
	if len(realtime.eventsByType(RealtimeEventDiscussionRunCompleted)) == 0 {
		t.Fatal("expected stale running run to emit a realtime status change")
	}
}
