package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestRuntimeApprovalCoordinatorExposesConfigurableHoldTimeout(t *testing.T) {
	if got := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{}).RuntimeApprovalHoldTimeout(); got != 10*time.Minute {
		t.Fatalf("default hold timeout = %s, want 10m", got)
	}
	if got := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{ApprovalHoldTimeout: 3 * time.Minute}).RuntimeApprovalHoldTimeout(); got != 3*time.Minute {
		t.Fatalf("configured hold timeout = %s, want 3m", got)
	}
}

func TestRuntimeApprovalCoordinatorCancellationPreservesNewerSharedHILMapping(t *testing.T) {
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{})
	oldKey := liveRuntimeApprovalKey("conversation-shared-hil", "run-old", "request-old")
	newKey := liveRuntimeApprovalKey("conversation-shared-hil", "run-new", "request-new")
	coordinator.pending[oldKey] = liveRuntimeApproval{
		Context:       runtimeApprovalContext{RunID: "run-old"},
		Request:       runtimecore.ApprovalRequest{RequestID: "request-old"},
		HILDecisionID: "hil-shared",
	}
	coordinator.pendingHIL["hil-shared"] = newKey

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		coordinator.removeWhenDone(ctx, oldKey, "run-old", "request-old")
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation cleanup did not finish")
	}

	if _, exists := coordinator.pending[oldKey]; exists {
		t.Fatal("cancelled runtime approval remained pending")
	}
	if got := coordinator.pendingHIL["hil-shared"]; got != newKey {
		t.Fatalf("shared HIL mapping = %q, want newer key %q", got, newKey)
	}
}

func TestRuntimeApprovalContextBoundaryPreservesScheduledApprovalIdentity(t *testing.T) {
	ctx := ContextWithRuntimeApproval(context.Background(), RuntimeApprovalContext{
		ConversationID:   "conversation-scheduled-hil",
		RunID:            "scheduled-hil-run",
		AgentID:          "jordan-belfort",
		LogicalAgentID:   "assistant:human-ding",
		ConversationKind: ConversationKindPersonalAssistant,
		RuntimeKind:      "pi",
		AssignedTo:       "human-ding",
	})
	got, ok := RuntimeApprovalContextFrom(ctx)
	if !ok {
		t.Fatal("approval context was not preserved")
	}
	if got.ConversationID != "conversation-scheduled-hil" || got.RunID != "scheduled-hil-run" || got.AgentID != "jordan-belfort" || got.LogicalAgentID != "assistant:human-ding" || got.ConversationKind != ConversationKindPersonalAssistant || got.RuntimeKind != "pi" || got.AssignedTo != "human-ding" {
		t.Fatalf("approval context = %+v", got)
	}
}

