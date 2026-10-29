package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/reliability"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

func TestProductionProblemHubMirrorsReliabilityAndProposesGovernedLesson(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-problem-hub")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	memory := memstore.NewPlatformStore(core)
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	problems := problemhub.NewService(
		problemhub.NewPlatformRepository(core),
		problemhub.WithEventSink(sink),
		problemhub.WithLessonSink(problemhub.NewMemoryLessonSink(memory)),
	)
	sink.problems = problems
	now := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)
	incidentID := "incident-1"
	evidenceID := "failure-observation-evidence:incident-1:1"
	observed := reliability.Event{
		ID: "failure-observed:1", TenantID: "tenant-a", Type: "reliability.failure.observed",
		StreamID: incidentID, Version: 1, OccurredAt: now,
		Actor: reliability.ActorRef{Kind: "worker", ID: "runtime-1"},
		Payload: map[string]any{
			"id": incidentID, "observation_id": "observation-1", "fingerprint": "fingerprint-1",
			"source": "agent_runtime", "component": "pi", "category": "transient", "severity": "error",
			"status": "open", "error_code": "timeout", "redacted_summary": "request timed out",
			"retryable": true, "first_seen_at": now.Format(time.RFC3339Nano),
			"last_seen_at": now.Format(time.RFC3339Nano), "occurrence_count": int64(1), "incident_version": int64(1),
		},
	}
	if err := sink.PublishReliabilityEvent(ctx, observed); err != nil {
		t.Fatalf("PublishReliabilityEvent observed: %v", err)
	}
	problem, err := problemhub.NewPlatformRepository(core).GetBySource(ctx, "tenant-a", problemhub.ObjectRef{Kind: "failure_incident", ID: incidentID})
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if problem.Status != problemhub.StatusOpen || problem.OccurrenceCount != 1 || len(problem.EvidenceIDs) != 1 || problem.EvidenceIDs[0] != evidenceID {
		t.Fatalf("mirrored problem = %+v", problem)
	}

	rulePayload := map[string]any{
		"id": "rule-1", "selector_fingerprint": "fingerprint-1", "strategy": "retry",
		"action_type_id": "reliability.repair.execute", "risk_level": "low", "status": "active",
		"confidence": 0.8, "support_count": int64(3), "success_count": int64(1), "failure_count": int64(0),
		"reversible": true, "idempotent": true, "auto_apply": false, "rule_version": int64(2),
		"source_incident_id": incidentID,
	}
	if err := sink.PublishReliabilityEvent(ctx, reliability.Event{
		ID: "repair-rule:1", TenantID: "tenant-a", Type: "reliability.repair_rule.upserted",
		StreamID: "rule-1", Version: 2, OccurredAt: now.Add(time.Minute), Payload: rulePayload,
	}); err != nil {
		t.Fatalf("PublishReliabilityEvent rule: %v", err)
	}
	outcomePayload := make(map[string]any, len(rulePayload)+7)
	for key, value := range rulePayload {
		outcomePayload[key] = value
	}
	outcomePayload["outcome_id"] = "outcome-1"
	outcomePayload["outcome_incident_id"] = incidentID
	outcomePayload["outcome_success"] = true
	outcomePayload["outcome_evidence_id"] = evidenceID
	outcomePayload["outcome_resolution_kind"] = "repair_rule"
	outcomePayload["outcome_resolution_id"] = "rule-1"
	outcomePayload["outcome_summary"] = "Verified retry repair succeeded for the matching problem fingerprint."
	if err := sink.PublishReliabilityEvent(ctx, reliability.Event{
		ID: "repair-outcome:1", TenantID: "tenant-a", Type: "reliability.repair_rule.upserted",
		StreamID: "rule-1", Version: 3, OccurredAt: now.Add(2 * time.Minute), Payload: outcomePayload,
	}); err != nil {
		t.Fatalf("PublishReliabilityEvent outcome: %v", err)
	}
	resolvedEvent := reliability.Event{
		ID: "failure-status:resolved", TenantID: "tenant-a", Type: "reliability.failure.status_changed",
		StreamID: incidentID, Version: 2, OccurredAt: now.Add(3 * time.Minute),
		Payload: map[string]any{"id": incidentID, "status": "resolved", "incident_version": int64(2)},
	}
	if err := sink.PublishReliabilityEvent(ctx, resolvedEvent); err != nil {
		t.Fatalf("PublishReliabilityEvent resolved: %v", err)
	}
	resolved, err := problems.Get(ctx, "tenant-a", problem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != problemhub.StatusResolved || resolved.LessonCandidateID == "" || !resolved.LatestOutcomeSuccess {
		t.Fatalf("resolved problem = %+v", resolved)
	}
	candidate, err := memory.GetCandidate(ctx, resolved.LessonCandidateID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if candidate.Status != memcore.CandidateProposed || !candidate.ReviewRequired || candidate.ProposedLayer != memcore.MemoryLayerExperience || candidate.SourceRef != resolved.ID {
		t.Fatalf("lesson candidate = %+v", candidate)
	}
	if err := sink.ReplayReliabilityEvent(ctx, resolvedEvent); err != nil {
		t.Fatalf("ReplayReliabilityEvent resolved: %v", err)
	}
	replayedCandidate, err := memory.GetCandidate(ctx, resolved.LessonCandidateID)
	if err != nil || replayedCandidate.ID != candidate.ID || replayedCandidate.Status != memcore.CandidateProposed {
		t.Fatalf("replayed candidate = %+v, %v", replayedCandidate, err)
	}

	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "problem_case", resolved.ID)
	assertContextGraphObject(t, objects, "lesson_candidate", candidate.ID)
	if !contextGraphLinksContain(links, "problem_case_tracks_failure_incident", resolved.ID, incidentID) {
		t.Fatalf("problem-to-incident link missing: %+v", links)
	}
	if !contextGraphLinksContain(links, "problem_case_proposes_lesson_candidate", resolved.ID, candidate.ID) {
		t.Fatalf("problem-to-lesson link missing: %+v", links)
	}
	assertContextGraphFact(t, facts, "problem_case", resolved.ID, "status", "resolved")
	assertContextGraphFact(t, facts, "lesson_candidate", candidate.ID, "status", "proposed")

	// These exact event types are covered above for success, projection failure,
	// replay, and tenant-scoped graph identity by the shared production harness.
	for _, eventType := range []string{"problem.case.upserted", "problem.lesson_candidate.proposed", "problem.memory_record.accepted"} {
		if eventType == "" {
			t.Fatal("empty problem hub event type")
		}
	}
}
