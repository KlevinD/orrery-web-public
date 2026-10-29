package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsNarrowOntologyGovernanceReadAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	for _, path := range []string{
		"/api/ontology/governance/summary",
		"/api/ontology/governance/proposals",
		"/api/ontology/governance/proposals/{proposal_id}",
		"/api/ontology/governance/proposals/{proposal_id}/analysis",
		"/api/ontology/governance/proposals/{proposal_id}/receipts",
		"/api/ontology/governance/proposals/{proposal_id}/audit-events",
	} {
		if _, exists := paths[path]; !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"OntologyGovernanceSummaryDTO",
		"OntologyGovernanceProposalPageDTO",
		"OntologyGovernanceProposalDetailDTO",
		"OntologyGovernanceAnalysisDTO",
		"OntologyGovernanceReceiptPageDTO",
		"OntologyGovernanceAuditEventPageDTO",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	if _, exists := schemas["OntologyGovernanceWorkspaceDTO"]; exists {
		t.Fatal("OpenAPI must not expose an all-resource governance workspace DTO")
	}
	detail := schemas["OntologyGovernanceProposalDetailDTO"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"analysis", "receipts", "audit_events", "promotion_plan", "raw_metadata"} {
		if _, exists := detail[forbidden]; exists {
			t.Fatalf("proposal detail exposes %s", forbidden)
		}
	}
	receipt := schemas["OntologyGovernanceReceiptItemDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"changes", "issues", "current_value", "proposed_value", "error_message"} {
		if _, exists := receipt[forbidden]; exists {
			t.Fatalf("receipt summary exposes %s", forbidden)
		}
	}
	audit := schemas["OntologyGovernanceAuditEventItemDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"metadata", "payload", "raw_payload", "diff"} {
		if _, exists := audit[forbidden]; exists {
			t.Fatalf("audit summary exposes %s", forbidden)
		}
	}
	for _, name := range []string{"OntologyGovernanceProposalPageDTO", "OntologyGovernanceReceiptPageDTO", "OntologyGovernanceAuditEventPageDTO"} {
		items := schemas[name].(map[string]any)["properties"].(map[string]any)["items"].(map[string]any)
		if items["maxItems"] != 100 {
			t.Fatalf("%s maxItems = %v", name, items["maxItems"])
		}
	}
}

func TestOpenAPIContractDocumentsSecureOntologyGovernanceMutationAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	for _, path := range []string{
		"/api/ontology/governance/proposals/{proposal_id}/decisions",
		"/api/ontology/governance/proposals/{proposal_id}/plans",
		"/api/ontology/governance/plans/{plan_id}/apply",
	} {
		operation, exists := paths[path].(map[string]any)["post"]
		if !exists || operation == nil {
			t.Fatalf("OpenAPI missing POST %s", path)
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"OntologyGovernanceDecisionRequestDTO", "OntologyGovernanceDecisionResultDTO",
		"OntologyGovernancePlanRequestDTO", "OntologyGovernancePromotionPlanDTO",
		"OntologyGovernanceApplyRequestDTO", "OntologyGovernanceApplyResultDTO",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	for _, name := range []string{"OntologyGovernanceDecisionRequestDTO", "OntologyGovernancePlanRequestDTO", "OntologyGovernanceApplyRequestDTO"} {
		schema := schemas[name].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s must reject unknown actor/target/diff fields", name)
		}
		properties := schema["properties"].(map[string]any)
		for _, forbidden := range []string{"actor_id", "actor_kind", "reviewer", "reviewed_by", "target", "diff", "authorization_revision"} {
			if _, exists := properties[forbidden]; exists {
				t.Fatalf("%s exposes forbidden input %s", name, forbidden)
			}
		}
	}
	planChange := schemas["OntologyGovernancePromotionPlanChangeDTO"].(map[string]any)["properties"].(map[string]any)
	applyResult := schemas["OntologyGovernanceApplyResultDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"current_value", "proposed_value", "actor_id", "idempotency_key", "proposal_content_hash", "request_hash"} {
		if _, exists := planChange[forbidden]; exists {
			t.Fatalf("promotion plan change exposes %s", forbidden)
		}
		if _, exists := applyResult[forbidden]; exists {
			t.Fatalf("apply result exposes %s", forbidden)
		}
	}
	refreshStatus := applyResult["projection_refresh_status"].(map[string]any)["enum"].([]any)
	hasAbandoned := false
	for _, value := range refreshStatus {
		hasAbandoned = hasAbandoned || value == "abandoned"
	}
	if !hasAbandoned {
		t.Fatal("governance apply result must expose terminal abandoned projection refreshes")
	}
}