func TestRuntimeApprovalCoordinatorNotifiesLiveStreamAfterPendingApprovalIsPersisted(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})
	if err := repo.SaveConversation(context.Background(), ConversationRecord{
		ConversationID: "conv-notify",
		HILMode:        DiscussionHILModeManual,
		Title:          "notify",
		Status:         "running",
		CreatedBy:      "user-1",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	notices := make(chan EventRecord, 1)
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(context.Background(), runtimeApprovalContext{
		ConversationID: "conv-notify",
		RunID:          "run-notify",
		AgentID:        "agent-1",
		RuntimeKind:    "pi",
		notify: func(event EventRecord) {
			notices <- event
		},
	}))
	defer cancel()

	request := runtimecore.ApprovalRequest{
		Source:    "pi",
		RequestID: "approval-notify",
		Kind:      runtimecore.ApprovalKindCommandExecution,
		ToolName:  "bash",
	}
	if err := coordinator.RequestApproval(runCtx, request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	select {
	case event := <-notices:
		if event.Status != "pending" || event.Metadata[runtimecore.ApprovalMetadataRequestID] != "approval-notify" || event.Metadata[runtimecore.ApprovalMetadataToolName] != "bash" {
			t.Fatalf("approval notice = %+v", event)
		}
		persisted, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-notify").Events, "approval-notify")
		if !ok || persisted.EventID != event.EventID {
			t.Fatalf("notice preceded durable event: notice=%+v persisted=%+v ok=%v", event, persisted, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("pending approval was not delivered to the live stream")
	}

	if err := coordinator.RequestApproval(runCtx, request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("retried RequestApproval: %v", err)
	}
	select {
	case duplicate := <-notices:
		t.Fatalf("retried approval emitted a duplicate live notice: %+v", duplicate)
	default:
	}
}

func TestRuntimeApprovalCoordinatorKeepsElicitationManualInAutoMode(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-auto",
		HILMode:        DiscussionHILModeAuto,
		Title:          "auto",
		Status:         "running",
		CreatedBy:      "local-user",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-auto",
		RunID:          "run-auto",
		AgentID:        "agent-1",
		RuntimeKind:    "codex",
	}))
	defer cancel()
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source:    "mcp",
		RequestID: "elicitation-auto",
		Kind:      runtimecore.ApprovalKindMCPElicitation,
		ToolName:  "mcp_elicitation",
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	}); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	select {
	case response := <-responseCh:
		t.Fatalf("elicitation was automatically answered: %#v", response)
	default:
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-auto").Events, "elicitation-auto")
	if !ok || event.Status != "pending" || event.Metadata["approval_hil_mode"] != DiscussionHILModeAuto {
		t.Fatalf("elicitation approval event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorKeepsForcedActionsManualInAutoMode(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-forced-manual", HILMode: DiscussionHILModeAuto, Title: "forced manual",
		Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-forced-manual", RunID: "run-forced-manual", AgentID: "jordan", RuntimeKind: "pi", AssignedTo: "human-ding",
	}))
	defer cancel()
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "publish-call-1", Kind: runtimecore.ApprovalKindTool,
		ToolName: "marketing_wechat_draft_create", Metadata: map[string]string{"approval_force_manual": "true"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	}); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		t.Fatalf("forced-manual action was automatically approved: %#v", response)
	default:
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-forced-manual").Events, "publish-call-1")
	if !ok || event.Status != "pending" || event.Metadata["approval_force_manual"] != "true" || event.Metadata["approval_assigned_to"] != "human-ding" {
		t.Fatalf("forced-manual approval event = %+v, ok=%v", event, ok)
	}
}

func TestServiceDecideRuntimeApprovalRejectsViewerOtherThanAssignedHuman(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-assigned", Title: "assigned", Status: "running", CreatedBy: "human-ding", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-assigned", ConversationID: "conv-assigned", AgentID: "jordan", Status: "running", RuntimeKind: "pi", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-assigned", RunID: "run-assigned", AgentID: "jordan", RuntimeKind: "pi", AssignedTo: "human-ding",
	}))
	defer cancel()
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{Source: "pi", RequestID: "approval-assigned", Kind: runtimecore.ApprovalKindTool, ToolName: "marketing_wechat_draft_create"}, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	command := RuntimeApprovalDecisionCommand{ConversationID: "conv-assigned", ApprovalID: "approval-assigned", ActionID: "approve_once", IdempotencyKey: "assigned-decision"}
	if _, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "human-other", ViewerKind: ViewerKindHuman}, command); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other viewer error = %v, want ErrForbidden", err)
	}
	if _, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "human-ding", ViewerKind: ViewerKindHuman}, command); err != nil {
		t.Fatalf("assigned viewer decision: %v", err)
	}
}

func TestRuntimeApprovalActionsHonorForcedOneTimePolicy(t *testing.T) {
	actions := RuntimeApprovalActions("/api/discussions/conv/approvals/approval/decision", "tool_use", "pending", "approve_once,deny")
	if len(actions) != 2 || actions[0].ActionID != "approve_once" || actions[1].ActionID != "deny" {
		t.Fatalf("forced one-time approval actions = %+v", actions)
	}
}

