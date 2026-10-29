package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultReliabilityMappingsProjectIncidentRuleReplayAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	incidentPayload := map[string]any{
		"id": "failure-incident:1", "fingerprint": "sha256:failure", "source": "agent_runtime", "component": "pi",
		"category": "transient", "severity": "error", "status": "open", "error_code": "timeout",
		"redacted_summary": "request timed out", "retryable": true, "first_seen_at": now.Format(time.RFC3339Nano),
		"last_seen_at": now.Format(time.RFC3339Nano), "occurrence_count": int64(1), "incident_version": int64(1),
	}
	incidentEvent := reliabilityOntologyEvent("failure-observed:1", "tenant-a", "reliability.failure.observed", "failure-incident:1", 1, now, incidentPayload)
	if report, err := runner.ApplyEvent(ctx, incidentEvent); err != nil || !report.OK {
		t.Fatalf("ApplyEvent incident: report=%+v err=%v", report, err)
	}
	if report, err := runner.ApplyEvent(ctx, incidentEvent); err != nil || !report.OK {
		t.Fatalf("replay incident: report=%+v err=%v", report, err)
	}
	statusEvent := reliabilityOntologyEvent("failure-status:2", "tenant-a", "reliability.failure.status_changed", "failure-incident:1", 2, now.Add(time.Minute), map[string]any{
		"id": "failure-incident:1", "status": "diagnosed", "incident_version": int64(2),
	})
	if report, err := runner.ApplyEvent(ctx, statusEvent); err != nil || !report.OK {
		t.Fatalf("ApplyEvent status: report=%+v err=%v", report, err)
	}
	rulePayload := map[string]any{
		"id": "repair-rule:1", "selector_fingerprint": "sha256:failure", "strategy": "retry",
		"action_type_id": "reliability.repair.execute", "risk_level": "low", "status": "proposed",
		"confidence": 0.6, "support_count": int64(3), "success_count": int64(0), "failure_count": int64(0),
		"reversible": true, "idempotent": true, "auto_apply": false, "rule_version": int64(1),
		"source_incident_id": "failure-incident:1",
	}
	if report, err := runner.ApplyEvent(ctx, reliabilityOntologyEvent("repair-rule:1", "tenant-a", "reliability.repair_rule.upserted", "repair-rule:1", 1, now.Add(2*time.Minute), rulePayload)); err != nil || !report.OK {
		t.Fatalf("ApplyEvent repair rule: report=%+v err=%v", report, err)
	}
	actionRunEvent := graphprotocol.DomainEvent{
		ID: "action-run-started:repair-dry-run:1", TenantID: "tenant-a", Type: "action.run.started", StreamID: "repair-dry-run:1", Version: 1,
		OccurredAt: now.Add(3 * time.Minute),
		Payload: map[string]any{
			"id": "repair-dry-run:1", "action_type_id": "reliability.repair.execute", "status": "started", "execution_mode": "dry_run",
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: "human", ID: "operator-1"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "action_run", ID: "repair-dry-run:1", TenantID: "tenant-a"},
	}
	if report, err := runner.ApplyEvent(ctx, actionRunEvent); err != nil || !report.OK {
		t.Fatalf("ApplyEvent repair action run: report=%+v err=%v", report, err)
	}
	dryRunPayload := map[string]any{
		"id": "evidence:repair-dry-run:1", "action_run_id": "repair-dry-run:1", "failure_incident_id": "failure-incident:1", "repair_rule_id": "repair-rule:1",
		"evidence_kind": "repair_dry_run", "summary": "Governed repair dry-run checks passed", "status": "passed",
		"source_ref": "repair-dry-run-provider:retry:v1", "content_ref": "repair-dry-run-result:1",
		"checks": []string{"tenant_bound", "selector_matched", "rule_governed", "no_mutation", "retryable"}, "strategy": "retry",
		"target_kind": "mission_agent_run", "target_observation_id": "failure-observed:1", "target_mission_id": "mission-1", "target_run_id": "run-original",
	}
	if report, err := runner.ApplyEvent(ctx, reliabilityOntologyEvent("repair-dry-run-checked:1", "tenant-a", "reliability.repair_dry_run.checked", "evidence:repair-dry-run:1", 1, now.Add(4*time.Minute), dryRunPayload)); err != nil || !report.OK {
		t.Fatalf("ApplyEvent repair dry-run evidence: report=%+v err=%v", report, err)
	}
	retryRunEvent := graphprotocol.DomainEvent{
		ID: "agent-run-started:retry-1", TenantID: "tenant-a", Type: "agent.runtime.run.started", StreamID: "agent-run:retry-1", Version: 1,
		OccurredAt: now.Add(5 * time.Minute),
		Payload: map[string]any{
			"id": "agent-run:retry-1", "runtime_run_id": "retry-1", "status": "completed", "agent_task_id": "agent-task:retry-1",
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: "service", ID: "agent-runtime"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "agent_run", ID: "retry-1", TenantID: "tenant-a"},
	}
	if report, err := runner.ApplyEvent(ctx, retryRunEvent); err != nil || !report.OK {
		t.Fatalf("ApplyEvent retry agent run: report=%+v err=%v", report, err)
	}
	repairApplyEvent := graphprotocol.DomainEvent{
		ID: "action-run-started:repair-apply:1", TenantID: "tenant-a", Type: "action.run.started", StreamID: "repair-apply:1", Version: 1,
		OccurredAt: now.Add(6 * time.Minute),
		Payload: map[string]any{
			"id": "repair-apply:1", "action_type_id": "reliability.repair.execute", "status": "started", "execution_mode": "apply",
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: "human", ID: "operator-1"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "action_run", ID: "repair-apply:1", TenantID: "tenant-a"},
	}
	if report, err := runner.ApplyEvent(ctx, repairApplyEvent); err != nil || !report.OK {
		t.Fatalf("ApplyEvent repair apply action run: report=%+v err=%v", report, err)
	}
	verificationPayload := map[string]any{
		"id": "evidence:repair-verification:1", "action_run_id": "repair-apply:1", "failure_incident_id": "failure-incident:1", "repair_rule_id": "repair-rule:1",
		"evidence_kind": "repair_verification", "summary": "Retried agent run terminal health verification", "status": "passed",
		"source_ref": "retry-1", "content_ref": "agent-run:retry-1", "original_run_id": "run-original", "retry_run_id": "retry-1",
		"retry_agent_run_id": "agent-run:retry-1", "queue_status": "completed", "failure_code": "",
	}
	verificationEvent := reliabilityOntologyEvent("repair-verification-checked:1", "tenant-a", "reliability.repair_verification.checked", "evidence:repair-verification:1", 1, now.Add(7*time.Minute), verificationPayload)
	if report, err := runner.ApplyEvent(ctx, verificationEvent); err != nil || !report.OK {
		t.Fatalf("ApplyEvent repair verification evidence: report=%+v err=%v", report, err)
	}
	if report, err := runner.ApplyEvent(ctx, verificationEvent); err != nil || !report.OK {
		t.Fatalf("replay repair verification evidence: report=%+v err=%v", report, err)
	}

	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "failure_incident", "failure-incident:1")
	assertContextGraphObject(t, objects, "repair_rule", "repair-rule:1")
	assertContextGraphObject(t, objects, "evidence_item", "evidence:repair-dry-run:1")
	assertContextGraphObject(t, objects, "evidence_item", "evidence:repair-verification:1")
	assertContextGraphLink(t, links, "repair_rule_learned_from_failure_incident", "repair-rule:1", "failure-incident:1")
	assertContextGraphLink(t, links, "evidence_item_supports_action_run", "evidence:repair-dry-run:1", "repair-dry-run:1")
	assertContextGraphLink(t, links, "evidence_item_supports_failure_incident", "evidence:repair-dry-run:1", "failure-incident:1")
	assertContextGraphLink(t, links, "evidence_item_supports_repair_rule", "evidence:repair-dry-run:1", "repair-rule:1")
	assertContextGraphLink(t, links, "evidence_item_supports_action_run", "evidence:repair-verification:1", "repair-apply:1")
	assertContextGraphLink(t, links, "evidence_item_supports_failure_incident", "evidence:repair-verification:1", "failure-incident:1")
	assertContextGraphLink(t, links, "evidence_item_supports_repair_rule", "evidence:repair-verification:1", "repair-rule:1")
	assertContextGraphLink(t, links, "evidence_item_verifies_agent_run", "evidence:repair-verification:1", "agent-run:retry-1")
	assertContextGraphFact(t, facts, "failure_incident", "failure-incident:1", "status", "diagnosed")
	assertContextGraphFact(t, facts, "repair_rule", "repair-rule:1", "auto_apply", false)
	assertContextGraphFact(t, facts, "action_run", "repair-dry-run:1", "execution_mode", "dry_run")
	assertContextGraphFact(t, facts, "evidence_item", "evidence:repair-dry-run:1", "strategy", "retry")
	assertContextGraphFact(t, facts, "evidence_item", "evidence:repair-dry-run:1", "target_run_id", "run-original")
	assertContextGraphFact(t, facts, "evidence_item", "evidence:repair-verification:1", "queue_status", "completed")
	assertContextGraphEvidence(t, evidence, "failure-observed:1")
	assertContextGraphEvidence(t, evidence, "repair-rule:1")
	assertContextGraphEvidence(t, evidence, "repair-dry-run-checked:1")
	assertContextGraphEvidence(t, evidence, "repair-verification-checked:1")

	otherTenantPayload := make(map[string]any, len(incidentPayload))
	for key, value := range incidentPayload {
		otherTenantPayload[key] = value
	}
	otherTenantPayload["status"] = "open"
	if _, err := runner.ApplyEvent(ctx, reliabilityOntologyEvent("failure-observed:tenant-b", "tenant-b", "reliability.failure.observed", "failure-incident:1", 1, now, otherTenantPayload)); err != nil {
		t.Fatalf("ApplyEvent tenant-b: %v", err)
	}
	_, _, tenantBFacts, _, err := graphStore.Snapshot(ctx, "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphFact(t, tenantBFacts, "failure_incident", "failure-incident:1", "status", "open")
	assertContextGraphFact(t, facts, "failure_incident", "failure-incident:1", "status", "diagnosed")
}

func TestDefaultReliabilityMappingsRejectMissingRequiredPayload(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatal(err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatal(err)
	}
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), contextgraph.NewMemoryStore())
	for _, eventType := range []string{"reliability.failure.observed", "reliability.failure.status_changed", "reliability.repair_rule.upserted", "reliability.repair_dry_run.checked", "reliability.repair_verification.checked"} {
		if _, err := runner.ApplyEvent(ctx, reliabilityOntologyEvent("invalid:"+eventType, "tenant-a", eventType, "invalid", 1, time.Now().UTC(), map[string]any{})); err == nil {
			t.Fatalf("mapping %s accepted empty payload", eventType)
		}
	}
}

func reliabilityOntologyEvent(id, tenantID, eventType, streamID string, version int64, occurredAt time.Time, payload map[string]any) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID: id, SourceEventID: id, TenantID: tenantID, Type: eventType, SchemaVersion: "v1",
		StreamID: streamID, Version: version, OccurredAt: occurredAt, Payload: payload,
		PublishedBy:     graphprotocol.ActorRef{Kind: "service", ID: "reliability"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "failure_incident", ID: streamID, TenantID: tenantID},
	}
}
