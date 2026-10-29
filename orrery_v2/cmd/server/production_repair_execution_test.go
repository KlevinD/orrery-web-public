package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/execution"
)

type allowRepairRuleActivation struct{}

func (allowRepairRuleActivation) AuthorizeRepairRuleActivation(context.Context, reliability.RepairRuleCommand) error {
	return nil
}

type recordingMissionAgentRetryCommand struct {
	calls  int
	viewer missioncontrol.ViewerRef
	cmd    missioncontrol.RetryAgentRunCommand
	err    error
}

func (c *recordingMissionAgentRetryCommand) RetryAgentRun(_ context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	c.calls++
	c.viewer, c.cmd = viewer, cmd
	if c.err != nil {
		return missioncontrol.CommandReceiptDTO{}, c.err
	}
	return missioncontrol.CommandReceiptDTO{CommandID: cmd.ReplacementRunID.String(), Status: "accepted", MissionID: cmd.MissionID.String()}, nil
}

type repairExecutionQueueStub struct {
	statuses map[string]execution.JobStatus
	cancels  int
}

func (q *repairExecutionQueueStub) GetStatus(_ context.Context, id string) (execution.JobStatus, bool, error) {
	status, ok := q.statuses[id]
	return status, ok, nil
}

func (q *repairExecutionQueueStub) Cancel(_ context.Context, id, _ string) error {
	q.cancels++
	status := q.statuses[id]
	status.Status = execution.StatusCancelled
	q.statuses[id] = status
	return nil
}

type recordingRepairVerificationEvidence struct {
	records []repairVerificationEvidenceRecord
	err     error
}

func (r *recordingRepairVerificationEvidence) RecordRepairVerificationEvidence(_ context.Context, _ actioncontract.ActionRun, record repairVerificationEvidenceRecord) error {
	r.records = append(r.records, record)
	return r.err
}

type repairExecutionFixture struct {
	now        time.Time
	repository *reliability.MemoryRepository
	control    *reliability.Service
	actions    *actioncontract.Service
	actionRepo *actioncontract.MemoryRepository
	incident   reliability.FailureIncident
	rule       reliability.RepairRule
	applyRun   actioncontract.ActionRun
	commands   *recordingMissionAgentRetryCommand
	evidence   *recordingRepairVerificationEvidence
	apply      *productionRepairApplyExecutor
}