func TestServiceDecideRuntimeApprovalRespondsToLivePendingRuntime(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
	})
	service := newTestService(ServiceOptions{
		Repository:                 repo,
		RuntimeApprovalCoordinator: coordinator,
		RealtimePublisher:          realtime,
		Clock:                      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-live", Title: "live", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-live", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-live", ConversationID: "conv-live", Role: "user", AuthorID: "local-user", AuthorName: "Local User", Content: "run a command", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-live", ConversationID: "conv-live", AgentID: "staffing-agent", Status: "running", RuntimeKind: "claude", SessionID: "session-live", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-live",
		AgentID:        "staffing-agent",
		RuntimeKind:    "claude",
	}))
	defer cancel()
	err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source:    "claude",
		RequestID: "approval-live",
		SessionID: "session-live",
		Kind:      runtimecore.ApprovalKindCommandExecution,
		ToolName:  "exec_command",
		Metadata:  map[string]string{"command": "npm test"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	view := mustConversationView(t, repo, "conv-live")
	event, ok := findRuntimeApprovalEvent(view.Events, "approval-live")
	if !ok || event.Status != "pending" {
		t.Fatalf("pending approval event = %+v, ok=%v", event, ok)
	}
	run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if !ok || run.Status != "running" {
		t.Fatalf("approval run = %+v, ok=%v", run, ok)
	}

	dto, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-live",
		ApprovalID:     "approval-live",
		ActionID:       "approve_session",
		Message:        "approved",
		Remember:       true,
		IdempotencyKey: "approval-live-decision",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}

	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionApproved || response.Reason != "approved" || !response.Remember {
			t.Fatalf("live response = %#v", response)
		}
		if response.Metadata["approval_id"] != "discussion-runtime-approval-conv-live-run-live-approval-live" || response.Metadata["approval_mode"] != "manual" ||
			response.Metadata["approval_decided_by"] != "local-user" || response.Metadata["approval_decided_at"] == "" {
			t.Fatalf("live approval metadata = %#v", response.Metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live approval responder")
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-live" || dto.Runs[0].Status != "running" {
		t.Fatalf("live decision should not create continuation run: %+v", dto.Runs)
	}
	event, ok = findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-live").Events, "approval-live")
	if !ok || event.Status != "approved" || event.Metadata["approval_continuation_run_id"] != "" {
		t.Fatalf("decided event = %+v, ok=%v", event, ok)
	}
	if len(realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)) == 0 {
		t.Fatal("expected realtime snapshot invalidation for live approval event")
	}
}

func TestServiceDecideRuntimeApprovalDeniesLivePendingRuntimeStopsRunningState(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
	})
	service := newTestService(ServiceOptions{
		Repository:                 repo,
		RuntimeApprovalCoordinator: coordinator,
		RealtimePublisher:          realtime,
		Clock:                      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-live", Title: "live", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-live", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-live", ConversationID: "conv-live", Role: "user", AuthorID: "local-user", AuthorName: "Local User", Content: "run a command", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-live", ConversationID: "conv-live", AgentID: "staffing-agent", Status: "running", RuntimeKind: "claude", SessionID: "session-live", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-live",
		AgentID:        "staffing-agent",
		RuntimeKind:    "claude",
	}))
	defer cancel()
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source:    "claude",
		RequestID: "approval-live",
		SessionID: "session-live",
		Kind:      runtimecore.ApprovalKindCommandExecution,
		ToolName:  "exec_command",
		Metadata:  map[string]string{"command": "npm test"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	}); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	dto, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-live",
		ApprovalID:     "approval-live",
		ActionID:       "deny",
		Message:        "do not run it",
		IdempotencyKey: "approval-live-deny",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}

	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionDenied || response.Reason != "do not run it" {
			t.Fatalf("live response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live approval responder")
	}
	if dto.Status != "completed" || len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" {
		t.Fatalf("denied live approval should stop running state: status=%q runs=%+v", dto.Status, dto.Runs)
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-live").Events, "approval-live")
	if !ok || event.Status != "denied" {
		t.Fatalf("decided event = %+v, ok=%v", event, ok)
	}
	runEvents := realtime.eventsByType(RealtimeEventDiscussionRunCompleted)
	if len(runEvents) == 0 || realtimePayloadString(t, runEvents[len(runEvents)-1], "status") != "completed" {
		t.Fatalf("run completed realtime events = %+v", runEvents)
	}
	if len(realtime.eventsByType(RealtimeEventUserDiscussionListInvalidated)) == 0 {
		t.Fatal("expected user discussion list invalidation after denied live approval")
	}
}

