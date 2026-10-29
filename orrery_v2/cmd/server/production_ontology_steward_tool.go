package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type ontologyStewardGovernanceCommands interface {
	IngestModelInferenceProposals(context.Context, identity.Principal, ontology.ModelInferenceMetadataProposalRequest) (ontology.MetadataProposalIngestResult, error)
	DecideProposal(context.Context, identity.Principal, governanceview.DecideProposalRequest) (ontology.OntologyMetadataProposal, error)
	CreatePromotionPlan(context.Context, identity.Principal, governanceview.CreatePromotionPlanRequest) (governanceview.PromotionPlanResult, error)
	ApplyPromotionPlan(context.Context, identity.Principal, governanceview.ApplyPromotionPlanRequest) (ontology.MetadataProposalPromotionReceipt, error)
	RetryProjectionRefresh(context.Context, identity.Principal, governanceview.RetryProjectionRefreshRequest) (governanceview.ProjectionRefreshResult, error)
}

type productionOntologyStewardToolExecutor struct {
	governance ontologyStewardGovernanceCommands
	inference  interface {
		Evaluate(context.Context, identity.Principal, ontologyreasoning.EvaluateRequest) (ontologyreasoning.ReasoningRun, error)
	}
}

func (e productionOntologyStewardToolExecutor) ExecuteOntologyTool(ctx context.Context, toolID string, req mcptransport.OntologyToolRequest) (mcptransport.OntologyToolResult, error) {
	if strings.TrimSpace(toolID) == string(toolstd.OntologyGroundedContext) || strings.TrimSpace(toolID) == string(toolstd.OntologyDocumentInference) || strings.TrimSpace(toolID) == string(toolstd.OntologyInfer) || strings.TrimSpace(toolID) == string(toolstd.OntologyInferenceWorkflow) || strings.TrimSpace(toolID) == string(toolstd.OntologyInferenceRules) {
		return e.executeInferenceTool(ctx, strings.TrimSpace(toolID), req)
	}
	if e.governance == nil {
		return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology governance is not configured")
	}
	principal := ontologyStewardPrincipal(req)
	switch strings.TrimSpace(toolID) {
	case string(toolstd.OntologyProposalCreate):
		if len(req.Candidates) == 0 {
			return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology proposal candidates are required")
		}
		candidates := make([]ontology.ModelInferenceMetadataCandidate, 0, len(req.Candidates))
		for _, candidate := range req.Candidates {
			candidates = append(candidates, ontology.ModelInferenceMetadataCandidate{
				TargetKind: candidate.TargetKind, TargetID: candidate.TargetID, MetadataKey: candidate.MetadataKey,
				ProposedValue: candidate.ProposedValue, Confidence: candidate.Confidence, Reason: candidate.Reason,
				EvidenceRefs: ontologyEvidenceRefs(candidate.EvidenceRefs),
			})
		}
		result, err := e.governance.IngestModelInferenceProposals(ctx, principal, ontology.ModelInferenceMetadataProposalRequest{
			TenantID: req.TenantID, Engine: req.Engine, Model: req.Model, ModelVersion: req.ModelVersion,
			RunID: req.RunID, InputHash: req.InputHash, PromptHash: req.PromptHash,
			SourceRef: req.SourceRef, SourceVersion: req.SourceVersion, Candidates: candidates,
		})
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		if len(result.Proposals) == 0 {
			return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology proposal contained no valid candidates: %s", strings.Join(result.Generation.ValidationErrors, "; "))
		}
		proposalIDs := make([]string, 0, len(result.Proposals))
		for _, proposal := range result.Proposals {
			proposalIDs = append(proposalIDs, proposal.ID)
		}
		return mcptransport.OntologyToolResult{Output: map[string]any{
			"proposal_ids": proposalIDs, "valid_candidate_count": result.Generation.ValidCandidateCount,
			"skipped_invalid_count": result.Generation.SkippedInvalidCount, "validation_errors": result.Generation.ValidationErrors,
			"blocking_count": result.Analysis.BlockingCount, "warning_count": result.Analysis.WarningCount,
		}}, nil
	case string(toolstd.OntologyProposalDecide):
		proposal, err := e.governance.DecideProposal(ctx, principal, governanceview.DecideProposalRequest{
			TenantID: req.TenantID, ProposalID: req.ProposalID, ExpectedRevision: req.ExpectedRevision,
			Decision: ontology.MetadataProposalStatus(strings.ToLower(strings.TrimSpace(req.Decision))),
			Reason:   req.Reason, EvidenceRefs: ontologyEvidenceRefs(req.EvidenceRefs),
		})
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		return mcptransport.OntologyToolResult{Output: map[string]any{
			"proposal_id": proposal.ID, "status": proposal.Status, "revision": proposal.Revision, "reviewed_by": proposal.ReviewedBy,
		}}, nil
	case string(toolstd.OntologyPromotionPlanCreate):
		plan, err := e.governance.CreatePromotionPlan(ctx, principal, governanceview.CreatePromotionPlanRequest{
			TenantID: req.TenantID, ProposalID: req.ProposalID, ExpectedRevision: req.ExpectedRevision,
		})
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		changes, err := ontologyToolJSONValue(plan.Changes)
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		return mcptransport.OntologyToolResult{Output: map[string]any{
			"plan_id": plan.PlanID, "proposal_id": plan.ProposalID, "proposal_revision": plan.ProposalRevision,
			"expires_at": plan.ExpiresAt, "changes": changes,
		}}, nil
	case string(toolstd.OntologyPromotionApply):
		receipt, err := e.governance.ApplyPromotionPlan(ctx, principal, governanceview.ApplyPromotionPlanRequest{
			TenantID: req.TenantID, PlanID: req.PlanID, IdempotencyKey: req.IdempotencyKey, Confirmed: req.Confirmed,
		})
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		changes, err := ontologyToolJSONValue(receipt.Changes)
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		return mcptransport.OntologyToolResult{Output: map[string]any{
			"receipt_id": receipt.ReceiptID, "proposal_id": receipt.ProposalID, "status": receipt.Status,
			"projection_refresh_status": receipt.ProjectionRefreshStatus, "changes": changes,
		}}, nil
	case string(toolstd.OntologyProjectionRefreshRetry):
		result, err := e.governance.RetryProjectionRefresh(ctx, principal, governanceview.RetryProjectionRefreshRequest{
			TenantID: req.TenantID, ReceiptID: req.ReceiptID, IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		return mcptransport.OntologyToolResult{Output: map[string]any{
			"receipt_id": result.ReceiptID, "proposal_id": result.ProposalID, "status": result.Status, "idempotent_replay": result.IdempotentReplay,
		}}, nil
	default:
		return mcptransport.OntologyToolResult{}, fmt.Errorf("unsupported ontology tool %s", toolID)
	}
}

func ontologyStewardPrincipal(req mcptransport.OntologyToolRequest) identity.Principal {
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(req.PrincipalID)
	}
	scopes := append([]string(nil), req.Permissions...)
	for _, required := range []string{toolstd.PermissionOntologyManage, "operations.manage", "org.manage"} {
		if !ontologyStewardHasScope(scopes, required) {
			scopes = append(scopes, required)
		}
	}
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "orrery-agent-runtime", Subject: agentID, UserID: agentID,
		AgentProfileID: agentID, ViewerKind: identity.ViewerKindAgent, TenantID: strings.TrimSpace(req.TenantID),
		CoarseScopes: scopes, CredentialScopes: append([]string(nil), scopes...),
		CredentialScopeRestricted: true,
	})
}

func ontologyStewardHasScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if strings.EqualFold(strings.TrimSpace(scope), target) {
			return true
		}
	}
	return false
}

func ontologyEvidenceRefs(refs []mcptransport.OntologyEvidenceRef) []ontology.MetadataEvidenceRef {
	out := make([]ontology.MetadataEvidenceRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ontology.MetadataEvidenceRef{Kind: ref.Kind, ID: ref.ID, Metadata: ref.Metadata})
	}
	return out
}

func ontologyToolJSONValue(value any) (any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	return out, nil
}
