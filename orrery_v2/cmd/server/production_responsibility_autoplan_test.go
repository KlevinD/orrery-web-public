package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/hil"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

type recordingResponsibilityAutoplanHILConsumer struct {
	resolutions []responsibilityAutoplanHILResolution
	err         error
}

func (c *recordingResponsibilityAutoplanHILConsumer) ObserveAutoplanHILResolution(_ context.Context, resolution responsibilityAutoplanHILResolution) error {
	c.resolutions = append(c.resolutions, resolution)
	return c.err
}

type recordingResponsibilityAutoplanService struct {
	evaluation      responsibility.AutoplanEvaluationResult
	evaluateErr     error
	evaluateInputs  []responsibility.AutoplanPortfolioRequest
	reconcileResult responsibility.AutoplanReconcileResult
	reconcileErr    error
	reconcileLimits []int
	resolutions     []responsibility.AutoplanHILResolution
	resolutionErr   error
}

func (s *recordingResponsibilityAutoplanService) EvaluatePortfolio(_ context.Context, input responsibility.AutoplanPortfolioRequest) (responsibility.AutoplanEvaluationResult, error) {
	s.evaluateInputs = append(s.evaluateInputs, input)
	return s.evaluation, s.evaluateErr
}

func (s *recordingResponsibilityAutoplanService) Reconcile(_ context.Context, limit int) (responsibility.AutoplanReconcileResult, error) {
	s.reconcileLimits = append(s.reconcileLimits, limit)
	return s.reconcileResult, s.reconcileErr
}

func (s *recordingResponsibilityAutoplanService) ObserveHILResolution(_ context.Context, resolution responsibility.AutoplanHILResolution) error {
	s.resolutions = append(s.resolutions, resolution)
	return s.resolutionErr
}

func TestProductionResponsibilityAutoplanAdapterMapsSchedulerEvaluationWithoutAction(t *testing.T) {
	now := time.Date(2026, 8, 14, 5, 6, 7, 0, time.FixedZone("UTC+8", 8*60*60))
	portfolio := responsibility.PeopleResponsibilityPortfolio{
		ID: "portfolio-1", TenantID: "tenant-a", BindingID: "binding-1", AgentProfileID: "agent-1",
		SourceRevision: "revision-1", SourceDigest: "digest-1", Status: responsibility.PeoplePortfolioStatusActive,
	}
	service := &recordingResponsibilityAutoplanService{evaluation: responsibility.AutoplanEvaluationResult{
		Reason: "autoplan_pending_approval", DecisionSource: "autoplan-1", PendingApproval: 1,
	}}
	adapter := newProductionResponsibilityAutoplanAdapter(service)

	result, err := adapter.EvaluateAutoplan(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: " tenant-a ", ScheduledTaskID: " pulse-1 ", ScheduledRunID: " run-1 ", ApprovalAssigneeID: " human-owner ", TriggeredAt: now,
	}, portfolio)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.evaluateInputs) != 1 {
		t.Fatalf("EvaluatePortfolio calls = %d", len(service.evaluateInputs))
	}
	input := service.evaluateInputs[0]
	if input.TenantID != "tenant-a" || input.ScheduledTaskID != "pulse-1" || input.ScheduledRunID != "run-1" || input.ApprovalAssigneeID != "human-owner" || !input.TriggeredAt.Equal(now) ||
		input.Portfolio.ID != portfolio.ID || input.Portfolio.TenantID != portfolio.TenantID || input.Portfolio.AgentProfileID != portfolio.AgentProfileID {
		t.Fatalf("EvaluatePortfolio input = %+v", input)
	}
	if !result.Evaluated || result.Actionable || result.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction ||
		result.DecisionSourceRef != "autoplan-1" || result.Reason != "autoplan_pending_approval" ||
		result.PortfolioID != portfolio.ID || result.PortfolioBindingID != portfolio.BindingID ||
		result.PortfolioVersionID != responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest) ||
		result.SourceRevision != portfolio.SourceRevision || result.SourceDigest != portfolio.SourceDigest || result.AgentProfileID != portfolio.AgentProfileID {
		t.Fatalf("scheduler result = %+v", result)
	}
}

