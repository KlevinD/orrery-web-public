package discussionsession

import (
	"context"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestServiceDecideHILRuntimeApprovalReleasesLiveResponderWhileLifecycleIsLocked(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	conversationID, runID, requestID, decisionID := "conv-live-hil-lock", "run-live-hil-lock", "tool-call-live-hil-lock", "hil-live-lock"
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: conversationID, TenantID: "tenant-hil", Title: "Live HIL lock", Status: "active", CreatedBy: "human-ding", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: runID, ConversationID: conversationID, AgentID: "agent-hil", Status: "running", RuntimeKind: "pi", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	event := EventRecord{EventID: requestID, ConversationID: conversationID, Kind: discussionRuntimeApprovalEventKind, Status: "pending", Metadata: map[string]string{
		runtimecore.ApprovalMetadataRequestID: requestID, "approval_run_id": runID, "hil_decision_id": decisionID,
	}, CreatedAt: testTime}
	if err := repo.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	responses := make(chan runtimecore.ApprovalResponse, 1)
	key := liveRuntimeApprovalKey(conversationID, runID, requestID)
	coordinator.remember(key, liveRuntimeApproval{
		Context: runtimeApprovalContext{ConversationID: conversationID, RunID: runID}, Request: runtimecore.ApprovalRequest{RequestID: requestID},
		Responder: func(response runtimecore.ApprovalResponse) error { responses <- response; return nil }, Event: event, HILDecisionID: decisionID,
	})
	release := service.lockRunLifecycle(conversationID)
	defer release()
	result := make(chan error, 1)
	go func() {
		_, err := service.DecideHILRuntimeApproval(ctx, ViewerRef{ViewerID: "human-ding", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
			ConversationID: conversationID, HILDecisionID: decisionID, ActionID: "approve_once",
			HILDecisionStatus: "resolved", HILReceiptID: "receipt-live-lock", HILReceiptEffect: "authorize",
			IdempotencyKey: "hil-runtime-resolution:hil-live-lock:receipt-live-lock",
		})
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("DecideHILRuntimeApproval: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("live HIL decision waited for the provider lifecycle lock")
	}
	select {
	case response := <-responses:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("live response = %+v", response)
		}
	default:
		t.Fatal("live responder was not released")
	}
	decided, ok := findRuntimeApprovalEventByHILDecisionID(mustConversationView(t, repo, conversationID).Events, decisionID)
	if !ok || decided.Status != "approved" || decided.Metadata["hil_receipt_id"] != "receipt-live-lock" {
		t.Fatalf("live HIL event = %+v, ok=%v", decided, ok)
	}
}