func TestServiceDecideRuntimeApprovalDoesNotOverwriteAlreadyCompletedLiveRunAfterDenial(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
	})
	service := newTestService(ServiceOptions{
		Repository:                 repo,
		RuntimeApprovalCoordinator: coordinator,
		RealtimePublisher:          realtime,
		Clock:                      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-live", Title: "live", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-live", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-live", ConversationID: "conv-live", Role: "user", AuthorID: "local-user", AuthorName: "Local User", Content: "run a command", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-live", ConversationID: "conv-live", AgentID: "staffing-agent", Status: "running", RuntimeKind: "claude", SessionID: "session-live", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-live",
		AgentID:        "staffing-agent",
		RuntimeKind:    "claude",
	}))
	defer cancel()
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source:    "claude",
		RequestID: "approval-live",
		SessionID: "session-live",
		Kind:      runtimecore.ApprovalKindCommandExecution,
		ToolName:  "exec_command",
		Metadata:  map[string]string{"command": "npm test"},
	}, func(response runtimecore.ApprovalResponse) error {
		completedAt := testTime.Add(2 * time.Minute)
		if err := repo.SaveRun(ctx, RunRecord{
			RunID:          "run-live",
			ConversationID: "conv-live",
			AgentID:        "staffing-agent",
			Status:         "completed",
			RuntimeKind:    "claude",
			SessionID:      "session-live",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-preserved",
				Name:       "WebFetch",
				Status:     "completed",
				Arguments:  map[string]string{"url": "https://example.test/spec.md"},
				CreatedAt:  completedAt,
			}},
			CreatedAt:   testTime,
			StartedAt:   testTime,
			CompletedAt: completedAt,
		}); err != nil {
			return err
		}
		view, err := repo.GetConversation(ctx, "conv-live")
		if err != nil {
			return err
		}
		view.Status = "completed"
		view.UpdatedAt = completedAt
		view.ViewedAt = completedAt
		if err := repo.SaveConversation(ctx, view.ConversationRecord); err != nil {
			return err
		}
		responseCh <- response
		return nil
	}); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	dto, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-live",
		ApprovalID:     "approval-live",
		ActionID:       "deny",
		Message:        "do not run it",
		IdempotencyKey: "approval-live-deny",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}

	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionDenied {
			t.Fatalf("live response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live approval responder")
	}
	if dto.Status != "completed" || len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" {
		t.Fatalf("completed live run should stay completed: status=%q runs=%+v", dto.Status, dto.Runs)
	}
	if len(dto.Runs[0].ToolCalls) != 1 || dto.Runs[0].ToolCalls[0].ToolCallID != "tool-preserved" {
		t.Fatalf("completed live run tool calls were overwritten: %+v", dto.Runs[0].ToolCalls)
	}
	if !dto.Runs[0].CompletedAt.Equal(testTime.Add(2 * time.Minute)) {
		t.Fatalf("completed live run timestamp was overwritten: %s", dto.Runs[0].CompletedAt)
	}
}

