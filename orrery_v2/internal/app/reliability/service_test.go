package reliability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recordingEventSink struct {
	events []Event
}

type allowRepairRuleActivation struct{}

func (allowRepairRuleActivation) AuthorizeRepairRuleActivation(context.Context, RepairRuleCommand) error {
	return nil
}

func (s *recordingEventSink) PublishReliabilityEvent(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestObserveFailureAggregatesIdempotentlyAndIsolatesTenants(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	sink := &recordingEventSink{}
	service := NewService(repository, WithClock(func() time.Time { return now }), WithEventSink(sink))
	observation := FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "agent runtime", Component: "worker-pool",
		ErrorCode: "TIMEOUT", Message: "request timed out token=super-secret\nstack trace must not be retained",
		AgentRunID: "agent-run:1", Actor: ActorRef{Kind: "agent", ID: "agent-1"}, OccurredAt: now.Add(-time.Minute),
	}

	first, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure first: %v", err)
	}
	replayed, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure replay: %v", err)
	}
	if replayed.OccurrenceCount != 1 || replayed.Version != 1 || replayed.ID != first.ID {
		t.Fatalf("replayed incident = %+v", replayed)
	}
	if strings.Contains(first.RedactedSummary, "super-secret") || strings.Contains(first.RedactedSummary, "stack trace") || !strings.Contains(first.RedactedSummary, "[REDACTED]") {
		t.Fatalf("redacted summary = %q", first.RedactedSummary)
	}
	if first.Category != FailureCategoryTransient || !first.Retryable {
		t.Fatalf("classification = category:%s retryable:%v", first.Category, first.Retryable)
	}

	observation.ID = "observation-2"
	second, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure second occurrence: %v", err)
	}
	if second.OccurrenceCount != 2 || second.Version != 2 || second.ID != first.ID {
		t.Fatalf("aggregated incident = %+v", second)
	}

	observation.ID = "observation-tenant-b"
	observation.TenantID = "tenant-b"
	otherTenant, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatalf("ObserveFailure other tenant: %v", err)
	}
	if otherTenant.ID == first.ID || otherTenant.OccurrenceCount != 1 {
		t.Fatalf("tenant-isolated incident = %+v, tenant-a=%+v", otherTenant, first)
	}
	if len(sink.events) != 3 {
		t.Fatalf("published events = %d, want only newly accepted observations", len(sink.events))
	}
}

