package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
)

func TestRuntimeApprovalCoordinatorAssignsHILToConversationCreator(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID: "hil-runtime-creator", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL}, ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})
	ctx := context.Background()
	saveRuntimeHILAssignmentConversation(t, repo, "conv-hil-creator", "run-hil-creator")

	runCtx := contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-creator", RunID: "run-hil-creator", AgentID: "A000010", RuntimeKind: "codex",
	})
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-creator", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	assertRuntimeHILAssignment(t, hilService.Input())
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-hil-creator").Events, "approval-hil-creator")
	if !ok || event.Metadata["approval_assigned_to"] != "H000001" {
		t.Fatalf("persisted HIL creator assignment = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorAssignsHILDuringStartupReplay(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID: "hil-runtime-replay", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL}, ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})
	ctx := context.Background()
	saveRuntimeHILAssignmentConversation(t, repo, "conv-hil-replay", "run-hil-replay")
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "event-hil-replay", ConversationID: "conv-hil-replay", Kind: discussionRuntimeApprovalEventKind, Status: "pending", CreatedAt: testTime,
		Metadata: map[string]string{
			"approval_hil_required":               "true",
			"approval_run_id":                     "run-hil-replay",
			"approval_agent_id":                   "A000010",
			runtimecore.ApprovalMetadataRequestID: "approval-hil-replay",
			runtimecore.ApprovalMetadataKind:      string(runtimecore.ApprovalKindCommandExecution),
			runtimecore.ApprovalMetadataToolName:  "exec_command",
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := coordinator.ReconcileHILRuntimeApprovals(ctx); err != nil {
		t.Fatalf("ReconcileHILRuntimeApprovals: %v", err)
	}

	assertRuntimeHILAssignment(t, hilService.Input())
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-hil-replay").Events, "event-hil-replay")
	if !ok || event.Metadata["hil_decision_id"] != "hil-runtime-replay" || event.Metadata["approval_assigned_to"] != "H000001" {
		t.Fatalf("replayed HIL assignment = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorRejectsHILWithoutConversationTenant(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})
	saveRuntimeHILAssignmentConversationWithTenant(t, repo, "conv-hil-no-tenant", "run-hil-no-tenant", "")

	err := coordinator.RequestApproval(contextWithRuntimeApproval(context.Background(), runtimeApprovalContext{
		ConversationID: "conv-hil-no-tenant", RunID: "run-hil-no-tenant", AgentID: "A000010", RuntimeKind: "codex",
	}), runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-no-tenant", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}, func(runtimecore.ApprovalResponse) error { return nil })
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RequestApproval error = %v, want ErrInvalidRequest", err)
	}
	if calls := hilService.Calls(); calls != 0 {
		t.Fatalf("HIL calls = %d, want 0", calls)
	}
}

func TestRuntimeHILApprovalAssigneeRequiresHumanConversationCreator(t *testing.T) {
	conversation := ConversationRecordView{
		ConversationRecord: ConversationRecord{CreatedBy: "agent-owner"},
		Members:            []ConversationMemberRecord{{MemberID: "agent-owner", MemberKind: string(ViewerKindAgent)}},
	}
	if assignedTo := runtimeHILApprovalAssignee("", conversation); assignedTo != "" {
		t.Fatalf("agent creator was assigned as human reviewer: %q", assignedTo)
	}
	conversation.Members = nil
	if assignedTo := runtimeHILApprovalAssignee("", conversation); assignedTo != "" {
		t.Fatalf("unverified creator was assigned as human reviewer: %q", assignedTo)
	}
	if assignedTo := runtimeHILApprovalAssignee(" explicit-reviewer ", conversation); assignedTo != "explicit-reviewer" {
		t.Fatalf("explicit reviewer = %q", assignedTo)
	}
	conversation.PersonalAssistantOriginSubjectID = "human-pa-owner"
	if assignedTo := runtimeHILApprovalAssignee("", conversation); assignedTo != "human-pa-owner" {
		t.Fatalf("personal assistant reviewer = %q", assignedTo)
	}
	conversation = ConversationRecordView{
		ConversationRecord: ConversationRecord{CreatedBy: "h000001"},
		Members:            []ConversationMemberRecord{{MemberID: "H000001", MemberKind: string(ViewerKindHuman)}},
	}
	if assignedTo := runtimeHILApprovalAssignee("", conversation); assignedTo != "H000001" {
		t.Fatalf("canonical conversation reviewer = %q", assignedTo)
	}
}

func saveRuntimeHILAssignmentConversation(t *testing.T, repo *Repository, conversationID, runID string) {
	t.Helper()
	saveRuntimeHILAssignmentConversationWithTenant(t, repo, conversationID, runID, "minvia")
}

func saveRuntimeHILAssignmentConversationWithTenant(t *testing.T, repo *Repository, conversationID, runID, tenantID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: conversationID, TenantID: tenantID, Title: "Creator approval", Status: "running", CreatedBy: "H000001", OwnerID: "ding", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{
		ConversationID: conversationID, MemberKind: string(ViewerKindUser), MemberID: "H000001", Role: "owner", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID: runID, ConversationID: conversationID, AgentID: "A000010", Status: "running", CreatedAt: testTime, StartedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertRuntimeHILAssignment(t *testing.T, input hil.CreateDecisionInput) {
	t.Helper()
	if input.TenantID != "minvia" || input.Requester.ID != "A000010" || input.PayloadPreview["approval_assigned_to"] != "H000001" {
		t.Fatalf("HIL creator assignment = %+v", input)
	}
	if input.Action != string(actioncontract.ActionSystemCommandExecute) || input.ActionTypeID != input.Action || input.RiskLevel != hil.RiskHigh {
		t.Fatalf("HIL canonical action assignment = %+v", input)
	}
}
