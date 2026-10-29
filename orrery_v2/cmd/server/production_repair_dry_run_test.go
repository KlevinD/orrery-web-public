package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/reliability"
)

type recordingRepairDryRunEvidenceRecorder struct {
	run       actioncontract.ActionRun
	record    repairDryRunEvidenceRecord
	calls     int
	recordErr error
}

func (r *recordingRepairDryRunEvidenceRecorder) RecordRepairDryRunEvidence(_ context.Context, run actioncontract.ActionRun, record repairDryRunEvidenceRecord) error {
	r.calls++
	r.run = run
	r.record = record
	return r.recordErr
}

func TestProductionRepairDryRunCompletesWithEvidenceWithoutLearning(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	reliabilityRepository, incident, rule := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	actionRepository := actioncontract.NewMemoryRepository()
	actionService := actioncontract.NewService(actionRepository, actioncontract.WithClock(func() time.Time { return now }))
	if err := actioncontract.InstallDefaultActionTypes(ctx, actionService); err != nil {
		t.Fatal(err)
	}
	run := testStartedRepairDryRun(t, ctx, reliabilityRepository, incident, rule, now)
	if err := actionRepository.SaveActionRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evidence := &recordingRepairDryRunEvidenceRecorder{}
	executor := newProductionRepairDryRunExecutor(reliabilityRepository, reliability.NewDefaultRepairProviderRegistry(), actionService, evidence, func() time.Time { return now })

	completed, err := executor.Execute(ctx, run)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if completed.Status != actioncontract.RunStatusCompleted || completed.ExecutionMode != actioncontract.ExecutionModeDryRun {
		t.Fatalf("completed run = %+v", completed)
	}
	preflight, err := reliabilityRepository.GetRepairPreflight(ctx, incident.TenantID, completed.EvidenceRefs[len(completed.EvidenceRefs)-1].ID)
	if err != nil {
		t.Fatalf("GetRepairPreflight: %v", err)
	}
	if !preflight.Passed || preflight.IncidentVersion != incident.Version || preflight.RuleVersion != rule.Version ||
		preflight.TargetObservationID == "" || preflight.TargetRunID != "run-failed" || !preflight.ExpiresAt.Equal(now.Add(productionRepairPreflightTTL)) {
		t.Fatalf("preflight binding = %+v", preflight)
	}
	if completed.ProviderCallRef.Kind != "repair_dry_run_provider" || completed.ProviderCallRef.ID == "" ||
		completed.ResultRef.Kind != "repair_dry_run_result" || completed.ResultRef.ID == "" ||
		len(completed.EvidenceRefs) != 1 || completed.EvidenceRefs[0].Kind != "evidence_item" {
		t.Fatalf("dry-run terminal material = %+v", completed)
	}
	if evidence.calls != 1 || evidence.run.ID != run.ID || evidence.record.EvidenceID != completed.EvidenceRefs[0].ID ||
		len(evidence.record.Checks) == 0 || evidence.record.IncidentID != incident.ID || evidence.record.RuleID != rule.ID {
		t.Fatalf("recorded dry-run evidence = %+v calls=%d", evidence.record, evidence.calls)
	}
	unchanged, err := reliabilityRepository.GetRepairRule(ctx, rule.TenantID, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SuccessCount != 0 || unchanged.FailureCount != 0 || unchanged.Version != rule.Version {
		t.Fatalf("dry-run changed learned rule = %+v", unchanged)
	}
}

func TestProductionRepairDryRunFailsClosedForUnregisteredStrategyWithoutLearning(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 15, 5, 0, 0, time.UTC)
	reliabilityRepository, incident, rule := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyPatch)
	actionRepository := actioncontract.NewMemoryRepository()
	actionService := actioncontract.NewService(actionRepository, actioncontract.WithClock(func() time.Time { return now }))
	if err := actioncontract.InstallDefaultActionTypes(ctx, actionService); err != nil {
		t.Fatal(err)
	}
	run := testStartedRepairDryRun(t, ctx, reliabilityRepository, incident, rule, now)
	if err := actionRepository.SaveActionRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evidence := &recordingRepairDryRunEvidenceRecorder{}
	executor := newProductionRepairDryRunExecutor(reliabilityRepository, reliability.NewDefaultRepairProviderRegistry(), actionService, evidence, func() time.Time { return now })

	failed, err := executor.Execute(ctx, run)
	if err != nil {
		t.Fatalf("Execute returned infrastructure error: %v", err)
	}
	if failed.Status != actioncontract.RunStatusFailed || failed.ErrorCode != "repair_dry_run_provider_unavailable" {
		t.Fatalf("failed run = %+v", failed)
	}
	if len(failed.EvidenceRefs) != 1 || failed.EvidenceRefs[0].ID != "action-run-failure-evidence:"+string(run.ID) {
		t.Fatalf("failure evidence = %+v", failed.EvidenceRefs)
	}
	unchanged, err := reliabilityRepository.GetRepairRule(ctx, rule.TenantID, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SuccessCount != 0 || unchanged.FailureCount != 0 || unchanged.Version != rule.Version || unchanged.Status != rule.Status {
		t.Fatalf("failed dry-run changed learned rule = %+v", unchanged)
	}
	if evidence.calls != 0 {
		t.Fatalf("rejected provider recorded %d passing evidence items", evidence.calls)
	}
}

