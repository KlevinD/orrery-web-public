package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformRepositoryPersistsIdempotentFailureAndRepairLearning(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "reliability-platform"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{reliabilityMigration(), reliabilityRepairExecutionMigration(), reliabilityDiagnosisMigration(), reliabilityRuleActivationMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	repository := NewPlatformRepository(store)
	service := NewService(repository, WithClock(func() time.Time { return now }))
	observation := FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "temporary timeout", OccurredAt: now,
		RepairTarget: &RepairTarget{Kind: RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "run-failed"},
	}
	incident, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure: %v", err)
	}
	if _, err := service.ObserveFailure(ctx, observation); err != nil {
		t.Fatalf("ObserveFailure replay: %v", err)
	}
	loaded, err := repository.GetIncident(ctx, "tenant-a", incident.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if loaded.OccurrenceCount != 1 || loaded.Version != 1 {
		t.Fatalf("idempotent incident = %+v", loaded)
	}
	target, err := repository.GetLatestRepairTarget(ctx, "tenant-a", incident.ID)
	if err != nil || target.ObservationID != observation.ID || target.RunID != "run-failed" {
		t.Fatalf("latest repair target = %+v err=%v", target, err)
	}
	resolved, err := service.TransitionIncident(ctx, "tenant-a", incident.ID, IncidentStatusResolved, ActorRef{Kind: "human", ID: "operator-1"})
	if err != nil {
		t.Fatalf("TransitionIncident: %v", err)
	}
	if resolved.Status != IncidentStatusResolved {
		t.Fatalf("resolved incident = %+v", resolved)
	}
	observation.ID = "observation-2"
	observation.OccurredAt = now.Add(time.Minute)
	reopened, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure recurrence: %v", err)
	}
	if reopened.Status != IncidentStatusOpen || reopened.OccurrenceCount != 2 {
		t.Fatalf("reopened incident = %+v", reopened)
	}
	resolvedAgain, err := service.TransitionIncident(ctx, "tenant-a", incident.ID, IncidentStatusResolved, ActorRef{Kind: "human", ID: "operator-1"})
	if err != nil {
		t.Fatalf("TransitionIncident recurrence: %v", err)
	}
	observation.ID = "observation-late"
	observation.Message = "older summary"
	observation.OccurredAt = now.Add(-time.Hour)
	late, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure late occurrence: %v", err)
	}
	if late.Status != IncidentStatusResolved || late.RedactedSummary != reopened.RedactedSummary || !late.LastSeenAt.Equal(reopened.LastSeenAt) {
		t.Fatalf("late occurrence overwrote current state: %+v (resolved=%+v)", late, resolvedAgain)
	}
	if !late.FirstSeenAt.Equal(now.Add(-time.Hour)) || late.OccurrenceCount != 3 {
		t.Fatalf("late occurrence was not aggregated: %+v", late)
	}

	ruleRequest := RepairRuleCommand{ID: "proposal-1", Rule: RepairRule{
		TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: RepairStrategyRetry,
		ActionTypeID: "reliability.repair.execute", RiskLevel: RiskLow, Status: RepairRuleStatusProposed,
		Confidence: 0.5, SupportCount: 2, Reversible: true, Idempotent: true, SourceIncidentID: incident.ID,
	}}
	rule, err := service.UpsertRepairRule(ctx, ruleRequest)
	if err != nil {
		t.Fatalf("UpsertRepairRule: %v", err)
	}
	replayedRule, err := service.UpsertRepairRule(ctx, ruleRequest)
	if err != nil {
		t.Fatalf("UpsertRepairRule replay: %v", err)
	}
	if replayedRule.ID != rule.ID || replayedRule.Version != 1 {
		t.Fatalf("replayed rule = %+v", replayedRule)
	}
	rules, err := repository.ListRepairRules(ctx, "tenant-a", incident.Fingerprint)
	if err != nil || len(rules) != 1 {
		t.Fatalf("ListRepairRules = %+v, %v", rules, err)
	}
	activationService := NewService(repository, WithClock(func() time.Time { return now.Add(90 * time.Second) }), WithRepairRuleActivationAuthorizer(allowRepairRuleActivation{}))
	activationCandidate := rule
	activationCandidate.Status = RepairRuleStatusActive
	rule, err = activationService.UpsertRepairRule(ctx, RepairRuleCommand{
		ID: "activation-1", Rule: activationCandidate, Actor: ActorRef{Kind: "human", ID: "operator-1"},
		Activation: &RepairRuleActivationAuthorization{DecisionID: "activation-review-1", ReceiptID: "receipt-1", ArgsHash: "sha256:activation", PreflightEvidenceID: "preflight-1"},
	})
	if err != nil {
		t.Fatalf("activate repair rule: %v", err)
	}
	persistedActivation, err := repository.GetRepairRule(ctx, rule.TenantID, rule.ID)
	if err != nil || persistedActivation.ActivationDecisionID != "activation-review-1" || persistedActivation.ActivatedBy.ID != "operator-1" || persistedActivation.ActivatedAt == nil {
		t.Fatalf("persisted activation=%+v err=%v", persistedActivation, err)
	}
	outcome := RepairOutcome{
		ID: "outcome-1", TenantID: "tenant-a", RuleID: rule.ID, IncidentID: incident.ID,
		Success: true, EvidenceID: "evidence-1", OccurredAt: now.Add(2 * time.Minute),
	}
	learned, err := service.RecordRepairOutcome(ctx, outcome)
	if err != nil {
		t.Fatalf("RecordRepairOutcome: %v", err)
	}
	if learned.SuccessCount != 1 || learned.Version != rule.Version+1 {
		t.Fatalf("learned rule = %+v", learned)
	}
	replayedOutcome, err := service.RecordRepairOutcome(ctx, outcome)
	if err != nil {
		t.Fatalf("RecordRepairOutcome replay: %v", err)
	}
	if replayedOutcome.SuccessCount != 1 || replayedOutcome.Version != learned.Version {
		t.Fatalf("replayed outcome changed rule: %+v", replayedOutcome)
	}
	outcome.EvidenceID = "evidence-2"
	if _, err := service.RecordRepairOutcome(ctx, outcome); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutated outcome replay error = %v, want ErrConflict", err)
	}
	currentRule, err := service.UpsertRepairRule(ctx, ruleRequest)
	if err != nil {
		t.Fatalf("UpsertRepairRule replay after outcome: %v", err)
	}
	if currentRule.Version != learned.Version || currentRule.SuccessCount != learned.SuccessCount {
		t.Fatalf("command replay did not return current rule: %+v", currentRule)
	}
	ruleRequest.ID = "proposal-2"
	if _, err := service.UpsertRepairRule(ctx, ruleRequest); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("stale rule update error = %v, want ErrInvalidRequest", err)
	}
}

