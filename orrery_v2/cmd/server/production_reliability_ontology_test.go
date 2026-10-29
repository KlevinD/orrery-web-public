package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestProductionReliabilityOntologyCapturesRedactedRuntimeFailureAndRepairRule(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-ontology")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repository := reliability.NewPlatformRepository(core)
	now := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	sink.reliability = reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }), reliability.WithEventSink(sink))
	started := runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: "tenant-a", AgentID: "agent-1",
		RunID: "runtime-a", RuntimeKind: "pi", Status: "running", StartedAt: now,
	}
	if err := sink.PublishRuntimeLifecycle(ctx, started); err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}
	failure := runtimedaemon.RuntimeFailureEvent{
		Kind: runtimedaemon.RuntimeLifecycleTimedOut, TenantID: "tenant-a", AgentID: "agent-1",
		RunID: "runtime-a", RuntimeKind: "pi", WorkerID: "worker-1", Code: "timeout",
		Message: "provider timed out token=top-secret\nraw stack must stay outside ontology",
	}
	if err := sink.PublishRuntimeFailure(ctx, failure); err != nil {
		t.Fatalf("PublishRuntimeFailure: %v", err)
	}
	incidentID := reliabilityEventObjectID(t, ctx, sink.events, "reliability.failure.observed", "tenant-a")
	incident, err := repository.GetIncident(ctx, "tenant-a", incidentID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if strings.Contains(incident.RedactedSummary, "top-secret") || strings.Contains(incident.RedactedSummary, "raw stack") {
		t.Fatalf("incident leaked raw diagnostics: %+v", incident)
	}
	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "failure_incident", incidentID)
	observationEvidenceID := fmt.Sprintf("failure-observation-evidence:%s:%d", incidentID, incident.Version)
	assertContextGraphObject(t, objects, "evidence_item", observationEvidenceID)
	if !contextGraphLinksContain(links, "evidence_item_supports_failure_incident", observationEvidenceID, incidentID) {
		t.Fatalf("failure observation evidence link missing: %+v", links)
	}
	if !contextGraphLinksContain(links, "failure_incident_observed_in_agent_run", incidentID, "agent-run:runtime-a") {
		t.Fatalf("failure to agent run link missing: %+v", links)
	}
	assertContextGraphFact(t, facts, "failure_incident", incidentID, "retryable", true)
	for _, fact := range facts {
		if value, ok := fact.Value.(string); ok && strings.Contains(value, "top-secret") {
			t.Fatalf("context graph leaked secret in fact %+v", fact)
		}
	}

	rule, err := sink.reliability.UpsertRepairRule(ctx, reliability.RepairRuleCommand{
		ID: "proposal-1", Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
		Rule: reliability.RepairRule{
			TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: reliability.RepairStrategyRetry,
			ActionTypeID: "reliability.repair.execute", RiskLevel: reliability.RiskHigh,
			Status: reliability.RepairRuleStatusProposed, Confidence: 0.6, SupportCount: 3,
			Reversible: true, Idempotent: true, SourceIncidentID: incident.ID,
		},
	})
	if err != nil {
		t.Fatalf("UpsertRepairRule: %v", err)
	}
	objects, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "repair_rule", rule.ID)
	if !contextGraphLinksContain(links, "repair_rule_learned_from_failure_incident", rule.ID, incident.ID) {
		t.Fatalf("repair learning link missing: %+v", links)
	}

	if err := sink.PublishRuntimeFailure(ctx, failure); err != nil {
		t.Fatalf("PublishRuntimeFailure replay: %v", err)
	}
	replayed, err := repository.GetIncident(ctx, "tenant-a", incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.OccurrenceCount != 1 || replayed.Version != 1 {
		t.Fatalf("replay changed incident aggregate: %+v", replayed)
	}
	if count := reliabilityEventCount(t, ctx, sink.events, "reliability.failure.observed", "tenant-a"); count != 1 {
		t.Fatalf("retained failure observation events = %d, want 1", count)
	}
	second, err := sink.reliability.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "runtime:runtime-b:timed_out:timeout", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "a later occurrence", OccurredAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ObserveFailure second occurrence: %v", err)
	}
	if second.ID != incidentID || second.Version != 2 || second.OccurrenceCount != 2 {
		t.Fatalf("second occurrence aggregate = %+v", second)
	}
	if err := sink.PublishRuntimeFailure(ctx, failure); err != nil {
		t.Fatalf("PublishRuntimeFailure old observation replay: %v", err)
	}
	current, err := repository.GetIncident(ctx, "tenant-a", incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || current.OccurrenceCount != 2 {
		t.Fatalf("old observation replay changed aggregate: %+v", current)
	}
	_, _, facts, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphFact(t, facts, "failure_incident", incidentID, "occurrence_count", int64(2))
	if count := reliabilityEventCount(t, ctx, sink.events, "reliability.failure.observed", "tenant-a"); count != 2 {
		t.Fatalf("retained failure observation events after ordered replay = %d, want 2", count)
	}
}