func newRepairExecutionFixture(t *testing.T) *repairExecutionFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 17, 0, 0, 0, time.UTC)
	repository := reliability.NewMemoryRepository()
	control := reliability.NewService(repository,
		reliability.WithClock(func() time.Time { return now }),
		reliability.WithRepairRuleActivationAuthorizer(allowRepairRuleActivation{}),
	)
	retryable := true
	incident, err := control.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "runtime-failure-1", TenantID: "mission:tenant-a/mission-1", Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "provider timed out", Retryable: &retryable, OccurredAt: now,
		RepairTarget: &reliability.RepairTarget{
			Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "run-failed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := control.UpsertRepairRule(ctx, reliability.RepairRuleCommand{
		ID: "activate-retry-rule", Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
		Rule: reliability.RepairRule{
			TenantID: incident.TenantID, SelectorFingerprint: incident.Fingerprint, Strategy: reliability.RepairStrategyRetry,
			ActionTypeID: string(productionRepairActionTypeID), RiskLevel: reliability.RiskHigh, Status: reliability.RepairRuleStatusActive,
			Confidence: 0.5, SupportCount: 1, Reversible: true, Idempotent: true, SourceIncidentID: incident.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	actionRepo := actioncontract.NewMemoryRepository()
	actions := actioncontract.NewService(actionRepo, actioncontract.WithClock(func() time.Time { return now }))
	if err := actioncontract.InstallDefaultActionTypes(ctx, actions); err != nil {
		t.Fatal(err)
	}
	target, err := repository.GetLatestRepairTarget(ctx, incident.TenantID, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	argsHash := repairInvocationArgsHash(incident, rule, &target)
	objectRefs := []actioncontract.ObjectRef{{Kind: "failure_incident", ID: incident.ID}, {Kind: "repair_rule", ID: rule.ID}}
	completedAt := now
	dryRun := actioncontract.ActionRun{
		ID: "repair-dry-run-source", TenantID: incident.TenantID, ActionTypeID: productionRepairActionTypeID,
		ActionCategory: actioncontract.CategoryHybrid, Actor: actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
		Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeSystem, ID: "reliability-control-plane"}, ObjectRefs: objectRefs,
		ArgsHash: argsHash, RiskLevel: actioncontract.RiskHigh, ExecutionMode: actioncontract.ExecutionModeDryRun,
		Status: actioncontract.RunStatusCompleted, EvidenceRefs: []actioncontract.Ref{{Kind: "evidence_item", ID: "preflight-evidence-1"}},
		CreatedAt: now, CompletedAt: &completedAt,
	}
	if err := actionRepo.SaveActionRun(ctx, dryRun); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.RecordRepairPreflight(ctx, reliability.RepairPreflight{
		EvidenceID: "preflight-evidence-1", TenantID: incident.TenantID, ActionRunID: string(dryRun.ID),
		IncidentID: incident.ID, IncidentVersion: incident.Version, RuleID: rule.ID, RuleVersion: rule.Version,
		Strategy: rule.Strategy, ArgsHash: dryRun.ArgsHash, TargetObservationID: target.ObservationID, TargetKind: target.Kind, TargetRunID: target.RunID,
		Passed: true, CheckedAt: now, ExpiresAt: now.Add(productionRepairPreflightTTL),
	}); err != nil {
		t.Fatal(err)
	}
	applyRun := actioncontract.ActionRun{
		ID: "repair-apply-1", TenantID: incident.TenantID, ActionTypeID: productionRepairActionTypeID,
		ActionCategory: actioncontract.CategoryHybrid, Actor: actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
		Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeSystem, ID: "reliability-control-plane"}, ObjectRefs: objectRefs,
		ArgsHash: dryRun.ArgsHash, RiskLevel: actioncontract.RiskHigh, ExecutionMode: actioncontract.ExecutionModeApply,
		Status: actioncontract.RunStatusStarted, EvidenceRefs: []actioncontract.Ref{{Kind: "evidence_item", ID: "preflight-evidence-1"}}, CreatedAt: now,
	}
	if err := actionRepo.SaveActionRun(ctx, applyRun); err != nil {
		t.Fatal(err)
	}
	commands := &recordingMissionAgentRetryCommand{}
	evidence := &recordingRepairVerificationEvidence{}
	apply := &productionRepairApplyExecutor{
		repository: repository, control: control, actions: actions, commands: commands, evidence: evidence,
		now: func() time.Time { return now },
	}
	return &repairExecutionFixture{
		now: now, repository: repository, control: control, actions: actions, actionRepo: actionRepo,
		incident: incident, rule: rule, applyRun: applyRun, commands: commands, evidence: evidence, apply: apply,
	}
}

func TestProductionRepairApplyDispatchesExactlyOneBoundAgentRetry(t *testing.T) {
	fixture := newRepairExecutionFixture(t)
	ctx := context.Background()
	started, err := fixture.apply.Execute(ctx, fixture.applyRun)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if started.Status != actioncontract.RunStatusStarted || fixture.commands.calls != 1 {
		t.Fatalf("apply result = %+v calls=%d", started, fixture.commands.calls)
	}
	wantRetryRunID := deterministicRepairRetryRunID(string(fixture.applyRun.ID))
	if fixture.commands.cmd.RunID != "run-failed" || fixture.commands.cmd.ReplacementRunID.String() != wantRetryRunID ||
		fixture.commands.cmd.MissionID != "mission-1" || fixture.commands.viewer.TenantID != "tenant-a" || fixture.commands.viewer.ViewerID != "operator-1" {
		t.Fatalf("retry dispatch viewer=%+v command=%+v", fixture.commands.viewer, fixture.commands.cmd)
	}
	executionRecord, err := fixture.repository.GetRepairExecution(ctx, fixture.incident.TenantID, string(fixture.applyRun.ID))
	if err != nil || executionRecord.Status != reliability.RepairExecutionMonitoring || executionRecord.RetryRunID != wantRetryRunID {
		t.Fatalf("repair execution = %+v err=%v", executionRecord, err)
	}
	incident, err := fixture.repository.GetIncident(ctx, fixture.incident.TenantID, fixture.incident.ID)
	if err != nil || incident.Status != reliability.IncidentStatusMonitoring {
		t.Fatalf("incident after dispatch = %+v err=%v", incident, err)
	}
}