func TestProductionRepairDryRunFailsWhenPassingEvidenceCannotBeRecorded(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 15, 10, 0, 0, time.UTC)
	reliabilityRepository, incident, rule := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	actionRepository := actioncontract.NewMemoryRepository()
	actionService := actioncontract.NewService(actionRepository, actioncontract.WithClock(func() time.Time { return now }))
	if err := actioncontract.InstallDefaultActionTypes(ctx, actionService); err != nil {
		t.Fatal(err)
	}
	run := testStartedRepairDryRun(t, ctx, reliabilityRepository, incident, rule, now)
	if err := actionRepository.SaveActionRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evidence := &recordingRepairDryRunEvidenceRecorder{recordErr: errors.New("evidence store unavailable")}
	executor := newProductionRepairDryRunExecutor(reliabilityRepository, reliability.NewDefaultRepairProviderRegistry(), actionService, evidence, func() time.Time { return now })

	failed, err := executor.Execute(ctx, run)
	if err != nil {
		t.Fatalf("Execute returned infrastructure error after durable failure: %v", err)
	}
	if evidence.calls != 1 || failed.Status != actioncontract.RunStatusFailed || failed.ErrorCode != "repair_dry_run_preflight_failed" {
		t.Fatalf("evidence failure result = %+v calls=%d", failed, evidence.calls)
	}
}

func TestProductionRepairDryRunRejectsBindingAfterNewFailureOccurrence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 15, 15, 0, 0, time.UTC)
	reliabilityRepository, incident, rule := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	actionRepository := actioncontract.NewMemoryRepository()
	actionService := actioncontract.NewService(actionRepository, actioncontract.WithClock(func() time.Time { return now }))
	if err := actioncontract.InstallDefaultActionTypes(ctx, actionService); err != nil {
		t.Fatal(err)
	}
	run := testStartedRepairDryRun(t, ctx, reliabilityRepository, incident, rule, now)
	if err := actionRepository.SaveActionRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	retryable := true
	control := reliability.NewService(reliabilityRepository, reliability.WithClock(func() time.Time { return now.Add(time.Minute) }))
	updated, err := control.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "observation-new", TenantID: incident.TenantID, Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "provider timed out", Retryable: &retryable, OccurredAt: now.Add(time.Minute),
		RepairTarget: &reliability.RepairTarget{
			Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "run-new",
		},
	})
	if err != nil {
		t.Fatalf("ObserveFailure: %v", err)
	}
	if updated.ID != incident.ID || updated.Version <= incident.Version {
		t.Fatalf("updated incident = %+v, original = %+v", updated, incident)
	}

	evidence := &recordingRepairDryRunEvidenceRecorder{}
	executor := newProductionRepairDryRunExecutor(reliabilityRepository, reliability.NewDefaultRepairProviderRegistry(), actionService, evidence, func() time.Time { return now.Add(time.Minute) })
	failed, err := executor.Execute(ctx, run)
	if err != nil {
		t.Fatalf("Execute returned infrastructure error: %v", err)
	}
	if failed.Status != actioncontract.RunStatusFailed || failed.ErrorCode != "repair_dry_run_preflight_failed" {
		t.Fatalf("stale dry-run result = %+v", failed)
	}
	if evidence.calls != 0 {
		t.Fatalf("stale binding reached the provider evidence recorder %d times", evidence.calls)
	}
	preflights, err := reliabilityRepository.ListRepairPreflightsByIncident(ctx, incident.TenantID, incident.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(preflights) != 0 {
		t.Fatalf("stale binding created preflights: %+v", preflights)
	}
}

func testRepairDryRunReliabilityState(t *testing.T, ctx context.Context, now time.Time, strategy reliability.RepairStrategy) (*reliability.MemoryRepository, reliability.FailureIncident, reliability.RepairRule) {
	t.Helper()
	repository := reliability.NewMemoryRepository()
	service := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }))
	retryable := true
	incident, err := service.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "observation-" + string(strategy), TenantID: "tenant-a", Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "provider timed out", Retryable: &retryable, OccurredAt: now,
		RepairTarget: &reliability.RepairTarget{Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "run-failed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := service.UpsertRepairRule(ctx, reliability.RepairRuleCommand{
		ID: "proposal-" + string(strategy), Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
		Rule: reliability.RepairRule{
			TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: strategy,
			ActionTypeID: string(productionRepairActionTypeID), RiskLevel: reliability.RiskHigh,
			Status: reliability.RepairRuleStatusProposed, Reversible: true, Idempotent: true, SourceIncidentID: incident.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return repository, incident, rule
}

func testStartedRepairDryRun(t *testing.T, ctx context.Context, repository reliability.Repository, incident reliability.FailureIncident, rule reliability.RepairRule, now time.Time) actioncontract.ActionRun {
	t.Helper()
	target, err := loadRepairInvocationTarget(ctx, repository, incident, rule)
	if err != nil {
		t.Fatal(err)
	}
	return actioncontract.ActionRun{
		ID: actioncontract.ActionRunID("repair-dry-run:" + string(rule.Strategy)), TenantID: incident.TenantID, ActionTypeID: productionRepairActionTypeID,
		ActionCategory: actioncontract.CategoryHybrid, Actor: actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
		Runtime:    actioncontract.RuntimeRef{Kind: actioncontract.RuntimeSystem, ID: "reliability-control-plane"},
		ObjectRefs: []actioncontract.ObjectRef{{Kind: "failure_incident", ID: incident.ID}, {Kind: "repair_rule", ID: rule.ID}},
		ArgsHash:   repairInvocationArgsHash(incident, rule, target), RiskLevel: actioncontract.RiskHigh,
		ExecutionMode: actioncontract.ExecutionModeDryRun, Status: actioncontract.RunStatusStarted, CreatedAt: now,
	}
}