func TestProductionReliabilityOntologyProjectsDiagnosisEvidenceAndRuleBasis(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-diagnosis")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 13, 15, 0, 0, time.UTC)
	repository := reliability.NewPlatformRepository(core)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	sink.reliability = reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }), reliability.WithEventSink(sink))
	incident, err := sink.reliability.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "diagnosis-observation", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "provider timed out", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidenceID := fmt.Sprintf("failure-observation-evidence:%s:%d", incident.ID, incident.Version)
	diagnosis, err := sink.reliability.CreateDiagnosisCase(ctx, reliability.DiagnosisCaseCommand{
		ID: "diagnosis-proposal-1", Actor: reliability.ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: reliability.DiagnosisCase{
			TenantID: incident.TenantID, IncidentID: incident.ID, RootCauseCategory: reliability.FailureCategoryTransient,
			RedactedSummary: "upstream provider timeout", Confidence: 0.82, EvidenceIDs: []string{evidenceID},
		},
	})
	if err != nil {
		t.Fatalf("CreateDiagnosisCase: %v", err)
	}
	confirmed, err := sink.reliability.DecideDiagnosisCase(ctx, reliability.DiagnosisDecisionCommand{
		ID: "diagnosis-confirm-1", TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID,
		Decision: reliability.DiagnosisDecisionConfirm, Reason: "evidence reviewed", Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
	})
	if err != nil {
		t.Fatalf("DecideDiagnosisCase: %v", err)
	}
	rule, err := sink.reliability.ProposeRepairRuleFromDiagnosis(ctx, reliability.RepairRuleCommand{
		ID: "diagnosis-rule-1", Actor: reliability.ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Rule: reliability.RepairRule{
			TenantID: incident.TenantID, DiagnosisCaseID: confirmed.ID, Strategy: reliability.RepairStrategyRetry,
			ActionTypeID: string(productionRepairActionTypeID), RiskLevel: reliability.RiskHigh, Reversible: true, Idempotent: true,
		},
	})
	if err != nil {
		t.Fatalf("ProposeRepairRuleFromDiagnosis: %v", err)
	}
	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, incident.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "diagnosis_case", confirmed.ID)
	assertContextGraphFact(t, facts, "diagnosis_case", confirmed.ID, "status", "confirmed")
	if !contextGraphLinksContain(links, "diagnosis_case_diagnoses_failure_incident", confirmed.ID, incident.ID) ||
		!contextGraphLinksContain(links, "evidence_item_supports_diagnosis_case", evidenceID, confirmed.ID) ||
		!contextGraphLinksContain(links, "repair_rule_supported_by_diagnosis_case", rule.ID, confirmed.ID) {
		t.Fatalf("diagnosis lineage missing: %+v", links)
	}
	activatedAt := now.Add(time.Minute)
	rule.Status = reliability.RepairRuleStatusActive
	activationService := reliability.NewService(repository, reliability.WithClock(func() time.Time { return activatedAt }), reliability.WithEventSink(sink), reliability.WithRepairRuleActivationAuthorizer(allowRepairRuleActivation{}))
	rule, err = activationService.UpsertRepairRule(ctx, reliability.RepairRuleCommand{
		ID: "activate-rule-1", Rule: rule, Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
		Activation: &reliability.RepairRuleActivationAuthorization{DecisionID: "activation-review-1", ReceiptID: "receipt-1", ArgsHash: "sha256:activation", PreflightEvidenceID: "preflight-1"},
	})
	if err != nil {
		t.Fatalf("activate and project repair rule: %v", err)
	}
	_, _, facts, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, incident.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphFact(t, facts, "repair_rule", rule.ID, "activation_decision_id", "activation-review-1")
	assertContextGraphFact(t, facts, "repair_rule", rule.ID, "activated_by_id", "operator-1")
}

