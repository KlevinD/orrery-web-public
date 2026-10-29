package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionOntologyStewardToolUsesGovernedProposalAndApplyCommands(t *testing.T) {
	commands := &recordingOntologyStewardGovernanceCommands{
		ingestResult: ontology.MetadataProposalIngestResult{
			Proposals:  []ontology.OntologyMetadataProposal{{ID: "proposal-1"}},
			Generation: ontology.ModelInferenceMetadataProposalResult{ValidCandidateCount: 1},
		},
		applyResult: ontology.MetadataProposalPromotionReceipt{
			ReceiptID: "receipt-1", ProposalID: "proposal-1", Status: ontology.PromotionReceiptStatusApplied,
			ProjectionRefreshStatus: projectionRefreshSucceeded,
		},
	}
	executor := productionOntologyStewardToolExecutor{governance: commands}
	created, err := executor.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyProposalCreate), mcptransport.OntologyToolRequest{
		TenantID: "minvia", AgentID: defaultOntologyStewardAgentID, Permissions: []string{toolstd.PermissionOntologyManage},
		Engine: "ontology-steward", Model: "agent-runtime", ModelVersion: "v1", RunID: "run-1", InputHash: "input", PromptHash: "prompt",
		Candidates: []mcptransport.OntologyProposalCandidate{{
			TargetKind: "mapping_rule", TargetID: "people.person.updated", MetadataKey: "source_domain", ProposedValue: "people",
			Confidence: 0.98, Reason: "People owns person updates", EvidenceRefs: []mcptransport.OntologyEvidenceRef{{Kind: "knowledge_document", ID: "doc-1"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commands.principal.Subject != defaultOntologyStewardAgentID || commands.principal.ViewerKind != identity.ViewerKindAgent ||
		!ontologyStewardHasScope(commands.principal.CoarseScopes, "operations.manage") ||
		commands.ingestRequest.TenantID != "minvia" || len(commands.ingestRequest.Candidates) != 1 ||
		commands.ingestRequest.Candidates[0].EvidenceRefs[0].ID != "doc-1" || created.Output["valid_candidate_count"] != 1 {
		t.Fatalf("governed proposal principal=%+v request=%+v output=%+v", commands.principal, commands.ingestRequest, created.Output)
	}

	applied, err := executor.ExecuteOntologyTool(context.Background(), string(toolstd.OntologyPromotionApply), mcptransport.OntologyToolRequest{
		TenantID: "minvia", AgentID: defaultOntologyStewardAgentID, Permissions: []string{toolstd.PermissionOntologyManage},
		PlanID: "plan-1", IdempotencyKey: "apply-1", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if commands.applyRequest.PlanID != "plan-1" || !commands.applyRequest.Confirmed ||
		applied.Output["receipt_id"] != "receipt-1" || applied.Output["projection_refresh_status"] != projectionRefreshSucceeded {
		t.Fatalf("governed apply request=%+v output=%+v", commands.applyRequest, applied.Output)
	}
}

type recordingOntologyStewardGovernanceCommands struct {
	principal     identity.Principal
	ingestRequest ontology.ModelInferenceMetadataProposalRequest
	ingestResult  ontology.MetadataProposalIngestResult
	applyRequest  governanceview.ApplyPromotionPlanRequest
	applyResult   ontology.MetadataProposalPromotionReceipt
}

func (c *recordingOntologyStewardGovernanceCommands) IngestModelInferenceProposals(_ context.Context, principal identity.Principal, request ontology.ModelInferenceMetadataProposalRequest) (ontology.MetadataProposalIngestResult, error) {
	c.principal, c.ingestRequest = principal, request
	return c.ingestResult, nil
}

func (c *recordingOntologyStewardGovernanceCommands) DecideProposal(context.Context, identity.Principal, governanceview.DecideProposalRequest) (ontology.OntologyMetadataProposal, error) {
	return ontology.OntologyMetadataProposal{}, nil
}

func (c *recordingOntologyStewardGovernanceCommands) CreatePromotionPlan(context.Context, identity.Principal, governanceview.CreatePromotionPlanRequest) (governanceview.PromotionPlanResult, error) {
	return governanceview.PromotionPlanResult{ExpiresAt: time.Now().UTC()}, nil
}

func (c *recordingOntologyStewardGovernanceCommands) ApplyPromotionPlan(_ context.Context, principal identity.Principal, request governanceview.ApplyPromotionPlanRequest) (ontology.MetadataProposalPromotionReceipt, error) {
	c.principal, c.applyRequest = principal, request
	return c.applyResult, nil
}

func (c *recordingOntologyStewardGovernanceCommands) RetryProjectionRefresh(context.Context, identity.Principal, governanceview.RetryProjectionRefreshRequest) (governanceview.ProjectionRefreshResult, error) {
	return governanceview.ProjectionRefreshResult{}, nil
}