func TestProductionRepairReconcilerCompletesOnlyAfterRetryHealthPasses(t *testing.T) {
	fixture := newRepairExecutionFixture(t)
	ctx := context.Background()
	if _, err := fixture.apply.Execute(ctx, fixture.applyRun); err != nil {
		t.Fatal(err)
	}
	retryRunID := deterministicRepairRetryRunID(string(fixture.applyRun.ID))
	queue := &repairExecutionQueueStub{statuses: map[string]execution.JobStatus{
		retryRunID: {ID: retryRunID, Status: execution.StatusCompleted, CompletedAt: fixture.now.Add(time.Minute)},
	}}
	reconciler := &productionRepairExecutionReconciler{
		repository: fixture.repository, control: fixture.control, actions: fixture.actions, queue: queue, evidence: fixture.evidence,
		now: func() time.Time { return fixture.now.Add(time.Minute) },
	}
	processed, err := reconciler.Sweep(ctx, 10)
	if err != nil || processed != 1 {
		t.Fatalf("Sweep processed=%d err=%v", processed, err)
	}
	run, _ := fixture.actions.GetActionRun(ctx, fixture.applyRun.ID)
	executionRecord, _ := fixture.repository.GetRepairExecution(ctx, fixture.incident.TenantID, string(fixture.applyRun.ID))
	incident, _ := fixture.repository.GetIncident(ctx, fixture.incident.TenantID, fixture.incident.ID)
	if run.Status != actioncontract.RunStatusCompleted || executionRecord.Status != reliability.RepairExecutionSucceeded || incident.Status != reliability.IncidentStatusResolved {
		t.Fatalf("verified success run=%+v execution=%+v incident=%+v", run, executionRecord, incident)
	}
	if len(fixture.evidence.records) != 1 || fixture.evidence.records[0].Status != "passed" || fixture.evidence.records[0].RetryRunID != retryRunID {
		t.Fatalf("verification evidence = %+v", fixture.evidence.records)
	}
	receipts, err := fixture.actions.ListWritebackReceipts(ctx, fixture.applyRun.ID)
	if err != nil || len(receipts) != 1 || receipts[0].ExternalOperationID != retryRunID {
		t.Fatalf("writeback receipts = %+v err=%v", receipts, err)
	}
}

func TestProductionRepairReconcilerFailsAndQuarantinesIncidentOnRetryFailure(t *testing.T) {
	fixture := newRepairExecutionFixture(t)
	ctx := context.Background()
	if _, err := fixture.apply.Execute(ctx, fixture.applyRun); err != nil {
		t.Fatal(err)
	}
	retryRunID := deterministicRepairRetryRunID(string(fixture.applyRun.ID))
	queue := &repairExecutionQueueStub{statuses: map[string]execution.JobStatus{
		retryRunID: {ID: retryRunID, Status: execution.StatusFailed, FailureReason: "permanent_failure"},
	}}
	reconciler := &productionRepairExecutionReconciler{
		repository: fixture.repository, control: fixture.control, actions: fixture.actions, queue: queue, evidence: fixture.evidence,
		now: func() time.Time { return fixture.now.Add(time.Minute) },
	}
	if _, err := reconciler.Sweep(ctx, 10); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	run, _ := fixture.actions.GetActionRun(ctx, fixture.applyRun.ID)
	executionRecord, _ := fixture.repository.GetRepairExecution(ctx, fixture.incident.TenantID, string(fixture.applyRun.ID))
	incident, _ := fixture.repository.GetIncident(ctx, fixture.incident.TenantID, fixture.incident.ID)
	if run.Status != actioncontract.RunStatusFailed || run.ErrorCode != "repair_retry_terminal_failed" ||
		executionRecord.Status != reliability.RepairExecutionFailed || incident.Status != reliability.IncidentStatusQuarantined {
		t.Fatalf("verified failure run=%+v execution=%+v incident=%+v", run, executionRecord, incident)
	}
	if len(fixture.evidence.records) != 1 || fixture.evidence.records[0].Status != "failed" {
		t.Fatalf("verification evidence = %+v", fixture.evidence.records)
	}
}
