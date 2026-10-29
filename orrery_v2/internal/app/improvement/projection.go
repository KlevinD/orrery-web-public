package improvement

import (
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

// The caller must load the review through RuntimeReviewForCase first. The hash
// below checks integrity/binding; it is not proof of independent business quality.
func RuntimeReviewOntologyEvent(c Case, review RuntimeReview) (graphprotocol.DomainEvent, error) {
	copy := review
	copy.ID = ""
	bound := false
	for _, ref := range c.Observation.RelatedObjects {
		bound = bound || (ref.Kind == "runtime_attempt" && ref.ID == review.AttemptID)
	}
	if !IsCapturedRuntimeCase(c) || c.TenantID != review.TenantID || c.ID != caseID(c.Observation) || !bound ||
		len(c.Observation.EvidenceIDs) < 2 || c.Observation.EvidenceIDs[0] != review.RequestID || c.Observation.EvidenceIDs[1] != review.TerminalID ||
		strings.TrimPrefix(c.Observation.Trigger, "captured_runtime_") != review.PipelineStatus ||
		!validRuntimeReviewPolicy(review) || review.ID != "runtime-review:"+Digest(copy) {
		return graphprotocol.DomainEvent{}, ErrInvalid
	}
	domains := make([]any, 0, len(review.Domains))
	for _, domain := range review.Domains {
		domains = append(domains, map[string]any{"kind": string(domain.Kind), "coverage": domain.Coverage, "gaps": domain.Gaps})
	}
	event := graphprotocol.DomainEvent{
		ID: "runtime-review-projection:" + review.ID, TenantID: c.TenantID, Type: "improvement.runtime_review.upserted",
		StreamID: review.ID, Version: 1, OccurredAt: review.AssessedAt,
		PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "runtime-evidence-review"}, SourceAccessRef: c.Observation.SourceAccessRef,
		Payload: map[string]any{
			"id": review.ID, "case_id": c.ID, "content_hash": Digest(review), "policy_hash": review.PolicySHA256,
			"pipeline_status": review.PipelineStatus, "trace_finalization": review.TraceStatus,
			"business_quality": review.BusinessQuality, "causal_attribution": review.CausalAttribution,
			"baseline_ready": review.BaselineReady, "domain_coverage": domains, "signals": review.Signals, "blockers": review.Blockers,
			"assessed_at": review.AssessedAt.Format(time.RFC3339Nano),
		},
	}
	if review.Protocol == runtimeReviewProtocolV2 {
		event.Type = "improvement.runtime_review_v2.upserted"
		event.Payload["execution_evidence_id"] = review.ExecutionID
		event.Payload["runtime_policy_sha256"] = review.RuntimePolicySHA256
		event.Payload["implementation_sha256"] = review.ImplementationSHA256
	}
	return event, nil
}

