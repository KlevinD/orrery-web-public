package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformRepairExecutionRepositoryBindsPreflightAndBreaksConcurrentRepairs(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "reliability-repair-execution"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{reliabilityMigration(), reliabilityRepairExecutionMigration(), reliabilityDiagnosisMigration(), reliabilityRuleActivationMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	repository := NewPlatformRepository(store)
	now := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	control := NewService(repository, WithClock(func() time.Time { return now }))
	incident, err := control.ObserveFailure(ctx, FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi", ErrorCode: "timeout", OccurredAt: now,
		RepairTarget: &RepairTarget{Kind: RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "failed-run-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := control.UpsertRepairRule(ctx, RepairRuleCommand{
		ID: "rule-command-1",
		Rule: RepairRule{
			ID: "rule-1", TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: RepairStrategyRetry,
			ActionTypeID: "reliability.repair.execute", RiskLevel: RiskHigh, Status: RepairRuleStatusProposed,
			Reversible: true, Idempotent: true, SourceIncidentID: incident.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight := RepairPreflight{
		EvidenceID: "preflight-1", TenantID: "tenant-a", ActionRunID: "dry-run-1",
		IncidentID: incident.ID, IncidentVersion: incident.Version, RuleID: rule.ID, RuleVersion: rule.Version,
		Strategy: RepairStrategyRetry, ArgsHash: "sha256:args", TargetObservationID: "observation-1",
		TargetKind: RepairTargetMissionAgentRun, TargetRunID: "failed-run-1", Passed: true,
		CheckedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
	stored, created, err := repository.RecordRepairPreflight(ctx, preflight)
	if err != nil || !created || !sameRepairPreflight(stored, preflight) {
		t.Fatalf("RecordRepairPreflight stored=%+v created=%v err=%v", stored, created, err)
	}
	if _, created, err := repository.RecordRepairPreflight(ctx, preflight); err != nil || created {
		t.Fatalf("preflight replay created=%v err=%v", created, err)
	}
	mutated := preflight
	mutated.TargetRunID = "different-run"
	if _, _, err := repository.RecordRepairPreflight(ctx, mutated); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutated preflight error=%v, want ErrConflict", err)
	}
	target, err := repository.GetLatestRepairTarget(ctx, "tenant-a", incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	executionRecord := RepairExecution{
		TenantID: "tenant-a", ActionRunID: "apply-1", IncidentID: incident.ID, RuleID: rule.ID,
		PreflightEvidenceID: preflight.EvidenceID, Strategy: RepairStrategyRetry, Target: target, RetryRunID: "retry-run-1",
		Status: RepairExecutionDispatching, StartedAt: now, DeadlineAt: now.Add(30 * time.Minute), UpdatedAt: now,
	}
	incident, err = control.TransitionIncident(ctx, "tenant-a", incident.ID, IncidentStatusDiagnosed, ActorRef{Kind: "human", ID: "operator-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.BeginRepairExecution(ctx, executionRecord); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale preflight execution error=%v, want ErrConflict", err)
	}
	preflight.EvidenceID = "preflight-2"
	preflight.ActionRunID = "dry-run-2"
	preflight.IncidentVersion = incident.Version
	preflight.CheckedAt = now.Add(time.Minute)
	preflight.ExpiresAt = now.Add(31 * time.Minute)
	if _, created, err := repository.RecordRepairPreflight(ctx, preflight); err != nil || !created {
		t.Fatalf("fresh preflight created=%v err=%v", created, err)
	}
	executionRecord.PreflightEvidenceID = preflight.EvidenceID
	executionRecord.StartedAt = now.Add(time.Minute)
	executionRecord.DeadlineAt = now.Add(31 * time.Minute)
	executionRecord.UpdatedAt = now.Add(time.Minute)
	started, created, err := repository.BeginRepairExecution(ctx, executionRecord)
	if err != nil || !created || started.Version != 1 || started.Status != RepairExecutionDispatching {
		t.Fatalf("BeginRepairExecution started=%+v created=%v err=%v", started, created, err)
	}
	if _, created, err := repository.BeginRepairExecution(ctx, executionRecord); err != nil || created {
		t.Fatalf("execution replay created=%v err=%v", created, err)
	}
	concurrent := executionRecord
	concurrent.ActionRunID, concurrent.RetryRunID = "apply-2", "retry-run-2"
	if _, _, err := repository.BeginRepairExecution(ctx, concurrent); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent repair error=%v, want ErrConflict", err)
	}
	monitoring, err := repository.UpdateRepairExecution(ctx, RepairExecutionUpdate{
		TenantID: started.TenantID, ActionRunID: started.ActionRunID, ExpectedVersion: started.Version,
		Status: RepairExecutionMonitoring, RetryRunID: started.RetryRunID, UpdatedAt: now.Add(time.Minute),
	})
	if err != nil || monitoring.Status != RepairExecutionMonitoring || monitoring.Version != 2 {
		t.Fatalf("monitoring execution=%+v err=%v", monitoring, err)
	}
	active, err := repository.ListActiveRepairExecutions(ctx, 10)
	if err != nil || len(active) != 1 || active[0].ActionRunID != monitoring.ActionRunID {
		t.Fatalf("active executions=%+v err=%v", active, err)
	}
	preflights, err := repository.ListRepairPreflightsByIncident(ctx, "tenant-a", incident.ID, 10)
	if err != nil || len(preflights) != 2 || preflights[0].EvidenceID != "preflight-2" || preflights[1].EvidenceID != "preflight-1" {
		t.Fatalf("incident preflights=%+v err=%v", preflights, err)
	}
	executions, err := repository.ListRepairExecutionsByIncident(ctx, "tenant-a", incident.ID, 10)
	if err != nil || len(executions) != 1 || executions[0].ActionRunID != monitoring.ActionRunID || executions[0].Status != RepairExecutionMonitoring {
		t.Fatalf("incident executions=%+v err=%v", executions, err)
	}
	otherTenant, err := repository.ListRepairPreflightsByIncident(ctx, "tenant-b", incident.ID, 10)
	if err != nil || len(otherTenant) != 0 {
		t.Fatalf("cross-tenant preflights=%+v err=%v", otherTenant, err)
	}
}
