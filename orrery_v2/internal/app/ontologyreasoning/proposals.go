package ontologyreasoning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

func (s *Service) CreateProposals(ctx context.Context, tenantID string, runID string) (ReasoningRun, error) {
	run, err := s.GetRun(ctx, tenantID, runID)
	if err != nil {
		return ReasoningRun{}, err
	}
	if run.Request.Evaluation != nil && run.Request.Evaluation.Historical {
		return ReasoningRun{}, ErrInvalidRequest
	}
	if len(run.Proposals) > 0 {
		return cloneReasoningRun(run), nil
	}
	if len(run.Validation) == 0 {
		run, err = s.ValidateRun(ctx, tenantID, runID)
		if err != nil {
			return ReasoningRun{}, err
		}
	}
	proposals := []ReasoningProposal{}
	for idx, result := range run.Validation {
		if result.Status == ValidationRejected {
			continue
		}
		candidate := Candidate{}
		if idx < len(run.Candidates) {
			candidate = run.Candidates[idx]
		}
		candidateID := result.CandidateID
		if candidateID == "" {
			candidateID = fmt.Sprintf("candidate-%d", idx+1)
		}
		proposal := ReasoningProposal{
			ID:               "ontology_reasoning_proposal:" + run.ID + ":" + candidateID,
			TenantID:         run.TenantID,
			RunID:            run.ID,
			CandidateID:      candidateID,
			Kind:             candidate.Kind,
			Status:           ProposalStatusProposed,
			ValidationStatus: result.Status,
			Confidence:       candidate.Confidence,
			Reason:           candidate.Reason,
			EvidenceRefs:     cloneRefs(candidate.EvidenceRefs),
			CreatedAt:        s.clock(),
		}
		if candidate.Kind == CandidateMetadata && result.Status == ValidationAccepted && s.metadata != nil {
			governance, err := reasoningMetadataProposal(run, candidateID, candidate, proposal.ID, proposal.CreatedAt)
			if err != nil {
				return ReasoningRun{}, err
			}
			if err := s.metadata.SaveMetadataProposal(ctx, governance); err != nil {
				return ReasoningRun{}, fmt.Errorf("save reasoning metadata proposal: %w", err)
			}
			proposal.GovernanceRef = &contextgraph.Ref{Kind: "ontology_metadata_proposal", ID: governance.ID}
		}
		proposals = append(proposals, proposal)
	}
	run.Proposals = proposals
	run.Status = RunStatusProposed
	return s.saveUpdatedRun(ctx, run)
}

func reasoningMetadataProposal(run ReasoningRun, candidateID string, candidate Candidate, proposalID string, createdAt time.Time) (ontology.OntologyMetadataProposal, error) {
	if candidate.MetadataTarget == nil {
		return ontology.OntologyMetadataProposal{}, fmt.Errorf("%w: metadata target is required", ErrInvalidRequest)
	}
	value, err := reasoningMetadataValue(candidate.MetadataValue)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	evidence := make([]ontology.MetadataEvidenceRef, 0, len(candidate.EvidenceRefs)+2)
	for _, ref := range candidate.EvidenceRefs {
		evidence = append(evidence, ontology.MetadataEvidenceRef{Kind: strings.TrimSpace(ref.Kind), ID: strings.TrimSpace(ref.ID)})
	}
	evidence = append(evidence,
		ontology.MetadataEvidenceRef{Kind: "ontology_reasoning_run", ID: run.ID},
		ontology.MetadataEvidenceRef{Kind: "ontology_reasoning_candidate", ID: candidateID},
	)
	return ontology.OntologyMetadataProposal{
		ID:            proposalID,
		TenantID:      run.TenantID,
		TargetKind:    strings.TrimSpace(candidate.MetadataTarget.Kind),
		TargetID:      strings.TrimSpace(candidate.MetadataTarget.ID),
		MetadataKey:   strings.TrimSpace(candidate.MetadataKey),
		ProposedValue: value,
		Source:        "ontology_reasoning",
		SourceKind:    ontology.MetadataProposalSourceModelInference,
		SourceRef:     run.ID + ":" + candidateID,
		SourceVersion: firstNonBlank(run.ModelVersion, run.Engine, "v1"),
		Confidence:    candidate.Confidence,
		Status:        ontology.MetadataProposalStatusProposed,
		Reason:        candidate.Reason,
		EvidenceRefs:  evidence,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}, nil
}

func reasoningMetadataValue(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: metadata value is not JSON serializable", ErrInvalidRequest)
	}
	return string(raw), nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