func TestProductionResponsibilityAutoplanAdapterReconcilesAndMapsHILResolution(t *testing.T) {
	reconcileErr := errors.New("temporary persistence failure")
	service := &recordingResponsibilityAutoplanService{reconcileErr: reconcileErr}
	adapter := newProductionResponsibilityAutoplanAdapter(service)
	if err := adapter.ReconcileAutoplans(context.Background(), 17); !errors.Is(err, reconcileErr) || len(service.reconcileLimits) != 1 || service.reconcileLimits[0] != 17 {
		t.Fatalf("reconcile limits=%v error=%v", service.reconcileLimits, err)
	}
	resolvedAt := time.Date(2026, 8, 14, 6, 7, 8, 0, time.UTC)
	if err := adapter.ObserveAutoplanHILResolution(context.Background(), responsibilityAutoplanHILResolution{
		TenantID: " tenant-a ", AutoplanID: " plan-1 ", ProposalDigest: " digest-1 ", DecisionID: " decision-1 ",
		ReceiptID: " receipt-1 ", Approved: true, Reason: " approved ", ResolvedAt: resolvedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if len(service.resolutions) != 1 {
		t.Fatalf("ObserveHILResolution calls = %d", len(service.resolutions))
	}
	resolution := service.resolutions[0]
	if resolution.TenantID != "tenant-a" || resolution.AutoplanID != "plan-1" || resolution.ProposalDigest != "digest-1" ||
		resolution.DecisionID != "decision-1" || resolution.ReceiptID != "receipt-1" || !resolution.Approved || resolution.Reason != "approved" || !resolution.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("ObserveHILResolution input = %+v", resolution)
	}
}

func TestProductionResponsibilityAutoplanAdapterConnectsHILObserverToService(t *testing.T) {
	service := &recordingResponsibilityAutoplanService{}
	observer := newProductionResponsibilityAutoplanHILObserver(newProductionResponsibilityAutoplanAdapter(service), nil)
	resolvedAt := time.Date(2026, 8, 14, 7, 8, 9, 0, time.UTC)
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{
			ID: "decision-1", TenantID: "tenant-a", Action: responsibilityAutoplanHILAction, ArgsHash: "digest-1", Status: hil.DecisionResolved,
			PayloadPreview: map[string]any{responsibilityAutoplanHILPlanIDKey: "plan-1", responsibilityAutoplanHILProposalDigest: "digest-1"},
		},
		Receipt: hil.DecisionReceipt{ID: "receipt-1", ArgsHash: "digest-1", Effect: hil.EffectAuthorize, CreatedAt: resolvedAt},
	})
	if len(service.resolutions) != 1 {
		t.Fatalf("ObserveHILResolution calls = %d", len(service.resolutions))
	}
	resolution := service.resolutions[0]
	if resolution.TenantID != "tenant-a" || resolution.AutoplanID != "plan-1" || resolution.ProposalDigest != "digest-1" ||
		resolution.DecisionID != "decision-1" || resolution.ReceiptID != "receipt-1" || !resolution.Approved || !resolution.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("service resolution = %+v", resolution)
	}
}