func TestDiagnosisCaseRequiresEvidenceHumanConfirmationAndCurrentIncident(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(func() time.Time { return now }))
	incident, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-diagnosis", TenantID: "tenant-a", Source: "agent_runtime", Component: "worker",
		ErrorCode: "timeout", Message: "provider timeout token=secret", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateDiagnosisCase(ctx, DiagnosisCaseCommand{
		ID: "diagnosis-without-evidence", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: DiagnosisCase{TenantID: incident.TenantID, IncidentID: incident.ID, RedactedSummary: "transient provider timeout"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing evidence error = %v", err)
	}
	diagnosis, err := service.CreateDiagnosisCase(ctx, DiagnosisCaseCommand{
		ID: "diagnosis-1", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: DiagnosisCase{
			TenantID: incident.TenantID, IncidentID: incident.ID, RootCauseCategory: FailureCategoryTransient,
			RedactedSummary: "upstream token=secret timed out", Confidence: 0.8, EvidenceIDs: []string{"evidence-2", "evidence-1", "evidence-1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateDiagnosisCase: %v", err)
	}
	if diagnosis.Status != DiagnosisCaseStatusProposed || diagnosis.IncidentVersion != incident.Version ||
		len(diagnosis.EvidenceIDs) != 2 || diagnosis.EvidenceIDs[0] != "evidence-1" || strings.Contains(diagnosis.RedactedSummary, "secret") {
		t.Fatalf("diagnosis = %+v", diagnosis)
	}
	replayed, err := service.CreateDiagnosisCase(ctx, DiagnosisCaseCommand{
		ID: "diagnosis-1", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: DiagnosisCase{
			TenantID: incident.TenantID, IncidentID: incident.ID, RootCauseCategory: FailureCategoryTransient,
			RedactedSummary: "upstream token=secret timed out", Confidence: 0.8, EvidenceIDs: []string{"evidence-1", "evidence-2"},
		},
	})
	if err != nil || replayed.ID != diagnosis.ID || replayed.Version != diagnosis.Version {
		t.Fatalf("diagnosis replay = %+v err=%v", replayed, err)
	}
	_, err = service.DecideDiagnosisCase(ctx, DiagnosisDecisionCommand{
		ID: "agent-confirm", TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID,
		Decision: DiagnosisDecisionConfirm, Reason: "looks correct", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("agent confirmation error = %v", err)
	}
	confirmed, err := service.DecideDiagnosisCase(ctx, DiagnosisDecisionCommand{
		ID: "human-confirm", TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID,
		Decision: DiagnosisDecisionConfirm, Reason: "evidence reviewed", Actor: ActorRef{Kind: "human", ID: "operator-1"},
	})
	if err != nil || confirmed.Status != DiagnosisCaseStatusConfirmed || confirmed.Version != 2 {
		t.Fatalf("confirmed diagnosis = %+v err=%v", confirmed, err)
	}
	rule, err := service.ProposeRepairRuleFromDiagnosis(ctx, RepairRuleCommand{
		ID: "rule-from-diagnosis", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Rule: RepairRule{
			TenantID: incident.TenantID, DiagnosisCaseID: confirmed.ID, Strategy: RepairStrategyRetry,
			ActionTypeID: "reliability.repair.execute", RiskLevel: RiskHigh, Reversible: true, Idempotent: true,
		},
	})
	if err != nil || rule.Status != RepairRuleStatusProposed || rule.SourceIncidentID != incident.ID ||
		rule.DiagnosisCaseID != confirmed.ID || rule.SupportCount != 1 || rule.AutoApply {
		t.Fatalf("proposed rule = %+v err=%v", rule, err)
	}
}

func TestDiagnosisConfirmationAndRuleProposalRejectStaleIncidentVersion(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 8, 45, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(func() time.Time { return now }))
	incident, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "agent_runtime", Component: "worker", ErrorCode: "timeout", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	diagnosis, err := service.CreateDiagnosisCase(ctx, DiagnosisCaseCommand{
		ID: "diagnosis-stale", Actor: ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: DiagnosisCase{TenantID: incident.TenantID, IncidentID: incident.ID, RedactedSummary: "provider timeout", Confidence: 0.7, EvidenceIDs: []string{"evidence-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-2", TenantID: "tenant-a", Source: "agent_runtime", Component: "worker", ErrorCode: "timeout", OccurredAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.DecideDiagnosisCase(ctx, DiagnosisDecisionCommand{
		ID: "confirm-stale", TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID,
		Decision: DiagnosisDecisionConfirm, Reason: "reviewed", Actor: ActorRef{Kind: "human", ID: "operator-1"},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale diagnosis confirmation error = %v", err)
	}
}

func TestRepairRuleLearnsFromVerifiedOutcomesAndQuarantinesOnFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(func() time.Time { return now }), WithRepairRuleActivationAuthorizer(allowRepairRuleActivation{}))
	incident, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "scheduler", Component: "dispatch",
		ErrorCode: "connection_reset", Message: "temporary connection reset", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := RepairRuleCommand{
		ID: "proposal-1", Actor: ActorRef{Kind: "human", ID: "operator-1"},
		Rule: RepairRule{
			TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: RepairStrategyRetry,
			ActionTypeID: "reliability.repair.execute", RiskLevel: RiskLow, Status: RepairRuleStatusActive,
			Confidence: 0.95, SupportCount: 3, SuccessCount: 3, Reversible: true, Idempotent: true,
			AutoApply: true, SourceIncidentID: incident.ID,
		},
	}
	rule, err := service.UpsertRepairRule(ctx, command)
	if err != nil {
		t.Fatalf("UpsertRepairRule: %v", err)
	}
	decision := EvaluateAutoApply(rule, incident, now)
	if !decision.Allowed {
		t.Fatalf("eligible rule rejected: %+v", decision)
	}

	failed, err := service.RecordRepairOutcome(ctx, RepairOutcome{
		ID: "outcome-1", TenantID: "tenant-a", RuleID: rule.ID, IncidentID: incident.ID,
		Success: false, EvidenceID: "evidence-1", OccurredAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordRepairOutcome: %v", err)
	}
	if failed.Status != RepairRuleStatusQuarantined || failed.AutoApply || failed.FailureCount != 1 {
		t.Fatalf("failed repair rule = %+v", failed)
	}
	replayed, err := service.RecordRepairOutcome(ctx, RepairOutcome{
		ID: "outcome-1", TenantID: "tenant-a", RuleID: rule.ID, IncidentID: incident.ID,
		Success: false, EvidenceID: "evidence-1", OccurredAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordRepairOutcome replay: %v", err)
	}
	if replayed.FailureCount != 1 || replayed.Version != failed.Version {
		t.Fatalf("replayed outcome changed rule = %+v", replayed)
	}
	if EvaluateAutoApply(replayed, incident, now).Allowed {
		t.Fatal("quarantined rule must never auto-apply")
	}
	replayedCommand, err := service.UpsertRepairRule(ctx, command)
	if err != nil {
		t.Fatalf("UpsertRepairRule command replay after learning: %v", err)
	}
	if replayedCommand.Version != failed.Version || replayedCommand.FailureCount != failed.FailureCount {
		t.Fatalf("command replay did not return current rule: %+v", replayedCommand)
	}
	command.ID = "proposal-2"
	if _, err := service.UpsertRepairRule(ctx, command); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("decreasing learned counters error = %v, want ErrInvalidRequest", err)
	}
}

func TestActiveRepairRuleRequiresGovernanceAuthorizer(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC)
	service := NewService(NewMemoryRepository(), WithClock(func() time.Time { return now }))
	incident, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "scheduler", Component: "dispatch",
		ErrorCode: "timeout", Message: "temporary timeout", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpsertRepairRule(ctx, RepairRuleCommand{
		ID: "activation-1", Actor: ActorRef{Kind: "agent", ID: "learner-1"},
		Rule: RepairRule{
			TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: RepairStrategyRetry,
			ActionTypeID: "reliability.repair.execute", RiskLevel: RiskLow, Status: RepairRuleStatusActive,
			Confidence: 0.95, SupportCount: 3, SuccessCount: 3, Reversible: true, Idempotent: true,
			AutoApply: true, SourceIncidentID: incident.ID,
		},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("active rule without governance error = %v, want ErrInvalidRequest", err)
	}
}

func TestRepairRuleActivationProvenanceMustBeCompleteAndActive(t *testing.T) {
	activatedAt := time.Date(2026, 9, 1, 9, 45, 0, 0, time.UTC)
	base := RepairRule{
		TenantID: "tenant-a", SelectorFingerprint: "sha256:rule", Strategy: RepairStrategyRetry,
		ActionTypeID: "reliability.repair.execute", RiskLevel: RiskLow, Status: RepairRuleStatusProposed,
		SourceIncidentID: "incident-1",
	}

	proposedWithProvenance := base
	proposedWithProvenance.ActivationDecisionID = "decision-1"
	proposedWithProvenance.ActivatedBy = ActorRef{Kind: "human", ID: "operator-1"}
	proposedWithProvenance.ActivatedAt = &activatedAt
	if err := validateRepairRule(proposedWithProvenance); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("proposed rule provenance error = %v, want ErrInvalidRequest", err)
	}

	incompleteActive := base
	incompleteActive.Status = RepairRuleStatusActive
	incompleteActive.ActivationDecisionID = "decision-1"
	if err := validateRepairRule(incompleteActive); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("incomplete active provenance error = %v, want ErrInvalidRequest", err)
	}

	completeActive := incompleteActive
	completeActive.ActivatedBy = ActorRef{Kind: "human", ID: "operator-1"}
	completeActive.ActivatedAt = &activatedAt
	if err := validateRepairRule(completeActive); err != nil {
		t.Fatalf("complete active provenance rejected: %v", err)
	}
	changedProvenance := completeActive
	changedProvenance.ActivationDecisionID = "decision-2"
	if err := validateRepairRuleUpdate(completeActive, changedProvenance); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("changed activation provenance error = %v, want ErrInvalidRequest", err)
	}
}

func TestObserveFailureRejectsReusedObservationIDForDifferentFingerprint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	service := NewService(NewMemoryRepository(), WithClock(func() time.Time { return now }))
	observation := FailureObservation{
		ID: "observation-1", TenantID: "tenant-a", Source: "scheduler", Component: "dispatch",
		ErrorCode: "timeout", Message: "temporary timeout", OccurredAt: now,
	}
	if _, err := service.ObserveFailure(ctx, observation); err != nil {
		t.Fatal(err)
	}
	observation.ErrorCode = "permission_denied"
	observation.Message = "permission denied"
	if _, err := service.ObserveFailure(ctx, observation); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused observation id error = %v, want ErrConflict", err)
	}
}

func TestObserveFailureKeepsLatestStateForLateObservation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	service := NewService(NewMemoryRepository(), WithClock(func() time.Time { return now }))
	latest, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-latest", TenantID: "tenant-a", Source: "scheduler", Component: "dispatch",
		ErrorCode: "timeout", Message: "latest summary", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.TransitionIncident(ctx, "tenant-a", latest.ID, IncidentStatusResolved, ActorRef{Kind: "human", ID: "operator-1"})
	if err != nil {
		t.Fatal(err)
	}
	late, err := service.ObserveFailure(ctx, FailureObservation{
		ID: "observation-late", TenantID: "tenant-a", Source: "scheduler", Component: "dispatch",
		ErrorCode: "timeout", Message: "old summary", OccurredAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if late.Status != IncidentStatusResolved || late.RedactedSummary != "latest summary" || !late.LastSeenAt.Equal(now) {
		t.Fatalf("late observation overwrote current incident state: %+v (resolved=%+v)", late, resolved)
	}
	if !late.FirstSeenAt.Equal(now.Add(-time.Hour)) || late.OccurrenceCount != 2 {
		t.Fatalf("late observation was not aggregated: %+v", late)
	}
}

func TestAutoApplyNeverAllowsCodePermissionOrDataMutation(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	incident := FailureIncident{Fingerprint: "sha256:exact"}
	for _, strategy := range []RepairStrategy{RepairStrategyPatch, RepairStrategyPermissionChange, RepairStrategyDataMutation} {
		rule := RepairRule{
			SelectorFingerprint: incident.Fingerprint, Strategy: strategy, RiskLevel: RiskLow,
			Status: RepairRuleStatusActive, Confidence: 1, SupportCount: 10, SuccessCount: 10,
			Reversible: true, Idempotent: true, AutoApply: true,
		}
		if decision := EvaluateAutoApply(rule, incident, now); decision.Allowed {
			t.Fatalf("strategy %s unexpectedly allowed", strategy)
		}
	}
}
