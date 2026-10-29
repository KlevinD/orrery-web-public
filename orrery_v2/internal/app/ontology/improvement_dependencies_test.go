package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func TestImprovementDependenciesGraphReplayIsolationAndIncompleteConformance(t *testing.T) {
	ctx := context.Background()
	runner, graph := improvementProjectionRunner(t)
	source := graphprotocol.AccessRef{TenantID: "tenant-deps", Kind: "agent_run", ID: "run-deps"}
	base := graphprotocol.DomainEvent{TenantID: source.TenantID, StreamID: "case-deps", Version: 1, OccurredAt: time.Unix(100, 0), SourceAccessRef: source, PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "test"}}
	base.ID, base.Type = "case-event", "improvement.case.upserted"
	base.Payload = map[string]any{"id": "case-deps", "status": "observed", "revision": 1, "trigger": "captured_runtime_completed", "summary": "internal", "task_ref": "agent_task:task-deps", "run_ref": "agent_run:run-deps", "evidence_ids": []string{"request-deps", "terminal-deps"}, "updated_at": base.OccurredAt.Format(time.RFC3339)}
	if _, err := runner.ApplyEvent(ctx, base); err != nil {
		t.Fatal(err)
	}
	review := base
	review.ID, review.Type, review.StreamID = "review-event", "improvement.runtime_review.upserted", "review-deps"
	review.Payload = map[string]any{"id": "review-deps", "case_id": "case-deps", "content_hash": "hash", "policy_hash": "policy", "pipeline_status": "completed", "trace_finalization": "succeeded", "business_quality": "unassessed", "causal_attribution": "unresolved", "baseline_ready": false, "domain_coverage": []any{}, "signals": []string{}, "blockers": []string{"baseline_incomplete"}, "assessed_at": base.OccurredAt.Format(time.RFC3339)}
	if _, err := runner.ApplyEvent(ctx, review); err != nil {
		t.Fatal(err)
	}
	event := base
	event.ID, event.Type, event.StreamID = "dependency-event", "improvement.runtime_dependencies.upserted", "dependency-deps"
	event.Payload = map[string]any{"id": "dependency-deps", "case_id": "case-deps", "runtime_review_id": "review-deps", "content_hash": "hash", "model_dispatches": 2, "tool_dispatches": 1, "dropped": 0, "invalid": 0, "runner_returned": true, "baseline_ready": false, "captured_at": base.OccurredAt.Format(time.RFC3339)}
	for i := 0; i < 2; i++ {
		if report, err := runner.ApplyEvent(ctx, event); err != nil || !report.OK {
			t.Fatalf("dependency conformance: %+v %v", report, err)
		}
	}
	objects, links, facts, _, err := graph.Snapshot(ctx, source.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	count, linked, ready := 0, 0, 0
	for _, o := range objects {
		if o.ObjectType == "improvement_runtime_dependencies" {
			count++
			if o.OwnerAccessRef.ID != source.ID || o.OwnerAccessRef.TenantID != source.TenantID {
				t.Fatal("source scope changed")
			}
		}
	}
	for _, l := range links {
		if l.LinkType == "improvement_case_has_runtime_dependencies" || l.LinkType == "improvement_runtime_dependencies_for_review" {
			linked++
		}
	}
	for _, f := range facts {
		if f.Subject.Kind == "improvement_runtime_dependencies" && f.Predicate == "baseline_ready" {
			ready++
			if f.Value != false {
				t.Fatal("false baseline readiness")
			}
		}
	}
	if count != 1 || linked != 2 || ready != 1 {
		t.Fatalf("missing/duplicate projection: %d/%d/%d", count, linked, ready)
	}
	other, _, _, _, _ := graph.Snapshot(ctx, "other-tenant")
	if len(other) != 0 {
		t.Fatal("cross-tenant projection")
	}
	event.ID = "missing-field"
	event.StreamID = "missing-deps"
	event.Payload["id"] = "missing-deps"
	delete(event.Payload, "runner_returned")
	if _, err := runner.ApplyEvent(ctx, event); err == nil {
		t.Fatal("incomplete dependency fields accepted")
	}
}