func TestProductionResponsibilityAutoplanHILObserverForwardsOnlyBoundTerminalDecisions(t *testing.T) {
	consumer := &recordingResponsibilityAutoplanHILConsumer{}
	var reported []error
	observer := newProductionResponsibilityAutoplanHILObserver(consumer, func(err error) { reported = append(reported, err) })
	resolvedAt := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{
			ID: "decision-1", TenantID: "tenant-a", Action: responsibilityAutoplanHILAction,
			ArgsHash: "sha256:proposal-1", Status: hil.DecisionResolved,
			PayloadPreview: map[string]any{
				responsibilityAutoplanHILPlanIDKey:      "autoplan-1",
				responsibilityAutoplanHILProposalDigest: "sha256:proposal-1",
			},
		},
		Receipt: hil.DecisionReceipt{ID: "receipt-1", ArgsHash: "sha256:proposal-1", Effect: hil.EffectAuthorize, CreatedAt: resolvedAt},
		Actor:   hil.ActorRef{Kind: hil.ActorHuman, ID: "reviewer-1"}, Reason: "approved",
	})
	if len(consumer.resolutions) != 1 {
		t.Fatalf("forwarded resolutions = %d, want 1", len(consumer.resolutions))
	}
	got := consumer.resolutions[0]
	if got.TenantID != "tenant-a" || got.AutoplanID != "autoplan-1" || got.DecisionID != "decision-1" || got.ReceiptID != "receipt-1" ||
		got.ProposalDigest != "sha256:proposal-1" || !got.Approved || !got.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("handoff = %+v", got)
	}

	// Pending decisions, unrelated actions, and malformed bindings must not
	// reach the planner consumer. Malformed targeted decisions are reported so
	// production wiring can place them on an alert/retry path.
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "pending", TenantID: "tenant-a", Action: responsibilityAutoplanHILAction, Status: hil.DecisionPending},
	})
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "other", TenantID: "tenant-a", Action: "runtime.approval", Status: hil.DecisionResolved},
	})
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "malformed", TenantID: "tenant-a", Action: responsibilityAutoplanHILAction, ArgsHash: "sha256:wrong", Status: hil.DecisionResolved,
			PayloadPreview: map[string]any{responsibilityAutoplanHILPlanIDKey: "autoplan-1", responsibilityAutoplanHILProposalDigest: "sha256:proposal-1"}},
		Receipt: hil.DecisionReceipt{ID: "receipt-malformed", ArgsHash: "sha256:proposal-1", Effect: hil.EffectAuthorize},
	})
	if len(consumer.resolutions) != 1 || len(reported) != 1 {
		t.Fatalf("after filtering resolutions=%d reported=%d", len(consumer.resolutions), len(reported))
	}
}

func TestProductionResponsibilityAutoplanHILObserverForwardsDeniedDecision(t *testing.T) {
	consumer := &recordingResponsibilityAutoplanHILConsumer{}
	observer := newProductionResponsibilityAutoplanHILObserver(consumer, nil)
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{
			ID: "decision-denied", TenantID: "tenant-a", Action: responsibilityAutoplanHILAction,
			ArgsHash: "sha256:proposal-2", Status: hil.DecisionDenied,
			PayloadPreview: map[string]any{responsibilityAutoplanHILPlanIDKey: "autoplan-2", responsibilityAutoplanHILProposalDigest: "sha256:proposal-2"},
		},
		Receipt: hil.DecisionReceipt{ID: "receipt-denied", ArgsHash: "sha256:proposal-2", Effect: hil.EffectDeny},
	})
	if len(consumer.resolutions) != 1 || consumer.resolutions[0].Approved {
		t.Fatalf("denied handoff = %+v", consumer.resolutions)
	}
}

type responsibilityAutoplanMissionCommandsFake struct {
	mission        missioncontrol.MissionDTO
	task           missioncontrol.TaskCardDTO
	err            error
	viewer         missioncontrol.ViewerRef
	createdMission missioncontrol.CreateMissionCommand
	createdTask    missioncontrol.CreateTaskCommand
	assignedTask   missioncontrol.AssignTaskCommand
	missionCalls   int
	taskCalls      int
	assignCalls    int
}

func (f *responsibilityAutoplanMissionCommandsFake) CreateMission(_ context.Context, viewer missioncontrol.ViewerRef, command missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.missionCalls++
	f.viewer = viewer
	f.createdMission = command
	if f.err != nil {
		return missioncontrol.MissionDTO{}, f.err
	}
	return f.mission, nil
}

func (f *responsibilityAutoplanMissionCommandsFake) CreateTask(_ context.Context, viewer missioncontrol.ViewerRef, command missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	f.taskCalls++
	f.viewer = viewer
	f.createdTask = command
	if f.err != nil {
		return missioncontrol.TaskCardDTO{}, f.err
	}
	return f.task, nil
}