func TestProductionReliabilityOntologyPersistsExactMissionAgentRepairTarget(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-repair-target")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repository := reliability.NewPlatformRepository(core)
	now := time.Date(2026, 9, 1, 13, 30, 0, 0, time.UTC)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	sink.reliability = reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }), reliability.WithEventSink(sink))
	failure := runtimedaemon.RuntimeFailureEvent{
		Kind: runtimedaemon.RuntimeLifecycleTimedOut, TenantID: "tenant-a", MissionID: "mission-1", TaskID: "task-1",
		AgentID: "agent-1", RunID: "run-original", RuntimeKind: "pi", QueueID: "mission:mission-1", Code: "timeout", Message: "provider timed out",
	}
	if err := sink.PublishRuntimeFailure(ctx, failure); err != nil {
		t.Fatalf("PublishRuntimeFailure: %v", err)
	}
	graphTenantID := missionScopeTenantID("tenant-a", "mission-1")
	incidentID := reliabilityEventObjectID(t, ctx, sink.events, "reliability.failure.observed", graphTenantID)
	incident, err := repository.GetIncident(ctx, graphTenantID, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := repository.GetLatestRepairTarget(ctx, graphTenantID, incidentID)
	if err != nil {
		t.Fatalf("GetLatestRepairTarget: %v", err)
	}
	if target.Kind != reliability.RepairTargetMissionAgentRun || target.AuthorizationTenantID != "tenant-a" ||
		target.MissionID != "mission-1" || target.RunID != "run-original" || target.ObservationID == "" || !target.OccurredAt.Equal(incident.LastSeenAt) {
		t.Fatalf("repair target = %+v incident=%+v", target, incident)
	}
}

func TestProductionReliabilityOntologyIsolatesTenants(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-tenants")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repository := reliability.NewPlatformRepository(core)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.reliability = reliability.NewService(repository, reliability.WithEventSink(sink))
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	incidentIDs := map[string]string{}
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		runID := "runtime-" + tenantID
		if err := sink.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
			Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: tenantID, AgentID: "agent-1",
			RunID: agentcore.AgentRunID(runID), RuntimeKind: "pi", Status: "running", StartedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := sink.PublishRuntimeFailure(ctx, runtimedaemon.RuntimeFailureEvent{
			Kind: runtimedaemon.RuntimeLifecycleFailed, TenantID: tenantID, AgentID: "agent-1",
			RunID: agentcore.AgentRunID(runID), RuntimeKind: "pi", Code: "provider_failed", Message: "provider failed",
		}); err != nil {
			t.Fatal(err)
		}
		incidentIDs[tenantID] = reliabilityEventObjectID(t, ctx, sink.events, "reliability.failure.observed", tenantID)
	}
	if incidentIDs["tenant-a"] == incidentIDs["tenant-b"] {
		t.Fatalf("tenant incident ids collided: %+v", incidentIDs)
	}
	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.ObjectType == "failure_incident" && object.ID == incidentIDs["tenant-b"] {
			t.Fatalf("tenant-a graph contains tenant-b incident: %+v", object)
		}
	}
}

func TestProductionReliabilityOntologyCapturesFailedActionRun(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-action-run")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repository := reliability.NewPlatformRepository(core)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.reliability = reliability.NewService(repository, reliability.WithEventSink(sink))
	createdAt := time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC)
	completedAt := createdAt.Add(time.Minute)
	run := actioncontract.ActionRun{
		ID: "action-run-failed", TenantID: "tenant-a", ActionTypeID: "connector.sync",
		Actor:   actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-1"},
		Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeWorker, ID: "worker-1"},
		Status:  actioncontract.RunStatusFailed, ErrorCode: "dependency_unavailable",
		ErrorMessage: "connector failed token=action-secret", CreatedAt: createdAt, CompletedAt: &completedAt,
	}
	if err := sink.ProjectActionRun(ctx, run); err != nil {
		t.Fatalf("ProjectActionRun: %v", err)
	}
	incidentID := reliabilityEventObjectID(t, ctx, sink.events, "reliability.failure.observed", "tenant-a")
	incident, err := repository.GetIncident(ctx, "tenant-a", incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(incident.RedactedSummary, "action-secret") {
		t.Fatalf("action failure leaked secret: %+v", incident)
	}
	objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "action_run", string(run.ID))
	assertContextGraphObject(t, objects, "failure_incident", incidentID)
	if !contextGraphLinksContain(links, "failure_incident_observed_in_action_run", incidentID, string(run.ID)) {
		t.Fatalf("failure to action run link missing: %+v", links)
	}
}