func TestReliabilityMigrationCreatesControlPlaneTables(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "reliability-migration"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{reliabilityMigration(), reliabilityRepairExecutionMigration(), reliabilityDiagnosisMigration(), reliabilityRuleActivationMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"reliability_failure_incidents",
		"reliability_failure_observations",
		"reliability_repair_rules",
		"reliability_repair_rule_commands",
		"reliability_repair_outcomes",
		"reliability_failure_repair_targets",
		"reliability_repair_preflights",
		"reliability_repair_executions",
		"reliability_diagnosis_cases",
		"reliability_diagnosis_evidence",
		"reliability_diagnosis_commands",
	} {
		exists, err := store.TableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}
}

func TestPlatformRepositoryPersistsAndDecidesEvidenceBackedDiagnosis(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "reliability-diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{reliabilityMigration(), reliabilityRepairExecutionMigration(), reliabilityDiagnosisMigration(), reliabilityRuleActivationMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := NewPlatformRepository(store)
	service := NewService(repository, WithClock(func() time.Time { return now }))
	incident, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi", ErrorCode: "timeout", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := DiagnosisCaseCommand{
		ID: "diagnosis-create-1", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: DiagnosisCase{
			TenantID: incident.TenantID, IncidentID: incident.ID, RedactedSummary: "upstream timeout",
			Confidence: 0.75, EvidenceIDs: []string{"evidence-2", "evidence-1"},
		},
	}
	diagnosis, err := service.CreateDiagnosisCase(ctx, command)
	if err != nil {
		t.Fatalf("CreateDiagnosisCase: %v", err)
	}
	replayed, err := service.CreateDiagnosisCase(ctx, command)
	if err != nil || replayed.ID != diagnosis.ID || replayed.Version != 1 {
		t.Fatalf("diagnosis replay = %+v err=%v", replayed, err)
	}
	items, err := repository.ListDiagnosisCasesByIncident(ctx, incident.TenantID, incident.ID, 10)
	if err != nil || len(items) != 1 || len(items[0].EvidenceIDs) != 2 || items[0].EvidenceIDs[0] != "evidence-1" {
		t.Fatalf("diagnosis list = %+v err=%v", items, err)
	}
	decision := DiagnosisDecisionCommand{
		ID: "diagnosis-confirm-1", TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID,
		Decision: DiagnosisDecisionConfirm, Reason: "evidence checked", Actor: ActorRef{Kind: "human", ID: "operator-1"},
	}
	confirmed, err := service.DecideDiagnosisCase(ctx, decision)
	if err != nil || confirmed.Status != DiagnosisCaseStatusConfirmed || confirmed.Version != 2 || confirmed.ReviewedBy.ID != "operator-1" {
		t.Fatalf("confirmed diagnosis = %+v err=%v", confirmed, err)
	}
	replayedDecision, err := service.DecideDiagnosisCase(ctx, decision)
	if err != nil || replayedDecision.Version != confirmed.Version {
		t.Fatalf("decision replay = %+v err=%v", replayedDecision, err)
	}
}
