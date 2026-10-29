package improvement

import (
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func BusinessAssessmentOntologyEvent(c Case, a BusinessAssessment) (graphprotocol.DomainEvent, error) {
	copy := a
	copy.ID = ""
	bound := false
	for _, ref := range c.Observation.RelatedObjects {
		bound = bound || (ref.Kind == "runtime_attempt" && ref.ID == a.AttemptID)
	}
	if !IsCapturedRuntimeCase(c) || c.ID != a.CaseID || c.TenantID != a.TenantID || !bound || a.Protocol != "orrery-task-business-assessment-v1" || a.ID != "task-business-assessment:"+Digest(copy) {
		return graphprotocol.DomainEvent{}, ErrInvalid
	}
	return graphprotocol.DomainEvent{ID: "business-assessment-projection:" + a.ID, TenantID: a.TenantID, Type: "improvement.business_assessment.upserted", StreamID: a.ID, Version: 1, OccurredAt: a.AssessedAt,
		PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "task-business-evidence"}, SourceAccessRef: c.Observation.SourceAccessRef,
		Payload: map[string]any{"id": a.ID, "case_id": c.ID, "runtime_review_id": a.RuntimeReviewID, "content_hash": Digest(a), "business_status": a.BusinessStatus, "causal_attribution": a.CausalAttribution,
			"submission_id": a.Outcome.SubmissionID, "qa_status": a.Outcome.QAStatus, "qa_required": a.Outcome.QARequired, "contract_sha256": a.Outcome.ContractSHA256, "source_sha256": a.Outcome.SourceSHA256,
			"deliverables": a.Outcome.Deliverables, "evidence": a.Outcome.Evidence, "blockers": a.Blockers, "assessed_at": a.AssessedAt.Format(time.RFC3339Nano)}}, nil
}
