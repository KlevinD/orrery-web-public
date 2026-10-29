package discussionsession

import (
	"context"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/hil"
)

func TestRuntimeApprovalCoordinatorSkipsHILReconciliationWhenHumanReviewDisabled(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-reconcile-disabled", "run-reconcile-disabled")
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-reconcile-disabled", ConversationID: "conv-reconcile-disabled", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			"approval_hil_required": "true", "approval_run_id": "run-reconcile-disabled", "approval_request_id": "call-reconcile-disabled",
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{ID: "hil-reconcile-disabled"}}}
	gateCalls := 0
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
		HumanReviewEnabled: func(context.Context) (bool, error) {
			gateCalls++
			return false, nil
		},
	})

	if err := coordinator.ReconcileHILRuntimeApprovals(ctx); err != nil {
		t.Fatalf("ReconcileHILRuntimeApprovals: %v", err)
	}
	if gateCalls != 1 || hilService.Calls() != 0 {
		t.Fatalf("human review gate calls = %d, HIL create calls = %d", gateCalls, hilService.Calls())
	}
}

func TestRuntimeApprovalCoordinatorDoesNotCreateHILWhenHumanReviewDisabled(t *testing.T) {
	repo := newMemoryRepository(t)
	saveRuntimeHILTestConversation(t, repo, "conv-request-disabled", "run-request-disabled")
	if err := repo.SaveEvent(context.Background(), EventRecord{
		EventID:        discussionRuntimeApprovalEventID("conv-request-disabled", "run-request-disabled", "call-request-disabled"),
		ConversationID: "conv-request-disabled",
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Metadata: map[string]string{
			"approval_hil_required": "true", "approval_run_id": "run-request-disabled", "approval_request_id": "call-request-disabled",
		},
		CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	hilService := &recordingRuntimeHILService{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
		HumanReviewEnabled: func(context.Context) (bool, error) { return false, nil },
	})
	ctx := contextWithRuntimeApproval(context.Background(), runtimeApprovalContext{
		ConversationID: "conv-request-disabled", RunID: "run-request-disabled", AgentID: "agent-hil", RuntimeKind: "codex",
	})
	var response runtimecore.ApprovalResponse
	err := coordinator.RequestApproval(ctx, runtimecore.ApprovalRequest{
		RequestID: "call-request-disabled", Kind: runtimecore.ApprovalKindTool, ToolName: "external_write",
	}, func(value runtimecore.ApprovalResponse) error {
		response = value
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if response.Decision != runtimecore.ApprovalDecisionApproved || hilService.Calls() != 0 {
		t.Fatalf("approval response = %+v, HIL create calls = %d", response, hilService.Calls())
	}
	event, ok := findRuntimeApprovalEventForRun(mustConversationView(t, repo, "conv-request-disabled").Events, "run-request-disabled", "call-request-disabled")
	if !ok || event.Status != "approved" || runtimeApprovalIsHILBacked(event) {
		t.Fatalf("disabled HIL retry event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorDoesNotResumeDetachedMissionAfterAuthorizationExpires(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-detached-expired", "responsibility-run-expired")
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-detached-expired", ConversationID: "conv-detached-expired", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			"hil_decision_id": "hil-detached-expired", "approval_run_id": "responsibility-run-expired",
			"approval_request_id": "tool-call-expired", "approval_tool_name": wechatDraftApprovalTool,
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	detached := &recordingDetachedRuntimeHILResolver{}
	coordinator.SetHILRuntimeApprovalResolver(service)
	coordinator.SetHILDetachedApprovalResolver(detached)
	resolution := hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "hil-detached-expired", TenantID: "tenant-hil", Action: "runtime.approval", Status: hil.DecisionResolved},
		Receipt: hil.DecisionReceipt{
			ID: "receipt-detached-expired", RequestID: "hil-detached-expired", Effect: hil.EffectAuthorize, SingleUse: true,
			ExpiresAt: testTime.Add(-time.Minute),
		},
		Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "ding"},
	}
	if err := coordinator.applyHILResolution(ctx, resolution); err != nil {
		t.Fatalf("applyHILResolution: %v", err)
	}
	if detached.calls != 0 {
		t.Fatalf("expired approval resumed detached mission: %+v", detached)
	}
	assertRuntimeHILTerminal(t, repo, "conv-detached-expired", "approval-detached-expired", "denied", "receipt-detached-expired")
}