func TestServiceDecideRuntimeApprovalRejectsUnsupportedModifiedLiveApproval(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})
	service := newTestService(ServiceOptions{
		Repository:                 repo,
		RuntimeApprovalCoordinator: coordinator,
		Clock:                      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-live", Title: "live", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-live", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-live", ConversationID: "conv-live", Role: "user", AuthorID: "local-user", Content: "run a command", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-live", ConversationID: "conv-live", AgentID: "staffing-agent", Status: "running", RuntimeKind: "codex", SessionID: "session-live", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-live",
		AgentID:        "staffing-agent",
		RuntimeKind:    "codex",
	}))
	defer cancel()
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source:    "codex",
		RequestID: "approval-live",
		SessionID: "session-live",
		Kind:      runtimecore.ApprovalKindCommandExecution,
		ToolName:  "exec_command",
		Metadata:  map[string]string{"command": "go test"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	_, err = service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-live",
		ApprovalID:     "approval-live",
		ActionID:       "approve_edited",
		ModifiedInput:  `{"command":"npm test"}`,
		IdempotencyKey: "approval-live-decision",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionDenied || response.Content != nil {
			t.Fatalf("live response = %#v, want denied without modified content", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live approval responder")
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-live").Events, "approval-live")
	if !ok || event.Status != "denied" || event.Metadata["approval_error"] == "" {
		t.Fatalf("decided event = %+v, ok=%v", event, ok)
	}
}

func TestServiceDecideRuntimeApprovalAllowsClaudeModifiedLiveApproval(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})
	service := newTestService(ServiceOptions{
		Repository:                 repo,
		RuntimeApprovalCoordinator: coordinator,
		Clock:                      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-live", Title: "live", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-live", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-live", ConversationID: "conv-live", Role: "user", AuthorID: "local-user", Content: "run a command", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-live", ConversationID: "conv-live", AgentID: "staffing-agent", Status: "running", RuntimeKind: "claude", SessionID: "session-live", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-live",
		AgentID:        "staffing-agent",
		RuntimeKind:    "claude",
	}))
	defer cancel()
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source:    "claude",
		RequestID: "approval-live",
		SessionID: "session-live",
		Kind:      runtimecore.ApprovalKindCommandExecution,
		ToolName:  "exec_command",
		Metadata:  map[string]string{"command": "go test"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	_, err = service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-live",
		ApprovalID:     "approval-live",
		ActionID:       "approve_edited",
		ModifiedInput:  `{"command":"npm test"}`,
		IdempotencyKey: "approval-live-decision",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionApproved || response.Content != `{"command":"npm test"}` {
			t.Fatalf("live response = %#v, want approved modified content", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live approval responder")
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-live").Events, "approval-live")
	if !ok || event.Status != "approved" {
		t.Fatalf("decided event = %+v, ok=%v", event, ok)
	}
}

func TestServiceDecideRuntimeApprovalKeepsConcurrentRequestsSeparatedByRun(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})
	service := newTestService(ServiceOptions{
		Repository:                 repo,
		RuntimeApprovalCoordinator: coordinator,
		Clock:                      fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-live", Title: "live", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-live", "local-user")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-live", ConversationID: "conv-live", Role: "user", AuthorID: "local-user", Content: "run commands", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	for _, run := range []RunRecord{
		{RunID: "run-a", ConversationID: "conv-live", AgentID: "agent-a", Status: "running", RuntimeKind: "claude", SessionID: "session-a", CreatedAt: testTime, StartedAt: testTime},
		{RunID: "run-b", ConversationID: "conv-live", AgentID: "agent-b", Status: "running", RuntimeKind: "claude", SessionID: "session-b", CreatedAt: testTime.Add(time.Nanosecond), StartedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	responseA := make(chan runtimecore.ApprovalResponse, 1)
	responseB := make(chan runtimecore.ApprovalResponse, 1)
	runACtx, cancelA := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-a",
		AgentID:        "agent-a",
		RuntimeKind:    "claude",
	}))
	defer cancelA()
	runBCtx, cancelB := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-live",
		RunID:          "run-b",
		AgentID:        "agent-b",
		RuntimeKind:    "claude",
	}))
	defer cancelB()
	for _, item := range []struct {
		ctx context.Context
		ch  chan runtimecore.ApprovalResponse
	}{
		{ctx: runACtx, ch: responseA},
		{ctx: runBCtx, ch: responseB},
	} {
		err := coordinator.RequestApproval(item.ctx, runtimecore.ApprovalRequest{
			Source:    "claude",
			RequestID: "approval-1",
			SessionID: "session",
			Kind:      runtimecore.ApprovalKindCommandExecution,
			ToolName:  "exec_command",
			Metadata:  map[string]string{"command": "npm test"},
		}, func(response runtimecore.ApprovalResponse) error {
			item.ch <- response
			return nil
		})
		if err != nil {
			t.Fatalf("RequestApproval: %v", err)
		}
	}

	view := mustConversationView(t, repo, "conv-live")
	var eventB EventRecord
	pendingApprovalEvents := 0
	seenEventIDs := map[string]bool{}
	for _, event := range view.Events {
		if event.Kind != discussionRuntimeApprovalEventKind {
			continue
		}
		pendingApprovalEvents++
		if seenEventIDs[event.EventID] {
			t.Fatalf("duplicate approval event id %q in %+v", event.EventID, view.Events)
		}
		seenEventIDs[event.EventID] = true
		if event.Metadata["approval_run_id"] == "run-b" {
			eventB = event
		}
	}
	if pendingApprovalEvents != 2 {
		t.Fatalf("pending approval events = %d, want 2: %+v", pendingApprovalEvents, view.Events)
	}
	if eventB.EventID == "" {
		t.Fatalf("run-b approval event not found: %+v", view.Events)
	}
	_, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-live",
		ApprovalID:     eventB.EventID,
		ActionID:       "approve_once",
		IdempotencyKey: "approval-b-decision",
	})
	if err != nil {
		t.Fatalf("DecideRuntimeApproval: %v", err)
	}

	select {
	case response := <-responseB:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("run-b response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run-b responder")
	}
	select {
	case response := <-responseA:
		t.Fatalf("run-a responder should not receive run-b decision: %#v", response)
	default:
	}
	view = mustConversationView(t, repo, "conv-live")
	for _, event := range view.Events {
		if event.Metadata["approval_run_id"] == "run-a" && event.Status != "pending" {
			t.Fatalf("run-a event status = %q, want pending: %+v", event.Status, event)
		}
	}
}