// OntologyEvents uses the originating task's access reference for every node,
// including immutable candidate versions and experiment artifacts. Raw task
// content and lease tokens are not copied into graph display properties.
func OntologyEvents(c Case) []graphprotocol.DomainEvent {
	events := []graphprotocol.DomainEvent{}
	eventTypes := map[string]string{
		"case": "improvement.case.upserted", "change_set": "improvement.change_set.upserted",
		"resource_version": "improvement.resource_version.upserted", "experiment": "improvement.experiment.upserted",
		"evaluation": "improvement.evaluation.upserted", "release": "improvement.release.upserted", "outcome": "improvement.outcome.upserted",
	}
	add := func(kind, id string, payload map[string]any) {
		payload["id"] = id
		events = append(events, graphprotocol.DomainEvent{
			ID:       fmt.Sprintf("improvement-projection:%s:%020d:%s:%s", c.ID, c.Revision, kind, id),
			TenantID: c.TenantID, Type: eventTypes[kind], StreamID: id,
			Version: c.Revision, OccurredAt: c.UpdatedAt, Payload: payload,
			PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "improvement-ledger"}, SourceAccessRef: c.Observation.SourceAccessRef,
		})
	}
	payload := map[string]any{"status": string(c.State), "revision": c.Revision, "trigger": c.Observation.Trigger, "summary": c.Observation.Summary,
		"task_ref": c.Observation.Task.Kind + ":" + c.Observation.Task.ID, "run_ref": c.Observation.Run.Kind + ":" + c.Observation.Run.ID,
		"evidence_ids": c.Observation.EvidenceIDs, "updated_at": c.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), "parent_case_id": c.Observation.ParentCaseID}
	// Expose host-owned routing facts through existing case mappings; private
	// Agent diagnosis text remains behind the source access boundary.
	switch c.State {
	case PendingDiagnosis:
		payload["summary"] = "故障未解决，已进入诊断队列；原执行与证据已保留"
	case Blocked:
		payload["summary"] = "故障未解决，诊断受阻（" + c.Reason + "）；请核对原执行与诊断证据"
	case Superseded:
		payload["summary"] = "同次维修反馈已归并，主案例：" + c.SupersededBy
	}
	if c.Observation.Task.Kind == "failure_incident" && strings.HasPrefix(c.Observation.Trigger, "managed_repair_") {
		// A new rule leaves installed/customized case mappings intact.
		eventTypes["case"] = "improvement.managed_repair.upserted"
		payload["failure_incident_id"] = c.Observation.Task.ID
	}
	if c.Observation.Run.Kind == "agent_run" {
		payload["agent_run_id"] = c.Observation.Run.ID
	}
	if c.Observation.Run.Kind == "action_run" {
		payload["action_run_id"] = c.Observation.Run.ID
	}
	if c.Observation.Task.Kind == "agent_task" {
		payload["agent_task_id"] = c.Observation.Task.ID
	}
	if c.Observation.Task.Kind == "task" {
		payload["task_id"] = c.Observation.Task.ID
	}
	if c.Diagnosis != nil {
		payload["diagnosis_case_id"] = c.Diagnosis.DiagnosisCaseID
		// Keep model prose private. Existing evidence mappings expose the exact
		// diagnosis receipt and input IDs under the originating task's ACL.
		ids := append([]string(nil), c.Observation.EvidenceIDs...)
		seen := map[string]bool{}
		for _, id := range ids {
			seen[id] = true
		}
		for _, id := range c.Diagnosis.EvidenceIDs {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		payload["evidence_ids"] = ids
	}
	add("case", c.ID, payload)
	// Scope stage IDs to a case. A caller-supplied ID cannot merge unrelated
	// tenants or tasks in the materialized graph.
	stageID := func(kind string) string { return "improvement-" + kind + ":" + Digest([]string{c.ID, kind}) }
	stage := func(kind, parent string, material any, refs []ArtifactRef) {
		p := map[string]any{"case_id": c.ID, "content_hash": Digest(material), "artifact_refs": refs}
		if parent != "" {
			p["parent_id"] = stageID(parent)
		}
		add(kind, stageID(kind), p)
	}
	if c.ChangeSet != nil {
		cs := c.ChangeSet
		stage("change_set", "", cs, []ArtifactRef{cs.Dataset, cs.Policy, cs.Generator, cs.Evaluator})
		resource := func(v ResourceVersion, candidate bool) {
			p := map[string]any{"target_kind": string(v.Kind), "resource_id": v.ID, "version": v.Version, "content_hash": v.SHA256}
			if candidate {
				p["candidate_change_set_id"] = stageID("change_set")
			} else {
				p["baseline_change_set_id"] = stageID("change_set")
			}
			add("resource_version", "improvement-resource:"+Digest([]any{c.ID, v}), p)
		}
		for _, v := range cs.Baseline {
			resource(v, false)
		}
		for _, change := range cs.Changes {
			resource(change.Candidate, true)
		}
	}
	if c.Experiment != nil {
		stage("experiment", "change_set", c.Experiment, []ArtifactRef{c.Experiment.Manifest, c.Experiment.Result})
	}
	if c.Evaluation != nil {
		stage("evaluation", "experiment", c.Evaluation, []ArtifactRef{c.Evaluation.Report})
	}
	if c.Release != nil {
		refs := []ArtifactRef{}
		for _, target := range c.Release.Targets {
			refs = append(refs, target.Receipt)
		}
		stage("release", "evaluation", c.Release, refs)
	}
	if c.Outcome != nil {
		stage("outcome", "release", c.Outcome, []ArtifactRef{c.Outcome.Report})
	}
	return events
}
