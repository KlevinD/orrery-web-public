package improvement

import (
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func TaskArtifactVerificationOntologyEvent(c Case, r TaskArtifactVerification) (graphprotocol.DomainEvent, error) {
	copy := r
	copy.ID = ""
	bound := false
	for _, ref := range c.Observation.RelatedObjects {
		bound = bound || ref.Kind == "runtime_attempt" && ref.ID == r.AttemptID
	}
	if !IsCapturedRuntimeCase(c) || c.ID != r.CaseID || c.TenantID != r.TenantID || !bound || r.Protocol != "orrery-task-artifact-verification-v1" || r.ID != "task-artifact-verification:"+Digest(copy) {
		return graphprotocol.DomainEvent{}, ErrInvalid
	}
	return graphprotocol.DomainEvent{ID: "artifact-verification-projection:" + r.ID, TenantID: r.TenantID, Type: "improvement.artifact_verification.upserted", StreamID: r.ID, Version: 1, OccurredAt: r.VerifiedAt, PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "task-artifact-verifier"}, SourceAccessRef: c.Observation.SourceAccessRef,
		Payload: map[string]any{"id": r.ID, "case_id": c.ID, "runtime_review_id": r.RuntimeReviewID, "business_assessment_id": r.BusinessAssessmentID, "submission_id": r.SubmissionID, "manifest_id": r.ManifestID, "content_hash": Digest(r), "verification_status": r.Status, "items": r.Items, "attempts": r.Attempts, "verified_at": r.VerifiedAt.Format(time.RFC3339Nano)}}, nil
}