func (f *responsibilityAutoplanMissionCommandsFake) AssignTask(_ context.Context, viewer missioncontrol.ViewerRef, command missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.assignCalls++
	f.viewer = viewer
	f.assignedTask = command
	if f.err != nil {
		return missioncontrol.CommandReceiptDTO{}, f.err
	}
	return missioncontrol.CommandReceiptDTO{CommandID: command.IdempotencyKey, Status: "accepted"}, nil
}

func testResponsibilityAutoplanRecord(t *testing.T) (responsibility.AutoplanRecord, responsibility.AutoplanStep) {
	t.Helper()
	step := responsibility.AutoplanStep{
		StepID: "step-1", Title: "Reconcile source drift", Description: "Compare the source snapshot and publish evidence.",
		ParentGoalRef: "people:position_goal:goal-1:v3", ParentGoal: "Keep delivery evidence current",
		Goal: "Reconcile source drift", AssigneeAgentID: "agent-1", Priority: 80,
		AcceptanceCriteria: []string{"A traceable reconciliation is published"},
		EvidenceRequirements: []responsibility.AutoplanEvidenceRequirement{{
			EvidenceRequirementID: "evidence-1", AcceptanceCriterion: "A traceable reconciliation is published", Kind: "test_log", Required: true,
		}},
		ExecutionScopes: []string{"mission.read", "evidence.write"}, RiskLevel: responsibility.AutoplanRiskLow,
	}
	proposal := responsibility.AutoplanProposal{
		Version: responsibility.AutoplanProposalVersion, Summary: "Reconcile responsibility source drift",
		SourceRefs: []string{"people:portfolio-version-1", step.ParentGoalRef}, Steps: []responsibility.AutoplanStep{step},
	}
	digest, err := responsibility.AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatal(err)
	}
	return responsibility.AutoplanRecord{
		ID: "autoplan-1", TenantID: "tenant-a", ActorMemberID: "member-1", AgentProfileID: step.AssigneeAgentID,
		PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "version-1",
		PolicyID: "policy-1", PolicyVersion: 3,
		Proposal: proposal, ProposalDigest: digest, ApprovedProposalDigest: digest,
		Status: responsibility.AutoplanStatusApproved,
	}, step
}

func TestProductionResponsibilityAutoplanMissionMaterializerMapsOneStepWithStableKeys(t *testing.T) {
	plan, step := testResponsibilityAutoplanRecord(t)
	commands := &responsibilityAutoplanMissionCommandsFake{
		mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, task: missioncontrol.TaskCardDTO{TaskID: "task-1"},
	}
	now := time.Date(2026, 8, 14, 2, 3, 4, 0, time.UTC)
	materializer := newProductionResponsibilityAutoplanMissionMaterializer(commands, func() time.Time { return now })
	first, err := materializer.MaterializeAutoplanStep(context.Background(), plan, step.StepID)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	second, err := materializer.MaterializeAutoplanStep(context.Background(), plan, step.StepID)
	if err != nil {
		t.Fatalf("repeat materialize: %v", err)
	}
	if first != second || first.MissionID != "mission-1" || first.TaskID != "task-1" || first.ResponsibilityID != responsibility.AssignmentID("tenant-a", responsibility.WorkKindMissionTask, "task-1") || !first.MaterializedAt.Equal(now) {
		t.Fatalf("receipts first=%+v second=%+v", first, second)
	}
	if commands.missionCalls != 2 || commands.taskCalls != 2 || commands.assignCalls != 2 {
		t.Fatalf("command calls mission=%d task=%d assign=%d", commands.missionCalls, commands.taskCalls, commands.assignCalls)
	}
	if commands.createdMission.IdempotencyKey == "" || commands.createdMission.IdempotencyKey != commands.createdTask.IdempotencyKey[:len(commands.createdTask.IdempotencyKey)-len(":task")]+":mission" {
		t.Fatalf("materialization keys mission=%q task=%q", commands.createdMission.IdempotencyKey, commands.createdTask.IdempotencyKey)
	}
	if commands.createdMission.FinalGoal != step.ParentGoal || !strings.Contains(commands.createdTask.Scope, step.ParentGoalRef) {
		t.Fatalf("goal decomposition mission=%q task_scope=%q", commands.createdMission.FinalGoal, commands.createdTask.Scope)
	}
	if commands.createdTask.SourceKind != responsibilityAutoplanSourceKind || commands.createdTask.SourceID != plan.ID || commands.createdTask.SourceStepID != step.StepID ||
		commands.createdTask.AssigneeID != step.AssigneeAgentID || !commands.createdTask.RequireExplicitToolScopes || len(commands.createdTask.EvidenceRequirements) != 1 || commands.createdTask.EvidenceRequirements[0].Required != true {
		t.Fatalf("task command = %+v", commands.createdTask)
	}
	envelope := commands.createdTask.ExecutionEnvelope
	if envelope == nil || envelope.TenantID != plan.TenantID || envelope.MissionID != "mission-1" || envelope.TaskID != responsibilityAutoplanTaskID(plan, step.StepID) ||
		envelope.AgentID != step.AssigneeAgentID || envelope.AgentProfileID != step.AssigneeAgentID || len(envelope.PermissionScopes) != len(step.ExecutionScopes) {
		t.Fatalf("task execution envelope = %+v", envelope)
	}
	if envelope.PortfolioID != plan.PortfolioID || envelope.PortfolioBindingID != plan.PortfolioBindingID || envelope.PortfolioVersionID != plan.PortfolioVersionID {
		t.Fatalf("task execution envelope lost portfolio snapshot = %+v", envelope)
	}
	if err := envelope.Verify(); err != nil {
		t.Fatalf("verify task execution envelope: %v", err)
	}
	if commands.assignedTask.TaskID != "task-1" || commands.assignedTask.AssigneeID != step.AssigneeAgentID {
		t.Fatalf("assignment command = %+v", commands.assignedTask)
	}
	if commands.viewer.ViewerID != responsibilityAutoplanMaterializerViewer || commands.viewer.Tenant() != plan.TenantID {
		t.Fatalf("materializer viewer = %+v", commands.viewer)
	}
}

