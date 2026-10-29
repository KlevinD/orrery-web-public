package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsOntologyGovernanceRevisionBinding(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	proposal := schemas["OntologyGovernanceProposalItemDTO"].(map[string]any)
	proposalProperties := proposal["properties"].(map[string]any)
	for _, name := range []string{"revision", "target_kind", "target_id", "status"} {
		if _, ok := proposalProperties[name].(map[string]any); !ok {
			t.Fatalf("proposal schema missing %s", name)
		}
	}
	decision := schemas["OntologyGovernanceDecisionRequestDTO"].(map[string]any)
	decisionProperties := decision["properties"].(map[string]any)
	if _, ok := decisionProperties["expected_revision"].(map[string]any); !ok {
		t.Fatal("decision schema missing expected_revision")
	}
	receipt := schemas["OntologyGovernanceApplyResultDTO"].(map[string]any)
	receiptProperties := receipt["properties"].(map[string]any)
	for _, name := range []string{"receipt_id", "proposal_id", "proposal_revision", "status", "applied"} {
		if _, ok := receiptProperties[name].(map[string]any); !ok {
			t.Fatalf("receipt schema missing %s", name)
		}
	}
}
