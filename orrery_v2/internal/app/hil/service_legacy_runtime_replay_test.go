package hil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceRepairsPendingLegacyRuntimeApprovalAssigneeReplay(t *testing.T) {
	ctx := context.Background()
	service, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-runtime-replay", Complete: true})
	defer closeStore()
	input := legacyRuntimeApprovalInput("runtime-approval-legacy-assignee")
	legacy, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("legacy CreateDecision: %v", err)
	}
	legacyFingerprint := legacy.Decision.RequestFingerprint
	input.PayloadPreview[approvalAssigneePayloadKey] = "human-owner"
	replayed, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("assignee replay CreateDecision: %v", err)
	}
	if replayed.Decision.ID != legacy.Decision.ID {
		t.Fatalf("assignee replay decision = %q, want %q", replayed.Decision.ID, legacy.Decision.ID)
	}
	persisted, err := repo.GetDecision(ctx, input.TenantID, legacy.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	wantFingerprint := stableFingerprint(createDecisionFingerprintInput(input))
	if assigned, valid := decisionApprovalAssignee(persisted); !valid || assigned != "human-owner" || persisted.RequestFingerprint != wantFingerprint || persisted.RequestFingerprint == legacyFingerprint {
		t.Fatalf("repaired runtime decision = %+v", persisted)
	}
	assertRuntimeApprovalMine(t, service, persisted, "human-owner", "other-human")

	input.PayloadPreview["approval_tool_name"] = "different_tool"
	if _, err := service.CreateDecision(ctx, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed assignee replay error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceRepairsLegacyRuntimeApprovalAcrossCanonicalActionUpgrade(t *testing.T) {
	ctx := context.Background()
	service, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-runtime-canonical-replay", Complete: true})
	defer closeStore()
	input := legacyRuntimeApprovalInput("runtime-approval-canonical-upgrade")
	legacy, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("legacy CreateDecision: %v", err)
	}

	input.Action = "system.command.execute"
	input.ActionTypeID = input.Action
	input.RiskLevel = RiskHigh
	input.PayloadPreview[approvalAssigneePayloadKey] = "human-owner"
	input.PayloadPreview["material_hash"] = input.ArgsHash
	replayed, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("canonical assignee replay CreateDecision: %v", err)
	}
	if replayed.Decision.ID != legacy.Decision.ID {
		t.Fatalf("canonical replay decision = %q, want %q", replayed.Decision.ID, legacy.Decision.ID)
	}
	persisted, err := repo.GetDecision(ctx, input.TenantID, legacy.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if assigned, valid := decisionApprovalAssignee(persisted); !valid || assigned != "human-owner" || persisted.RequestFingerprint != stableFingerprint(createDecisionFingerprintInput(input)) {
		t.Fatalf("canonical replay repair = %+v", persisted)
	}
}

func TestServiceRejectsLegacyRuntimeApprovalCanonicalActionTypeMismatch(t *testing.T) {
	ctx := context.Background()
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-runtime-canonical-mismatch", Complete: true})
	defer closeStore()
	input := legacyRuntimeApprovalInput("runtime-approval-canonical-mismatch")
	if _, err := service.CreateDecision(ctx, input); err != nil {
		t.Fatalf("legacy CreateDecision: %v", err)
	}
	input.Action = "system.command.execute"
	input.ActionTypeID = "system.file.change"
	input.PayloadPreview[approvalAssigneePayloadKey] = "human-owner"
	if _, err := service.CreateDecision(ctx, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("canonical mismatch error = %v, want ErrIdempotencyConflict", err)
	}
	input.Action = "runtime.approval"
	if _, err := service.CreateDecision(ctx, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("legacy action type mismatch error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceRepairsLegacyRuntimeApprovalAssigneeOnBusinessKeyHit(t *testing.T) {
	ctx := context.Background()
	service, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-runtime-business-replay", Complete: true})
	defer closeStore()
	input := legacyRuntimeApprovalInput("runtime-business-attempt-1")
	input.BusinessKey = "runtime.approval.intent:stable"
	legacy, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("legacy CreateDecision: %v", err)
	}

	input.IdempotencyKey = "runtime-business-attempt-2"
	input.ArgsHash = "sha256:runtime-business-attempt-2"
	input.Resource.ID = "conversation-1:approval-attempt-2"
	input.Purpose = "same durable business approval from a restarted runtime"
	input.Action = "communication.wechat_draft.create"
	input.ActionTypeID = input.Action
	input.RiskLevel = RiskHigh
	input.PayloadPreview[approvalAssigneePayloadKey] = "human-owner"
	replayed, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("business-key assignee replay CreateDecision: %v", err)
	}
	if replayed.Decision.ID != legacy.Decision.ID {
		t.Fatalf("business-key replay decision = %q, want %q", replayed.Decision.ID, legacy.Decision.ID)
	}
	persisted, err := repo.GetDecision(ctx, input.TenantID, legacy.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if assigned, valid := decisionApprovalAssignee(persisted); !valid || assigned != "human-owner" {
		t.Fatalf("business-key repaired decision = %+v", persisted)
	}
	page, err := repo.ListDecisions(ctx, DecisionListQuery{TenantID: input.TenantID, Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("business-key decisions = %+v, err=%v", page, err)
	}
	assertRuntimeApprovalMine(t, service, persisted, "human-owner", "other-human")
}

func TestServiceReusesTerminalLegacyRuntimeApprovalWithoutRepair(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   DecisionMode
		status DecisionStatus
	}{
		{name: "auto-resolved", mode: ModeAuto, status: DecisionResolved},
		{name: "deny-denied", mode: ModeDeny, status: DecisionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-runtime-terminal-" + test.name, Complete: true})
			defer closeStore()
			input := legacyRuntimeApprovalInput("runtime-approval-terminal-" + test.name)
			legacy := legacyRuntimeDecision(input, "legacy-runtime-terminal-"+test.name)
			legacy.Route = RouteDecision{Mode: test.mode}
			legacy.Status = test.status
			if err := repo.CreateDecision(ctx, legacy); err != nil {
				t.Fatalf("CreateDecision fixture: %v", err)
			}
			input.PayloadPreview[approvalAssigneePayloadKey] = "human-owner"
			replayed, err := service.CreateDecision(ctx, input)
			if err != nil || replayed.Decision.ID != legacy.ID || replayed.Decision.Status != test.status {
				t.Fatalf("terminal assignee replay = %+v, err=%v", replayed.Decision, err)
			}
			persisted, err := repo.GetDecision(ctx, input.TenantID, legacy.ID)
			if err != nil || decisionHasApprovalAssignee(persisted.PayloadPreview) || persisted.RequestFingerprint != legacy.RequestFingerprint {
				t.Fatalf("terminal legacy decision was repaired = %+v, err=%v", persisted, err)
			}
		})
	}
}

func TestServiceRejectsLegacyRuntimeApprovalAssigneeRepairForWorkflow(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "runtime-approval-workflow-replay")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	input := legacyRuntimeApprovalInput("runtime-approval-workflow-replay")
	fixture := workflowRepositoryInstanceFixture()
	fixture.decision = legacyRuntimeDecision(input, "legacy-runtime-workflow")
	fixture.caseRecord.TenantID = input.TenantID
	fixture.caseRecord.Requester = input.Requester
	fixture.caseRecord.ActionTypeID = input.ActionTypeID
	fixture.caseRecord.Resource = input.Resource
	fixture.instance.TenantID = input.TenantID
	fixture.instance.DecisionRequestID = fixture.decision.ID
	fixture.instance.ArgsHash = input.ArgsHash
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatalf("CreateDecisionWorkflow: %v", err)
	}
	service := NewService(repo, fixedContextProvider{snapshot: fixture.decision.Context}, &sequenceHILIDs{}, WithClock(func() time.Time { return testRepositoryNow }))
	input.PayloadPreview[approvalAssigneePayloadKey] = "human-owner"
	if _, err := service.CreateDecision(ctx, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("workflow assignee replay error = %v, want ErrIdempotencyConflict", err)
	}
	persisted, err := repo.GetDecision(ctx, input.TenantID, fixture.decision.ID)
	if err != nil || decisionHasApprovalAssignee(persisted.PayloadPreview) {
		t.Fatalf("workflow decision was repaired = %+v, err=%v", persisted, err)
	}
}