func TestProductionResponsibilityAutoplanMissionMaterializerRejectsUnapprovedOrUnknownStep(t *testing.T) {
	plan, step := testResponsibilityAutoplanRecord(t)
	commands := &responsibilityAutoplanMissionCommandsFake{mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, task: missioncontrol.TaskCardDTO{TaskID: "task-1"}}
	materializer := newProductionResponsibilityAutoplanMissionMaterializer(commands, time.Now)
	plan.Status = responsibility.AutoplanStatusPendingApproval
	if _, err := materializer.MaterializeAutoplanStep(context.Background(), plan, step.StepID); err == nil {
		t.Fatal("pending approval plan was materialized")
	}
	plan.Status = responsibility.AutoplanStatusApproved
	if _, err := materializer.MaterializeAutoplanStep(context.Background(), plan, "missing-step"); err == nil {
		t.Fatal("unknown plan step was materialized")
	}
	if commands.missionCalls != 0 || commands.taskCalls != 0 || commands.assignCalls != 0 {
		t.Fatalf("invalid plan invoked commands: %+v", commands)
	}
}

func TestProductionResponsibilityAutoplanHILObserverReportsConsumerFailure(t *testing.T) {
	consumer := &recordingResponsibilityAutoplanHILConsumer{err: errors.New("store unavailable")}
	var reported []error
	observer := newProductionResponsibilityAutoplanHILObserver(consumer, func(err error) { reported = append(reported, err) })
	observer.DecisionResolved(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "decision-error", TenantID: "tenant-a", Action: responsibilityAutoplanHILAction, ArgsHash: "sha256:d", Status: hil.DecisionResolved,
			PayloadPreview: map[string]any{responsibilityAutoplanHILPlanIDKey: "autoplan-error", responsibilityAutoplanHILProposalDigest: "sha256:d"}},
		Receipt: hil.DecisionReceipt{ID: "receipt-error", ArgsHash: "sha256:d", Effect: hil.EffectAuthorize},
	})
	if len(consumer.resolutions) != 1 || len(reported) != 1 || !strings.Contains(reported[0].Error(), "store unavailable") {
		t.Fatalf("consumer error resolutions=%d reported=%v", len(consumer.resolutions), reported)
	}
}
