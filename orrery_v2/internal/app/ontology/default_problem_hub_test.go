package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestDefaultProblemHubMappingsProjectProblemLessonAndAcceptedMemory(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	projections := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)))
	if err := RegisterMappingRuleProjections(ctx, registry, projections, ""); err != nil {
		t.Fatal(err)
	}
	graph := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projections, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)), graph)
	now := time.Date(2026, 9, 3, 6, 0, 0, 0, time.UTC)
	evidenceID := "failure-observation-evidence:incident-1:1"
	if _, err := runner.ApplyEvent(ctx, reliabilityOntologyEvent("failure-observed:problem", "tenant-a", "reliability.failure.observed", "incident-1", 1, now, map[string]any{
		"id": "incident-1", "fingerprint": "fingerprint-1", "source": "agent_runtime", "component": "pi",
		"category": "transient", "severity": "error", "status": "open", "error_code": "timeout",
		"redacted_summary": "request timed out", "retryable": true,
		"first_seen_at": now.Format(time.RFC3339Nano), "last_seen_at": now.Format(time.RFC3339Nano),
		"occurrence_count": int64(1), "incident_version": int64(1),
	})); err != nil {
		t.Fatalf("seed failure incident: %v", err)
	}
	if _, err := runner.ApplyEvent(ctx, reliabilityOntologyEvent("repair-rule:problem", "tenant-a", "reliability.repair_rule.upserted", "rule-1", 1, now, map[string]any{
		"id": "rule-1", "selector_fingerprint": "fingerprint-1", "strategy": "retry",
		"action_type_id": "reliability.repair.execute", "risk_level": "low", "status": "active",
		"confidence": 0.8, "support_count": int64(4), "success_count": int64(1), "failure_count": int64(0),
		"reversible": true, "idempotent": true, "auto_apply": false, "rule_version": int64(2),
		"source_incident_id": "incident-1",
	})); err != nil {
		t.Fatalf("seed repair rule: %v", err)
	}
	problemPayload := map[string]any{
		"id": "problem-1", "fingerprint": "reliability:fingerprint-1", "source_domain": "reliability",
		"category": "transient", "severity": "error", "status": "resolved", "title": "PI timeout",
		"redacted_summary": "request timed out", "owner_kind": "team", "owner_id": "platform",
		"primary_source_kind": "failure_incident", "primary_source_id": "incident-1",
		"source_failure_incident_id": "incident-1", "evidence_ids": []string{evidenceID},
		"primary_evidence_id": evidenceID, "occurrence_count": int64(1),
		"latest_outcome_id": "outcome-1", "latest_outcome_success": true,
		"latest_outcome_summary": "Verified retry repair succeeded.",
		"latest_resolution_kind": "repair_rule", "latest_resolution_id": "rule-1",
		"resolution_repair_rule_id": "rule-1", "lesson_candidate_id": "lesson-1",
		"problem_version": int64(4), "first_seen_at": now.Format(time.RFC3339Nano),
		"last_seen_at": now.Format(time.RFC3339Nano), "resolved_at": now.Format(time.RFC3339Nano),
		"updated_at": now.Format(time.RFC3339Nano),
	}
	problemEvent := problemHubOntologyEvent("problem-case:4", "tenant-a", "problem.case.upserted", "problem-1", 4, now, problemPayload)
	if report, err := runner.ApplyEvent(ctx, problemEvent); err != nil || !report.OK {
		t.Fatalf("apply problem.case.upserted: report=%+v err=%v", report, err)
	}
	if report, err := runner.ApplyEvent(ctx, problemEvent); err != nil || !report.OK {
		t.Fatalf("replay problem.case.upserted: report=%+v err=%v", report, err)
	}
	lessonEvent := problemHubOntologyEvent("problem-lesson:1", "tenant-a", "problem.lesson_candidate.proposed", "lesson-1", 1, now, map[string]any{
		"id": "lesson-1", "memory_candidate_id": "lesson-1", "problem_case_id": "problem-1",
		"status": "proposed", "proposed_layer": "experience", "topic": "reliability.transient",
		"summary": "Verified resolution: PI timeout", "confidence": 0.7, "review_required": true,
		"evidence_ids": []string{evidenceID}, "primary_evidence_id": evidenceID, "created_at": now.Format(time.RFC3339Nano),
	})
	if report, err := runner.ApplyEvent(ctx, lessonEvent); err != nil || !report.OK {
		t.Fatalf("apply problem.lesson_candidate.proposed: report=%+v err=%v", report, err)
	}
	if _, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID: "memory-accepted:problem", TenantID: "tenant-a", Type: "memory.record.accepted", StreamID: "memory-1", Version: 1,
		OccurredAt: now, Payload: map[string]any{
			"id": "memory-1", "summary": "Retry transient PI timeouts", "layer": "experience",
			"topic": "reliability.transient", "scope_kind": "organization", "scope_id": "tenant-a",
			"lifecycle_status": "active", "confidence": 0.8,
			"source_refs": []string{"problem_case:problem-1"}, "evidence_refs": []string{"evidence_item:" + evidenceID},
		}, PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "memory_record", ID: "memory-1", TenantID: "tenant-a"},
	}); err != nil {
		t.Fatalf("seed memory record: %v", err)
	}
	acceptedEvent := problemHubOntologyEvent("problem-memory-accepted:1", "tenant-a", "problem.memory_record.accepted", "memory-1", 1, now, map[string]any{
		"id": "memory-1", "problem_case_id": "problem-1", "problem_lesson_candidate_id": "lesson-1", "status": "accepted",
	})
	acceptedEvent.SourceAccessRef = graphprotocol.AccessRef{Kind: "memory_record", ID: "memory-1", TenantID: "tenant-a"}
	if report, err := runner.ApplyEvent(ctx, acceptedEvent); err != nil || !report.OK {
		t.Fatalf("apply problem.memory_record.accepted: report=%+v err=%v", report, err)
	}

	objects, links, facts, evidence, err := graph.Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "problem_case", "problem-1")
	assertContextGraphObject(t, objects, "lesson_candidate", "lesson-1")
	assertContextGraphObject(t, objects, "memory_record", "memory-1")
	assertContextGraphLink(t, links, "problem_case_tracks_failure_incident", "problem-1", "incident-1")
	assertContextGraphLink(t, links, "evidence_item_supports_problem_case", evidenceID, "problem-1")
	assertContextGraphLink(t, links, "problem_case_resolved_by_repair_rule", "problem-1", "rule-1")
	assertContextGraphLink(t, links, "problem_case_proposes_lesson_candidate", "problem-1", "lesson-1")
	assertContextGraphLink(t, links, "evidence_item_supports_lesson_candidate", evidenceID, "lesson-1")
	assertContextGraphLink(t, links, "memory_about_problem_case", "memory-1", "problem-1")
	assertContextGraphLink(t, links, "memory_realizes_lesson_candidate", "memory-1", "lesson-1")
	assertContextGraphFact(t, facts, "problem_case", "problem-1", "status", "resolved")
	assertContextGraphFact(t, facts, "lesson_candidate", "lesson-1", "status", "proposed")
	assertContextGraphFact(t, facts, "lesson_candidate", "lesson-1", "review_outcome", "accepted")
	assertContextGraphFact(t, facts, "lesson_candidate", "lesson-1", "accepted_memory_id", "memory-1")
	assertContextGraphEvidence(t, evidence, "problem-case:4")
	assertContextGraphEvidence(t, evidence, "problem-lesson:1")
	assertContextGraphEvidence(t, evidence, "problem-memory-accepted:1")

	otherPayload := make(map[string]any, len(problemPayload))
	for key, value := range problemPayload {
		otherPayload[key] = value
	}
	otherPayload["status"] = "open"
	if _, err := runner.ApplyEvent(ctx, problemHubOntologyEvent("problem-case:tenant-b", "tenant-b", "problem.case.upserted", "problem-1", 1, now, otherPayload)); err != nil {
		t.Fatalf("tenant-isolated problem projection: %v", err)
	}
	_, _, tenantBFacts, _, err := graph.Snapshot(ctx, "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphFact(t, tenantBFacts, "problem_case", "problem-1", "status", "open")
	assertContextGraphFact(t, facts, "problem_case", "problem-1", "status", "resolved")
}

func TestDefaultProblemHubMappingsRejectMissingRequiredPayload(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	projections := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)))
	if err := RegisterMappingRuleProjections(ctx, registry, projections, ""); err != nil {
		t.Fatal(err)
	}
	runner := NewMappingRuleProjectionRunner(projections, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)), contextgraph.NewMemoryStore())
	for _, eventType := range []string{"problem.case.upserted", "problem.lesson_candidate.proposed", "problem.memory_record.accepted"} {
		if _, err := runner.ApplyEvent(ctx, problemHubOntologyEvent("invalid:"+eventType, "tenant-a", eventType, "invalid", 1, time.Now().UTC(), map[string]any{})); err == nil {
			t.Fatalf("mapping %s accepted empty payload", eventType)
		}
	}
}

func problemHubOntologyEvent(id, tenantID, eventType, streamID string, version int64, occurredAt time.Time, payload map[string]any) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID: id, SourceEventID: id, TenantID: tenantID, Type: eventType, SchemaVersion: "v1",
		StreamID: streamID, Version: version, OccurredAt: occurredAt, Payload: payload,
		PublishedBy:     graphprotocol.ActorRef{Kind: "service", ID: "problem-hub"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "problem_case", ID: streamID, TenantID: tenantID},
	}
}