func legacyRuntimeApprovalInput(idempotencyKey string) CreateDecisionInput {
	refs := []ObjectRef{{Kind: "conversation", ID: "conversation-1"}, {Kind: "agent_run", ID: "run-1"}}
	return CreateDecisionInput{
		TenantID: "tenant-1", Requester: ActorRef{Kind: ActorAgent, ID: "agent-runtime"},
		Action: "runtime.approval", ActionTypeID: "command_execution",
		Resource: ObjectRef{Kind: "runtime_approval", ID: "conversation-1:approval-1"}, ObjectRefs: append([]ObjectRef(nil), refs...),
		ArgsHash: "sha256:runtime-approval", Purpose: "review runtime command", RiskLevel: RiskMedium,
		PreferredMode: ModeHIL, HILRequired: true, SubjectRefs: append([]ObjectRef(nil), refs...),
		PayloadPreview: map[string]any{"approval_tool_name": "exec_command"}, IdempotencyKey: idempotencyKey,
		ExpiresAt: testRepositoryNow.Add(time.Hour),
	}
}

func legacyRuntimeDecision(input CreateDecisionInput, decisionID string) DecisionRequest {
	return DecisionRequest{
		ID: decisionID, TenantID: input.TenantID, Requester: normalizeActor(input.Requester), Action: input.Action,
		ActionTypeID: input.ActionTypeID, Resource: normalizeObjectRef(input.Resource), ObjectRefs: normalizeObjectRefs(input.ObjectRefs),
		ArgsHash: input.ArgsHash, Purpose: input.Purpose, RiskLevel: input.RiskLevel, PreferredMode: input.PreferredMode,
		Route:   RouteDecision{Mode: ModeHIL, Reasons: []string{"runtime human review is required"}},
		Context: OntologyContextSnapshot{BundleID: "bundle-runtime-workflow", Complete: true}, PayloadPreview: cloneMap(input.PayloadPreview),
		IdempotencyKey: input.IdempotencyKey, RequestFingerprint: stableFingerprint(createDecisionFingerprintInput(input)),
		Status: DecisionPending, CreatedAt: testRepositoryNow, ExpiresAt: input.ExpiresAt,
	}
}

func assertRuntimeApprovalMine(t *testing.T, service *Service, decision DecisionRequest, ownerID, otherID string) {
	t.Helper()
	owner, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: decision.TenantID, Mode: ModeHIL, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: ownerID},
	})
	if err != nil || owner.Total != 1 || len(owner.Items) != 1 || owner.Items[0].ID != decision.ID {
		t.Fatalf("owner runtime HIL inbox = %+v, err=%v", owner, err)
	}
	other, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: decision.TenantID, Mode: ModeHIL, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: otherID},
	})
	if err != nil || other.Total != 0 || len(other.Items) != 0 {
		t.Fatalf("other runtime HIL inbox = %+v, err=%v", other, err)
	}
}