func TestProductionReliabilityOntologyLearnsFromTerminalRepairActionRuns(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-repair-outcomes")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 14, 45, 0, 0, time.UTC)
	repository := reliability.NewPlatformRepository(core)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, func() time.Time { return now })
	sink.reliability = reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }), reliability.WithEventSink(sink))
	incident, err := sink.reliability.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "observation-repair", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi",
		ErrorCode: "timeout", Message: "provider timed out", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := sink.reliability.UpsertRepairRule(ctx, reliability.RepairRuleCommand{
		ID: "proposal-repair", Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
		Rule: reliability.RepairRule{
			TenantID: "tenant-a", SelectorFingerprint: incident.Fingerprint, Strategy: reliability.RepairStrategyRetry,
			ActionTypeID: "reliability.repair.execute", RiskLevel: reliability.RiskHigh,
			Status: reliability.RepairRuleStatusProposed, Reversible: true, Idempotent: true, SourceIncidentID: incident.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, status := range []actioncontract.RunStatus{actioncontract.RunStatusCompleted, actioncontract.RunStatusFailed} {
		dryRunCompletedAt := now.Add(time.Duration(index+1) * time.Second)
		dryRun := actioncontract.ActionRun{
			ID: actioncontract.ActionRunID("repair-dry-run-" + string(status)), TenantID: "tenant-a", ActionTypeID: productionRepairActionTypeID,
			Actor:         actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
			Runtime:       actioncontract.RuntimeRef{Kind: actioncontract.RuntimeSystem, ID: "reliability-control-plane"},
			ObjectRefs:    []actioncontract.ObjectRef{{Kind: "failure_incident", ID: incident.ID}, {Kind: "repair_rule", ID: rule.ID}},
			EvidenceRefs:  []actioncontract.Ref{{Kind: "evidence_item", ID: "repair-dry-run-evidence-" + string(status)}},
			Status:        status,
			RiskLevel:     actioncontract.RiskHigh,
			ExecutionMode: actioncontract.ExecutionModeDryRun,
			ArgsHash:      "sha256:repair-dry-run-" + string(status), CreatedAt: now, CompletedAt: &dryRunCompletedAt,
		}
		if status == actioncontract.RunStatusFailed {
			dryRun.ErrorCode = "repair_dry_run_preflight_failed"
			dryRun.ErrorMessage = "Repair dry-run preflight did not pass"
		}
		projectStartedReliabilityActionRun(t, ctx, sink, dryRun)
		if err := sink.ProjectActionRun(ctx, dryRun); err != nil {
			t.Fatalf("ProjectActionRun %s dry-run: %v", status, err)
		}
		if status == actioncontract.RunStatusCompleted {
			record := repairDryRunEvidenceRecord{
				EvidenceID: "repair-dry-run-evidence-completed", ProviderCallID: "repair-dry-run-provider:retry:v1",
				ResultID: "repair-dry-run-result:completed", Checks: []string{"tenant_bound", "no_mutation"},
				IncidentID: incident.ID, RuleID: rule.ID, Strategy: reliability.RepairStrategyRetry, ObservedAt: dryRunCompletedAt,
			}
			if err := sink.RecordRepairDryRunEvidence(ctx, dryRun, record); err != nil {
				t.Fatalf("RecordRepairDryRunEvidence: %v", err)
			}
			if err := sink.RecordRepairDryRunEvidence(ctx, dryRun, record); err != nil {
				t.Fatalf("RecordRepairDryRunEvidence replay: %v", err)
			}
		}
		unchanged, err := repository.GetRepairRule(ctx, "tenant-a", rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if unchanged.SuccessCount != 0 || unchanged.FailureCount != 0 || unchanged.Version != rule.Version || unchanged.Status != rule.Status {
			t.Fatalf("%s dry-run changed repair learning: %+v", status, unchanged)
		}
	}
	completedAt := now.Add(time.Minute)
	completedRun := actioncontract.ActionRun{
		ID: "repair-action-success", TenantID: "tenant-a", ActionTypeID: "reliability.repair.execute",
		Actor:        actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
		Runtime:      actioncontract.RuntimeRef{Kind: actioncontract.RuntimeSystem, ID: "reliability-control-plane"},
		ObjectRefs:   []actioncontract.ObjectRef{{Kind: "failure_incident", ID: incident.ID}, {Kind: "repair_rule", ID: rule.ID}},
		EvidenceRefs: []actioncontract.Ref{{Kind: "evidence_item", ID: "repair-verification-success"}},
		Status:       actioncontract.RunStatusCompleted, RiskLevel: actioncontract.RiskHigh,
		ArgsHash: "sha256:repair-success", CreatedAt: now, CompletedAt: &completedAt,
	}
	successEvidence := repairVerificationEvidenceRecord{
		EvidenceID: "repair-verification-success", IncidentID: incident.ID, RuleID: rule.ID,
		OriginalRunID: "run-original", RetryRunID: "run-retry-success", Status: "passed", QueueStatus: "completed", ObservedAt: completedAt,
	}
	projectStartedReliabilityActionRun(t, ctx, sink, completedRun)
	if err := sink.RecordRepairVerificationEvidence(ctx, completedRun, successEvidence); err != nil {
		t.Fatalf("RecordRepairVerificationEvidence: %v", err)
	}
	if err := sink.RecordRepairVerificationEvidence(ctx, completedRun, successEvidence); err != nil {
		t.Fatalf("RecordRepairVerificationEvidence replay: %v", err)
	}
	if err := sink.ProjectActionRun(ctx, completedRun); err != nil {
		t.Fatalf("ProjectActionRun completed repair: %v", err)
	}
	learned, err := repository.GetRepairRule(ctx, "tenant-a", rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if learned.SuccessCount != 1 || learned.FailureCount != 0 || learned.Version != 2 {
		t.Fatalf("successful repair outcome = %+v", learned)
	}
	if err := sink.ProjectActionRun(ctx, completedRun); err != nil {
		t.Fatalf("ProjectActionRun completed repair replay: %v", err)
	}
	replayed, err := repository.GetRepairRule(ctx, "tenant-a", rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.SuccessCount != 1 || replayed.Version != learned.Version {
		t.Fatalf("completed repair replay changed rule: %+v", replayed)
	}

	failedAt := now.Add(2 * time.Minute)
	failedRun := completedRun
	failedRun.ID = "repair-action-failed"
	failedRun.Status = actioncontract.RunStatusFailed
	failedRun.ErrorCode = "repair_retry_terminal_verification_failed"
	failedRun.ErrorMessage = "post-repair health check failed"
	failedRun.EvidenceRefs = []actioncontract.Ref{{Kind: "evidence_item", ID: "repair-verification-failed"}}
	failedRun.ArgsHash = "sha256:repair-failed"
	failedRun.CompletedAt = &failedAt
	projectStartedReliabilityActionRun(t, ctx, sink, failedRun)
	if err := sink.RecordRepairVerificationEvidence(ctx, failedRun, repairVerificationEvidenceRecord{
		EvidenceID: "repair-verification-failed", IncidentID: incident.ID, RuleID: rule.ID,
		OriginalRunID: "run-original", RetryRunID: "run-retry-failed", Status: "failed", QueueStatus: "failed",
		FailureCode: "provider_failed", ObservedAt: failedAt,
	}); err != nil {
		t.Fatalf("RecordRepairVerificationEvidence failed repair: %v", err)
	}
	if err := sink.ProjectActionRun(ctx, failedRun); err != nil {
		t.Fatalf("ProjectActionRun failed repair: %v", err)
	}
	quarantined, err := repository.GetRepairRule(ctx, "tenant-a", rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if quarantined.Status != reliability.RepairRuleStatusQuarantined || quarantined.FailureCount != 1 || quarantined.SuccessCount != 1 || quarantined.Version != 3 {
		t.Fatalf("failed repair outcome = %+v", quarantined)
	}
	if err := sink.ProjectActionRun(ctx, failedRun); err != nil {
		t.Fatalf("ProjectActionRun failed repair replay: %v", err)
	}
	stable, err := repository.GetRepairRule(ctx, "tenant-a", rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stable.FailureCount != 1 || stable.Version != quarantined.Version {
		t.Fatalf("failed repair replay changed rule: %+v", stable)
	}
	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		linkType string
		fromID   string
		toID     string
	}{
		{linkType: "action_run_targets_failure_incident", fromID: string(completedRun.ID), toID: incident.ID},
		{linkType: "action_run_targets_repair_rule", fromID: string(completedRun.ID), toID: rule.ID},
		{linkType: "evidence_item_supports_action_run", fromID: "repair-verification-success", toID: string(completedRun.ID)},
		{linkType: "evidence_item_supports_action_run", fromID: "repair-dry-run-evidence-completed", toID: "repair-dry-run-completed"},
		{linkType: "evidence_item_supports_failure_incident", fromID: "repair-dry-run-evidence-completed", toID: incident.ID},
		{linkType: "evidence_item_supports_repair_rule", fromID: "repair-dry-run-evidence-completed", toID: rule.ID},
		{linkType: "evidence_item_supports_failure_incident", fromID: "repair-verification-success", toID: incident.ID},
		{linkType: "evidence_item_supports_repair_rule", fromID: "repair-verification-success", toID: rule.ID},
		{linkType: "evidence_item_verifies_agent_run", fromID: "repair-verification-success", toID: ontologyAgentRunID("run-retry-success")},
	} {
		if !contextGraphLinksContain(links, expected.linkType, expected.fromID, expected.toID) {
			t.Fatalf("repair lineage link %+v missing: %+v", expected, links)
		}
	}
	assertContextGraphObject(t, objects, "evidence_item", "repair-dry-run-evidence-completed")
	assertContextGraphObject(t, objects, "evidence_item", "repair-verification-success")
	assertContextGraphFact(t, facts, "evidence_item", "repair-dry-run-evidence-completed", "strategy", "retry")
	assertContextGraphFact(t, facts, "evidence_item", "repair-verification-success", "queue_status", "completed")
	if count := reliabilityEventCount(t, ctx, sink.events, "reliability.repair_verification.checked", "tenant-a"); count != 2 {
		t.Fatalf("repair verification events = %d, want 2", count)
	}
}

func projectStartedReliabilityActionRun(t *testing.T, ctx context.Context, sink *productionAgentExecutionOntologySink, terminal actioncontract.ActionRun) {
	t.Helper()
	started := terminal
	started.Status = actioncontract.RunStatusStarted
	started.CompletedAt = nil
	started.ErrorCode = ""
	started.ErrorMessage = ""
	if err := sink.ProjectActionRun(ctx, started); err != nil {
		t.Fatalf("ProjectActionRun %s started: %v", terminal.ID, err)
	}
}

func TestProductionReliabilityOntologyMappingsRejectMissingRequiredPayload(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-reliability-invalid-payload")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{
		"reliability.failure.observed",
		"reliability.failure.status_changed",
		"reliability.diagnosis_case.upserted",
		"reliability.repair_rule.upserted",
		"reliability.repair_dry_run.checked",
		"reliability.repair_verification.checked",
	} {
		_, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
			ID: "invalid:" + eventType, TenantID: "tenant-invalid", Type: eventType, StreamID: "invalid", Version: 1,
			OccurredAt: nowForReliabilityTest(), Payload: map[string]any{},
			PublishedBy:     graphprotocol.ActorRef{Kind: "service", ID: "test"},
			SourceAccessRef: trustedOntologySourceRef("failure_incident", "invalid", "tenant-invalid"),
		})
		if err == nil {
			t.Fatalf("mapping %s accepted empty payload", eventType)
		}
	}
}

func reliabilityEventObjectID(t *testing.T, ctx context.Context, log productionOntologyEventLog, eventType, tenantID string) string {
	t.Helper()
	events, err := log.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == eventType && event.TenantID == tenantID {
			id, _ := event.Payload["id"].(string)
			if id != "" {
				return id
			}
		}
	}
	t.Fatalf("event %s for tenant %s not found", eventType, tenantID)
	return ""
}

func reliabilityEventCount(t *testing.T, ctx context.Context, log productionOntologyEventLog, eventType, tenantID string) int {
	t.Helper()
	events, err := log.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == eventType && event.TenantID == tenantID {
			count++
		}
	}
	return count
}

func nowForReliabilityTest() time.Time {
	return time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
}
